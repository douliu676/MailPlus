package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/lib/pq"
)

// The caller holds the database maintenance lock. pg_restore --clean only
// removes objects present in the archive, so a newer operation table can keep
// an FK to card_keys and prevent an older archive from replacing its PK.
// Validate and expand the archive first, then run the newer-table cleanup and
// the entire restore in one psql transaction. Never commit cleanup separately.
func restoreDatabaseFromFile(ctx context.Context, dsn, archivePath string) error {
	tool, err := postgresToolPath("pg_restore")
	if err != nil {
		return err
	}
	psql, err := postgresToolPath("psql")
	if err != nil {
		return err
	}
	inspect := exec.CommandContext(ctx, tool, "--list", archivePath)
	configureBackgroundProcess(inspect)
	inspect.Env = postgresToolEnv(tool)
	toc, err := inspect.CombinedOutput()
	if err != nil {
		toc = bytes.ReplaceAll(toc, []byte(archivePath), []byte("backup.dump"))
		return errors.New(postgresCommandMessage("备份文件校验失败", dsn, toc, err, ctx.Err()))
	}
	schemas := mailDatabaseBackupSchemas(toc)
	if len(schemas) == 0 {
		return errors.New("备份文件不包含完整的卡密表结构和数据，请选择本系统生成的完整 .dump 备份")
	}
	script, err := os.CreateTemp("", "mailplus-db-restore-*.sql")
	if err != nil {
		return errors.New("创建数据库恢复临时文件失败")
	}
	scriptPath := script.Name()
	defer os.Remove(scriptPath)
	if err := script.Close(); err != nil {
		return errors.New("准备数据库恢复临时文件失败")
	}

	// File output does not connect to or modify the target database. Keeping
	// the expanded SQL on disk also avoids buffering the whole database in RAM.
	cmd := exec.CommandContext(ctx, tool, "--clean", "--if-exists", "--no-owner", "--no-privileges", "--file", scriptPath, archivePath)
	configureBackgroundProcess(cmd)
	cmd.Env = postgresToolEnv(tool)
	output, err := cmd.CombinedOutput()
	if err != nil {
		output = bytes.ReplaceAll(output, []byte(archivePath), []byte("backup.dump"))
		output = bytes.ReplaceAll(output, []byte(scriptPath), []byte("restore.sql"))
		return errors.New(postgresCommandMessage("备份文件校验失败", dsn, output, err, ctx.Err()))
	}

	// A current backup recreates this table and all of its archived rows.
	// With a legacy backup, startup migrations recreate it empty, so newer
	// reservations cannot attach themselves to restored historical card IDs.
	// No CASCADE: an unexpected external dependency must fail and roll back.
	cleanup := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		cleanup = append(cleanup, "DROP TABLE IF EXISTS "+pq.QuoteIdentifier(schema)+".card_key_mail_operations;")
	}
	cmd = exec.CommandContext(ctx, psql,
		"--no-psqlrc", "--no-password", "--quiet", "--set", "ON_ERROR_STOP=on",
		"--single-transaction", "--dbname", dsn,
		"--command", strings.Join(cleanup, "\n"),
		"--file", scriptPath,
	)
	configureBackgroundProcess(cmd)
	cmd.Env = postgresToolEnv(psql)
	output, err = cmd.CombinedOutput()
	if err != nil {
		output = bytes.ReplaceAll(output, []byte(scriptPath), []byte("restore.sql"))
		return errors.New(postgresCommandMessage("数据库恢复失败", dsn, output, err, ctx.Err()))
	}
	return nil
}

func mailDatabaseBackupSchemas(toc []byte) []string {
	// pg_restore --list records: dumpID; catalogOID objectOID description
	// namespace name owner. Match TABLE and TABLE DATA separately; a schema-
	// only, data-only or unrelated archive must not trigger operation cleanup.
	definitions := make(map[string]bool)
	data := make(map[string]bool)
	for _, line := range strings.Split(string(toc), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 || !strings.HasSuffix(fields[0], ";") || fields[3] != "TABLE" {
			continue
		}
		if fields[5] == "card_keys" {
			definitions[fields[4]] = true
		}
		if len(fields) >= 8 && fields[4] == "DATA" && fields[6] == "card_keys" {
			data[fields[5]] = true
		}
	}
	var schemas []string
	for schema := range definitions {
		if data[schema] {
			schemas = append(schemas, schema)
		}
	}
	sort.Strings(schemas)
	return schemas
}

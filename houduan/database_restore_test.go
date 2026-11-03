package main

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

func restoreFixture(t *testing.T) (*sql.DB, string, string) {
	t.Helper()
	db, raw := regressionDatabaseLegacy(t)
	for _, name := range []string{"pg_dump", "pg_restore", "psql"} {
		if _, err := postgresToolPath(name); err != nil {
			t.Fatal(err)
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	schema := query.Get("search_path")
	if !strings.HasPrefix(schema, "mailplus_regression_") {
		t.Fatal("restore tests must use their isolated regression schema")
	}
	// lib/pq accepts search_path directly; PostgreSQL command-line clients
	// require it to be passed through the standard libpq options parameter.
	query.Del("search_path")
	query.Set("options", "-c search_path="+schema)
	u.RawQuery = strings.ReplaceAll(query.Encode(), "+", "%20")
	dsn := u.String()
	t.Setenv("DATABASE_URL", dsn)
	_, err = db.Exec(`INSERT INTO card_keys(id,key,status,usage_limit,bound_email,used_at) VALUES (1,'restore-fixture','used',8,'sender@example.invalid','2020-01-02T03:04:05Z');
		INSERT INTO card_key_mail_access_records(card_key_id,email,client_key,last_message_key,charged_count) VALUES (1,'sender@example.invalid','restore-device','original-message',2);
		INSERT INTO card_key_use_logs(card_key_id,card_key,bound_email,mail_subject,user_ip,used_at) VALUES (1,'restore-fixture','sender@example.invalid','Original subject','192.0.2.1','2020-01-02T03:04:05Z');
		INSERT INTO mail_accounts(group_id,server_id,email,password,imap_host,smtp_host,imap_protocol,imap_port,imap_ssl,smtp_protocol,smtp_port,smtp_ssl,remark,status)
		VALUES (2,0,'sender@example.invalid','fixture-password','127.0.0.1','127.0.0.1','IMAP',143,FALSE,'SMTP',25,FALSE,'original account','active')`)
	if err != nil {
		t.Fatal(err)
	}
	return db, dsn, schema
}

func dumpRestoreFixture(t *testing.T, dsn, schema string, extraArgs ...string) string {
	t.Helper()
	tool, _ := postgresToolPath("pg_dump")
	archive := filepath.Join(t.TempDir(), "fixture.dump")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args := []string{"--format=custom", "--no-owner", "--no-privileges", "--schema", schema, "--dbname", dsn, "--file", archive}
	cmd := exec.CommandContext(ctx, tool, append(args, extraArgs...)...)
	configureBackgroundProcess(cmd)
	cmd.Env = postgresToolEnv(tool)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(postgresCommandMessage("fixture dump", dsn, output, err, ctx.Err()))
	}
	return archive
}

func upgradeRestoreFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := ensureCardKeyMailLogColumns(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := ensurePublicMailTables(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func addRestoreOperation(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO card_key_mail_operations(id,card_key_id,card_key,request_id,operation_type,status,email,reserved_count,expires_at)
		VALUES ($1,1,'restore-fixture',$1,'send','unknown','sender@example.invalid',1,NOW())`, id)
	if err != nil {
		t.Fatal(err)
	}
}

func checkRestoredOriginalRows(t *testing.T, db *sql.DB) {
	t.Helper()
	var key, email, subject, message, remark string
	var count, limit int
	var usedAt time.Time
	if err := db.QueryRow(`SELECT key,bound_email,usage_limit,used_at FROM card_keys WHERE id=1`).Scan(&key, &email, &limit, &usedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT charged_count,last_message_key FROM card_key_mail_access_records WHERE card_key_id=1`).Scan(&count, &message); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT mail_subject FROM card_key_use_logs WHERE card_key_id=1`).Scan(&subject); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT remark FROM mail_accounts WHERE email='sender@example.invalid'`).Scan(&remark); err != nil {
		t.Fatal(err)
	}
	if key != "restore-fixture" || email != "sender@example.invalid" || limit != 8 || count != 2 || message != "original-message" || subject != "Original subject" || remark != "original account" || usedAt.UTC().Format(time.RFC3339) != "2020-01-02T03:04:05Z" {
		t.Fatal("restored data differs from the archived card, quota, account or log")
	}
}

func TestDatabaseRestoreLegacyBackupWithNewMailOperations(t *testing.T) {
	db, dsn, schema := restoreFixture(t)
	archive := dumpRestoreFixture(t, dsn, schema)
	upgradeRestoreFixture(t, db)
	addRestoreOperation(t, db, "post-backup-operation")
	if _, err := db.Exec(`UPDATE card_keys SET usage_limit=99; UPDATE card_key_mail_access_records SET charged_count=7; UPDATE mail_accounts SET remark='newer account'; UPDATE card_key_use_logs SET mail_subject='Newer subject'`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := restoreDatabaseFromFile(ctx, dsn, archive); err != nil {
		t.Fatal(err)
	}
	checkRestoredOriginalRows(t, db)
	// The restart migrations recreate fields and tables absent from the old
	// dump, without carrying newer reservations into historical card IDs.
	upgradeRestoreFixture(t, db)
	checkRestoredOriginalRows(t, db)
	var operations int
	if err := db.QueryRow(`SELECT COUNT(*) FROM card_key_mail_operations`).Scan(&operations); err != nil || operations != 0 {
		t.Fatalf("old backup retained newer operations: count=%d err=%v", operations, err)
	}
	if _, err := db.Exec(`INSERT INTO card_key_mail_operations(id,card_key_id,card_key,request_id,operation_type,status,email,expires_at)
		VALUES ('invalid-card',999,'missing','invalid-card','send','unknown','sender@example.invalid',NOW())`); err == nil {
		t.Fatal("restart did not recreate the operation foreign key")
	}
}

func TestDatabaseRestoreCurrentBackupPreservesReservations(t *testing.T) {
	db, dsn, schema := restoreFixture(t)
	upgradeRestoreFixture(t, db)
	addRestoreOperation(t, db, "archived-operation")
	archive := dumpRestoreFixture(t, dsn, schema)
	if _, err := db.Exec(`UPDATE card_key_mail_operations SET status='failed',reserved_count=0; UPDATE card_key_mail_access_records SET charged_count=7`); err != nil {
		t.Fatal(err)
	}
	addRestoreOperation(t, db, "post-backup-operation")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := restoreDatabaseFromFile(ctx, dsn, archive); err != nil {
		t.Fatal(err)
	}
	checkRestoredOriginalRows(t, db)
	var id, status string
	var count, reserved int
	if err := db.QueryRow(`SELECT COUNT(*) FROM card_key_mail_operations`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("operation count=%d err=%v", count, err)
	}
	if err := db.QueryRow(`SELECT id,status,reserved_count FROM card_key_mail_operations`).Scan(&id, &status, &reserved); err != nil || id != "archived-operation" || status != "unknown" || reserved != 1 {
		t.Fatalf("archived reservation changed: id=%s status=%s reserved=%d err=%v", id, status, reserved, err)
	}
}

func TestDatabaseRestoreUsesArchivedSchemaInsteadOfSearchPath(t *testing.T) {
	db, dsn, schema := restoreFixture(t)
	archive := dumpRestoreFixture(t, dsn, schema)
	upgradeRestoreFixture(t, db)
	addRestoreOperation(t, db, "archived-schema-operation")
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Del("options")
	u.RawQuery = query.Encode()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// pg_restore restores schema-qualified objects even when the target
	// connection's default search_path points somewhere else.
	if err := restoreDatabaseFromFile(ctx, u.String(), archive); err != nil {
		t.Fatal(err)
	}
	checkRestoredOriginalRows(t, db)
	upgradeRestoreFixture(t, db)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM card_key_mail_operations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("archive schema was not cleaned: count=%d err=%v", count, err)
	}
}

func TestDatabaseRestoreFailureRollsBackNewTableCleanup(t *testing.T) {
	db, dsn, schema := restoreFixture(t)
	qualified := pq.QuoteIdentifier(schema) + ".restore_guard"
	if _, err := db.Exec(`CREATE FUNCTION ` + qualified + `() RETURNS boolean LANGUAGE sql AS 'SELECT true';
		CREATE TABLE restore_guard_rows(id INTEGER, CONSTRAINT restore_guard_check CHECK (` + qualified + `()));
		INSERT INTO restore_guard_rows VALUES (1);
		CREATE OR REPLACE FUNCTION ` + qualified + `() RETURNS boolean LANGUAGE sql AS 'SELECT false'`); err != nil {
		t.Fatal(err)
	}
	// This is a readable, valid custom-format archive. Restoring its data
	// fails a real CHECK constraint after the cleanup statements have run.
	archive := dumpRestoreFixture(t, dsn, schema)
	upgradeRestoreFixture(t, db)
	addRestoreOperation(t, db, "keep-on-rollback")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := restoreDatabaseFromFile(ctx, dsn, archive)
	if err == nil || !strings.Contains(err.Error(), "restore_guard_check") {
		t.Fatalf("expected the data import CHECK failure, got %v", err)
	}
	checkRestoredOriginalRows(t, db)
	var reserved int
	if err := db.QueryRow(`SELECT reserved_count FROM card_key_mail_operations WHERE id='keep-on-rollback'`).Scan(&reserved); err != nil || reserved != 1 {
		t.Fatalf("failed restore changed existing operation data: reserved=%d err=%v", reserved, err)
	}
}

func TestDatabaseRestoreInvalidArchiveLeavesDatabaseUntouched(t *testing.T) {
	db, dsn, schema := restoreFixture(t)
	upgradeRestoreFixture(t, db)
	addRestoreOperation(t, db, "keep-invalid-archive")
	archive := filepath.Join(t.TempDir(), "invalid.dump")
	if err := os.WriteFile(archive, []byte("not a PostgreSQL backup"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ name, path string }{
		{"invalid-format", archive},
		{"schema-only", dumpRestoreFixture(t, dsn, schema, "--schema-only")},
		{"data-only", dumpRestoreFixture(t, dsn, schema, "--data-only")},
		{"unrelated-table", dumpRestoreFixture(t, dsn, schema, "--table", schema+".mail_accounts")},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := restoreDatabaseFromFile(ctx, dsn, fixture.path); err == nil {
				t.Fatal("incomplete or invalid backup was accepted")
			}
			checkRestoredOriginalRows(t, db)
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM card_key_mail_operations WHERE id='keep-invalid-archive'`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("invalid archive changed operation table: count=%d err=%v", count, err)
			}
		})
	}
}

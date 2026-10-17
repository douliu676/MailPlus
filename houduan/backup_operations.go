package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// PostgreSQL advisory locks coordinate manual, scheduled and restore operations
// across backend instances using this database, and are released on disconnect.
func lockDatabaseMaintenance(ctx context.Context) (func(), error) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(5571033751457186123)`); err != nil {
		tx.Rollback()
		db.Close()
		return nil, err
	}
	return func() { _ = tx.Rollback(); _ = db.Close() }, nil
}

func createScheduledDatabaseBackup(ctx context.Context, settings backupScheduleSettings) (databaseBackupRunResult, error) {
	release, err := lockDatabaseMaintenance(ctx)
	if err != nil {
		return databaseBackupRunResult{}, err
	}
	defer release()
	outputDir := backupStorageDir()
	if err := os.MkdirAll(outputDir, 0700); err != nil {
		return databaseBackupRunResult{}, err
	}
	filename := fmt.Sprintf("mailplus-db-backup-%s-%s.dump", time.Now().Format("20060102-150405"), newToken()[:16])
	outputPath := filepath.Join(outputDir, filename)
	partial, err := os.CreateTemp(outputDir, ".mailplus-backup-*.partial")
	if err != nil {
		return databaseBackupRunResult{}, err
	}
	partialPath := partial.Name()
	if err := partial.Close(); err != nil {
		os.Remove(partialPath)
		return databaseBackupRunResult{}, err
	}
	defer os.Remove(partialPath)
	if err := dumpDatabaseToFile(ctx, partialPath); err != nil {
		return databaseBackupRunResult{}, err
	}
	if err := os.Rename(partialPath, outputPath); err != nil {
		return databaseBackupRunResult{}, err
	}
	result := databaseBackupRunResult{LocalPath: outputPath, Filename: filename}
	if settings.WebDAV.Enabled {
		if err := uploadBackupToWebDAV(ctx, settings.WebDAV, outputPath, filename); err != nil {
			result.WebDAVError = err
		} else {
			result.WebDAVError = cleanupWebDAVDatabaseBackups(ctx, settings.WebDAV, clampInt(settings.RetainCount, 1, 365))
		}
	}
	// Ignore incomplete and unrelated files, and only prune after a complete dump.
	cleanupLocalDatabaseBackups(clampInt(settings.RetainCount, 1, 365))
	return result, nil
}

func redactAdminSettings(settings map[string]interface{}) map[string]interface{} {
	public := make(map[string]interface{}, len(settings)+1)
	for key, value := range settings {
		public[key] = value
	}
	public["backup_webdav_password_configured"] = settingString(settings, "backup_webdav_password") != ""
	public["backup_webdav_password"] = ""
	return public
}

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWebDAVSettingsNeverReturnStoredPassword(t *testing.T) {
	settings := map[string]interface{}{"backup_webdav_password": "synthetic-secret", "site_name": "fixture"}
	response := redactAdminSettings(settings)
	if response["backup_webdav_password"] != "" || response["backup_webdav_password_configured"] != true {
		t.Fatal("secret was not redacted")
	}
	if settings["backup_webdav_password"] != "synthetic-secret" {
		t.Fatal("redaction removed the scheduler's stored credential")
	}
	saved := backupScheduleSettings{WebDAV: webDAVBackupSettings{URL: "http://localhost/dav", Username: "fixture", Password: "synthetic-secret"}}
	req := databaseBackupTaskRequest{WebDAVURL: saved.WebDAV.URL, WebDAVUser: saved.WebDAV.Username}
	if req.applyTo(saved).WebDAV.Password != "synthetic-secret" {
		t.Fatal("blank masked input erased saved credential")
	}
	req.WebDAVURL = "http://localhost/another-dav"
	if req.applyTo(saved).WebDAV.Password != "" {
		t.Fatal("saved secret was forwarded to a different target")
	}
	req.WebDAVURL = saved.WebDAV.URL
	req.WebDAVClearPassword = true
	if req.applyTo(saved).WebDAV.Password != "" {
		t.Fatal("explicit clear did not remove password")
	}
}

func TestBackupRetentionKeepsIncompleteAndUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	files := []string{"mailplus-db-backup-old.dump", "mailplus-db-backup-new.dump", "manual-archive.dump", ".mailplus-backup-fixture.partial"}
	for i, name := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
		stamp := time.Now().Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	cleanupLocalDatabaseBackups(1)
	if _, err := os.Stat(filepath.Join(dir, files[0])); !os.IsNotExist(err) {
		t.Fatal("old managed backup was not pruned")
	}
	for _, name := range files[1:] {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("removed %s", name)
		}
	}
}

package main

import "testing"

func TestPostgresAdminDSNUsesTargetDatabaseName(t *testing.T) {
	dbName, adminDSN, ok := postgresAdminDSN("postgres://postgres:secret@127.0.0.1:5432/mail_admin2?sslmode=disable")
	if !ok {
		t.Fatal("postgresAdminDSN returned ok=false")
	}
	if dbName != "mail_admin2" {
		t.Fatalf("dbName = %q, want mail_admin2", dbName)
	}
	want := "postgres://postgres:secret@127.0.0.1:5432/postgres?sslmode=disable"
	if adminDSN != want {
		t.Fatalf("adminDSN = %q, want %q", adminDSN, want)
	}
}

func TestPostgresAdminDSNSkipsSystemDatabase(t *testing.T) {
	_, _, ok := postgresAdminDSN("postgres://postgres:secret@127.0.0.1:5432/postgres?sslmode=disable")
	if ok {
		t.Fatal("postgresAdminDSN should skip postgres database")
	}
}

func TestUnsafeDefaultDatabaseURL(t *testing.T) {
	cases := []string{
		"postgres://postgres:postgres@127.0.0.1:5432/mail_admin?sslmode=disable",
		"postgres://postgres@127.0.0.1:5432/mail_admin?sslmode=disable",
		"postgresql://postgres:@127.0.0.1:5432/mail_admin?sslmode=disable",
		"postgres://mail_admin:CHANGE_ME_STRONG_PASSWORD@127.0.0.1:5432/mail_admin?sslmode=disable",
	}
	for _, dsn := range cases {
		if !isUnsafeDefaultDatabaseURL(dsn) {
			t.Fatalf("isUnsafeDefaultDatabaseURL(%q) = false, want true", dsn)
		}
	}
}

func TestStrongDatabaseURLIsAllowed(t *testing.T) {
	cases := []string{
		"postgres://mail_admin:strong-password@127.0.0.1:5432/mail_admin?sslmode=disable",
		"postgres://postgres:strong-password@127.0.0.1:5432/mail_admin?sslmode=disable",
	}
	for _, dsn := range cases {
		if isUnsafeDefaultDatabaseURL(dsn) {
			t.Fatalf("isUnsafeDefaultDatabaseURL(%q) = true, want false", dsn)
		}
	}
}

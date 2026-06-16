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

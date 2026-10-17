package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
	"mail-admin/houduan/ent"
)

// Opt-in PostgreSQL regressions use only a uniquely created schema in a
// dedicated local test database; production DATABASE_URL is never consumed.
func regressionDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	raw := os.Getenv("MAILPLUS_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("set MAILPLUS_TEST_DATABASE_URL to a dedicated local *_test database")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || !strings.HasSuffix(u.Path, "_test") {
		t.Fatal("integration tests require a local database ending in _test")
	}
	base, err := sql.Open("postgres", raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := "mailplus_regression_" + newToken()[:16]
	if _, err := base.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		base.Close()
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	q.Set("application_name", schema)
	u.RawQuery = q.Encode()
	dsn := u.String()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = base.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
		_ = base.Close()
	})
	t.Setenv("DATABASE_URL", dsn)
	ddl := `
CREATE TABLE users (id SERIAL PRIMARY KEY,username TEXT NOT NULL UNIQUE,email TEXT NOT NULL DEFAULT '',avatar_url TEXT NOT NULL DEFAULT '',balance DOUBLE PRECISION NOT NULL DEFAULT 0,password_hash TEXT NOT NULL,role TEXT NOT NULL DEFAULT 'user',enabled BOOLEAN NOT NULL DEFAULT TRUE,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),must_change_password BOOLEAN NOT NULL DEFAULT FALSE);
CREATE TABLE balance_records (id SERIAL PRIMARY KEY,user_id INTEGER NOT NULL,type TEXT NOT NULL,amount DOUBLE PRECISION NOT NULL,balance_after DOUBLE PRECISION NOT NULL,remark TEXT NOT NULL DEFAULT '' CHECK (remark <> 'reject-fixture'),created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE mail_groups (id SERIAL PRIMARY KEY,parent_id INTEGER NOT NULL DEFAULT 0,name TEXT NOT NULL);
CREATE TABLE outlook_groups (LIKE mail_groups INCLUDING ALL);
INSERT INTO mail_groups(id,name) VALUES (2,'fixture'); INSERT INTO outlook_groups(id,name) VALUES (2,'fixture');
CREATE TABLE mail_servers(id SERIAL PRIMARY KEY,name TEXT NOT NULL,imap_host TEXT NOT NULL,smtp_host TEXT NOT NULL);
CREATE TABLE mail_accounts(id SERIAL PRIMARY KEY,group_id INTEGER NOT NULL,server_id INTEGER NOT NULL,email TEXT NOT NULL UNIQUE,password TEXT NOT NULL,imap_host TEXT NOT NULL,smtp_host TEXT NOT NULL,imap_protocol TEXT NOT NULL,imap_port INTEGER NOT NULL,imap_ssl BOOLEAN NOT NULL,smtp_protocol TEXT NOT NULL,smtp_port INTEGER NOT NULL,smtp_ssl BOOLEAN NOT NULL,remark TEXT NOT NULL,status TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE outlook_accounts(id SERIAL PRIMARY KEY,group_id INTEGER NOT NULL,email TEXT NOT NULL UNIQUE,password TEXT NOT NULL,client_id TEXT NOT NULL,refresh_token TEXT NOT NULL,remark TEXT NOT NULL,status TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE card_keys(id SERIAL PRIMARY KEY,key TEXT NOT NULL UNIQUE,status TEXT NOT NULL DEFAULT 'unused',usage_limit INTEGER NOT NULL,mail_days INTEGER NOT NULL DEFAULT 0,mail_keyword TEXT NOT NULL DEFAULT '',bound_email TEXT NOT NULL DEFAULT '',used_by TEXT NOT NULL DEFAULT '',used_at TIMESTAMPTZ,updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE card_key_mail_access_records(card_key_id INTEGER NOT NULL,email TEXT NOT NULL,client_key TEXT NOT NULL,client_ip TEXT NOT NULL DEFAULT '',last_request_at TIMESTAMPTZ,last_message_key TEXT NOT NULL DEFAULT '',charged_count INTEGER NOT NULL DEFAULT 0,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),UNIQUE(card_key_id,email,client_key));`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	return db, dsn
}

func serveJSON(t *testing.T, engine http.Handler, method, path, token string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	var result map[string]interface{}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("JSON response: %v", err)
	}
	return response.Code, result
}

func TestDatabaseSingleAdminCredentialLifecycle(t *testing.T) {
	db, dsn := regressionDatabase(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte(defaultAdminPassword), bcrypt.MinCost)
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role,must_change_password) VALUES (7,'admin',$1,'admin',TRUE)`, string(hash)); err != nil {
		t.Fatal(err)
	}
	client, err := ent.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	s := &appState{db: client, sessions: newSessionStore(), loginLimiter: newLoginRateLimiter()}
	r := gin.New()
	r.POST("/login", s.login)
	r.POST("/refresh", s.refreshAuth)
	r.POST("/logout", s.logout)
	r.GET("/profile", s.authMiddleware(), s.getProfile)
	r.PUT("/profile", s.authMiddleware(), s.adminMiddleware(), s.updateProfile)
	r.PUT("/password", s.authMiddleware(), s.changePassword)
	r.PUT("/users/:id", s.authMiddleware(), s.adminMiddleware(), s.updateUser)
	r.PATCH("/users/:id/status", s.authMiddleware(), s.adminMiddleware(), s.updateUserStatus)
	r.DELETE("/users/:id", s.authMiddleware(), s.adminMiddleware(), s.deleteUser)
	r.POST("/users", s.authMiddleware(), s.adminMiddleware(), s.createUser)
	code, result := serveJSON(t, r, "POST", "/login", "", loginRequest{Account: "admin", Password: defaultAdminPassword})
	if code != 200 {
		t.Fatal("initial login failed")
	}
	initial := result["data"].(map[string]interface{})
	oldToken := initial["access_token"].(string)
	if initial["must_change_password"] != true {
		t.Fatal("initial-password gate missing")
	}
	if code, _ := serveJSON(t, r, "PUT", "/profile", oldToken, updateProfileRequest{Username: "bypass"}); code != 403 {
		t.Fatal("profile bypassed initial-password gate")
	}
	const password = "fixture-new-admin-password"
	code, result = serveJSON(t, r, "PUT", "/password", oldToken, changePasswordRequest{OldPassword: defaultAdminPassword, NewPassword: password})
	if code != 200 {
		t.Fatal("initial password change failed")
	}
	initial = result["data"].(map[string]interface{})
	token := initial["access_token"].(string)
	if token == oldToken || initial["must_change_password"] != false {
		t.Fatal("initial change did not rotate the session")
	}
	if code, _ := serveJSON(t, r, "GET", "/profile", oldToken, nil); code != 401 {
		t.Fatal("initial session survived password change")
	}
	if code, _ := serveJSON(t, r, "POST", "/users", token, map[string]string{}); code != 403 {
		t.Fatal("user creation became enabled")
	}
	for _, path := range []string{"/users/7/status", "/users/7"} {
		method := "PATCH"
		body := interface{}(map[string]string{"status": "disabled"})
		if path == "/users/7" {
			method = "PUT"
			body = map[string]interface{}{"enabled": false}
		}
		if code, _ := serveJSON(t, r, method, path, token, body); code != 400 {
			t.Fatal("sole administrator could be disabled")
		}
	}
	if code, _ := serveJSON(t, r, "DELETE", "/users/7", token, nil); code != 400 {
		t.Fatal("renamable administrator could be deleted")
	}
	if code, _ := serveJSON(t, r, "PUT", "/profile", token, updateProfileRequest{Username: "renamed-admin"}); code != 200 {
		t.Fatal("administrator rename failed")
	}
	if code, _ := serveJSON(t, r, "GET", "/profile", token, nil); code != 401 {
		t.Fatal("old session survived rename")
	}
	code, result = serveJSON(t, r, "POST", "/login", "", loginRequest{Account: "renamed-admin", Password: password})
	if code != 200 {
		t.Fatal("renamed administrator could not login")
	}
	token = result["data"].(map[string]interface{})["access_token"].(string)
	if code, _ := serveJSON(t, r, "PUT", "/password", token, changePasswordRequest{OldPassword: password, NewPassword: "fixture-second-password"}); code != 200 {
		t.Fatal("normal password change failed")
	}
	if code, _ := serveJSON(t, r, "GET", "/profile", token, nil); code != 401 {
		t.Fatal("old session survived normal password change")
	}
	var id int
	db.QueryRow(`SELECT id FROM users WHERE username='renamed-admin'`).Scan(&id)
	if id != 7 {
		t.Fatal("administrator primary key changed")
	}
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES (8,'legacy-user',$1,'user'),(9,'next-user',$1,'user')`, string(hash)); err != nil {
		t.Fatal(err)
	}
	if code, _ := serveJSON(t, r, "POST", "/login", "", loginRequest{Account: "legacy-user", Password: defaultAdminPassword}); code != 401 {
		t.Fatal("legacy ordinary account can log in to single-admin system")
	}
	if err := deleteUserRecord(context.Background(), 8); err != nil {
		t.Fatal(err)
	}
	db.QueryRow(`SELECT id FROM users WHERE username='next-user'`).Scan(&id)
	if id != 9 {
		t.Fatal("deletion renumbered another account")
	}
}

func TestDatabaseConcurrentBalanceAndRollback(t *testing.T) {
	db, _ := regressionDatabase(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES (1,'fixture-admin','synthetic','admin')`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 24)
	start := make(chan struct{})
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := updateBalanceAtomically(ctx, db, 1, updateBalanceRequest{Type: "deposit", Amount: 1})
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var balance float64
	var count int
	db.QueryRow(`SELECT balance FROM users WHERE id=1`).Scan(&balance)
	db.QueryRow(`SELECT COUNT(*) FROM balance_records`).Scan(&count)
	if balance != 24 || count != 24 {
		t.Fatalf("balance=%v records=%d", balance, count)
	}
	if _, err := updateBalanceAtomically(ctx, db, 1, updateBalanceRequest{Type: "deposit", Amount: 5, Remark: "reject-fixture"}); err == nil {
		t.Fatal("forced record failure unexpectedly succeeded")
	}
	db.QueryRow(`SELECT balance FROM users WHERE id=1`).Scan(&balance)
	if balance != 24 {
		t.Fatal("failed record insert did not roll back balance")
	}
	if _, err := updateBalanceAtomically(ctx, db, 1, updateBalanceRequest{Type: "deduct", Amount: 25}); err == nil {
		t.Fatal("overdraft was allowed")
	}
}

func TestDatabaseAdminEditPreservesUnspecifiedBalance(t *testing.T) {
	db, _ := regressionDatabase(t)
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role,email,balance) VALUES (1,'fixture-admin','synthetic','admin','fixture@example.invalid',20)`); err != nil {
		t.Fatal(err)
	}
	item, changed, err := saveManagedUser(context.Background(), db, 1, saveUserRequest{Password: "fixture-password-reset"})
	if err != nil || !changed || item.Balance != 20 || item.Email != "fixture@example.invalid" {
		t.Fatal("credential reset altered unspecified profile fields")
	}
	next := 35.0
	if _, _, err := saveManagedUser(context.Background(), db, 1, saveUserRequest{Balance: &next}); err != nil {
		t.Fatal(err)
	}
	var amount, balance float64
	if err := db.QueryRow(`SELECT amount,balance_after FROM balance_records WHERE user_id=1`).Scan(&amount, &balance); err != nil {
		t.Fatal(err)
	}
	if amount != 15 || balance != 35 {
		t.Fatal("absolute balance adjustment was not recorded atomically")
	}
}

func TestDatabaseBackupLockHonorsCancellation(t *testing.T) {
	regressionDatabase(t)
	release, err := lockDatabaseMaintenance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	second, err := lockDatabaseMaintenance(ctx)
	if second != nil {
		second()
	}
	if err == nil {
		t.Fatal("another backup bypassed the maintenance lock")
	}
	release()
	third, err := lockDatabaseMaintenance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	third()
}

func TestDatabaseBatchMailImportsRollback(t *testing.T) {
	db, _ := regressionDatabase(t)
	s := &appState{}
	r := gin.New()
	r.POST("/mail", s.batchCreateMailAccounts)
	r.POST("/outlook", s.batchCreateOutlookAccounts)
	for _, kind := range []string{"mail", "outlook"} {
		first := "fixture@example.invalid----fixture-password"
		if kind == "outlook" {
			first += "----fixture-client----fixture-refresh"
		}
		body := map[string]interface{}{"group_id": 2, "content": first + "\ninvalid-line", "imap_host": "127.0.0.1", "smtp_host": "127.0.0.1"}
		code, result := serveJSON(t, r, "POST", "/"+kind, "", body)
		if code != 400 || !strings.Contains(result["msg"].(string), "2") {
			t.Fatal("batch error did not identify line 2")
		}
		var count int
		db.QueryRow(`SELECT COUNT(*) FROM ` + kind + `_accounts`).Scan(&count)
		if count != 0 {
			t.Fatal("invalid batch left partial records")
		}
		body["content"] = first + "\n" + first
		if code, _ := serveJSON(t, r, "POST", "/"+kind, "", body); code != 400 {
			t.Fatal("duplicate batch was accepted")
		}
		db.QueryRow(`SELECT COUNT(*) FROM ` + kind + `_accounts`).Scan(&count)
		if count != 0 {
			t.Fatal("database rejection left partial records")
		}
		body["content"] = first
		if code, _ := serveJSON(t, r, "POST", "/"+kind, "", body); code != 200 {
			t.Fatalf("valid %s batch rejected", kind)
		}
	}
}

func TestDatabaseCardQuotaUnderContention(t *testing.T) {
	db, dsn := regressionDatabase(t)
	ctx := context.Background()
	card := publicCardKey{ID: 1, Key: "fixture-card", UsageLimit: 1}
	if _, err := db.Exec(`INSERT INTO card_keys(id,key,usage_limit) VALUES (1,$1,1)`, card.Key); err != nil {
		t.Fatal(err)
	}
	for _, client := range []string{"device-a", "device-b"} {
		if _, _, _, err := beginPublicMailRead(ctx, db, card, "fixture@example.invalid", client, "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
	}
	lock, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err := lock.Exec(`SELECT id FROM card_keys WHERE id=1 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	results := make(chan bool, 2)
	for _, client := range []string{"device-a", "device-b"} {
		go func(client string) {
			_, charged, err := chargePublicMailUsage(ctx, db, card, "fixture@example.invalid", client, "message-1", 0)
			results <- err == nil && charged
		}(client)
	}
	u, _ := url.Parse(dsn)
	app := u.Query().Get("application_name")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		db.QueryRow(`SELECT COUNT(*) FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock'`, app).Scan(&waiting)
		if waiting == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	success := 0
	for i := 0; i < 2; i++ {
		select {
		case ok := <-results:
			if ok {
				success++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("card charge deadlock")
		}
	}
	var used int
	db.QueryRow(`SELECT SUM(charged_count) FROM card_key_mail_access_records`).Scan(&used)
	if success != 1 || used != 1 {
		t.Fatalf("quota exceeded: successful=%d used=%d", success, used)
	}
	var chargedClient string
	db.QueryRow(`SELECT client_key FROM card_key_mail_access_records WHERE charged_count=1`).Scan(&chargedClient)
	if count, charged, err := chargePublicMailUsage(ctx, db, card, "fixture@example.invalid", chargedClient, "message-1", 1); err != nil || charged || count != 1 {
		t.Fatal("same message charged twice")
	}
}

func TestDatabaseConcurrentCooldownIsAtomic(t *testing.T) {
	db, _ := regressionDatabase(t)
	card := publicCardKey{ID: 1, Key: "fixture-card", UsageLimit: 20}
	db.Exec(`INSERT INTO card_keys(id,key,usage_limit) VALUES (1,$1,20)`, card.Key)
	start := make(chan struct{})
	results := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			<-start
			_, _, wait, err := beginPublicMailRead(context.Background(), db, card, "fixture@example.invalid", "same-device", "127.0.0.1")
			results <- err == nil && wait == 0
		}()
	}
	close(start)
	accepted := 0
	for i := 0; i < 10; i++ {
		if <-results {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatal(fmt.Sprintf("cooldown admitted %d requests", accepted))
	}
}

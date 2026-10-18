package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func renewalDatabase(t *testing.T, migrate bool) *sql.DB {
	t.Helper()
	db, _ := regressionDatabase(t)
	for _, statement := range []string{
		"ALTER TABLE mail_accounts ADD COLUMN status_reason TEXT NOT NULL DEFAULT '', ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), DROP CONSTRAINT mail_accounts_email_key, ADD UNIQUE(group_id,email)",
		"INSERT INTO mail_groups(id,name) VALUES (3,'other fixture')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := ensureProxySystemTables(context.Background()); err != nil {
		t.Fatal(err)
	}
	if migrate {
		if err := ensureMailAccountRenewalColumn(context.Background(), db); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func renewalAccount(t *testing.T, db *sql.DB, groupID, port int) (mailAccountResponse, saveMailAccountRequest) {
	t.Helper()
	request := saveMailAccountRequest{
		Email: "renewal@example.invalid", Password: "fixture-password", GroupID: groupID,
		ImapHost: "127.0.0.1", ImapProtocol: "IMAP", ImapPort: port,
		SMTPHost: "127.0.0.1", SMTPProtocol: "SMTP", SMTPPort: port,
	}
	item, err := createMailAccountWithDB(context.Background(), db, request)
	if err != nil {
		t.Fatal(err)
	}
	return item, request
}

func renewalTime(t *testing.T, db *sql.DB, id int) sql.NullTime {
	t.Helper()
	var value sql.NullTime
	if err := db.QueryRow("SELECT last_renewed_at FROM mail_accounts WHERE id=$1", id).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func setRenewalTime(t *testing.T, db *sql.DB, id int, value time.Time) {
	t.Helper()
	if _, err := db.Exec("UPDATE mail_accounts SET last_renewed_at=$2 WHERE id=$1", id, value); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseMailRenewalMigrationAndDisplay(t *testing.T) {
	db := renewalDatabase(t, false)
	first, request := renewalAccount(t, db, 2, 143)
	second, _ := renewalAccount(t, db, 3, 143)
	if err := ensureMailAccountRenewalColumn(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if renewalTime(t, db, first.ID).Valid {
		t.Fatal("migration invented a renewal time for an old account")
	}
	old := time.Date(2020, 1, 2, 3, 4, 5, 123456000, time.UTC)
	setRenewalTime(t, db, first.ID, old)
	if err := ensureMailAccountRenewalColumn(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if !renewalTime(t, db, first.ID).Time.Equal(old) {
		t.Fatal("repeated migration overwrote an existing renewal")
	}
	displayTime := renewalTime(t, db, first.ID).Time.Format("2006/01/02 15:04:05")

	r := gin.New()
	s := &appState{}
	r.GET("/accounts", s.listMailAccounts)
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, httptest.NewRequest("GET", "/accounts", nil))
	var response struct {
		Data mailAccountListResponse
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || recorder.Code != 200 || len(response.Data.Items) != 2 {
		t.Fatalf("list response failed: status=%d err=%v", recorder.Code, err)
	}
	for _, item := range response.Data.Items {
		if item.ID == first.ID && item.LastRenewedAt != displayTime {
			t.Fatal("saved renewal was not returned by the list API")
		}
		if item.ID == second.ID && item.LastRenewedAt != "" {
			t.Fatal("an unrenewed account was given a display time")
		}
	}

	for _, password := range []string{"", "fixture-replacement-password"} {
		request.Password = password
		request.Remark = "edited fixture"
		saved, err := updateMailAccountRecord(context.Background(), first.ID, request)
		if err != nil || saved.LastRenewedAt != displayTime {
			t.Fatalf("editing lost the response renewal: %v", err)
		}
		if !renewalTime(t, db, first.ID).Time.Equal(old) {
			t.Fatal("ordinary editing renewed the account")
		}
	}
}

type renewalIMAPFixture struct {
	port   int
	reject atomic.Bool
}

func startRenewalIMAPFixture(t *testing.T) *renewalIMAPFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fixture := &renewalIMAPFixture{port: listener.Addr().(*net.TCPAddr).Port}
	var clients sync.Map
	var workers sync.WaitGroup
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			clients.Store(conn, true)
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer clients.Delete(conn)
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = io.WriteString(conn, "* OK fixture\r\n")
				reader := bufio.NewReader(conn)
				for {
					line, err := readMailLine(reader)
					if err != nil {
						return
					}
					fields := strings.Fields(line)
					if len(fields) < 2 {
						return
					}
					tag := fields[0]
					switch fields[1] {
					case "LOGIN":
						if fixture.reject.Load() {
							_, _ = fmt.Fprintf(conn, "%s NO fixture authentication failed\r\n", tag)
						} else {
							_, _ = fmt.Fprintf(conn, "%s OK logged in\r\n", tag)
						}
					case "SELECT", "EXAMINE":
						if strings.Contains(line, "INBOX") {
							_, _ = fmt.Fprintf(conn, "* 1 EXISTS\r\n* OK [UIDVALIDITY 42] valid\r\n%s OK selected\r\n", tag)
						} else {
							_, _ = fmt.Fprintf(conn, "%s NO no such mailbox\r\n", tag)
						}
					case "LIST":
						_, _ = fmt.Fprintf(conn, "* LIST (\\HasNoChildren) \"/\" \"INBOX\"\r\n%s OK listed\r\n", tag)
					case "FETCH":
						header := quickFixtureMail(1, false)
						_, _ = fmt.Fprintf(conn, "* 1 FETCH (UID 1 BODY[HEADER.FIELDS (SUBJECT FROM TO DATE)] {%d}\r\n%s)\r\n%s OK fetched\r\n", len(header), header, tag)
					case "UID":
						body := quickFixtureMail(1, true)
						_, _ = fmt.Fprintf(conn, "* 1 FETCH (UID 1 BODY[] {%d}\r\n%s)\r\n%s OK fetched\r\n", len(body), body, tag)
					case "LOGOUT":
						_, _ = fmt.Fprintf(conn, "* BYE fixture\r\n%s OK logout\r\n", tag)
						return
					default:
						_, _ = fmt.Fprintf(conn, "%s BAD unsupported\r\n", tag)
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-stopped
		clients.Range(func(key, _ interface{}) bool { _ = key.(net.Conn).Close(); return true })
		workers.Wait()
	})
	return fixture
}

func TestDatabaseMailRenewalSuccessfulActivityAndFailures(t *testing.T) {
	db := renewalDatabase(t, true)
	fixture := startRenewalIMAPFixture(t)
	first, _ := renewalAccount(t, db, 2, fixture.port)
	other, _ := renewalAccount(t, db, 3, fixture.port)
	baseline := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	s := &appState{}
	r := gin.New()
	r.POST("/accounts/:id/test", s.testMailAccount)
	r.POST("/accounts/:id/receive", s.receiveMailMessages)
	r.POST("/accounts/:id/detail", s.receiveMailDetail)
	request := func(action string, payload interface{}) error {
		code, _ := serveJSON(t, r, "POST", fmt.Sprintf("/accounts/%d/%s", first.ID, action), "", payload)
		if code != 200 {
			return fmt.Errorf("fixture request returned %d", code)
		}
		return nil
	}
	operations := []struct {
		name string
		run  func() error
	}{
		{"single test", func() error { return request("test", testMailAccountRequest{Type: "all"}) }},
		{"batch test", func() error { return runMailAccountConnectivityTest(context.Background(), db, first.ID, "all") }},
		{"receive list", func() error { return request("receive", receiveMailMessagesRequest{Limit: 1}) }},
		{"receive detail", func() error {
			return request("detail", receiveMailDetailRequest{UID: 1, Mailbox: "INBOX", Folder: "inbox"})
		}},
		{"quick receive", func() error {
			_, err := fetchQuickIMAPPlainMessages(context.Background(), first.Email, 1)
			return err
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			setRenewalTime(t, db, first.ID, baseline)
			if _, err := db.Exec("UPDATE mail_accounts SET status='normal' WHERE id=$1", first.ID); err != nil {
				t.Fatal(err)
			}
			fixture.reject.Store(false)
			if err := operation.run(); err != nil {
				t.Fatal(err)
			}
			success := renewalTime(t, db, first.ID)
			if !success.Valid || !success.Time.After(baseline) {
				t.Fatal("successful receive-protocol activity was not recorded")
			}
			if renewalTime(t, db, other.ID).Valid {
				t.Fatal("renewed another group's account sharing the same email")
			}
			fixture.reject.Store(true)
			if err := operation.run(); err == nil {
				t.Fatal("fixture authentication failure was accepted")
			}
			if !renewalTime(t, db, first.ID).Time.Equal(success.Time) {
				t.Fatal("a failed operation changed the renewal time")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := renewalTime(t, db, first.ID)
	recordMailAccountRenewal(ctx, db, first.ID)
	if !renewalTime(t, db, first.ID).Time.Equal(before.Time) {
		t.Fatal("cancelled activity changed the renewal time")
	}
}

func TestDatabaseMailRenewalExportImportPreservesHistory(t *testing.T) {
	db := renewalDatabase(t, true)
	item, _ := renewalAccount(t, db, 2, 143)
	old := time.Date(2020, 1, 2, 3, 4, 5, 123456000, time.UTC)
	setRenewalTime(t, db, item.ID, old)
	var exported mailDataAccount
	for _, selector := range []accountExportSelector{{IDs: []int{item.ID}}, {}} {
		items, err := queryExportMailAccounts(context.Background(), db, selector)
		if err != nil || len(items) != 1 {
			t.Fatalf("account export lost renewal precision: %v", err)
		}
		parsed, err := time.Parse(time.RFC3339Nano, items[0].LastRenewedAt)
		if err != nil || !parsed.Equal(old) {
			t.Fatal("export changed the renewal instant or precision")
		}
		var buffer bytes.Buffer
		if err := writeExportAccountsJSON(context.Background(), db, &buffer, selector); err != nil {
			t.Fatal(err)
		}
		var streamed []mailDataAccount
		if err := json.Unmarshal(buffer.Bytes(), &streamed); err != nil || len(streamed) != 1 || streamed[0].LastRenewedAt != items[0].LastRenewedAt {
			t.Fatal("streaming export lost the renewal")
		}
		exported = items[0]
	}
	saveImport := func(account mailDataAccount) {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if count, err := importMailAccounts(context.Background(), tx, []mailDataAccount{account}, nil, nil); err != nil || count != 1 {
			t.Fatalf("import failed: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	exported.GroupID = 3
	saveImport(exported)
	var importedID int
	if err := db.QueryRow("SELECT id FROM mail_accounts WHERE group_id=3").Scan(&importedID); err != nil {
		t.Fatal(err)
	}
	if !renewalTime(t, db, importedID).Time.Equal(old) {
		t.Fatal("import into a new group lost the renewal")
	}
	for _, value := range []string{"", old.Add(-time.Hour).Format(time.RFC3339Nano)} {
		exported.LastRenewedAt = value
		saveImport(exported)
		if !renewalTime(t, db, importedID).Time.Equal(old) {
			t.Fatal("an older import cleared or rolled back existing renewal history")
		}
	}
}

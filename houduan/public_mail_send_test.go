package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	stdmail "net/mail"
	"net/textproto"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type publicSMTPFixture struct {
	listener net.Listener
	mode     string
	release  <-chan struct{}
	stop     chan struct{}
	message  chan []byte
	calls    atomic.Int32
	wg       sync.WaitGroup
	mu       sync.Mutex
	conns    []net.Conn
}

func newPublicSMTPFixture(t *testing.T, mode string, release <-chan struct{}) *publicSMTPFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &publicSMTPFixture{listener: listener, mode: mode, release: release, stop: make(chan struct{}), message: make(chan []byte, 16)}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conns = append(f.conns, conn)
			f.mu.Unlock()
			f.wg.Add(1)
			go func() { defer f.wg.Done(); defer conn.Close(); f.serve(conn) }()
		}
	}()
	t.Cleanup(func() {
		close(f.stop)
		listener.Close()
		f.mu.Lock()
		for _, conn := range f.conns {
			conn.Close()
		}
		f.mu.Unlock()
		f.wg.Wait()
	})
	return f
}

func (f *publicSMTPFixture) account() mailAccountTestConfig {
	return mailAccountTestConfig{Email: "sender@example.invalid", Password: "fixture-password", SMTPHost: "127.0.0.1", SMTPPort: f.listener.Addr().(*net.TCPAddr).Port, SMTPProtocol: "SMTP"}
}

func (f *publicSMTPFixture) serve(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	text := textproto.NewConn(conn)
	_ = text.PrintfLine("220 fixture ESMTP")
	for {
		line, err := text.ReadLine()
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch command {
		case "EHLO":
			_ = text.PrintfLine("250-fixture")
			_ = text.PrintfLine("250 AUTH PLAIN LOGIN")
		case "AUTH":
			if f.mode == "auth-failed" {
				_ = text.PrintfLine("535 authentication rejected")
			} else {
				_ = text.PrintfLine("235 authenticated")
			}
		case "MAIL":
			_ = text.PrintfLine("250 sender accepted")
		case "RCPT":
			if f.mode == "recipient-failed" {
				_ = text.PrintfLine("550 recipient rejected")
			} else {
				_ = text.PrintfLine("250 recipient accepted")
			}
		case "DATA":
			_ = text.PrintfLine("354 send body")
			body, err := text.ReadDotBytes()
			if err != nil {
				return
			}
			f.calls.Add(1)
			f.message <- body
			if f.release != nil {
				select {
				case <-f.release:
				case <-f.stop:
					return
				}
			}
			if f.mode == "unknown" {
				return
			}
			if f.mode == "data-failed" {
				_ = text.PrintfLine("550 message rejected")
			} else {
				_ = text.PrintfLine("250 queued")
			}
		case "QUIT":
			if f.mode != "quit-failed" {
				_ = text.PrintfLine("221 goodbye")
			}
			return
		default:
			_ = text.PrintfLine("500 unexpected command")
		}
	}
}

func TestDatabasePublicMailSMTPAcceptance(t *testing.T) {
	for _, mode := range []string{"success", "quit-failed", "auth-failed", "recipient-failed", "data-failed", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			db, _ := regressionDatabase(t)
			preparePublicSMTPProxy(t, db)
			fixture := newPublicSMTPFixture(t, mode, nil)
			err := sendMailAccountMessage(context.Background(), fixture.account(), sendMailMessageRequest{Recipient: "recipient@example.invalid"})
			var uncertain *uncertainMailSendError
			if mode == "success" || mode == "quit-failed" {
				if err != nil {
					t.Fatalf("accepted mail reported as failure: %v", err)
				}
				message, err := stdmail.ReadMessage(bytes.NewReader(<-fixture.message))
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(message.Body)
				if message.Header.Get("Subject") != "" || strings.TrimSpace(string(body)) != "" {
					t.Fatal("optional empty fields were changed")
				}
			} else if err == nil {
				t.Fatal("rejected/ambiguous message reported as success")
			} else if errors.As(err, &uncertain) != (mode == "unknown") {
				t.Fatalf("incorrect certainty: %v", err)
			}
		})
	}
}

func TestDatabasePublicMailSMTPTimeoutIsUncertain(t *testing.T) {
	db, _ := regressionDatabase(t)
	preparePublicSMTPProxy(t, db)
	fixture := newPublicSMTPFixture(t, "success", make(chan struct{}))
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := sendMailAccountMessage(ctx, fixture.account(), sendMailMessageRequest{Recipient: "recipient@example.invalid"})
	var uncertain *uncertainMailSendError
	if !errors.As(err, &uncertain) || time.Since(started) > 3*time.Second {
		t.Fatalf("SMTP cancellation/uncertainty: %v", err)
	}
}

func TestPublicMailOutlookAcceptance(t *testing.T) {
	for _, status := range []int{202, 403, 503, 0} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var payload map[string]any
				if json.NewDecoder(r.Body).Decode(&payload) != nil {
					t.Error("invalid Graph payload")
				}
				message := payload["message"].(map[string]any)
				if message["subject"] != "" || message["body"].(map[string]any)["content"] != "" {
					t.Error("empty fields rejected")
				}
				if status == 0 {
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
					return
				}
				w.WriteHeader(status)
				if status != 202 {
					_, _ = io.WriteString(w, `{"error":{"message":"fixture rejection"}}`)
				}
			}))
			defer server.Close()
			err := submitOutlookMail(context.Background(), server.Client(), server.URL, "sender@example.invalid", "fixture-token", sendMailMessageRequest{Recipient: "recipient@example.invalid"})
			var unknown *uncertainMailSendError
			if (err == nil) != (status == 202) || errors.As(err, &unknown) != (status == 503 || status == 0) || requests.Load() != 1 {
				t.Fatalf("Graph status=%d err=%v requests=%d", status, err, requests.Load())
			}
		})
	}
}

func preparePublicSMTPProxy(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS proxy_settings(scope TEXT PRIMARY KEY,enabled BOOLEAN NOT NULL,proxy_node_id INTEGER NOT NULL);
		INSERT INTO proxy_settings VALUES ('imap',FALSE,0) ON CONFLICT (scope) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
}

func insertPublicSendFixture(t *testing.T, db *sql.DB, smtp *publicSMTPFixture, limit int) publicCardKey {
	t.Helper()
	card := publicCardKey{ID: 1, Key: "fixture-card", UsageLimit: limit, BoundEmail: "sender@example.invalid"}
	if _, err := db.Exec(`INSERT INTO card_keys(id,key,usage_limit,bound_email) VALUES (1,$1,$2,$3)`, card.Key, limit, card.BoundEmail); err != nil {
		t.Fatal(err)
	}
	if smtp != nil {
		preparePublicSMTPProxy(t, db)
		a := smtp.account()
		_, err := db.Exec(`INSERT INTO mail_accounts(group_id,server_id,email,password,imap_host,smtp_host,imap_protocol,imap_port,imap_ssl,smtp_protocol,smtp_port,smtp_ssl,remark,status)
			VALUES (2,0,$1,$2,'127.0.0.1',$3,'IMAP',143,FALSE,'SMTP',$4,FALSE,'','active')`, a.Email, a.Password, a.SMTPHost, a.SMTPPort)
		if err != nil {
			t.Fatal(err)
		}
	}
	return card
}

func publicMailTestRouter() *gin.Engine {
	state := &appState{}
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.POST("/mail/:key/send", state.sendPublicMail)
	r.GET("/mail/:key/send/:requestID", state.getPublicMailSendStatus)
	r.POST("/mail/:key/messages", state.getPublicMailMessages)
	r.GET("/mail/:key/all", state.getPublicMailPlain)
	r.GET("/mail/:key/all/*email", state.getPublicMailPlain)
	r.GET("/mail/:key", state.getPublicMailInfo)
	r.POST("/resolve/:operationID", state.resolvePublicMailSend)
	return r
}

func publicSendPayload(id string) map[string]any {
	return map[string]any{"request_id": id, "recipient": "recipient@example.invalid", "nickname": "", "subject": "Fixture subject", "body": ""}
}

func waitPublicSend(t *testing.T, done <-chan *httptest.ResponseRecorder) (int, map[string]any) {
	t.Helper()
	select {
	case response := <-done:
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return response.Code, result
	case <-time.After(8 * time.Second):
		t.Fatal("send did not finish")
		return 0, nil
	}
}

func TestDatabasePublicMailMigrationPreservesLegacy(t *testing.T) {
	db, _ := regressionDatabaseLegacy(t)
	_, err := db.Exec(`INSERT INTO card_keys(id,key,status,usage_limit,bound_email,used_at) VALUES (1,'legacy-card','used',5,'legacy@example.invalid','2020-01-02T03:04:05Z');
		INSERT INTO card_key_mail_access_records(card_key_id,email,client_key,last_message_key,charged_count) VALUES(1,'legacy@example.invalid','legacy-device','legacy-message',2);
		INSERT INTO card_key_use_logs(card_key_id,card_key,bound_email,mail_subject,user_ip,used_at) VALUES(1,'legacy-card','legacy@example.invalid','Legacy subject','192.0.2.1','2020-01-02T03:04:05Z')`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := ensureCardKeyMailLogColumns(context.Background(), db); err != nil {
			t.Fatal(err)
		}
		if err := ensurePublicMailTables(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var key, status, email, subject, kind, previous string
	var count, limit int
	var usedAt time.Time
	if err := db.QueryRow(`SELECT key,status,bound_email,usage_limit,used_at FROM card_keys WHERE id=1`).Scan(&key, &status, &email, &limit, &usedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT mail_subject,operation_type FROM card_key_use_logs`).Scan(&subject, &kind); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT charged_count,last_message_key FROM card_key_mail_access_records`).Scan(&count, &previous); err != nil {
		t.Fatal(err)
	}
	if key != "legacy-card" || status != "used" || email != "legacy@example.invalid" || limit != 5 || count != 2 || subject != "Legacy subject" || kind != "receive" || previous != "legacy-message" || usedAt.UTC().Format(time.RFC3339) != "2020-01-02T03:04:05Z" {
		t.Fatal("migration rewrote historical records")
	}
}

func TestDatabasePublicMailSingleOperationAndReplay(t *testing.T) {
	db, _ := regressionDatabase(t)
	release := make(chan struct{})
	smtp := newPublicSMTPFixture(t, "quit-failed", release)
	card := insertPublicSendFixture(t, db, smtp, 1)
	r := publicMailTestRouter()
	payload := publicSendPayload("fixture-send-request-0001")
	raw, _ := json.Marshal(payload)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest("POST", "/mail/keys="+card.Key+"/send", bytes.NewReader(raw)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { response := httptest.NewRecorder(); r.ServeHTTP(response, request); done <- response }()
	select {
	case <-smtp.message:
	case response := <-done:
		t.Fatalf("send rejected before SMTP: %d %s", response.Code, response.Body.String())
	case <-time.After(5 * time.Second):
		t.Fatal("SMTP was not reached")
	}
	// Browsers/proxies can replay a POST after losing the HTTP response.
	// The original identifier must recover its running result before the busy check.
	replayCode, replayResult := serveJSON(t, r, "POST", "/mail/keys=fixture-card/send", "", payload)
	if replayCode != 200 || replayResult["data"].(map[string]any)["status"] != "running" || replayResult["data"].(map[string]any)["replayed"] != true || smtp.calls.Load() != 1 {
		t.Fatalf("running POST replay was not recovered: %v", replayResult)
	}
	for _, path := range []string{"/mail/keys=fixture-card/messages", "/mail/keys=fixture-card/all", "/mail/keys=fixture-card/all/sender@example.invalid", "/mail/keys=fixture-card/send"} {
		method := "POST"
		if strings.Contains(path, "/all") {
			method = "GET"
		}
		body, _ := json.Marshal(publicSendPayload("fixture-send-request-0002"))
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.RemoteAddr = "192.0.2.22:4567"
		req.Header.Set("User-Agent", "another browser")
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		if res.Code != 409 {
			t.Fatalf("concurrent %s admitted: %d %s", path, res.Code, res.Body.String())
		}
	}
	// A closed browser cannot prevent the accepted message from being charged.
	cancel()
	close(release)
	code, result := waitPublicSend(t, done)
	data := result["data"].(map[string]any)
	if code != 200 || data["status"] != "success" || data["used_count"] != float64(1) || data["remaining"] != float64(0) {
		t.Fatalf("settlement: %v", result)
	}
	code, result = serveJSON(t, r, "POST", "/mail/keys=fixture-card/send", "", payload)
	if code != 200 || result["data"].(map[string]any)["replayed"] != true || smtp.calls.Load() != 1 {
		t.Fatal("POST replay sent or charged twice")
	}
	for _, path := range []string{"/mail/keys=fixture-card/messages", "/mail/keys=fixture-card/send"} {
		code, _ := serveJSON(t, r, "POST", path, "", publicSendPayload("fixture-send-request-0003"))
		if code != 403 {
			t.Fatalf("exhausted card admitted: %s %d", path, code)
		}
	}
	var kind, state, recipient, subject string
	if err := db.QueryRow(`SELECT operation_type,operation_status,recipient,mail_subject FROM card_key_use_logs`).Scan(&kind, &state, &recipient, &subject); err != nil {
		t.Fatal(err)
	}
	if kind != "send" || state != "success" || recipient != "recipient@example.invalid" || subject != "Fixture subject" {
		t.Fatal("incorrect send log")
	}
	if code, _ := serveJSON(t, r, "POST", "/mail/keys=fixture-card/send", "", map[string]any{"request_id": payload["request_id"], "recipient": "different@example.invalid"}); code != 409 {
		t.Fatal("request identifier reused with different content")
	}
}

func TestDatabasePublicMailFailureAndUnknown(t *testing.T) {
	for _, mode := range []string{"data-failed", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			db, _ := regressionDatabase(t)
			smtp := newPublicSMTPFixture(t, mode, nil)
			insertPublicSendFixture(t, db, smtp, 1)
			r := publicMailTestRouter()
			code, result := serveJSON(t, r, "POST", "/mail/keys=fixture-card/send", "", publicSendPayload("fixture-send-request-0001"))
			data, ok := result["data"].(map[string]any)
			if !ok {
				t.Fatalf("missing send result: %d %v", code, result)
			}
			if data["used_count"] != float64(0) {
				t.Fatal("non-success charged quota")
			}
			if mode == "data-failed" {
				if code != 400 || data["status"] != "failed" || data["remaining"] != float64(1) || data["reserved_count"] != float64(0) {
					t.Fatalf("failure not released: %v", result)
				}
				return
			}
			if code != 200 || data["status"] != "unknown" || data["remaining"] != float64(0) || data["reserved_count"] != float64(1) {
				t.Fatalf("unknown was refunded: %v", result)
			}
			// Recreating handlers and clearing display logs cannot release quota.
			if _, err := db.Exec(`DELETE FROM card_key_use_logs`); err != nil {
				t.Fatal(err)
			}
			r = publicMailTestRouter()
			if code, _ := serveJSON(t, r, "POST", "/mail/keys=fixture-card/messages", "", nil); code != 403 {
				t.Fatal("unknown reservation was bypassed by receiving")
			}
			code, replay := serveJSON(t, r, "POST", "/mail/keys=fixture-card/send", "", publicSendPayload("fixture-send-request-0001"))
			if code != 200 || replay["data"].(map[string]any)["status"] != "unknown" || smtp.calls.Load() != 1 {
				t.Fatal("unknown delivery retried")
			}
			operation := data["operation_id"].(string)
			if code, _ := serveJSON(t, r, "POST", "/resolve/"+operation, "", map[string]string{"status": "success"}); code != 200 {
				t.Fatal("confirmation failed")
			}
			if code, _ := serveJSON(t, r, "POST", "/resolve/"+operation, "", map[string]string{"status": "failed"}); code != 409 {
				t.Fatal("settled result was reversed")
			}
			var used, reserved, logs int
			db.QueryRow(`SELECT COALESCE(SUM(charged_count),0) FROM card_key_mail_access_records`).Scan(&used)
			db.QueryRow(`SELECT COALESCE(SUM(reserved_count),0) FROM card_key_mail_operations`).Scan(&reserved)
			db.QueryRow(`SELECT COUNT(*) FROM card_key_use_logs WHERE operation_status='success'`).Scan(&logs)
			if used != 1 || reserved != 0 || logs != 1 {
				t.Fatalf("confirmation used=%d reserved=%d logs=%d", used, reserved, logs)
			}
		})
	}
}

func TestDatabasePublicMailReadRulesAndLeaseRecovery(t *testing.T) {
	db, _ := regressionDatabase(t)
	card := insertPublicSendFixture(t, db, nil, 3)
	ctx := context.Background()
	for i, key := range []string{"message-1", "message-1", ""} {
		op, _, _, _, err := beginPublicMailOperation(ctx, db, card, publicMailOperation{Kind: "receive", RequestID: fmt.Sprintf("read-%d", i), Email: card.BoundEmail})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, _, err := beginPublicMailOperation(ctx, db, card, publicMailOperation{Kind: "receive", RequestID: "another-read", Email: card.BoundEmail}); err == nil {
			t.Fatal("concurrent reads allowed")
		}
		if _, _, _, _, err := beginPublicMailOperation(ctx, db, card, publicMailOperation{Kind: "send", RequestID: "another-send", Email: card.BoundEmail}); err == nil {
			t.Fatal("send while receiving allowed")
		}
		if _, _, _, err := beginPublicMailRead(ctx, db, card, card.BoundEmail, "device", "192.0.2.1"); err != nil {
			t.Fatal(err)
		}
		quota, charged, err := finishPublicMailRead(ctx, db, card, op, "device", key, "Fixture received subject")
		if err != nil || charged != (i == 0) || quota.UsedCount != 1 || quota.Busy {
			t.Fatalf("receive rules: %+v charged=%v err=%v", quota, charged, err)
		}
		db.Exec(`UPDATE card_key_mail_access_records SET last_request_at=NOW()-INTERVAL '1 minute'`)
	}
	var logs int
	db.QueryRow(`SELECT COUNT(*) FROM card_key_use_logs WHERE operation_type='receive'`).Scan(&logs)
	if logs != 3 {
		t.Fatal("receive logs lost")
	}
	op, _, _, _, err := beginPublicMailOperation(ctx, db, card, publicMailOperation{Kind: "send", RequestID: "interrupted-send", Email: card.BoundEmail})
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`UPDATE card_key_mail_operations SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, op.ID)
	_, quota, err := loadPublicMailSnapshot(ctx, db, card)
	if err != nil || quota.Busy || quota.ReservedCount != 1 || quota.Remaining != 1 {
		t.Fatalf("interrupted send lost reservation: %+v %v", quota, err)
	}
	if _, _, err := finishPublicMailSend(ctx, db, op.ID, "failed", "checked fixture", true); err != nil {
		t.Fatal(err)
	}
	_, quota, err = loadPublicMailSnapshot(ctx, db, card)
	if err != nil || quota.Remaining != 2 || quota.UsedCount != 1 {
		t.Fatal("failed confirmation did not release only its reservation")
	}
}

func TestDatabasePublicMailSettlementFailureKeepsReservation(t *testing.T) {
	db, _ := regressionDatabase(t)
	smtp := newPublicSMTPFixture(t, "success", nil)
	card := insertPublicSendFixture(t, db, smtp, 1)
	_, err := db.Exec(`CREATE FUNCTION reject_send_settlement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='success' THEN RAISE EXCEPTION 'fixture settlement failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER reject_send_settlement BEFORE UPDATE ON card_key_mail_operations FOR EACH ROW EXECUTE FUNCTION reject_send_settlement()`)
	if err != nil {
		t.Fatal(err)
	}
	r := publicMailTestRouter()
	_, result := serveJSON(t, r, "POST", "/mail/keys=fixture-card/send", "", publicSendPayload("fixture-send-request-0001"))
	if result["data"].(map[string]any)["status"] != "unknown" {
		t.Fatal("failed settlement was reported as a free failure")
	}
	db.Exec(`UPDATE card_key_mail_operations SET expires_at=NOW()-INTERVAL '1 second'`)
	_, quota, err := loadPublicMailSnapshot(context.Background(), db, card)
	if err != nil || quota.UsedCount != 0 || quota.ReservedCount != 1 || quota.Remaining != 0 || smtp.calls.Load() != 1 {
		t.Fatalf("non-atomic settlement: %+v %v", quota, err)
	}
}

func TestDatabasePublicMailConcurrentStarts(t *testing.T) {
	db, _ := regressionDatabase(t)
	card := insertPublicSendFixture(t, db, nil, 1)
	start := make(chan struct{})
	type outcome struct {
		operation publicMailOperation
		created   bool
		err       error
	}
	results := make(chan outcome, 16)
	for i := 0; i < cap(results); i++ {
		go func(i int) {
			<-start
			kind := "receive"
			if i%2 == 0 {
				kind = "send"
			}
			op, _, _, created, err := beginPublicMailOperation(context.Background(), db, card, publicMailOperation{
				Kind: kind, RequestID: fmt.Sprintf("concurrent-%d", i), Email: card.BoundEmail,
			})
			results <- outcome{op, created, err}
		}(i)
	}
	close(start)
	accepted := 0
	for i := 0; i < cap(results); i++ {
		select {
		case result := <-results:
			if result.err == nil && result.created {
				accepted++
				continue
			}
			var busy *publicMailOperationError
			if !errors.As(result.err, &busy) || busy.Status != http.StatusConflict {
				t.Fatalf("unexpected concurrent result: %v", result.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent operation deadlock")
		}
	}
	if accepted != 1 {
		t.Fatalf("one credit admitted %d operations", accepted)
	}
	// Another card remains independent; the lock is not global to the service.
	other := publicCardKey{ID: 2, Key: "other-fixture-card", UsageLimit: 1}
	if _, err := db.Exec(`INSERT INTO card_keys(id,key,usage_limit) VALUES (2,$1,1)`, other.Key); err != nil {
		t.Fatal(err)
	}
	if _, _, _, created, err := beginPublicMailOperation(context.Background(), db, other, publicMailOperation{Kind: "receive", RequestID: "independent-read", Email: card.BoundEmail}); err != nil || !created {
		t.Fatalf("different card blocked: %v", err)
	}
}

func TestDatabasePublicMailPendingLogSurvivesCleanup(t *testing.T) {
	db, _ := regressionDatabase(t)
	card := insertPublicSendFixture(t, db, nil, 1)
	op, _, _, _, err := beginPublicMailOperation(context.Background(), db, card, publicMailOperation{Kind: "send", RequestID: "cleanup-pending-send", Email: card.BoundEmail})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`WITH expired AS (UPDATE card_key_mail_operations SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1)
		UPDATE card_key_use_logs SET used_at=NOW()-INTERVAL '30 days' WHERE operation_id=$1`, op.ID); err != nil {
		t.Fatal(err)
	}
	if err := recoverPublicMailLogOperations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := cleanupCardKeyUseLogsWithDB(context.Background(), db, 1); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.QueryRow(`SELECT operation_status FROM card_key_use_logs WHERE operation_id=$1`, op.ID).Scan(&status); err != nil || status != "unknown" {
		t.Fatalf("pending log removed: %s %v", status, err)
	}
}

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type quickIMAPFixture struct {
	count     int
	noJunk    bool
	failList  bool
	bodyDelay time.Duration
	logins    atomic.Int32
	bodies    atomic.Int32
	active    atomic.Int32
	peak      atomic.Int32
}

func (f *quickIMAPFixture) session(t *testing.T, ctx context.Context) (*quickIMAPSession, error) {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	t.Cleanup(func() { _ = client.Close(); _ = server.Close(); <-done })
	go func() {
		defer close(done)
		defer server.Close()
		_, _ = io.WriteString(server, "* OK fixture\r\n")
		reader := bufio.NewReader(server)
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
				f.logins.Add(1)
				_, err = fmt.Fprintf(server, "%s OK login\r\n", tag)
			case "EXAMINE":
				count := f.count
				if strings.Contains(line, "Junk") {
					count = 0
				}
				_, err = fmt.Fprintf(server, "* %d EXISTS\r\n* OK [UIDVALIDITY 42] valid\r\n%s OK [READ-ONLY] selected\r\n", count, tag)
			case "LIST":
				if f.failList {
					_, err = fmt.Fprintf(server, "%s NO list unavailable\r\n", tag)
				} else {
					_, err = io.WriteString(server, "* LIST (\\HasNoChildren) \"/\" \"INBOX\"\r\n")
					if !f.noJunk {
						_, err = io.WriteString(server, "* LIST (\\Junk) \"/\" \"Junk\"\r\n")
					}
					if err == nil {
						_, err = fmt.Fprintf(server, "%s OK list\r\n", tag)
					}
				}
			case "FETCH":
				var start, end int
				_, _ = fmt.Sscanf(fields[2], "%d:%d", &start, &end)
				for uid := start; uid <= end && err == nil; uid++ {
					header := quickFixtureMail(uid, false)
					_, err = fmt.Fprintf(server, "* %d FETCH (UID %d BODY[HEADER.FIELDS (SUBJECT FROM TO DATE)] {%d}\r\n%s)\r\n", uid, uid, len(header), header)
				}
				if err == nil {
					_, err = fmt.Fprintf(server, "%s OK fetched\r\n", tag)
				}
			case "UID":
				var uid int
				_, _ = fmt.Sscanf(fields[3], "%d", &uid)
				f.bodies.Add(1)
				active := f.active.Add(1)
				for previous := f.peak.Load(); active > previous && !f.peak.CompareAndSwap(previous, active); previous = f.peak.Load() {
				}
				if f.bodyDelay > 0 {
					select {
					case <-time.After(f.bodyDelay):
					case <-ctx.Done():
					}
				}
				raw := quickFixtureMail(uid, true)
				_, err = fmt.Fprintf(server, "* %d FETCH (UID %d BODY[] {%d}\r\n%s)\r\n%s OK fetched\r\n", uid, uid, len(raw), raw, tag)
				f.active.Add(-1)
			default:
				_, err = fmt.Fprintf(server, "%s BAD unsupported\r\n", tag)
			}
			if err != nil {
				return
			}
		}
	}()
	return newQuickIMAPSession(ctx, client, mailAccountTestConfig{Email: "fixture@example.invalid", Password: "fixture"})
}

func quickFixtureMail(uid int, body bool) string {
	header := fmt.Sprintf("Subject: Message %d\r\nFrom: sender@example.invalid\r\nTo: fixture@example.invalid\r\nDate: Sat, 17 Oct 2026 00:00:00 +0000\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n", uid)
	if body {
		return header + fmt.Sprintf("Body %d\r\n", uid)
	}
	return header
}

func TestQuickMailSummaryDoesNotFetchBodies(t *testing.T) {
	for _, count := range []int{10, 30, 50} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			fixture := &quickIMAPFixture{count: count}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			session, err := fixture.session(t, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			result, warnings, err := receiveQuickIMAPHeadersFromSession(session, count)
			if err != nil || len(warnings) != 0 || len(result.Inbox) != count || len(result.Trash) != 0 {
				t.Fatalf("headers: count=%d warnings=%v err=%v", len(result.Inbox), warnings, err)
			}
			seen := make(map[int]bool)
			for _, item := range result.Inbox {
				if seen[item.UID] || item.UIDValidity != 42 {
					t.Fatalf("duplicate or invalid header: %+v", item)
				}
				seen[item.UID] = true
			}
			if fixture.logins.Load() != 1 || fixture.bodies.Load() != 0 {
				t.Fatalf("summary used logins=%d body reads=%d", fixture.logins.Load(), fixture.bodies.Load())
			}
		})
	}
}

func TestQuickMailFullReadReusesTwoConnectionsAndKeepsOrder(t *testing.T) {
	fixture := &quickIMAPFixture{count: 50, bodyDelay: 3 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	items := make([]receivedMailMessage, 50)
	for i := range items {
		items[i] = receivedMailMessage{UID: 50 - i, UIDValidity: 42, Mailbox: "INBOX", Folder: "inbox"}
	}
	result, err := collectQuickMailDetails(ctx, items, 2, func(ctx context.Context) (quickMailDetailSession, error) {
		session, err := fixture.session(t, ctx)
		if err != nil {
			return nil, err
		}
		return session, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != len(items) || fixture.logins.Load() != 2 || fixture.peak.Load() != 2 {
		t.Fatalf("unexpected result=%d logins=%d peak=%d", len(result), fixture.logins.Load(), fixture.peak.Load())
	}
	for i, detail := range result {
		if detail.UID != items[i].UID || strings.TrimSpace(detail.Body) != fmt.Sprintf("Body %d", items[i].UID) {
			t.Fatalf("wrong item at %d: %+v", i, detail)
		}
	}
}

func TestQuickIMAPCancellationInterruptsBlockedRead(t *testing.T) {
	fixture := &quickIMAPFixture{count: 1, bodyDelay: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, err := fixture.session(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result := make(chan error, 1)
	go func() {
		_, err := session.readDetail(receivedMailMessage{UID: 1, Mailbox: "INBOX", Folder: "inbox"})
		result <- err
	}()
	deadline := time.After(time.Second)
	for fixture.bodies.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("body read never started")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled read succeeded")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cancel did not interrupt the socket")
	}
}

func TestQuickIMAPRejectsStaleMailboxUIDs(t *testing.T) {
	fixture := &quickIMAPFixture{count: 1}
	session, err := fixture.session(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	_, err = session.readDetail(receivedMailMessage{UID: 1, UIDValidity: 99, Mailbox: "INBOX", Folder: "inbox"})
	if err == nil || fixture.bodies.Load() != 0 {
		t.Fatal("stale mailbox UID was fetched")
	}
}

func TestQuickIMAPKeepsInboxWhenJunkListingFails(t *testing.T) {
	fixture := &quickIMAPFixture{count: 10, failList: true}
	session, err := fixture.session(t, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, warnings, err := receiveQuickIMAPHeadersFromSession(session, 10)
	if err != nil || len(result.Inbox) != 10 || len(warnings) != 1 {
		t.Fatalf("lost partial results: %+v %v %v", result, warnings, err)
	}
}

func TestQuickIMAPFolderDiscoveryPrefersProviderJunkAndSkipsGuesses(t *testing.T) {
	boxes := []imapMailboxInfo{
		{Name: "Trash", DecodedName: "Trash"},
		{Name: "INBOX"},
		{Name: "ProviderSpam", Flags: `\Junk`},
		{Name: "Spam", Flags: `\Noselect`},
	}
	candidates := quickIMAPTrashCandidates(boxes)
	if len(candidates) != 2 || candidates[0] != "ProviderSpam" || candidates[1] != "Trash" {
		t.Fatalf("wrong folder priority or nonexistent folder probed: %v", candidates)
	}
	if candidates := quickIMAPTrashCandidates([]imapMailboxInfo{{Name: "INBOX"}}); len(candidates) != 0 {
		t.Fatalf("guessed folders despite successful LIST: %v", candidates)
	}
}

func TestQuickMailConnectionLimitAndCancellation(t *testing.T) {
	account := mailAccountTestConfig{Email: "limits@example.invalid", ImapHost: "mail.example.invalid", ImapPort: 993}
	first, err := acquireQuickMailConnection(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	second, err := acquireQuickMailConnection(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	defer second()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if release, err := acquireQuickMailConnection(ctx, account); !errors.Is(err, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		t.Fatalf("third connection was not limited: %v", err)
	}
	first()
	third, err := acquireQuickMailConnection(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	third()
}

func TestQuickMailDetailRequiresAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"imap", "outlook"} {
		writer := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(writer)
		c.Request = httptest.NewRequest(http.MethodPost, "/detail", strings.NewReader(`{"email":"fixture@example.invalid","folder":"inbox","uid":1,"message_id":"fixture"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		state := &appState{}
		if mode == "imap" {
			state.quickMailDetailIMAP(c)
		} else {
			state.quickMailDetailOutlook(c)
		}
		if writer.Code != http.StatusUnauthorized {
			t.Fatalf("%s detail bypassed auth: %d", mode, writer.Code)
		}
	}
}

func TestQuickMailSummaryFlagPreservesLegacyDefault(t *testing.T) {
	for _, entry := range []struct {
		body  string
		wants bool
	}{{`{}`, true}, {`{"include_body":true}`, true}, {`{"include_body":false}`, false}} {
		var req quickMailReceiveRequest
		if err := json.Unmarshal([]byte(entry.body), &req); err != nil {
			t.Fatal(err)
		}
		if req.wantsBody() != entry.wants {
			t.Fatalf("wrong default for %s", entry.body)
		}
	}
}

func TestQuickMailTimeoutReturnsGatewayTimeout(t *testing.T) {
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/receive", nil)
	ctx, cancel := context.WithDeadline(c.Request.Context(), time.Now().Add(-time.Second))
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	writeQuickMailError(c, "读取邮件正文", context.DeadlineExceeded)
	if writer.Code != http.StatusGatewayTimeout || !strings.Contains(writer.Body.String(), "超时") {
		t.Fatalf("wrong timeout response: %d %s", writer.Code, writer.Body.String())
	}
}

func TestQuickMailRejectsInvalidDetailIdentifiers(t *testing.T) {
	req := quickMailDetailRequest{UID: 1, Folder: "inbox", Mailbox: "INBOX"}
	if !validQuickMailDetail(req, false) {
		t.Fatal("valid identifier rejected")
	}
	req.Mailbox = "INBOX\r\nLOGOUT"
	if validQuickMailDetail(req, false) {
		t.Fatal("accepted a protocol control character")
	}
}

func TestFetchHeadersAcceptsUIDAfterLiteral(t *testing.T) {
	var wire bytes.Buffer
	for uid := 1; uid <= 3; uid++ {
		header := quickFixtureMail(uid, false)
		fmt.Fprintf(&wire, "* %d FETCH (BODY[HEADER.FIELDS (SUBJECT FROM TO DATE)] {%d}\r\n%s UID %d)\r\n", uid, len(header), header, uid)
	}
	wire.WriteString("q1 OK done\r\n")
	items, err := readFetchHeaders(bufio.NewReader(&wire), "q1", "inbox", "INBOX")
	if err != nil || len(items) != 3 {
		t.Fatalf("lost headers: %+v %v", items, err)
	}
	for i, item := range items {
		if item.UID != i+1 {
			t.Fatalf("lost UID: %+v", item)
		}
	}
}

func TestQuickMailWorkerFailureCancelsOtherWorkers(t *testing.T) {
	var closed atomic.Int32
	var started sync.WaitGroup
	started.Add(2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := collectQuickMailDetails(ctx, []receivedMailMessage{{UID: 1}, {UID: 2}, {UID: 3}}, 2, func(ctx context.Context) (quickMailDetailSession, error) {
		return &quickMailFailingFixture{ctx: ctx, started: &started, closed: &closed}, nil
	})
	if err == nil || closed.Load() != 2 {
		t.Fatalf("workers not cleaned up: err=%v closed=%d", err, closed.Load())
	}
}

type quickMailFailingFixture struct {
	ctx     context.Context
	started *sync.WaitGroup
	closed  *atomic.Int32
}

func (f *quickMailFailingFixture) Close() error { f.closed.Add(1); return nil }
func (f *quickMailFailingFixture) readDetail(item receivedMailMessage) (receiveMailDetailResponse, error) {
	f.started.Done()
	f.started.Wait()
	if item.UID == 1 {
		return receiveMailDetailResponse{}, errors.New("fixture read failed")
	}
	<-f.ctx.Done()
	return receiveMailDetailResponse{}, f.ctx.Err()
}

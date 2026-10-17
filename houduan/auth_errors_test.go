package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mail-admin/houduan/ent"

	"github.com/gin-gonic/gin"
)

func TestSessionUnauthorizedIsDistinctFromQuickMailKeyFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	state := &appState{sessions: newSessionStore()}
	current := &ent.User{ID: 1, Username: "fixture-admin", PasswordHash: "fixture-hash", Role: "admin", Enabled: true}
	validToken, _ := state.sessions.issue(current)
	state.sessions.sessions["fixture-expired-session"] = authSession{UserID: 1, Kind: accessSession, ExpiresAt: time.Now().Add(-time.Second)}
	engine := gin.New()
	engine.GET("/api/user/profile", state.authMiddleware(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	for _, token := range []string{"", "fixture-unknown-session", "fixture-expired-session"} {
		t.Run(token, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/user/profile", nil)
			if token != "" {
				request.Header.Set("Authorization", "Bearer "+token)
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || response.Header().Get(authErrorHeader) != authErrorSession {
				t.Fatalf("session rejection lost its auth scope: status=%d headers=%v", response.Code, response.Header())
			}
		})
	}

	// An old rejected request must not revoke the independently issued current session.
	request := httptest.NewRequest(http.MethodGet, "/api/user/profile", nil)
	request.Header.Set("Authorization", "Bearer "+validToken)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get(authErrorHeader) != "" {
		t.Fatalf("valid session was rejected: status=%d headers=%v", response.Code, response.Header())
	}
}

func TestQuickMailReceiveAndDetailKeepKeyErrorsLocalToTheReader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	state := &appState{sessions: newSessionStore()}
	handlers := map[string]gin.HandlerFunc{
		"imap/receive":    state.quickMailReceiveIMAP,
		"imap/detail":     state.quickMailDetailIMAP,
		"outlook/receive": state.quickMailReceiveOutlook,
		"outlook/detail":  state.quickMailDetailOutlook,
	}
	for name, handler := range handlers {
		for _, token := range []string{"", "fixture-previous-session"} {
			t.Run(name+"/"+token, func(t *testing.T) {
				response := httptest.NewRecorder()
				context, _ := gin.CreateTestContext(response)
				context.Request = httptest.NewRequest(http.MethodPost, "/api/admin/quick-mail/"+name,
					strings.NewReader(`{"email":"fixture@example.invalid","uid":1,"uid_validity":42,"mailbox":"INBOX","folder":"inbox","message_id":"fixture"}`))
				context.Request.Header.Set("Content-Type", "application/json")
				if token != "" {
					context.Request.Header.Set("Authorization", "Bearer "+token)
				}
				handler(context)
				if response.Code != http.StatusUnauthorized || response.Header().Get(authErrorHeader) != authErrorQuickMailKey {
					t.Fatalf("reader key rejection was confused with backend logout: status=%d headers=%v", response.Code, response.Header())
				}
			})
		}
	}
}

func TestQuickMailPlainKeyFailurePreservesUnauthorizedStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodGet, "/imap/mail/fixture/all/fixture", nil)
	if (&appState{}).authorizeQuickMailPlainRequest(context, "") {
		t.Fatal("missing key was accepted")
	}
	if response.Code != http.StatusUnauthorized || response.Header().Get(authErrorHeader) != authErrorQuickMailKey {
		t.Fatalf("plain reader key rejection lost its scope: status=%d headers=%v", response.Code, response.Header())
	}
}

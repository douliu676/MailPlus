package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"mail-admin/houduan/ent"
)

func sessionFixtureUser() *ent.User {
	return &ent.User{ID: 7, Username: "renamed-admin", PasswordHash: "synthetic-password-hash", Role: "admin", Enabled: true}
}

func TestAccessAndRefreshHaveDifferentPurposes(t *testing.T) {
	state := &appState{sessions: newSessionStore()}
	access, refresh := state.sessions.issue(sessionFixtureUser())
	engine := gin.New()
	engine.GET("/protected", state.authMiddleware(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for _, tc := range []struct {
		token  string
		status int
	}{{access, 204}, {refresh, 401}} {
		request := httptest.NewRequest(http.MethodGet, "/protected", nil)
		request.Header.Set("Authorization", "Bearer "+tc.token)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("status=%d want=%d", response.Code, tc.status)
		}
	}
}

func TestRefreshRotationIsSingleUseAndLogoutRevokesItsPair(t *testing.T) {
	store := newSessionStore()
	user := sessionFixtureUser()
	access, refresh := store.issue(user)
	var wg sync.WaitGroup
	results := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if next, _, ok := store.rotate(refresh, user); ok {
				results <- next
			}
		}()
	}
	wg.Wait()
	close(results)
	if len(results) != 1 {
		t.Fatalf("refresh accepted %d times", len(results))
	}
	if _, ok := store.get(access); ok {
		t.Fatal("old access token survived rotation")
	}
	newAccess := <-results
	store.revokeTokenFamily(newAccess)
	if len(store.sessions) != 0 {
		t.Fatal("logout left another token in the session family")
	}
}

func TestCredentialBindingAndUserRevocation(t *testing.T) {
	store := newSessionStore()
	user := sessionFixtureUser()
	a, r := store.issue(user)
	old, _ := store.get(a)
	user.Username = "new-admin-name"
	if old.Credentials == credentialFingerprint(user) {
		t.Fatal("rename did not change credential binding")
	}
	if _, _, ok := store.rotate(r, user); ok {
		t.Fatal("old refresh token worked after rename")
	}
	user.PasswordHash = "new-synthetic-hash"
	store.issue(user)
	store.revokeUser(user.ID)
	if len(store.sessions) != 0 {
		t.Fatal("credential change did not revoke every login")
	}
	if err := canSetUserEnabled(user, false); err == nil {
		t.Fatal("administrator may be disabled")
	}
}

package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"mail-admin/houduan/ent"

	"github.com/gin-gonic/gin"
)

type sessionKind string

const (
	accessSession     sessionKind = "access"
	refreshSession    sessionKind = "refresh"
	accessSessionTTL              = 2 * time.Hour
	refreshSessionTTL             = 24 * time.Hour
)

var errInvalidSession = errors.New("invalid session")

type authSession struct {
	UserID      int
	ExpiresAt   time.Time
	Kind        sessionKind
	Family      string
	Credentials string
}

type sessionStore struct {
	mu       sync.RWMutex
	sessions map[string]authSession
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]authSession)}
}

// Binding sessions to credentials also rejects a login racing with a password
// change, and sessions held by another backend using the same database.
func credentialFingerprint(current *ent.User) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(current.Username+"\x00"+current.PasswordHash)))
}

func (s *sessionStore) get(token string) (authSession, bool) {
	if s == nil || token == "" {
		return authSession{}, false
	}
	s.mu.RLock()
	session, ok := s.sessions[token]
	s.mu.RUnlock()
	return session, ok && time.Now().Before(session.ExpiresAt)
}

func (s *sessionStore) issue(current *ent.User) (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issueLocked(current)
}

func (s *sessionStore) issueLocked(current *ent.User) (string, string) {
	now := time.Now()
	for token, session := range s.sessions {
		if !now.Before(session.ExpiresAt) {
			delete(s.sessions, token)
		}
	}
	access, refresh, family := newToken(), newToken(), newToken()
	credentials := credentialFingerprint(current)
	s.sessions[access] = authSession{current.ID, now.Add(accessSessionTTL), accessSession, family, credentials}
	s.sessions[refresh] = authSession{current.ID, now.Add(refreshSessionTTL), refreshSession, family, credentials}
	return access, refresh
}

func (s *sessionStore) revokeUser(userID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, session := range s.sessions {
		if session.UserID == userID {
			delete(s.sessions, token)
		}
	}
}

func (s *sessionStore) revokeFamilyLocked(family string) {
	for token, session := range s.sessions {
		if session.Family == family {
			delete(s.sessions, token)
		}
	}
}

func (s *sessionStore) revokeTokenFamily(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session, ok := s.sessions[token]; ok {
		s.revokeFamilyLocked(session.Family)
	}
}

func (s *sessionStore) rotate(token string, current *ent.User) (string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[token]
	if !ok || session.Kind != refreshSession || !time.Now().Before(session.ExpiresAt) ||
		session.UserID != current.ID || session.Credentials != credentialFingerprint(current) {
		return "", "", false
	}
	s.revokeFamilyLocked(session.Family)
	access, refresh := s.issueLocked(current)
	return access, refresh, true
}

func bearerToken(c *gin.Context) string {
	header := strings.TrimSpace(c.GetHeader("Authorization"))
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return ""
	}
	return strings.TrimSpace(header[len("Bearer "):])
}

func (s *appState) sessionUser(ctx context.Context, session authSession) (*ent.User, error) {
	current, err := s.db.User.Get(ctx, session.UserID)
	if ent.IsNotFound(err) {
		return nil, errInvalidSession
	}
	if err != nil {
		return nil, err
	}
	if !current.Enabled || current.Role != "admin" || session.Credentials != credentialFingerprint(current) {
		return nil, errInvalidSession
	}
	return current, nil
}

func writeSessionError(c *gin.Context, err error) {
	if !errors.Is(err, errInvalidSession) {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, apiResponse{Code: 503, Msg: "暂时无法验证登录状态，请稍后重试"})
		return
	}
	c.Header(authErrorHeader, authErrorSession)
	c.AbortWithStatusJSON(http.StatusUnauthorized, apiResponse{Code: 401, Msg: "登录已失效，请重新登录"})
}

func authPayload(current *ent.User, access, refresh string, mustChange bool) authResponse {
	return authResponse{AccessToken: access, RefreshToken: refresh, ExpiresIn: int(accessSessionTTL.Seconds()),
		TokenType: "Bearer", MustChangePassword: mustChange, User: toAuthUser(current)}
}

func (s *appState) refreshAuth(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	session, ok := s.sessions.get(req.RefreshToken)
	if !ok || session.Kind != refreshSession {
		writeSessionError(c, errInvalidSession)
		return
	}
	current, err := s.sessionUser(c.Request.Context(), session)
	if err != nil {
		writeSessionError(c, err)
		return
	}
	access, refresh, ok := s.sessions.rotate(req.RefreshToken, current)
	if !ok {
		writeSessionError(c, errInvalidSession)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: authPayload(current, access, refresh, userMustChangePassword(c.Request.Context(), current.ID)), Msg: "ok"})
}

func (s *appState) logout(c *gin.Context) {
	// Logout is idempotent, including when the session has expired already.
	s.sessions.revokeTokenFamily(bearerToken(c))
	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
}

func validateNewPassword(password string) error {
	if len(password) < 8 || len(password) > 72 {
		return errors.New("密码需要 8 至 72 个字节")
	}
	if password == defaultAdminPassword {
		return errors.New("新密码不能继续使用初始密码")
	}
	return nil
}

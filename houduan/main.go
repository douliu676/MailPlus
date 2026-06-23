package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	stdmail "net/mail"
	"net/smtp"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"mail-admin/houduan/ent"
	"mail-admin/houduan/ent/systemsetting"
	"mail-admin/houduan/ent/user"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	encryptedzip "github.com/alexmullins/zip"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

const (
	defaultAdminAccount    = "admin"
	defaultAdminPassword   = "admin123"
	defaultPublicLogoPath  = "/logo.png"
	exportIDBatchSize      = 5000
	taskProgressBatchSize  = 5000
	taskResultCleanupDelay = 10 * time.Minute
	taskCompletedMaxAge    = 24 * time.Hour
	taskStaleMaxAge        = 3 * 24 * time.Hour
	batchDeleteSize        = 1000
	updateCheckTimeout     = 8 * time.Second
	updateCheckCacheTTL    = 20 * time.Minute
	updateCheckMaxBodySize = 1 << 20
	updateCheckMaxAttempts = 3
	updateCheckRetryDelay  = 600 * time.Millisecond
	databaseBackupTimeout  = 10 * time.Minute
	databaseRestoreTimeout = 30 * time.Minute
	restoreRestartDelay    = 1500 * time.Millisecond
	restoreRestartExitCode = 42
	githubLatestReleaseAPI = "https://api.github.com/repos/%s/releases/latest"
	githubLatestReleaseWeb = "https://github.com/%s/releases/latest"
)

var (
	defaultPublicLogoOnce  sync.Once
	defaultPublicLogoValue string
)

// Override these at build time with:
// -ldflags "-X main.appVersion=v1.2.3 -X main.appUpdateGitHubRepo=owner/repo"
var (
	appVersion          = "v1.0.1"
	appUpdateGitHubRepo = "douliu676/MailPlus"
)

var removedSystemSettingKeys = []string{
	"api_base_url",
	"app_update_check_url",
	"contact_info",
	"custom_menu_items",
	"custom_endpoints",
	"doc_url",
	"frontend_url",
	"home_content",
	"hide_ccs_import_button",
	"login_agreement_enabled",
	"login_agreement_mode",
	"login_agreement_updated_at",
	"login_agreement_documents",
	"registration_enabled",
	"email_verify_enabled",
	"registration_email_suffix_whitelist",
	"promo_code_enabled",
	"password_reset_enabled",
	"invitation_code_enabled",
	"totp_enabled",
	"totp_encryption_key_configured",
	"api_key_acl_trust_forwarded_ip",
	"turnstile_enabled",
	"turnstile_site_key",
	"turnstile_secret_key",
	"turnstile_secret_key_configured",
	"smtp_host",
	"smtp_port",
	"smtp_username",
	"smtp_password",
	"smtp_password_configured",
	"smtp_from_email",
	"smtp_from_name",
	"smtp_use_tls",
	"backup_s3_endpoint",
	"backup_s3_region",
	"backup_s3_bucket",
	"backup_s3_prefix",
	"backup_s3_access_key_id",
	"backup_s3_secret_access_key",
	"backup_s3_force_path_style",
	"backup_schedule_cron_expr",
	"backup_schedule_retain_days",
}

type appState struct {
	db           *ent.Client
	sessions     *sessionStore
	outlookOAuth *outlookOAuthStore
	tasks        *taskStore
	proxies      *proxyRuntime
	updates      *updateCheckCache
	backups      *backupScheduler
}

type authSession struct {
	UserID    int
	ExpiresAt time.Time
}

type sessionStore struct {
	mu       sync.RWMutex
	sessions map[string]authSession
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: map[string]authSession{}}
}

func (s *sessionStore) set(token string, userID int, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[token] = authSession{UserID: userID, ExpiresAt: time.Now().Add(ttl)}
}

func (s *sessionStore) get(token string) (authSession, bool) {
	s.mu.RLock()
	session, ok := s.sessions[token]
	s.mu.RUnlock()
	if !ok {
		return authSession{}, false
	}
	if time.Now().After(session.ExpiresAt) {
		s.mu.Lock()
		delete(s.sessions, token)
		s.mu.Unlock()
		return authSession{}, false
	}
	return session, true
}

type apiResponse struct {
	Code int         `json:"code"`
	Data interface{} `json:"data,omitempty"`
	Msg  string      `json:"msg"`
}

type backgroundTask struct {
	ID                 string `json:"id"`
	Type               string `json:"type"`
	Status             string `json:"status"`
	Total              int    `json:"total"`
	Done               int    `json:"done"`
	Success            int    `json:"success"`
	Failed             int    `json:"failed"`
	Message            string `json:"message"`
	FileName           string `json:"file_name,omitempty"`
	DownloadURL        string `json:"download_url,omitempty"`
	ResultPath         string `json:"-"`
	ResultCleanupAfter string `json:"result_cleanup_after,omitempty"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

type taskStore struct {
	databaseURL string
}

func newTaskStore() *taskStore {
	return &taskStore{databaseURL: databaseURL()}
}

func (s *taskStore) create(taskType string, total int, message string) backgroundTask {
	now := time.Now().Format(time.RFC3339)
	task := &backgroundTask{
		ID:        newToken(),
		Type:      taskType,
		Status:    "running",
		Total:     total,
		Message:   message,
		CreatedAt: now,
		UpdatedAt: now,
	}
	db, err := sql.Open("postgres", s.databaseURL)
	if err == nil {
		defer db.Close()
		_, _ = db.Exec(`
INSERT INTO background_tasks (id, type, status, total, done, success, failed, message, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
`, task.ID, task.Type, task.Status, task.Total, task.Done, task.Success, task.Failed, task.Message)
	}
	return *task
}

func (s *taskStore) get(id string) (backgroundTask, bool) {
	db, err := sql.Open("postgres", s.databaseURL)
	if err != nil {
		return backgroundTask{}, false
	}
	defer db.Close()
	var task backgroundTask
	var createdAt, updatedAt time.Time
	var cleanupAfter sql.NullTime
	err = db.QueryRow(`
SELECT id, type, status, total, done, success, failed, message, result_path, result_name, result_cleanup_after, created_at, updated_at
FROM background_tasks
WHERE id = $1
`, id).Scan(&task.ID, &task.Type, &task.Status, &task.Total, &task.Done, &task.Success, &task.Failed, &task.Message, &task.ResultPath, &task.FileName, &cleanupAfter, &createdAt, &updatedAt)
	if err != nil {
		return backgroundTask{}, false
	}
	if task.Status == "success" && task.ResultPath != "" {
		task.DownloadURL = "/api/admin/tasks/" + task.ID + "/download"
	}
	if cleanupAfter.Valid {
		task.ResultCleanupAfter = cleanupAfter.Time.Format(time.RFC3339)
	}
	task.CreatedAt = createdAt.Format(time.RFC3339)
	task.UpdatedAt = updatedAt.Format(time.RFC3339)
	return task, true
}

func (s *taskStore) list(limit int) ([]backgroundTask, error) {
	db, err := sql.Open("postgres", s.databaseURL)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`
SELECT id, type, status, total, done, success, failed, message, result_path, result_name, result_cleanup_after, created_at, updated_at
FROM background_tasks
ORDER BY updated_at DESC
LIMIT $1
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]backgroundTask, 0)
	for rows.Next() {
		var task backgroundTask
		var createdAt, updatedAt time.Time
		var cleanupAfter sql.NullTime
		if err := rows.Scan(&task.ID, &task.Type, &task.Status, &task.Total, &task.Done, &task.Success, &task.Failed, &task.Message, &task.ResultPath, &task.FileName, &cleanupAfter, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		if task.Status == "success" && task.ResultPath != "" {
			task.DownloadURL = "/api/admin/tasks/" + task.ID + "/download"
		}
		if cleanupAfter.Valid {
			task.ResultCleanupAfter = cleanupAfter.Time.Format(time.RFC3339)
		}
		task.CreatedAt = createdAt.Format(time.RFC3339)
		task.UpdatedAt = updatedAt.Format(time.RFC3339)
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *taskStore) update(id string, update func(*backgroundTask)) {
	task, ok := s.get(id)
	if !ok {
		return
	}
	update(&task)
	db, err := sql.Open("postgres", s.databaseURL)
	if err != nil {
		return
	}
	defer db.Close()
	_, _ = db.Exec(`
UPDATE background_tasks
SET status = $2, total = $3, done = $4, success = $5, failed = $6, message = $7, updated_at = NOW()
WHERE id = $1
`, task.ID, task.Status, task.Total, task.Done, task.Success, task.Failed, task.Message)
}

func (s *taskStore) recordProgress(id string, err error) {
	db, openErr := sql.Open("postgres", s.databaseURL)
	if openErr != nil {
		return
	}
	defer db.Close()
	if err != nil {
		_, _ = db.Exec(`
UPDATE background_tasks
SET done = done + 1, failed = failed + 1, message = $2, updated_at = NOW()
WHERE id = $1
`, id, err.Error())
		return
	}
	_, _ = db.Exec(`
UPDATE background_tasks
SET done = done + 1, success = success + 1, updated_at = NOW()
WHERE id = $1
`, id)
}

func (s *taskStore) recordBatchSuccess(id string, count int) {
	if count <= 0 {
		return
	}
	db, openErr := sql.Open("postgres", s.databaseURL)
	if openErr != nil {
		return
	}
	defer db.Close()
	_, _ = db.Exec(`
UPDATE background_tasks
SET done = done + $2, success = success + $2, updated_at = NOW()
WHERE id = $1
`, id, count)
}

func (s *taskStore) finish(id string) {
	db, err := sql.Open("postgres", s.databaseURL)
	if err != nil {
		return
	}
	defer db.Close()
	_, _ = db.Exec(`
UPDATE background_tasks
SET status = CASE
	WHEN failed > 0 AND success = 0 THEN 'failed'
	WHEN failed > 0 THEN 'partial'
	ELSE 'success'
END,
updated_at = NOW()
WHERE id = $1
`, id)
}

func (s *taskStore) setResult(id string, path string, filename string, message string) {
	db, err := sql.Open("postgres", s.databaseURL)
	if err != nil {
		return
	}
	defer db.Close()
	_, _ = db.Exec(`
UPDATE background_tasks
SET result_path = $2, result_name = $3, result_cleanup_after = NULL, message = $4, updated_at = NOW()
WHERE id = $1
`, id, path, filename, message)
}

func (s *taskStore) markResultDownloaded(id string, cleanupAfter time.Time) {
	db, err := sql.Open("postgres", s.databaseURL)
	if err != nil {
		return
	}
	defer db.Close()
	_, _ = db.Exec(`
UPDATE background_tasks
SET result_cleanup_after = COALESCE(result_cleanup_after, $2), updated_at = NOW()
WHERE id = $1 AND result_path <> ''
`, id, cleanupAfter)
}

func (s *taskStore) delete(id string) error {
	task, ok := s.get(id)
	if !ok {
		return sql.ErrNoRows
	}
	if err := removeTaskResultFile(task.ResultPath); err != nil {
		return err
	}
	db, err := sql.Open("postgres", s.databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`
DELETE FROM background_tasks
WHERE id = $1
`, id)
	return err
}

func (s *taskStore) clearExpiredResults() error {
	db, err := sql.Open("postgres", s.databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	if err := cleanupExpiredTaskResults(ctx, db); err != nil {
		return err
	}
	return cleanupStaleBackgroundTasks(ctx, db)
}

func (s *taskStore) runResultCleanupLoop(stop <-chan struct{}) {
	_ = s.clearExpiredResults()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = s.clearExpiredResults()
		case <-stop:
			return
		}
	}
}

func (s *taskStore) fail(id string, err error) {
	message := "任务失败"
	if err != nil {
		message = err.Error()
	}
	s.update(id, func(task *backgroundTask) {
		task.Status = "failed"
		task.Message = message
		if task.Failed == 0 {
			task.Failed = 1
		}
		if task.Done == 0 {
			task.Done = task.Failed
		}
	})
}

func shouldReportTaskProgress(done int, total int) bool {
	if done <= 0 {
		return false
	}
	return done == 1 || done == total || done%taskProgressBatchSize == 0
}

type publicSettings struct {
	SiteName             string `json:"site_name"`
	SiteLogo             string `json:"site_logo"`
	SiteSubtitle         string `json:"site_subtitle"`
	TableDefaultPageSize int    `json:"table_default_page_size"`
	TablePageSizeOptions []int  `json:"table_page_size_options"`
}

type updateCheckResponse struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	HasUpdate      bool   `json:"has_update"`
	Status         string `json:"status"`
	SourceURL      string `json:"source_url"`
	ReleaseURL     string `json:"release_url"`
	Message        string `json:"message"`
	CheckedAt      string `json:"checked_at"`
	UsingCached    bool   `json:"using_cached"`
}

type updateCheckCache struct {
	mu            sync.Mutex
	result        updateCheckResponse
	cachedAt      time.Time
	lastSuccess   updateCheckResponse
	lastSuccessAt time.Time
}

type githubLatestReleaseResponse struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	HTMLURL string `json:"html_url"`
}

func newUpdateCheckCache() *updateCheckCache {
	return &updateCheckCache{}
}

type loginRequest struct {
	Account  string `json:"account"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authUser struct {
	ID        int     `json:"id"`
	Username  string  `json:"username"`
	Email     string  `json:"email"`
	AvatarURL string  `json:"avatar_url"`
	Balance   float64 `json:"balance"`
	Role      string  `json:"role"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
}

type authResponse struct {
	AccessToken        string   `json:"access_token"`
	RefreshToken       string   `json:"refresh_token"`
	ExpiresIn          int      `json:"expires_in"`
	TokenType          string   `json:"token_type"`
	MustChangePassword bool     `json:"must_change_password"`
	User               authUser `json:"user"`
}

type adminUserResponse struct {
	ID        int     `json:"id"`
	Username  string  `json:"username"`
	Email     string  `json:"email"`
	AvatarURL string  `json:"avatar_url"`
	Balance   float64 `json:"balance"`
	Role      string  `json:"role"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
}

type userListResponse struct {
	Items    []adminUserResponse `json:"items"`
	Total    int                 `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
	Pages    int                 `json:"pages"`
}

type saveUserRequest struct {
	Username string  `json:"username"`
	Email    string  `json:"email"`
	Password string  `json:"password"`
	Balance  float64 `json:"balance"`
	Role     string  `json:"role"`
	Enabled  *bool   `json:"enabled"`
}

type updateStatusRequest struct {
	Status string `json:"status"`
}

type updateBalanceRequest struct {
	Amount float64 `json:"amount"`
	Type   string  `json:"type"`
	Remark string  `json:"remark"`
}

type balanceRecordResponse struct {
	ID           int     `json:"id"`
	UserID       int     `json:"user_id"`
	Type         string  `json:"type"`
	Amount       float64 `json:"amount"`
	BalanceAfter float64 `json:"balance_after"`
	Remark       string  `json:"remark"`
	CreatedAt    string  `json:"created_at"`
}

type mailGroupResponse struct {
	ID        int    `json:"id"`
	ParentID  int    `json:"parent_id"`
	Name      string `json:"name"`
	System    bool   `json:"system"`
	SortOrder int    `json:"sort_order"`
	Count     int    `json:"count"`
	CreatedAt string `json:"created_at"`
}

type saveMailGroupRequest struct {
	Name      string `json:"name"`
	ParentID  int    `json:"parent_id"`
	SortOrder *int   `json:"sort_order"`
}

type mailServerResponse struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	ImapHost  string `json:"imap_host"`
	SMTPHost  string `json:"smtp_host"`
	CreatedAt string `json:"created_at"`
}

type saveMailServerRequest struct {
	Name     string `json:"name"`
	ImapHost string `json:"imap_host"`
	SMTPHost string `json:"smtp_host"`
}

type mailAccountResponse struct {
	ID           int    `json:"id"`
	GroupID      int    `json:"group_id"`
	GroupName    string `json:"group_name"`
	Email        string `json:"email"`
	ServerID     int    `json:"server_id"`
	ServerName   string `json:"server_name"`
	ImapHost     string `json:"imap_host"`
	SMTPHost     string `json:"smtp_host"`
	ImapProtocol string `json:"imap_protocol"`
	ImapPort     int    `json:"imap_port"`
	ImapSSL      bool   `json:"imap_ssl"`
	SMTPProtocol string `json:"smtp_protocol"`
	SMTPPort     int    `json:"smtp_port"`
	SMTPSSL      bool   `json:"smtp_ssl"`
	Remark       string `json:"remark"`
	Status       string `json:"status"`
	StatusReason string `json:"status_reason"`
	CreatedAt    string `json:"created_at"`
}

type mailAccountListResponse struct {
	Items    []mailAccountResponse `json:"items"`
	Total    int                   `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
	Pages    int                   `json:"pages"`
	Normal   int                   `json:"normal"`
	Error    int                   `json:"error"`
}

type saveMailAccountRequest struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	GroupID      int    `json:"group_id"`
	ServerID     int    `json:"server_id"`
	ImapHost     string `json:"imap_host"`
	SMTPHost     string `json:"smtp_host"`
	ImapProtocol string `json:"imap_protocol"`
	ImapPort     int    `json:"imap_port"`
	ImapSSL      bool   `json:"imap_ssl"`
	SMTPProtocol string `json:"smtp_protocol"`
	SMTPPort     int    `json:"smtp_port"`
	SMTPSSL      bool   `json:"smtp_ssl"`
	Remark       string `json:"remark"`
}

type batchMailAccountRequest struct {
	Content      string `json:"content"`
	GroupID      int    `json:"group_id"`
	ServerID     int    `json:"server_id"`
	ImapHost     string `json:"imap_host"`
	SMTPHost     string `json:"smtp_host"`
	ImapProtocol string `json:"imap_protocol"`
	ImapPort     int    `json:"imap_port"`
	ImapSSL      bool   `json:"imap_ssl"`
	SMTPProtocol string `json:"smtp_protocol"`
	SMTPPort     int    `json:"smtp_port"`
	SMTPSSL      bool   `json:"smtp_ssl"`
}

type testMailAccountRequest struct {
	Type string `json:"type"`
}

type receiveMailMessagesRequest struct {
	Limit int `json:"limit"`
}

type mailDataExportRequest struct {
	IDs      []int             `json:"ids"`
	Filter   accountListFilter `json:"filter"`
	Password string            `json:"password"`
}

type accountListFilter struct {
	GroupID int    `json:"group_id"`
	Search  string `json:"search"`
}

type accountExportSelector struct {
	IDs    []int
	Filter accountListFilter
}

type mailAccountBatchActionRequest struct {
	Action   string            `json:"action"`
	IDs      []int             `json:"ids"`
	Filter   accountListFilter `json:"filter"`
	TestType string            `json:"test_type"`
}

type mailDataGroup struct {
	ID        int    `json:"id"`
	ParentID  int    `json:"parent_id"`
	Name      string `json:"name"`
	System    bool   `json:"system"`
	SortOrder int    `json:"sort_order"`
	CreatedAt string `json:"created_at"`
}

type mailDataAccount struct {
	ID           int    `json:"id"`
	GroupID      int    `json:"group_id"`
	Email        string `json:"email"`
	Password     string `json:"password"`
	ServerID     int    `json:"server_id"`
	ImapHost     string `json:"imap_host"`
	SMTPHost     string `json:"smtp_host"`
	ImapProtocol string `json:"imap_protocol"`
	ImapPort     int    `json:"imap_port"`
	ImapSSL      bool   `json:"imap_ssl"`
	SMTPProtocol string `json:"smtp_protocol"`
	SMTPPort     int    `json:"smtp_port"`
	SMTPSSL      bool   `json:"smtp_ssl"`
	Remark       string `json:"remark"`
	Status       string `json:"status"`
	StatusReason string `json:"status_reason"`
	CreatedAt    string `json:"created_at"`
}

type mailDataPayload struct {
	ExportedAt string            `json:"exported_at"`
	Groups     []mailDataGroup   `json:"groups"`
	Accounts   []mailDataAccount `json:"accounts"`
}

type mailDataImportResult struct {
	Groups   int `json:"groups"`
	Accounts int `json:"accounts"`
}

type receivedMailMessage struct {
	UID       int    `json:"uid"`
	Folder    string `json:"folder"`
	Mailbox   string `json:"mailbox"`
	Subject   string `json:"subject"`
	From      string `json:"from"`
	To        string `json:"to"`
	Time      string `json:"time"`
	Timestamp int64  `json:"timestamp"`
}

type receiveMailDetailRequest struct {
	UID     int    `json:"uid"`
	Mailbox string `json:"mailbox"`
	Folder  string `json:"folder"`
}

type sendMailMessageRequest struct {
	Nickname  string `json:"nickname"`
	Recipient string `json:"recipient"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
}

type receiveMailDetailResponse struct {
	receivedMailMessage
	Body string `json:"body"`
	HTML string `json:"html"`
}

type receiveMailMessagesResponse struct {
	Inbox []receivedMailMessage `json:"inbox"`
	Trash []receivedMailMessage `json:"trash"`
}

type mailAccountTestConfig struct {
	Email        string
	Password     string
	ImapHost     string
	ImapProtocol string
	ImapPort     int
	ImapSSL      bool
	SMTPHost     string
	SMTPProtocol string
	SMTPPort     int
	SMTPSSL      bool
}

type smtpLoginAuth struct {
	username string
	password string
	step     int
}

type updateProfileRequest struct {
	Username  string  `json:"username"`
	Email     string  `json:"email"`
	AvatarURL *string `json:"avatar_url"`
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func main() {
	_ = godotenv.Load()

	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	ctx := context.Background()
	db := openDB()
	defer db.Close()

	if err := db.Schema.Create(ctx); err != nil {
		panic(fmt.Errorf("run database migration: %w", err))
	}

	if err := ensureUserSecurityColumns(ctx); err != nil {
		panic(fmt.Errorf("ensure user security columns: %w", err))
	}

	if err := ensureBalanceRecordTable(ctx); err != nil {
		panic(fmt.Errorf("ensure balance record table: %w", err))
	}

	if err := ensureBackgroundTaskTable(ctx); err != nil {
		panic(fmt.Errorf("ensure background task table: %w", err))
	}

	if err := ensureMailGroupTable(ctx); err != nil {
		panic(fmt.Errorf("ensure mail group table: %w", err))
	}

	if err := ensureMailManagementTables(ctx); err != nil {
		panic(fmt.Errorf("ensure mail management tables: %w", err))
	}

	if err := ensureOutlookManagementTables(ctx); err != nil {
		panic(fmt.Errorf("ensure outlook management tables: %w", err))
	}

	if err := ensureProxySystemTables(ctx); err != nil {
		panic(fmt.Errorf("ensure proxy system tables: %w", err))
	}

	if err := ensureCardKeySystemTables(ctx); err != nil {
		panic(fmt.Errorf("ensure card key system tables: %w", err))
	}

	if err := ensurePublicMailTables(ctx); err != nil {
		panic(fmt.Errorf("ensure public mail tables: %w", err))
	}

	if err := seedDefaults(ctx, db); err != nil {
		panic(fmt.Errorf("seed defaults: %w", err))
	}

	if err := compactUserIDs(ctx); err != nil {
		panic(fmt.Errorf("compact user ids: %w", err))
	}

	if err := ensureSingleAdmin(ctx); err != nil {
		panic(fmt.Errorf("ensure single admin: %w", err))
	}

	proxyRuntime := defaultProxyRuntime
	defer proxyRuntime.stop()
	state := &appState{db: db, sessions: newSessionStore(), outlookOAuth: newOutlookOAuthStore(), tasks: newTaskStore(), proxies: proxyRuntime, updates: newUpdateCheckCache()}
	state.backups = newBackupScheduler(state)
	taskCleanupStop := make(chan struct{})
	defer close(taskCleanupStop)
	go state.tasks.runResultCleanupLoop(taskCleanupStop)
	cardKeyLogCleanupStop := make(chan struct{})
	defer close(cardKeyLogCleanupStop)
	go state.runCardKeyUseLogCleanupLoop(cardKeyLogCleanupStop)
	backupSchedulerStop := make(chan struct{})
	defer close(backupSchedulerStop)
	go state.backups.run(backupSchedulerStop)
	app := gin.Default()

	app.GET("/imap/mail/:key/all/:email", state.getQuickMailIMAPPlain)
	app.GET("/imap/mail/:key/all/:email/:limit", state.getQuickMailIMAPPlain)
	app.GET("/outlook/mail/:key/all/:email", state.getQuickMailOutlookPlain)
	app.GET("/outlook/mail/:key/all/:email/:limit", state.getQuickMailOutlookPlain)

	api := app.Group("/api")
	{
		api.GET("/health", state.health)
		api.GET("/settings/bootstrap.js", state.getPublicSettingsBootstrap)
		api.GET("/settings/public", state.getPublicSettings)
		api.POST("/auth/login", state.login)
		api.GET("/admin/outlook-oauth/callback", state.outlookOAuthCallback)
		publicAPI := api.Group("/public")
		{
			publicAPI.GET("/mail/:key", state.getPublicMailInfo)
			publicAPI.GET("/mail/:key/all", state.getPublicMailPlain)
			publicAPI.GET("/mail/:key/all/*email", state.getPublicMailPlain)
			publicAPI.POST("/mail/:key/messages", state.getPublicMailMessages)
		}
		userAPI := api.Group("/user", state.authMiddleware())
		{
			userAPI.GET("/profile", state.getProfile)
			userAPI.PUT("/profile", state.updateProfile)
			userAPI.GET("/quick-mail-key", state.getQuickMailKey)
			userAPI.PUT("/quick-mail-key", state.updateQuickMailKey)
			userAPI.PUT("/password", state.changePassword)
		}
		api.POST("/admin/quick-mail/imap/receive", state.quickMailReceiveIMAP)
		api.POST("/admin/quick-mail/outlook/receive", state.quickMailReceiveOutlook)
		adminAPI := api.Group("/admin", state.authMiddleware(), state.adminMiddleware())
		{
			adminAPI.GET("/settings", state.getAdminSettings)
			adminAPI.PUT("/settings", state.updateAdminSettings)
			adminAPI.GET("/update-check", state.checkAppUpdate)
			adminAPI.GET("/database-backup/files", state.listDatabaseBackupFiles)
			adminAPI.GET("/database-backup/files/:name", state.downloadDatabaseBackupFile)
			adminAPI.DELETE("/database-backup/files/:name", state.deleteDatabaseBackupFile)
			adminAPI.POST("/database-backup/manual-task", state.createManualDatabaseBackupTask)
			adminAPI.POST("/database-backup/webdav-test", state.testBackupWebDAV)
			adminAPI.GET("/database-backup/export", state.exportDatabaseBackup)
			adminAPI.POST("/database-backup/restore", state.restoreDatabaseBackup)
			adminAPI.GET("/tasks", state.listTasks)
			adminAPI.GET("/tasks/:id", state.getTask)
			adminAPI.GET("/tasks/:id/download", state.downloadTaskResult)
			adminAPI.DELETE("/tasks/:id", state.deleteTask)
			adminAPI.DELETE("/tasks/:id/result", state.clearTaskResult)
			adminAPI.GET("/proxy/nodes", state.listProxyNodes)
			adminAPI.POST("/proxy/nodes", state.createProxyNode)
			adminAPI.POST("/proxy/nodes/import", state.importProxyNodes)
			adminAPI.PUT("/proxy/nodes/:id", state.updateProxyNode)
			adminAPI.DELETE("/proxy/nodes/:id", state.deleteProxyNode)
			adminAPI.POST("/proxy/nodes/:id/test", state.testProxyNode)
			adminAPI.GET("/proxy/settings", state.getProxySettings)
			adminAPI.PUT("/proxy/settings", state.updateProxySettings)
			adminAPI.GET("/proxy/runtime", state.getProxyRuntime)
			adminAPI.GET("/card-key-groups", state.listCardKeyGroups)
			adminAPI.POST("/card-key-groups", state.createCardKeyGroup)
			adminAPI.PUT("/card-key-groups/:id", state.updateCardKeyGroup)
			adminAPI.DELETE("/card-key-groups/:id", state.deleteCardKeyGroup)
			adminAPI.GET("/card-keys", state.listCardKeys)
			adminAPI.POST("/card-keys", state.createCardKey)
			adminAPI.POST("/card-keys/batch", state.batchCreateCardKeys)
			adminAPI.POST("/card-keys/batch-action", state.batchCardKeyAction)
			adminAPI.PUT("/card-keys/:id", state.updateCardKey)
			adminAPI.DELETE("/card-keys/:id", state.deleteCardKey)
			adminAPI.GET("/card-key-logs", state.listCardKeyUseLogs)
			adminAPI.DELETE("/card-key-logs", state.clearCardKeyUseLogs)
			adminAPI.GET("/mail-groups", state.listMailGroups)
			adminAPI.POST("/mail-groups", state.createMailGroup)
			adminAPI.PUT("/mail-groups/:id", state.updateMailGroup)
			adminAPI.DELETE("/mail-groups/:id", state.deleteMailGroup)
			adminAPI.GET("/mail-servers", state.listMailServers)
			adminAPI.POST("/mail-servers", state.createMailServer)
			adminAPI.PUT("/mail-servers/:id", state.updateMailServer)
			adminAPI.DELETE("/mail-servers/:id", state.deleteMailServer)
			adminAPI.GET("/mail-accounts", state.listMailAccounts)
			adminAPI.POST("/mail-accounts", state.createMailAccount)
			adminAPI.POST("/mail-accounts/batch", state.batchCreateMailAccounts)
			adminAPI.POST("/mail-accounts/batch-action", state.batchMailAccountAction)
			adminAPI.POST("/mail-data/export", state.exportMailDataZip)
			adminAPI.POST("/mail-data/export-task", state.createMailDataExportTask)
			adminAPI.POST("/mail-data/import", state.importMailDataZip)
			adminAPI.POST("/mail-data/import-task", state.createMailDataImportTask)
			adminAPI.PUT("/mail-accounts/:id", state.updateMailAccount)
			adminAPI.DELETE("/mail-accounts/:id", state.deleteMailAccount)
			adminAPI.POST("/mail-accounts/:id/test", state.testMailAccount)
			adminAPI.POST("/mail-accounts/:id/receive", state.receiveMailMessages)
			adminAPI.POST("/mail-accounts/:id/receive/detail", state.receiveMailDetail)
			adminAPI.POST("/mail-accounts/:id/send", state.sendMailMessage)
			adminAPI.GET("/outlook-groups", state.listOutlookGroups)
			adminAPI.POST("/outlook-groups", state.createOutlookGroup)
			adminAPI.PUT("/outlook-groups/:id", state.updateOutlookGroup)
			adminAPI.DELETE("/outlook-groups/:id", state.deleteOutlookGroup)
			adminAPI.GET("/outlook-accounts", state.listOutlookAccounts)
			adminAPI.POST("/outlook-accounts", state.createOutlookAccount)
			adminAPI.POST("/outlook-accounts/batch", state.batchCreateOutlookAccounts)
			adminAPI.POST("/outlook-accounts/batch-action", state.batchOutlookAccountActionV2)
			adminAPI.POST("/outlook-data/export", state.exportOutlookDataZip)
			adminAPI.POST("/outlook-data/export-task", state.createOutlookDataExportTask)
			adminAPI.POST("/outlook-data/import", state.importOutlookDataZip)
			adminAPI.POST("/outlook-data/import-task", state.createOutlookDataImportTask)
			adminAPI.PUT("/outlook-accounts/:id", state.updateOutlookAccount)
			adminAPI.DELETE("/outlook-accounts/:id", state.deleteOutlookAccount)
			adminAPI.POST("/outlook-accounts/:id/test", state.testOutlookAccount)
			adminAPI.GET("/outlook-accounts/:id/messages", state.listOutlookMessages)
			adminAPI.POST("/outlook-accounts/:id/messages/batch-detail", state.getOutlookMessageDetails)
			adminAPI.GET("/outlook-accounts/:id/messages/:messageID", state.getOutlookMessageDetail)
			adminAPI.GET("/outlook-oauth/authorize", state.outlookOAuthAuthorize)
			adminAPI.POST("/outlook-oauth/exchange", state.outlookOAuthExchange)
			adminAPI.GET("/outlook-oauth/result", state.outlookOAuthResult)
			adminAPI.GET("/users", state.listUsers)
			adminAPI.POST("/users", state.createUser)
			adminAPI.PUT("/users/:id", state.updateUser)
			adminAPI.PATCH("/users/:id/status", state.updateUserStatus)
			adminAPI.PATCH("/users/:id/balance", state.updateUserBalance)
			adminAPI.GET("/users/:id/balance-records", state.listUserBalanceRecords)
			adminAPI.DELETE("/users/:id", state.deleteUser)
		}
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "4400"
	}
	host := strings.TrimSpace(os.Getenv("HOST"))
	if host == "" {
		host = "127.0.0.1"
	}

	addr := net.JoinHostPort(host, port)
	server := &http.Server{Addr: addr, Handler: app}
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ListenAndServe()
	}()

	shutdownSignal := make(chan os.Signal, 1)
	signal.Notify(shutdownSignal, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(shutdownSignal)

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(err)
		}
	case <-shutdownSignal:
		proxyRuntime.stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(err)
		}
	}
}

func openDB() *ent.Client {
	dsn := databaseURL()

	ensureDatabase(dsn)

	drv, err := entsql.Open(dialect.Postgres, dsn)
	if err != nil {
		panic(fmt.Errorf("connect database: %w", err))
	}

	return ent.NewClient(ent.Driver(drv))
}

func databaseURL() string {
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		panic("DATABASE_URL is required; configure a dedicated PostgreSQL user with a strong password")
	}
	if isUnsafeDefaultDatabaseURL(dsn) {
		panic("DATABASE_URL uses the unsafe default credential postgres/postgres; configure a dedicated PostgreSQL user with a strong password")
	}
	return dsn
}

func isUnsafeDefaultDatabaseURL(dsn string) bool {
	parsed, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil {
		return false
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return false
	}
	password, hasPassword := parsed.User.Password()
	if !hasPassword || password == "" || strings.Contains(strings.ToUpper(password), "CHANGE_ME") {
		return true
	}
	return strings.EqualFold(parsed.User.Username(), "postgres") && password == "postgres"
}

func ensureDatabase(dsn string) {
	dbName, adminDSN, ok := postgresAdminDSN(dsn)
	if !ok {
		return
	}

	db, err := sql.Open("postgres", adminDSN)
	if err != nil {
		return
	}
	defer db.Close()

	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", dbName).Scan(&exists); err != nil {
		return
	}
	if exists {
		return
	}

	if _, err := db.Exec(fmt.Sprintf("CREATE DATABASE %s", pq.QuoteIdentifier(dbName))); err != nil {
		panic(fmt.Errorf("create database %s: %w", dbName, err))
	}
}

func postgresAdminDSN(dsn string) (string, string, bool) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", "", false
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return "", "", false
	}
	dbName := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if dbName == "" {
		return "", "", false
	}
	unescapedName, err := url.PathUnescape(dbName)
	if err != nil {
		return "", "", false
	}
	if unescapedName == "" || unescapedName == "postgres" || unescapedName == "template0" || unescapedName == "template1" {
		return "", "", false
	}
	parsed.Path = "/postgres"
	parsed.RawPath = ""
	return unescapedName, parsed.String(), true
}

func seedDefaults(ctx context.Context, db *ent.Client) error {
	userCount, err := db.User.Query().Count(ctx)
	if err != nil {
		return err
	}

	if userCount == 0 {
		hash, err := bcrypt.GenerateFromPassword([]byte(defaultAdminPassword), bcrypt.DefaultCost)
		if err != nil {
			return err
		}

		admin, err := db.User.Create().
			SetUsername(defaultAdminAccount).
			SetEmail("admin@example.com").
			SetPasswordHash(string(hash)).
			SetRole("admin").
			SetEnabled(true).
			Save(ctx)
		if err != nil {
			return err
		}
		if err := setMustChangePassword(ctx, admin.ID, true); err != nil {
			return err
		}
	}

	if err := markDefaultPasswordUsersMustChange(ctx, db); err != nil {
		return err
	}

	if err := cleanupRemovedSystemSettings(ctx, db); err != nil {
		return err
	}

	for key, value := range defaultSystemSettings() {
		exists, err := db.SystemSetting.Query().Where(systemsetting.Key(key)).Exist(ctx)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := db.SystemSetting.Create().SetKey(key).SetValue(settingValueToString(value)).Save(ctx); err != nil {
			return err
		}
	}

	if err := ensureDefaultSiteLogo(ctx, db); err != nil {
		return err
	}

	return nil
}

func ensureUserSecurityColumns(ctx context.Context) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.ExecContext(ctx, `
ALTER TABLE users ADD COLUMN IF NOT EXISTS must_change_password BOOLEAN NOT NULL DEFAULT FALSE
`)
	return err
}

func markDefaultPasswordUsersMustChange(ctx context.Context, db *ent.Client) error {
	items, err := db.User.Query().All(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if bcrypt.CompareHashAndPassword([]byte(item.PasswordHash), []byte(defaultAdminPassword)) == nil {
			if err := setMustChangePassword(ctx, item.ID, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func userMustChangePassword(ctx context.Context, id int) bool {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return false
	}
	defer db.Close()

	var mustChange bool
	if err := db.QueryRowContext(ctx, `SELECT must_change_password FROM users WHERE id = $1`, id).Scan(&mustChange); err != nil {
		return false
	}
	return mustChange
}

func setMustChangePassword(ctx context.Context, id int, value bool) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.ExecContext(ctx, `UPDATE users SET must_change_password = $2, updated_at = NOW() WHERE id = $1`, id, value)
	return err
}

func cleanupRemovedSystemSettings(ctx context.Context, db *ent.Client) error {
	_, err := db.SystemSetting.Delete().Where(systemsetting.KeyIn(removedSystemSettingKeys...)).Exec(ctx)
	return err
}

func ensureDefaultSiteLogo(ctx context.Context, db *ent.Client) error {
	logo := defaultPublicLogoDataURL()
	item, err := db.SystemSetting.Query().Where(systemsetting.Key("site_logo")).Only(ctx)
	if ent.IsNotFound(err) {
		_, err = db.SystemSetting.Create().SetKey("site_logo").SetValue(logo).Save(ctx)
		return err
	}
	if err != nil {
		return err
	}
	if item.Value != "" && item.Value != defaultPublicLogoPath {
		return nil
	}
	_, err = item.Update().SetValue(logo).Save(ctx)
	return err
}

func (s *appState) getAdminSettings(c *gin.Context) {
	settings, err := s.readSettings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u8bfb\u53d6\u7cfb\u7edf\u8bbe\u7f6e\u5931\u8d25"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: settings, Msg: "ok"})
}

func (s *appState) updateAdminSettings(c *gin.Context) {
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u8bf7\u6c42\u53c2\u6570\u9519\u8bef"})
		return
	}

	ctx := c.Request.Context()
	defaults := defaultSystemSettings()
	for key, value := range req {
		if _, ok := defaults[key]; !ok {
			continue
		}
		if key == "site_logo" && strings.TrimSpace(settingValueToString(value)) == "" {
			value = defaultPublicLogoDataURL()
		}

		existing, err := s.db.SystemSetting.Query().Where(systemsetting.Key(key)).Only(ctx)
		if err == nil {
			if _, err := existing.Update().SetValue(settingValueToString(value)).Save(ctx); err != nil {
				c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u4fdd\u5b58\u7cfb\u7edf\u8bbe\u7f6e\u5931\u8d25"})
				return
			}
			continue
		}
		if !ent.IsNotFound(err) {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u4fdd\u5b58\u7cfb\u7edf\u8bbe\u7f6e\u5931\u8d25"})
			return
		}
		if _, err := s.db.SystemSetting.Create().SetKey(key).SetValue(settingValueToString(value)).Save(ctx); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u4fdd\u5b58\u7cfb\u7edf\u8bbe\u7f6e\u5931\u8d25"})
			return
		}
	}
	if _, ok := req["card_key_log_cleanup_days"]; ok {
		_ = s.clearExpiredCardKeyUseLogs(ctx)
	}

	settings, err := s.readSettings(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u8bfb\u53d6\u7cfb\u7edf\u8bbe\u7f6e\u5931\u8d25"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: settings, Msg: "ok"})
}

func (s *appState) exportDatabaseBackup(c *gin.Context) {
	tool, err := postgresToolPath("pg_dump")
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: err.Error()})
		return
	}

	temp, err := os.CreateTemp("", "mailplus-db-backup-*.dump")
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "创建备份临时文件失败"})
		return
	}
	tempPath := temp.Name()
	_ = temp.Close()
	defer os.Remove(tempPath)

	dsn := databaseURL()
	ctx, cancel := context.WithTimeout(c.Request.Context(), databaseBackupTimeout)
	defer cancel()

	cmd := exec.CommandContext(
		ctx,
		tool,
		"--format=custom",
		"--clean",
		"--if-exists",
		"--no-owner",
		"--no-privileges",
		"--file", tempPath,
		"--dbname", dsn,
	)
	cmd.Env = postgresToolEnv(tool)
	output, err := cmd.CombinedOutput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: postgresCommandMessage("数据库备份失败", dsn, output, err, ctx.Err())})
		return
	}

	filename := fmt.Sprintf("mailplus-db-backup-%s.dump", time.Now().Format("20060102-150405"))
	c.FileAttachment(tempPath, filename)
}

type databaseBackupFileResponse struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	CreatedAt  string `json:"created_at"`
	ModifiedAt string `json:"modified_at"`
	Directory  string `json:"directory"`
}

func (s *appState) listDatabaseBackupFiles(c *gin.Context) {
	files, err := collectDatabaseBackupFiles()
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取备份文件失败"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: files, Msg: "ok"})
}

func (s *appState) downloadDatabaseBackupFile(c *gin.Context) {
	filePath, filename, ok := resolveDatabaseBackupFile(c.Param("name"))
	if !ok {
		c.JSON(http.StatusNotFound, apiResponse{Code: 404, Msg: "备份文件不存在"})
		return
	}
	c.FileAttachment(filePath, filename)
}

func (s *appState) deleteDatabaseBackupFile(c *gin.Context) {
	filePath, _, ok := resolveDatabaseBackupFile(c.Param("name"))
	if !ok {
		c.JSON(http.StatusNotFound, apiResponse{Code: 404, Msg: "备份文件不存在"})
		return
	}
	if err := os.Remove(filePath); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除备份文件失败"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
}

type databaseBackupTaskRequest struct {
	RetainCount   int    `json:"backup_schedule_retain_count"`
	WebDAVEnabled bool   `json:"backup_webdav_enabled"`
	WebDAVURL     string `json:"backup_webdav_url"`
	WebDAVUser    string `json:"backup_webdav_username"`
	WebDAVPass    string `json:"backup_webdav_password"`
	WebDAVDir     string `json:"backup_webdav_remote_dir"`
}

func (req databaseBackupTaskRequest) applyTo(settings backupScheduleSettings) backupScheduleSettings {
	if req.RetainCount > 0 {
		settings.RetainCount = req.RetainCount
	}
	settings.WebDAV = webDAVBackupSettings{
		Enabled:   req.WebDAVEnabled,
		URL:       req.WebDAVURL,
		Username:  req.WebDAVUser,
		Password:  req.WebDAVPass,
		RemoteDir: req.WebDAVDir,
	}
	return settings
}

func (req databaseBackupTaskRequest) webDAVSettings() webDAVBackupSettings {
	return webDAVBackupSettings{
		Enabled:   true,
		URL:       req.WebDAVURL,
		Username:  req.WebDAVUser,
		Password:  req.WebDAVPass,
		RemoteDir: req.WebDAVDir,
	}
}

func bindDatabaseBackupTaskRequest(c *gin.Context, settings backupScheduleSettings) (backupScheduleSettings, bool) {
	var req databaseBackupTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return settings, true
		}
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "备份参数格式错误"})
		return settings, false
	}
	return req.applyTo(settings), true
}

func (s *appState) createManualDatabaseBackupTask(c *gin.Context) {
	settings, err := s.readBackupScheduleSettings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取备份设置失败"})
		return
	}
	var ok bool
	settings, ok = bindDatabaseBackupTaskRequest(c, settings)
	if !ok {
		return
	}
	task := s.tasks.create("database_backup", backupTaskTotal(settings), "手动备份任务已创建")
	go s.runDatabaseBackupTask(task.ID, settings)
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: task, Msg: "ok"})
}

func (s *appState) testBackupWebDAV(c *gin.Context) {
	var req databaseBackupTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "WebDAV 参数格式错误"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if err := testWebDAVBackupStorage(ctx, req.webDAVSettings()); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: gin.H{"message": "WebDAV 连接测试成功"}, Msg: "ok"})
}

func (s *appState) restoreDatabaseBackup(c *gin.Context) {
	tool, err := postgresToolPath("pg_restore")
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: err.Error()})
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请选择要恢复的备份文件"})
		return
	}
	if !strings.EqualFold(filepath.Ext(file.Filename), ".dump") {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请上传系统生成的 .dump 备份文件"})
		return
	}

	tempPath, err := saveUploadedDatabaseBackup(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "保存上传备份文件失败"})
		return
	}
	defer os.Remove(tempPath)

	dsn := databaseURL()
	ctx, cancel := context.WithTimeout(c.Request.Context(), databaseRestoreTimeout)
	defer cancel()

	cmd := exec.CommandContext(
		ctx,
		tool,
		"--clean",
		"--if-exists",
		"--no-owner",
		"--no-privileges",
		"--single-transaction",
		"--dbname", dsn,
		tempPath,
	)
	cmd.Env = postgresToolEnv(tool)
	output, err := cmd.CombinedOutput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: postgresCommandMessage("数据库恢复失败", dsn, output, err, ctx.Err())})
		return
	}

	c.JSON(http.StatusOK, apiResponse{
		Code: 0,
		Data: gin.H{
			"message":           "数据库恢复完成，程序正在重启",
			"restart_scheduled": true,
		},
		Msg: "ok",
	})
	scheduleRestoreRestart()
}

func scheduleRestoreRestart() {
	go func() {
		time.Sleep(restoreRestartDelay)
		defaultProxyRuntime.stop()
		os.Exit(restoreRestartExitCode)
	}()
}

func saveUploadedDatabaseBackup(fileHeader *multipart.FileHeader) (string, error) {
	source, err := fileHeader.Open()
	if err != nil {
		return "", err
	}
	defer source.Close()

	temp, err := os.CreateTemp("", "mailplus-db-restore-*.dump")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	defer temp.Close()

	if _, err := io.Copy(temp, source); err != nil {
		_ = os.Remove(tempPath)
		return "", err
	}
	return tempPath, nil
}

func collectDatabaseBackupFiles() ([]databaseBackupFileResponse, error) {
	files := make([]databaseBackupFileResponse, 0)
	seenDirs := map[string]bool{}
	seenFiles := map[string]bool{}
	for _, dir := range databaseBackupDirectories() {
		cleanDir := normalizeBackupPath(dir)
		dirKey := backupPathKey(cleanDir)
		if seenDirs[dirKey] {
			continue
		}
		seenDirs[dirKey] = true

		entries, err := os.ReadDir(cleanDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !isDatabaseBackupFile(entry.Name()) {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			filePath := normalizeBackupPath(filepath.Join(cleanDir, entry.Name()))
			fileKey := backupPathKey(filePath)
			if seenFiles[fileKey] {
				continue
			}
			seenFiles[fileKey] = true
			files = append(files, databaseBackupFileResponse{
				Name:       entry.Name(),
				Size:       info.Size(),
				CreatedAt:  info.ModTime().Format(time.RFC3339),
				ModifiedAt: info.ModTime().Format(time.RFC3339),
				Directory:  filepath.Dir(filePath),
			})
		}
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].ModifiedAt > files[j].ModifiedAt
	})
	return files, nil
}

func resolveDatabaseBackupFile(rawName string) (string, string, bool) {
	filename, err := url.PathUnescape(rawName)
	if err != nil {
		filename = rawName
	}
	filename = filepath.Base(filename)
	if filename == "." || filename == string(filepath.Separator) || !isDatabaseBackupFile(filename) {
		return "", "", false
	}
	seenDirs := map[string]bool{}
	for _, dir := range databaseBackupDirectories() {
		cleanDir := normalizeBackupPath(dir)
		dirKey := backupPathKey(cleanDir)
		if seenDirs[dirKey] {
			continue
		}
		seenDirs[dirKey] = true
		candidate := filepath.Join(cleanDir, filename)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return normalizeBackupPath(candidate), filename, true
		}
	}
	return "", "", false
}

func databaseBackupDirectories() []string {
	dirs := []string{}
	if configured := strings.TrimSpace(os.Getenv("BACKUP_DIR")); configured != "" {
		dirs = append(dirs, configured)
	}
	dirs = append(dirs, backupStorageDir(), "backups", filepath.Join("..", "backups"))
	if executable, err := os.Executable(); err == nil {
		executableDir := filepath.Dir(executable)
		dirs = append(dirs,
			filepath.Join(executableDir, "backups"),
			filepath.Join(executableDir, "..", "backups"),
		)
	}
	return dirs
}

func normalizeBackupPath(value string) string {
	cleanPath := filepath.Clean(value)
	if absolutePath, err := filepath.Abs(cleanPath); err == nil {
		cleanPath = absolutePath
	}
	if realPath, err := filepath.EvalSymlinks(cleanPath); err == nil {
		cleanPath = realPath
	}
	return filepath.Clean(cleanPath)
}

func backupPathKey(value string) string {
	key := normalizeBackupPath(value)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

func isDatabaseBackupFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".dump" || ext == ".backup" || ext == ".bak"
}

func isManagedDatabaseBackupFile(name string) bool {
	return strings.HasPrefix(filepath.Base(name), "mailplus-db-backup-") && isDatabaseBackupFile(name)
}

func backupStorageDir() string {
	if configured := strings.TrimSpace(os.Getenv("BACKUP_DIR")); configured != "" {
		return filepath.Clean(configured)
	}
	if executable, err := os.Executable(); err == nil {
		executableDir := filepath.Dir(executable)
		tempDir := filepath.Clean(os.TempDir())
		if tempDir == "" || !strings.HasPrefix(filepath.Clean(executableDir), tempDir) {
			return filepath.Join(executableDir, "backups")
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Join(cwd, "backups")
	}
	return "backups"
}

type backupScheduler struct {
	state      *appState
	mu         sync.Mutex
	lastRunKey string
	running    bool
}

type backupScheduleSettings struct {
	Enabled      bool
	Frequency    string
	Time         string
	IntervalDays int
	Weekday      int
	MonthDay     int
	RetainCount  int
	WebDAV       webDAVBackupSettings
}

type webDAVBackupSettings struct {
	Enabled   bool
	URL       string
	Username  string
	Password  string
	RemoteDir string
}

func newBackupScheduler(state *appState) *backupScheduler {
	return &backupScheduler{state: state}
}

func (s *backupScheduler) run(stop <-chan struct{}) {
	s.check(time.Now())
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			s.check(now)
		case <-stop:
			return
		}
	}
}

func (s *backupScheduler) check(now time.Time) {
	settings, err := s.state.readBackupScheduleSettings(context.Background())
	if err != nil || !settings.Enabled {
		return
	}
	runKey, due := backupScheduleRunKey(settings, now)
	if !due || runKey == "" {
		return
	}

	s.mu.Lock()
	if s.running || s.lastRunKey == runKey {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.lastRunKey = runKey
	s.mu.Unlock()

	task := s.state.tasks.create("database_backup", backupTaskTotal(settings), "定时备份任务已创建")
	go func(taskID string) {
		defer func() {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}()
		s.state.runDatabaseBackupTask(taskID, settings)
	}(task.ID)
}

func (s *appState) readBackupScheduleSettings(ctx context.Context) (backupScheduleSettings, error) {
	values, err := s.readSettings(ctx)
	if err != nil {
		return backupScheduleSettings{}, err
	}
	return backupScheduleSettings{
		Enabled:      settingBool(values, "backup_schedule_enabled"),
		Frequency:    settingString(values, "backup_schedule_frequency"),
		Time:         settingString(values, "backup_schedule_time"),
		IntervalDays: settingInt(values, "backup_schedule_interval_days"),
		Weekday:      settingInt(values, "backup_schedule_weekday"),
		MonthDay:     settingInt(values, "backup_schedule_month_day"),
		RetainCount:  settingInt(values, "backup_schedule_retain_count"),
		WebDAV: webDAVBackupSettings{
			Enabled:   settingBool(values, "backup_webdav_enabled"),
			URL:       settingString(values, "backup_webdav_url"),
			Username:  settingString(values, "backup_webdav_username"),
			Password:  settingString(values, "backup_webdav_password"),
			RemoteDir: settingString(values, "backup_webdav_remote_dir"),
		},
	}, nil
}

func backupScheduleRunKey(settings backupScheduleSettings, now time.Time) (string, bool) {
	hour, minute := parseScheduleClock(settings.Time)
	if now.Hour() != hour || now.Minute() != minute {
		return "", false
	}
	dateKey := now.Format("2006-01-02")
	switch settings.Frequency {
	case "interval_days":
		interval := clampInt(settings.IntervalDays, 1, 365)
		localMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		dayNumber := int(localMidnight.Unix() / int64(24*time.Hour/time.Second))
		if dayNumber%interval != 0 {
			return "", false
		}
		return fmt.Sprintf("interval:%d:%s:%02d:%02d", interval, dateKey, hour, minute), true
	case "weekly":
		weekday := clampInt(settings.Weekday, 1, 7)
		if int(now.Weekday()+6)%7+1 != weekday {
			return "", false
		}
		return fmt.Sprintf("weekly:%d:%s:%02d:%02d", weekday, dateKey, hour, minute), true
	case "monthly":
		day := clampInt(settings.MonthDay, 1, 31)
		if now.Day() != day {
			return "", false
		}
		return fmt.Sprintf("monthly:%d:%s:%02d:%02d", day, dateKey, hour, minute), true
	default:
		return fmt.Sprintf("daily:%s:%02d:%02d", dateKey, hour, minute), true
	}
}

func parseScheduleClock(value string) (int, int) {
	parts := strings.Split(value, ":")
	hour, minute := 3, 0
	if len(parts) > 0 {
		if parsed, err := strconv.Atoi(parts[0]); err == nil {
			hour = clampInt(parsed, 0, 23)
		}
	}
	if len(parts) > 1 {
		if parsed, err := strconv.Atoi(parts[1]); err == nil {
			minute = clampInt(parsed, 0, 59)
		}
	}
	return hour, minute
}

type databaseBackupRunResult struct {
	LocalPath   string
	Filename    string
	WebDAVError error
}

func backupTaskTotal(settings backupScheduleSettings) int {
	if settings.WebDAV.Enabled {
		return 2
	}
	return 1
}

func (s *appState) runDatabaseBackupTask(taskID string, settings backupScheduleSettings) {
	s.tasks.update(taskID, func(task *backgroundTask) {
		task.Total = backupTaskTotal(settings)
		task.Message = "正在生成本地备份"
	})
	ctx, cancel := context.WithTimeout(context.Background(), databaseBackupTimeout)
	defer cancel()
	result, err := createScheduledDatabaseBackup(ctx, settings)
	if err != nil {
		s.tasks.fail(taskID, err)
		fmt.Printf("database backup failed: %v\n", err)
		return
	}
	if settings.WebDAV.Enabled {
		if result.WebDAVError != nil {
			message := fmt.Sprintf("本地备份已保存，WebDAV 上传失败: %v", result.WebDAVError)
			s.tasks.update(taskID, func(task *backgroundTask) {
				task.Total = 2
				task.Done = 2
				task.Success = 1
				task.Failed = 1
				task.Message = message
			})
			s.tasks.finish(taskID)
			fmt.Printf("database backup webdav upload failed: %v\n", result.WebDAVError)
			return
		}
		s.tasks.update(taskID, func(task *backgroundTask) {
			task.Total = 2
			task.Done = 2
			task.Success = 2
			task.Message = "本地备份和 WebDAV 上传已完成"
		})
		s.tasks.finish(taskID)
		return
	}
	s.tasks.update(taskID, func(task *backgroundTask) {
		task.Total = 1
		task.Done = 1
		task.Success = 1
		task.Message = "本地备份已完成"
	})
	s.tasks.finish(taskID)
}

func createScheduledDatabaseBackup(ctx context.Context, settings backupScheduleSettings) (databaseBackupRunResult, error) {
	outputDir := backupStorageDir()
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return databaseBackupRunResult{}, err
	}
	filename := fmt.Sprintf("mailplus-db-backup-%s.dump", time.Now().Format("20060102-150405"))
	outputPath := filepath.Join(outputDir, filename)
	if err := dumpDatabaseToFile(ctx, outputPath); err != nil {
		_ = os.Remove(outputPath)
		return databaseBackupRunResult{}, err
	}
	cleanupLocalDatabaseBackups(clampInt(settings.RetainCount, 1, 365))
	result := databaseBackupRunResult{LocalPath: outputPath, Filename: filename}
	if settings.WebDAV.Enabled {
		if err := uploadBackupToWebDAV(ctx, settings.WebDAV, outputPath, filename); err != nil {
			result.WebDAVError = err
			return result, nil
		}
		if err := cleanupWebDAVDatabaseBackups(ctx, settings.WebDAV, clampInt(settings.RetainCount, 1, 365)); err != nil {
			result.WebDAVError = err
		}
	}
	return result, nil
}

func dumpDatabaseToFile(ctx context.Context, outputPath string) error {
	tool, err := postgresToolPath("pg_dump")
	if err != nil {
		return err
	}
	dsn := databaseURL()
	cmd := exec.CommandContext(
		ctx,
		tool,
		"--format=custom",
		"--clean",
		"--if-exists",
		"--no-owner",
		"--no-privileges",
		"--file", outputPath,
		"--dbname", dsn,
	)
	cmd.Env = postgresToolEnv(tool)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errors.New(postgresCommandMessage("数据库备份失败", dsn, output, err, ctx.Err()))
	}
	return nil
}

func cleanupLocalDatabaseBackups(retainCount int) {
	dir := backupStorageDir()
	entries, err := os.ReadDir(dir)
	if err != nil || retainCount <= 0 {
		return
	}
	files := make([]databaseBackupFileResponse, 0)
	for _, entry := range entries {
		if entry.IsDir() || !isDatabaseBackupFile(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, databaseBackupFileResponse{
			Name:       entry.Name(),
			Size:       info.Size(),
			CreatedAt:  info.ModTime().Format(time.RFC3339),
			ModifiedAt: info.ModTime().Format(time.RFC3339),
			Directory:  dir,
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModifiedAt > files[j].ModifiedAt
	})
	for index, file := range files {
		if index < retainCount {
			continue
		}
		_ = os.Remove(filepath.Join(file.Directory, file.Name))
	}
}

func uploadBackupToWebDAV(ctx context.Context, settings webDAVBackupSettings, localPath string, filename string) error {
	baseURL := strings.TrimSpace(settings.URL)
	if baseURL == "" {
		return errors.New("WebDAV 地址为空")
	}
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	targetURL, err := buildWebDAVFileURL(baseURL, settings.RemoteDir, filename)
	if err != nil {
		return err
	}
	if err := ensureWebDAVDirectory(ctx, baseURL, settings); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, file)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	if settings.Username != "" || settings.Password != "" {
		req.SetBasicAuth(settings.Username, settings.Password)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		response.Body.Close()
		return fmt.Errorf("WebDAV 上传失败: %s %s", response.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func testWebDAVBackupStorage(ctx context.Context, settings webDAVBackupSettings) error {
	baseURL := strings.TrimSpace(settings.URL)
	if baseURL == "" {
		return errors.New("WebDAV 地址为空")
	}
	if err := ensureWebDAVDirectory(ctx, baseURL, settings); err != nil {
		return err
	}
	filename := fmt.Sprintf(".mailplus-webdav-test-%s.tmp", time.Now().Format("20060102-150405"))
	targetURL, err := buildWebDAVFileURL(baseURL, settings.RemoteDir, filename)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, bytes.NewReader([]byte("mailplus webdav test")))
	if err != nil {
		return err
	}
	if settings.Username != "" || settings.Password != "" {
		req.SetBasicAuth(settings.Username, settings.Password)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		status := response.Status
		response.Body.Close()
		return fmt.Errorf("WebDAV 测试上传失败: %s %s", status, strings.TrimSpace(string(body)))
	}
	response.Body.Close()
	if err := deleteWebDAVFile(ctx, settings, filename); err != nil {
		return fmt.Errorf("WebDAV 测试文件删除失败: %w", err)
	}
	return nil
}

type webDAVBackupFileInfo struct {
	Name       string
	ModifiedAt time.Time
}

type webDAVMultiStatus struct {
	Responses []webDAVResponse `xml:"response"`
}

type webDAVResponse struct {
	Href     string           `xml:"href"`
	Propstat []webDAVPropstat `xml:"propstat"`
}

type webDAVPropstat struct {
	Prop webDAVProp `xml:"prop"`
}

type webDAVProp struct {
	GetLastModified string `xml:"getlastmodified"`
}

func cleanupWebDAVDatabaseBackups(ctx context.Context, settings webDAVBackupSettings, retainCount int) error {
	if retainCount <= 0 {
		return nil
	}
	files, err := listWebDAVDatabaseBackups(ctx, settings)
	if err != nil {
		return err
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].ModifiedAt.IsZero() && !files[j].ModifiedAt.IsZero() {
			return files[i].ModifiedAt.After(files[j].ModifiedAt)
		}
		return files[i].Name > files[j].Name
	})
	for index, file := range files {
		if index < retainCount {
			continue
		}
		if err := deleteWebDAVFile(ctx, settings, file.Name); err != nil {
			return err
		}
	}
	return nil
}

func listWebDAVDatabaseBackups(ctx context.Context, settings webDAVBackupSettings) ([]webDAVBackupFileInfo, error) {
	baseURL := strings.TrimSpace(settings.URL)
	if baseURL == "" {
		return nil, errors.New("WebDAV 地址为空")
	}
	dirURL, err := buildWebDAVDirectoryURL(baseURL, settings.RemoteDir)
	if err != nil {
		return nil, err
	}
	propfindBody := `<?xml version="1.0" encoding="utf-8"?><propfind xmlns="DAV:"><prop><getlastmodified /></prop></propfind>`
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", dirURL, strings.NewReader(propfindBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	if settings.Username != "" || settings.Password != "" {
		req.SetBasicAuth(settings.Username, settings.Password)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return nil, fmt.Errorf("WebDAV 读取备份列表失败: %s %s", response.Status, strings.TrimSpace(string(body)))
	}
	var result webDAVMultiStatus
	if err := xml.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&result); err != nil {
		return nil, err
	}
	files := make([]webDAVBackupFileInfo, 0)
	for _, item := range result.Responses {
		name := webDAVBackupNameFromHref(item.Href)
		if !isManagedDatabaseBackupFile(name) {
			continue
		}
		files = append(files, webDAVBackupFileInfo{
			Name:       name,
			ModifiedAt: webDAVResponseModifiedAt(item),
		})
	}
	return files, nil
}

func webDAVResponseModifiedAt(response webDAVResponse) time.Time {
	for _, propstat := range response.Propstat {
		if value := strings.TrimSpace(propstat.Prop.GetLastModified); value != "" {
			if parsed, err := http.ParseTime(value); err == nil {
				return parsed
			}
		}
	}
	return time.Time{}
}

func webDAVBackupNameFromHref(href string) string {
	parsed, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return ""
	}
	value, err := url.PathUnescape(path.Base(parsed.Path))
	if err != nil {
		value = path.Base(parsed.Path)
	}
	return filepath.Base(value)
}

func deleteWebDAVFile(ctx context.Context, settings webDAVBackupSettings, filename string) error {
	baseURL := strings.TrimSpace(settings.URL)
	if baseURL == "" {
		return errors.New("WebDAV 地址为空")
	}
	targetURL, err := buildWebDAVFileURL(baseURL, settings.RemoteDir, filename)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, targetURL, nil)
	if err != nil {
		return err
	}
	if settings.Username != "" || settings.Password != "" {
		req.SetBasicAuth(settings.Username, settings.Password)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || (response.StatusCode >= 200 && response.StatusCode < 300) {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
	return fmt.Errorf("WebDAV 删除旧备份失败: %s %s", response.Status, strings.TrimSpace(string(body)))
}

func ensureWebDAVDirectory(ctx context.Context, baseURL string, settings webDAVBackupSettings) error {
	remoteDir := strings.TrimSpace(settings.RemoteDir)
	if remoteDir == "" || remoteDir == "/" {
		return nil
	}
	dirURLs, err := buildWebDAVDirectoryURLs(baseURL, remoteDir)
	if err != nil {
		return err
	}
	for _, dirURL := range dirURLs {
		req, err := http.NewRequestWithContext(ctx, "MKCOL", dirURL, nil)
		if err != nil {
			return err
		}
		if settings.Username != "" || settings.Password != "" {
			req.SetBasicAuth(settings.Username, settings.Password)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		if response.StatusCode == http.StatusCreated || response.StatusCode == http.StatusMethodNotAllowed || response.StatusCode == http.StatusOK {
			response.Body.Close()
			continue
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			response.Body.Close()
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("WebDAV 创建目录失败: %s %s", response.Status, strings.TrimSpace(string(body)))
	}

	return nil
}

func buildWebDAVFileURL(baseURL string, remoteDir string, filename string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	joinedPath := parsed.Path
	if joinedPath == "" {
		joinedPath = "/"
	}
	cleanRemoteDir := strings.TrimSpace(remoteDir)
	if cleanRemoteDir != "" && cleanRemoteDir != "/" {
		joinedPath = path.Join(joinedPath, cleanRemoteDir)
	}
	joinedPath = path.Join(joinedPath, filename)
	if !strings.HasPrefix(joinedPath, "/") {
		joinedPath = "/" + joinedPath
	}
	parsed.Path = joinedPath
	return parsed.String(), nil
}

func buildWebDAVDirectoryURL(baseURL string, remoteDir string) (string, error) {
	dirURLs, err := buildWebDAVDirectoryURLs(baseURL, remoteDir)
	if err != nil {
		return "", err
	}
	if len(dirURLs) > 0 {
		return dirURLs[len(dirURLs)-1], nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String(), nil
}

func buildWebDAVDirectoryURLs(baseURL string, remoteDir string) ([]string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	joinedPath := parsed.Path
	if joinedPath == "" {
		joinedPath = "/"
	}
	parts := strings.FieldsFunc(strings.TrimSpace(remoteDir), func(r rune) bool {
		return r == '/' || r == '\\'
	})
	urls := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		joinedPath = path.Join(joinedPath, part)
		if !strings.HasPrefix(joinedPath, "/") {
			joinedPath = "/" + joinedPath
		}
		next := *parsed
		next.Path = joinedPath
		urls = append(urls, next.String())
	}
	return urls, nil
}

func settingBool(values map[string]interface{}, key string) bool {
	value, _ := values[key].(bool)
	return value
}

func settingString(values map[string]interface{}, key string) string {
	value, _ := values[key].(string)
	return value
}

func settingInt(values map[string]interface{}, key string) int {
	switch value := values[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	case string:
		parsed, _ := strconv.Atoi(value)
		return parsed
	default:
		return 0
	}
}

func clampInt(value int, min int, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func postgresToolPath(name string) (string, error) {
	executable := name
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}

	if binDir := strings.TrimSpace(os.Getenv("POSTGRES_BIN")); binDir != "" {
		candidate := filepath.Join(binDir, executable)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	if executable != name {
		if path, err := exec.LookPath(executable); err == nil {
			return path, nil
		}
	}

	if runtime.GOOS == "windows" {
		matches, _ := filepath.Glob(filepath.Join(`C:\Program Files\PostgreSQL`, "*", "bin", executable))
		sort.Strings(matches)
		for i := len(matches) - 1; i >= 0; i-- {
			if info, err := os.Stat(matches[i]); err == nil && !info.IsDir() {
				return matches[i], nil
			}
		}
	}

	return "", fmt.Errorf("找不到 PostgreSQL 工具 %s，请安装 PostgreSQL 客户端，或设置 POSTGRES_BIN", name)
}

func postgresToolEnv(toolPath string) []string {
	env := os.Environ()
	binDir := strings.TrimSpace(os.Getenv("POSTGRES_BIN"))
	if binDir == "" {
		binDir = filepath.Dir(toolPath)
	}
	if binDir == "." || binDir == "" {
		return env
	}
	nextPath := binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	return upsertEnv(env, "PATH", nextPath)
}

func upsertEnv(env []string, key string, value string) []string {
	prefix := strings.ToUpper(key) + "="
	entry := key + "=" + value
	for index, item := range env {
		if strings.HasPrefix(strings.ToUpper(item), prefix) {
			env[index] = entry
			return env
		}
	}
	return append(env, entry)
}

func postgresCommandMessage(prefix string, dsn string, output []byte, err error, ctxErr error) string {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return prefix + "：操作超时，请稍后重试"
	}

	detail := strings.TrimSpace(string(output))
	if detail == "" && err != nil {
		detail = err.Error()
	}
	detail = redactDatabaseURL(detail, dsn)
	if len([]rune(detail)) > 1200 {
		runes := []rune(detail)
		detail = string(runes[len(runes)-1200:])
	}
	if detail == "" {
		return prefix
	}
	return prefix + "：" + detail
}

func redactDatabaseURL(text string, dsn string) string {
	if dsn == "" || text == "" {
		return text
	}
	redacted := strings.ReplaceAll(text, dsn, "[DATABASE_URL]")
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.User == nil {
		return redacted
	}
	if password, ok := parsed.User.Password(); ok && password != "" {
		redacted = strings.ReplaceAll(redacted, password, "******")
	}
	return redacted
}

func (s *appState) health(c *gin.Context) {
	c.JSON(http.StatusOK, apiResponse{
		Code: 0,
		Data: gin.H{"status": "ok"},
		Msg:  "ok",
	})
}

func (s *appState) checkAppUpdate(c *gin.Context) {
	refreshValue := c.Query("refresh")
	forceValue := c.Query("force")
	forceRefresh := refreshValue == "1" || strings.EqualFold(refreshValue, "true") || forceValue == "1" || strings.EqualFold(forceValue, "true")
	result := s.updates.getOrRefresh(c.Request.Context(), forceRefresh)
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: result, Msg: "ok"})
}

func (cache *updateCheckCache) getOrRefresh(ctx context.Context, forceRefresh bool) updateCheckResponse {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	now := time.Now()
	if !forceRefresh && !cache.cachedAt.IsZero() && now.Sub(cache.cachedAt) < updateCheckCacheTTL {
		return cache.result
	}

	result := checkLatestAppRelease(ctx, now)
	if result.Status == "error" && !cache.lastSuccessAt.IsZero() {
		cachedResult := cache.lastSuccess
		cachedResult.Message = result.Message + "，已显示上次成功检查结果"
		cachedResult.CheckedAt = result.CheckedAt
		cachedResult.UsingCached = true
		cache.result = cachedResult
		cache.cachedAt = time.Now()
		return cachedResult
	}

	if result.Status != "error" {
		result.UsingCached = false
		cache.lastSuccess = result
		cache.lastSuccessAt = time.Now()
	}
	cache.result = result
	cache.cachedAt = time.Now()
	return result
}

func checkLatestAppRelease(ctx context.Context, checkedAt time.Time) updateCheckResponse {
	currentVersion := normalizeVersionText(appVersion)
	repo := normalizeGitHubRepo(appUpdateGitHubRepo)
	sourceURL := ""
	releaseURL := ""
	if repo != "" {
		sourceURL = fmt.Sprintf(githubLatestReleaseAPI, repo)
		releaseURL = fmt.Sprintf(githubLatestReleaseWeb, repo)
	}

	result := updateCheckResponse{
		CurrentVersion: currentVersion,
		Status:         "error",
		SourceURL:      sourceURL,
		ReleaseURL:     releaseURL,
		Message:        "检查更新失败",
		CheckedAt:      checkedAt.Format(time.RFC3339),
	}

	if !isUsableGitHubRepo(repo) {
		result.Message = "固定更新仓库未配置"
		return result
	}

	release, err := fetchLatestGitHubRelease(ctx, sourceURL)
	if err != nil {
		result.Message = err.Error()
		return result
	}

	latestVersion := normalizeVersionText(release.TagName)
	result.LatestVersion = latestVersion
	if strings.TrimSpace(release.HTMLURL) != "" {
		result.ReleaseURL = strings.TrimSpace(release.HTMLURL)
	}
	if compareVersions(currentVersion, latestVersion) < 0 {
		result.HasUpdate = true
		result.Status = "outdated"
		result.Message = "发现新版本"
	} else {
		result.Status = "latest"
		result.Message = "已是最新版本"
	}
	return result
}

func normalizeGitHubRepo(value string) string {
	repo := strings.TrimSpace(value)
	repo = strings.TrimPrefix(repo, "https://github.com/")
	repo = strings.TrimPrefix(repo, "http://github.com/")
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.Trim(repo, "/")
	parts := strings.Split(repo, "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return repo
}

func isUsableGitHubRepo(repo string) bool {
	if repo == "" || strings.Contains(repo, "YOUR_GITHUB_OWNER") || strings.Contains(repo, "YOUR_REPO") {
		return false
	}
	return strings.Count(repo, "/") == 1 && !strings.ContainsAny(repo, " \t\r\n")
}

func fetchLatestGitHubRelease(ctx context.Context, sourceURL string) (githubLatestReleaseResponse, error) {
	var lastErr error
	for attempt := 1; attempt <= updateCheckMaxAttempts; attempt++ {
		release, retryable, err := fetchLatestGitHubReleaseOnce(ctx, sourceURL)
		if err == nil {
			return release, nil
		}
		lastErr = err
		if !retryable || attempt == updateCheckMaxAttempts {
			break
		}
		delay := time.Duration(attempt) * updateCheckRetryDelay
		select {
		case <-ctx.Done():
			return githubLatestReleaseResponse{}, friendlyUpdateCheckError(ctx.Err())
		case <-time.After(delay):
		}
	}
	return githubLatestReleaseResponse{}, lastErr
}

func fetchLatestGitHubReleaseOnce(ctx context.Context, sourceURL string) (githubLatestReleaseResponse, bool, error) {
	requestCtx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return githubLatestReleaseResponse{}, false, fmt.Errorf("创建更新检查请求失败")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "mail-admin/"+strings.TrimPrefix(appVersion, "v"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return githubLatestReleaseResponse{}, true, friendlyUpdateCheckError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return githubLatestReleaseResponse{}, isRetryableUpdateCheckStatus(resp.StatusCode), friendlyUpdateCheckStatusMessage(resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, updateCheckMaxBodySize+1))
	if err != nil {
		return githubLatestReleaseResponse{}, true, fmt.Errorf("读取更新信息失败，请稍后重试")
	}
	if len(body) > updateCheckMaxBodySize {
		return githubLatestReleaseResponse{}, false, fmt.Errorf("更新信息过大")
	}

	var release githubLatestReleaseResponse
	if err := json.Unmarshal(body, &release); err != nil {
		return githubLatestReleaseResponse{}, true, fmt.Errorf("GitHub 更新接口返回异常，请稍后重试")
	}
	if strings.TrimSpace(release.TagName) == "" {
		return githubLatestReleaseResponse{}, false, fmt.Errorf("GitHub Release 缺少版本号")
	}
	return release, false, nil
}

func friendlyUpdateCheckError(err error) error {
	if err == nil {
		return fmt.Errorf("检查更新失败，请稍后重试")
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("连接 GitHub 超时，请稍后重试")
	}
	return fmt.Errorf("连接 GitHub 失败，请稍后重试")
}

func friendlyUpdateCheckStatusMessage(statusCode int) error {
	switch statusCode {
	case http.StatusNotFound:
		return fmt.Errorf("没有找到 GitHub Release，请确认仓库已公开并已发布 Release")
	case http.StatusForbidden:
		return fmt.Errorf("GitHub 暂时拒绝更新检查请求，请稍后重试")
	case http.StatusTooManyRequests:
		return fmt.Errorf("GitHub 请求过于频繁，请稍后重试")
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return fmt.Errorf("连接 GitHub 超时，请稍后重试")
	case http.StatusInternalServerError:
		return fmt.Errorf("GitHub 服务暂时不可用，请稍后重试")
	default:
		return fmt.Errorf("GitHub 更新接口暂时不可用，请稍后重试")
	}
}

func isRetryableUpdateCheckStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}

func normalizeVersionText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(value), "v") {
		return "v" + strings.TrimSpace(value[1:])
	}
	return "v" + value
}

func compareVersions(current string, latest string) int {
	currentParts := versionNumberParts(current)
	latestParts := versionNumberParts(latest)
	maxLen := len(currentParts)
	if len(latestParts) > maxLen {
		maxLen = len(latestParts)
	}
	for i := 0; i < maxLen; i++ {
		currentPart, latestPart := 0, 0
		if i < len(currentParts) {
			currentPart = currentParts[i]
		}
		if i < len(latestParts) {
			latestPart = latestParts[i]
		}
		if currentPart < latestPart {
			return -1
		}
		if currentPart > latestPart {
			return 1
		}
	}
	return 0
}

func versionNumberParts(value string) []int {
	matches := regexp.MustCompile(`\d+`).FindAllString(value, -1)
	parts := make([]int, 0, len(matches))
	for _, match := range matches {
		part, err := strconv.Atoi(match)
		if err == nil {
			parts = append(parts, part)
		}
	}
	return parts
}

func (s *appState) readPublicSettings(ctx context.Context) (publicSettings, error) {
	settings, err := s.db.SystemSetting.Query().All(ctx)
	if err != nil {
		return publicSettings{}, err
	}

	values := map[string]string{}
	for _, item := range settings {
		values[item.Key] = item.Value
	}
	defaultPageSize := parsePublicTablePageSize(values["table_default_page_size"], 20)

	return publicSettings{
		SiteName:             valueOrDefault(values["site_name"], "\u90ae\u7bb1\u7ba1\u7406\u7cfb\u7edf"),
		SiteLogo:             siteLogoOrDefault(values["site_logo"]),
		SiteSubtitle:         valueOrDefault(values["site_subtitle"], "\u6279\u91cf\u8d26\u53f7\u4e0e\u4efb\u52a1\u7ba1\u7406\u5e73\u53f0"),
		TableDefaultPageSize: defaultPageSize,
		TablePageSizeOptions: parsePublicTablePageSizeOptions(values["table_page_size_options"], defaultPageSize),
	}, nil
}

func (s *appState) getPublicSettingsBootstrap(c *gin.Context) {
	settings, err := s.readPublicSettings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u8bfb\u53d6\u7cfb\u7edf\u8bbe\u7f6e\u5931\u8d25"})
		return
	}

	payload, err := json.Marshal(settings)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u751f\u6210\u521d\u59cb\u8bbe\u7f6e\u5931\u8d25"})
		return
	}

	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(
		http.StatusOK,
		"application/javascript; charset=utf-8",
		[]byte("window.__APP_PUBLIC_SETTINGS__="+string(payload)+";try{localStorage.setItem('mail_public_settings',JSON.stringify(window.__APP_PUBLIC_SETTINGS__))}catch(e){};\n"),
	)
}

func (s *appState) getPublicSettings(c *gin.Context) {
	settings, err := s.readPublicSettings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u8bfb\u53d6\u7cfb\u7edf\u8bbe\u7f6e\u5931\u8d25"})
		return
	}

	c.JSON(http.StatusOK, apiResponse{
		Code: 0,
		Data: settings,
		Msg:  "ok",
	})
}

func (s *appState) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u8bf7\u6c42\u53c2\u6570\u9519\u8bef"})
		return
	}

	account := req.Account
	if account == "" {
		account = req.Email
	}

	admin, err := s.db.User.Query().Where(user.Username(account)).Only(c.Request.Context())
	if err != nil || !admin.Enabled {
		c.JSON(http.StatusUnauthorized, apiResponse{Code: 401, Msg: "\u8d26\u53f7\u6216\u5bc6\u7801\u9519\u8bef"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, apiResponse{Code: 401, Msg: "\u8d26\u53f7\u6216\u5bc6\u7801\u9519\u8bef"})
		return
	}

	accessToken := newToken()
	refreshToken := newToken()
	expiresIn := int((2 * time.Hour).Seconds())
	s.sessions.set(accessToken, admin.ID, time.Duration(expiresIn)*time.Second)
	s.sessions.set(refreshToken, admin.ID, 24*time.Hour)
	mustChangePassword := userMustChangePassword(c.Request.Context(), admin.ID)

	c.JSON(http.StatusOK, apiResponse{
		Code: 0,
		Data: authResponse{
			AccessToken:        accessToken,
			RefreshToken:       refreshToken,
			ExpiresIn:          expiresIn,
			TokenType:          "Bearer",
			MustChangePassword: mustChangePassword,
			User: authUser{
				ID:        admin.ID,
				Username:  admin.Username,
				Email:     admin.Email,
				AvatarURL: admin.AvatarURL,
				Balance:   admin.Balance,
				Role:      admin.Role,
				Status:    userStatus(admin),
				CreatedAt: admin.CreatedAt.Format(time.RFC3339),
			},
		},
		Msg: "ok",
	})
}

func (s *appState) getProfile(c *gin.Context) {
	current, ok := s.currentUser(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: toAuthUser(current), Msg: "ok"})
}

func (s *appState) updateProfile(c *gin.Context) {
	current, ok := s.currentUser(c)
	if !ok {
		return
	}

	var req updateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u8bf7\u6c42\u53c2\u6570\u9519\u8bef"})
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u7528\u6237\u540d\u4e0d\u80fd\u4e3a\u7a7a"})
		return
	}

	update := current.Update().SetUsername(req.Username)
	if strings.TrimSpace(req.Email) != "" {
		update.SetEmail(strings.TrimSpace(req.Email))
	}
	if req.AvatarURL != nil {
		update.SetAvatarURL(strings.TrimSpace(*req.AvatarURL))
	}

	item, err := update.Save(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u8d44\u6599\u66f4\u65b0\u5931\u8d25"})
		return
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: toAuthUser(item), Msg: "ok"})
}

func (s *appState) changePassword(c *gin.Context) {
	current, ok := s.currentUser(c)
	if !ok {
		return
	}

	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u8bf7\u6c42\u53c2\u6570\u9519\u8bef"})
		return
	}
	if len(req.NewPassword) < 8 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u5bc6\u7801\u81f3\u5c11\u9700\u8981 8 \u4e2a\u5b57\u7b26"})
		return
	}
	if req.NewPassword == defaultAdminPassword {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "New password cannot use the default initial password"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(current.PasswordHash), []byte(req.OldPassword)); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u5f53\u524d\u5bc6\u7801\u9519\u8bef"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u5bc6\u7801\u4fee\u6539\u5931\u8d25"})
		return
	}
	if _, err := current.Update().SetPasswordHash(string(hash)).Save(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u5bc6\u7801\u4fee\u6539\u5931\u8d25"})
		return
	}
	if err := setMustChangePassword(c.Request.Context(), current.ID, false); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u5bc6\u7801\u4fee\u6539\u5931\u8d25"})
		return
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
}

func (s *appState) listUsers(c *gin.Context) {
	ctx := c.Request.Context()
	page := parsePositiveInt(c.Query("page"), 1)
	pageSize := parsePositiveInt(c.Query("page_size"), 20)
	search := c.Query("search")
	role := c.Query("role")
	status := c.Query("status")
	sortBy := c.DefaultQuery("sort_by", user.FieldCreatedAt)
	sortOrder := normalizeSortOrder(c.Query("sort_order"))

	query := s.db.User.Query()
	if search != "" {
		query = query.Where(user.Or(user.UsernameContainsFold(search), user.EmailContainsFold(search)))
	}
	if role != "" {
		query = query.Where(user.Role(role))
	}
	if status == "active" {
		query = query.Where(user.Enabled(true))
	}
	if status == "disabled" {
		query = query.Where(user.Enabled(false))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u83b7\u53d6\u7528\u6237\u5217\u8868\u5931\u8d25"})
		return
	}

	sortField := user.FieldCreatedAt
	switch sortBy {
	case "id":
		sortField = user.FieldID
	case "email":
		sortField = user.FieldEmail
	case "username":
		sortField = user.FieldUsername
	case "role":
		sortField = user.FieldRole
	case "balance":
		sortField = user.FieldBalance
	case "status":
		sortField = user.FieldEnabled
	case "created_at":
		sortField = user.FieldCreatedAt
	}

	if sortOrder == "DESC" {
		if sortField == user.FieldID {
			query = query.Order(ent.Desc(sortField))
		} else {
			query = query.Order(ent.Desc(sortField), ent.Desc(user.FieldID))
		}
	} else if sortField == user.FieldID {
		query = query.Order(ent.Asc(sortField))
	} else {
		query = query.Order(ent.Asc(sortField), ent.Asc(user.FieldID))
	}

	items, err := query.Offset((page - 1) * pageSize).Limit(pageSize).All(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u83b7\u53d6\u7528\u6237\u5217\u8868\u5931\u8d25"})
		return
	}

	users := make([]adminUserResponse, 0, len(items))
	for _, item := range items {
		users = append(users, toAdminUser(item))
	}

	pages := 0
	if total > 0 {
		pages = (total + pageSize - 1) / pageSize
	}

	c.JSON(http.StatusOK, apiResponse{
		Code: 0,
		Data: userListResponse{Items: users, Total: total, Page: page, PageSize: pageSize, Pages: pages},
		Msg:  "ok",
	})
}

func (s *appState) createUser(c *gin.Context) {
	c.JSON(http.StatusForbidden, apiResponse{Code: 403, Msg: "User creation is disabled"})
}

func createUserWithReusableID(ctx context.Context, username, email, passwordHash string, balance float64, role string, enabled bool) (*ent.User, error) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return nil, err
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now()
	var item ent.User
	err = tx.QueryRowContext(ctx, `
WITH next_id AS (
	SELECT COALESCE(
		(
			SELECT MIN(candidate)
			FROM generate_series(1, COALESCE((SELECT MAX(id) FROM users), 0) + 1) AS candidate
			WHERE NOT EXISTS (SELECT 1 FROM users WHERE id = candidate)
		),
		1
	) AS id
),
inserted AS (
	INSERT INTO users (id, username, email, password_hash, balance, role, enabled, created_at, updated_at)
	SELECT id, $1, $2, $3, $4, $5, $6, $7, $7 FROM next_id
	RETURNING id, username, email, avatar_url, balance, password_hash, role, enabled, created_at, updated_at
)
SELECT id, username, COALESCE(email, ''), COALESCE(avatar_url, ''), balance, password_hash, role, enabled, created_at, updated_at
FROM inserted
`, username, email, passwordHash, balance, role, enabled, now).Scan(
		&item.ID,
		&item.Username,
		&item.Email,
		&item.AvatarURL,
		&item.Balance,
		&item.PasswordHash,
		&item.Role,
		&item.Enabled,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('users', 'id'), COALESCE((SELECT MAX(id) FROM users), 1), true)`); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &item, nil
}

func compactUserIDs(ctx context.Context) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
WITH ordered AS (
	SELECT id AS old_id, row_number() OVER (ORDER BY id) AS new_id
	FROM users
)
UPDATE balance_records r
SET user_id = ordered.new_id
FROM ordered
WHERE r.user_id = ordered.old_id
`); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE users SET id = -id`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
WITH ordered AS (
	SELECT id, row_number() OVER (ORDER BY -id) AS new_id
	FROM users
)
UPDATE users u
SET id = ordered.new_id
FROM ordered
WHERE u.id = ordered.id
`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('users', 'id'), GREATEST(COALESCE((SELECT MAX(id) FROM users), 1), 1), true)`); err != nil {
		return err
	}

	return tx.Commit()
}

func ensureSingleAdmin(ctx context.Context) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.ExecContext(ctx, `
WITH first_admin AS (
	SELECT MIN(id) AS id
	FROM users
	WHERE role = 'admin'
)
UPDATE users
SET role = 'user'
WHERE role = 'admin'
AND id <> (SELECT id FROM first_admin)
`)
	return err
}

func ensureBalanceRecordTable(ctx context.Context) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS balance_records (
	id SERIAL PRIMARY KEY,
	user_id INTEGER NOT NULL,
	type TEXT NOT NULL,
	amount DOUBLE PRECISION NOT NULL,
	balance_after DOUBLE PRECISION NOT NULL,
	remark TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
)
`)
	return err
}

func ensureBackgroundTaskTable(ctx context.Context) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err = db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS background_tasks (
	id TEXT PRIMARY KEY,
	type TEXT NOT NULL,
	status TEXT NOT NULL,
	total INTEGER NOT NULL DEFAULT 0,
	done INTEGER NOT NULL DEFAULT 0,
	success INTEGER NOT NULL DEFAULT 0,
	failed INTEGER NOT NULL DEFAULT 0,
	message TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
)
`); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, `ALTER TABLE background_tasks ADD COLUMN IF NOT EXISTS result_path TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, `ALTER TABLE background_tasks ADD COLUMN IF NOT EXISTS result_name TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, `ALTER TABLE background_tasks ADD COLUMN IF NOT EXISTS result_cleanup_after TIMESTAMP WITH TIME ZONE`); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS background_tasks_updated_at_idx ON background_tasks (updated_at DESC)`); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS background_tasks_result_cleanup_after_idx ON background_tasks (result_cleanup_after) WHERE result_path <> ''`); err != nil {
		return err
	}
	if err = cleanupExpiredTaskResults(ctx, db); err != nil {
		return err
	}
	return cleanupStaleBackgroundTasks(ctx, db)
}

func ensureMailGroupTable(ctx context.Context) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err = db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS mail_groups (
	id SERIAL PRIMARY KEY,
	parent_id INTEGER NOT NULL DEFAULT 0,
	name TEXT NOT NULL,
	system BOOLEAN NOT NULL DEFAULT FALSE,
	created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
)
`); err != nil {
		return err
	}

	if _, err = db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS mail_groups_name_parent_idx ON mail_groups (parent_id, name)`); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, `ALTER TABLE mail_groups ADD COLUMN IF NOT EXISTS sort_order INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}

	if _, err = db.ExecContext(ctx, `
INSERT INTO mail_groups (id, parent_id, name, system)
VALUES (1, 0, '全部邮箱', TRUE)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, parent_id = 0, system = TRUE
`); err != nil {
		return err
	}

	if _, err = db.ExecContext(ctx, `
INSERT INTO mail_groups (id, parent_id, name, system)
VALUES (2, 0, '默认分组', TRUE)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, parent_id = 0, system = TRUE
`); err != nil {
		return err
	}

	if _, err = db.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('mail_groups', 'id'), GREATEST(COALESCE((SELECT MAX(id) FROM mail_groups), 2), 2), true)`); err != nil {
		return err
	}
	return normalizeGroupSortOrders(ctx, db, "mail_groups")
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

func normalizeGroupSortOrders(ctx context.Context, db *sql.DB, table string) error {
	return normalizeGroupSortOrdersWithExec(ctx, db, table)
}

func normalizeGroupSortOrdersTx(ctx context.Context, tx *sql.Tx, table string) error {
	return normalizeGroupSortOrdersWithExec(ctx, tx, table)
}

func normalizeGroupSortOrdersWithExec(ctx context.Context, exec sqlExecer, table string) error {
	tableName, err := safeGroupTableName(table)
	if err != nil {
		return err
	}
	_, err = exec.ExecContext(ctx, fmt.Sprintf(`
WITH ranked AS (
	SELECT id, ROW_NUMBER() OVER (
		PARTITION BY parent_id
		ORDER BY
			CASE WHEN sort_order > 0 THEN sort_order ELSE 2147483647 END,
			id
	) AS new_sort_order
	FROM %s
	WHERE system = FALSE
)
UPDATE %s AS g
SET sort_order = ranked.new_sort_order
FROM ranked
WHERE g.id = ranked.id AND g.sort_order <> ranked.new_sort_order
`, tableName, tableName))
	return err
}

func safeGroupTableName(table string) (string, error) {
	switch table {
	case "mail_groups", "outlook_groups":
		return table, nil
	default:
		return "", fmt.Errorf("unsupported group table %q", table)
	}
}

func normalizeRequestedGroupSortOrder(requested *int, max int, fallback int) int {
	if max < 1 {
		return 1
	}
	if requested == nil {
		if fallback >= 1 && fallback <= max {
			return fallback
		}
		return max
	}
	value := *requested
	if value < 1 {
		return 1
	}
	if value > max {
		return max
	}
	return value
}

func ensureMailManagementTables(ctx context.Context) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err = db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS mail_servers (
	id SERIAL PRIMARY KEY,
	name TEXT NOT NULL,
	imap_host TEXT NOT NULL,
	smtp_host TEXT NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
)
`); err != nil {
		return err
	}

	alterStatements := []string{
		`ALTER TABLE mail_servers DROP COLUMN IF EXISTS domain`,
		`ALTER TABLE mail_servers DROP COLUMN IF EXISTS receive_port`,
		`ALTER TABLE mail_servers DROP COLUMN IF EXISTS account_type`,
		`ALTER TABLE mail_servers DROP COLUMN IF EXISTS ssl`,
		`ALTER TABLE mail_servers DROP COLUMN IF EXISTS receive_interval`,
		`ALTER TABLE mail_servers DROP COLUMN IF EXISTS send_port`,
		`ALTER TABLE mail_servers DROP COLUMN IF EXISTS send_encryption`,
		`ALTER TABLE mail_servers DROP COLUMN IF EXISTS send_interval`,
		`ALTER TABLE mail_servers DROP COLUMN IF EXISTS disabled`,
		`ALTER TABLE mail_servers DROP COLUMN IF EXISTS remark`,
	}
	for _, statement := range alterStatements {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	if _, err = db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS mail_servers_name_idx ON mail_servers (name)`); err != nil {
		return err
	}

	if err = seedDefaultMailServers(ctx, db); err != nil {
		return err
	}

	_, err = db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS mail_accounts (
	id SERIAL PRIMARY KEY,
	group_id INTEGER NOT NULL DEFAULT 2,
	server_id INTEGER NOT NULL DEFAULT 0,
	email TEXT NOT NULL,
	password TEXT NOT NULL DEFAULT '',
	imap_host TEXT NOT NULL DEFAULT '',
	smtp_host TEXT NOT NULL DEFAULT '',
	imap_protocol TEXT NOT NULL DEFAULT 'IMAP',
	imap_port INTEGER NOT NULL DEFAULT 993,
	imap_ssl BOOLEAN NOT NULL DEFAULT TRUE,
	smtp_protocol TEXT NOT NULL DEFAULT 'SMTP(SSL)',
	smtp_port INTEGER NOT NULL DEFAULT 465,
	smtp_ssl BOOLEAN NOT NULL DEFAULT TRUE,
	remark TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active',
	status_reason TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
)
`)
	if err != nil {
		return err
	}

	if _, err = db.ExecContext(ctx, `ALTER TABLE mail_accounts ADD COLUMN IF NOT EXISTS status_reason TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}

	indexStatements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS mail_accounts_group_email_unique_idx ON mail_accounts (group_id, email)`,
		`DROP INDEX IF EXISTS mail_accounts_email_idx`,
		`CREATE INDEX IF NOT EXISTS mail_accounts_group_id_idx ON mail_accounts (group_id)`,
		`CREATE INDEX IF NOT EXISTS mail_accounts_status_idx ON mail_accounts (status)`,
		`CREATE INDEX IF NOT EXISTS mail_accounts_created_at_id_idx ON mail_accounts (created_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS mail_accounts_group_created_at_id_idx ON mail_accounts (group_id, created_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS mail_accounts_group_id_id_idx ON mail_accounts (group_id, id)`,
		`CREATE INDEX IF NOT EXISTS mail_accounts_server_id_id_idx ON mail_accounts (server_id, id)`,
		`CREATE INDEX IF NOT EXISTS mail_accounts_group_server_id_id_idx ON mail_accounts (group_id, server_id, id)`,
		`CREATE INDEX IF NOT EXISTS mail_accounts_status_id_idx ON mail_accounts (status, id)`,
		`CREATE INDEX IF NOT EXISTS mail_accounts_group_status_id_idx ON mail_accounts (group_id, status, id)`,
		`CREATE INDEX IF NOT EXISTS mail_accounts_group_email_id_idx ON mail_accounts (group_id, email, id)`,
	}
	for _, statement := range indexStatements {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	if _, err = db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS pg_trgm`); err == nil {
		_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS mail_accounts_email_trgm_idx ON mail_accounts USING gin (email gin_trgm_ops)`)
		_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS mail_accounts_remark_trgm_idx ON mail_accounts USING gin (remark gin_trgm_ops)`)
	}
	return nil
}

func seedDefaultMailServers(ctx context.Context, db *sql.DB) error {
	defaultServers := []saveMailServerRequest{
		{Name: "网易企业邮箱IMAP", ImapHost: "imaphz.qiye.163.com", SMTPHost: "smtphz.qiye.163.com"},
		{Name: "腾讯企业邮箱", ImapHost: "imap.exmail.qq.com", SMTPHost: "smtp.exmail.qq.com"},
		{Name: "论客企业邮箱", ImapHost: "edu.icoremail.net", SMTPHost: "edu.icoremail.net"},
		{Name: "网易企业邮箱POP", ImapHost: "pophz.qiye.163.com", SMTPHost: "smtphz.qiye.163.com"},
		{Name: "263企业邮箱", ImapHost: "imap.263.net", SMTPHost: "smtp.263.net"},
		{Name: "QQ邮箱", ImapHost: "imap.qq.com", SMTPHost: "smtp.qq.com"},
		{Name: "网易 163邮箱", ImapHost: "imap.163.com", SMTPHost: "smtp.163.com"},
		{Name: "网易 163VIP邮箱", ImapHost: "imap.vip.163.com", SMTPHost: "smtp.vip.163.com"},
	}
	for _, server := range defaultServers {
		if _, err := db.ExecContext(ctx, `
INSERT INTO mail_servers (name, imap_host, smtp_host)
VALUES ($1, $2, $3)
ON CONFLICT (name) DO UPDATE SET
	imap_host = EXCLUDED.imap_host,
	smtp_host = EXCLUDED.smtp_host,
	updated_at = NOW()
`, server.Name, server.ImapHost, server.SMTPHost); err != nil {
			return err
		}
	}
	return nil
}

func (s *appState) listMailGroups(c *gin.Context) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "获取邮箱分组失败"})
		return
	}
	defer db.Close()

	accountCountWhere := ""
	if queryBool(c, "exclude_card_key_bound") {
		accountCountWhere = `
	WHERE NOT EXISTS (
		SELECT 1
		FROM card_keys ck
		WHERE TRIM(ck.bound_email) <> ''
		  AND LOWER(TRIM(ck.bound_email)) = LOWER(TRIM(mail_accounts.email))
	)`
	}

	rows, err := db.QueryContext(c.Request.Context(), `
SELECT g.id, g.parent_id, g.name, g.system, g.sort_order, COALESCE(stats.count, 0) AS count, g.created_at
FROM mail_groups g
LEFT JOIN (
	SELECT group_id, COUNT(*) AS count
	FROM mail_accounts
	`+accountCountWhere+`
	GROUP BY group_id
) stats ON stats.group_id = g.id
ORDER BY system DESC, sort_order ASC, id ASC
`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "获取邮箱分组失败"})
		return
	}
	defer rows.Close()

	groups := []mailGroupResponse{}
	for rows.Next() {
		var group mailGroupResponse
		var createdAt time.Time
		if err := rows.Scan(&group.ID, &group.ParentID, &group.Name, &group.System, &group.SortOrder, &group.Count, &createdAt); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "获取邮箱分组失败"})
			return
		}
		group.CreatedAt = createdAt.Format(time.RFC3339)
		groups = append(groups, group)
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: groups, Msg: "ok"})
}

func (s *appState) createMailGroup(c *gin.Context) {
	var req saveMailGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "分组名称不能为空"})
		return
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "创建分组失败"})
		return
	}
	defer db.Close()

	if req.ParentID > 0 && !mailGroupExists(c.Request.Context(), db, req.ParentID) {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "父级分组不存在"})
		return
	}
	if req.ParentID > 0 && mailGroupParentID(c.Request.Context(), db, req.ParentID) > 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "子分组下不能继续添加子分组"})
		return
	}
	if req.ParentID > 0 && mailGroupHasAccounts(c.Request.Context(), db, req.ParentID) {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "该分组下已有邮箱，不能继续添加子分组"})
		return
	}

	tx, err := db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "创建分组失败"})
		return
	}
	defer tx.Rollback()

	if err = normalizeGroupSortOrdersTx(c.Request.Context(), tx, "mail_groups"); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "创建分组失败"})
		return
	}

	var siblingCount int
	if err = tx.QueryRowContext(c.Request.Context(), `SELECT COUNT(*) FROM mail_groups WHERE parent_id = $1 AND system = FALSE`, req.ParentID).Scan(&siblingCount); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "创建分组失败"})
		return
	}
	sortOrder := normalizeRequestedGroupSortOrder(req.SortOrder, siblingCount+1, siblingCount+1)
	if _, err = tx.ExecContext(c.Request.Context(), `UPDATE mail_groups SET sort_order = sort_order + 1 WHERE parent_id = $1 AND system = FALSE AND sort_order >= $2`, req.ParentID, sortOrder); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "创建分组失败"})
		return
	}

	var group mailGroupResponse
	var createdAt time.Time
	err = tx.QueryRowContext(c.Request.Context(), `
INSERT INTO mail_groups (parent_id, name, system, sort_order)
VALUES ($1, $2, FALSE, $3)
RETURNING id, parent_id, name, system, sort_order, created_at
`, req.ParentID, req.Name, sortOrder).Scan(&group.ID, &group.ParentID, &group.Name, &group.System, &group.SortOrder, &createdAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "分组名称已存在或创建失败"})
		return
	}
	if err = tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "创建分组失败"})
		return
	}
	group.CreatedAt = createdAt.Format(time.RFC3339)

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: group, Msg: "ok"})
}

func (s *appState) updateMailGroup(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	var req saveMailGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "分组名称不能为空"})
		return
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "编辑分组失败"})
		return
	}
	defer db.Close()

	tx, err := db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "编辑分组失败"})
		return
	}
	defer tx.Rollback()

	if err = normalizeGroupSortOrdersTx(c.Request.Context(), tx, "mail_groups"); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "编辑分组失败"})
		return
	}

	var parentID int
	var currentSortOrder int
	var system bool
	if err = tx.QueryRowContext(c.Request.Context(), `SELECT parent_id, sort_order, system FROM mail_groups WHERE id = $1`, id).Scan(&parentID, &currentSortOrder, &system); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "分组不存在"})
		return
	}
	if system {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "系统分组不能编辑"})
		return
	}

	var siblingCount int
	if err = tx.QueryRowContext(c.Request.Context(), `SELECT COUNT(*) FROM mail_groups WHERE parent_id = $1 AND system = FALSE`, parentID).Scan(&siblingCount); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "编辑分组失败"})
		return
	}
	sortOrder := normalizeRequestedGroupSortOrder(req.SortOrder, siblingCount, currentSortOrder)
	if sortOrder < currentSortOrder {
		if _, err = tx.ExecContext(c.Request.Context(), `UPDATE mail_groups SET sort_order = sort_order + 1 WHERE parent_id = $1 AND system = FALSE AND id <> $2 AND sort_order >= $3 AND sort_order < $4`, parentID, id, sortOrder, currentSortOrder); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "编辑分组失败"})
			return
		}
	} else if sortOrder > currentSortOrder {
		if _, err = tx.ExecContext(c.Request.Context(), `UPDATE mail_groups SET sort_order = sort_order - 1 WHERE parent_id = $1 AND system = FALSE AND id <> $2 AND sort_order <= $3 AND sort_order > $4`, parentID, id, sortOrder, currentSortOrder); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "编辑分组失败"})
			return
		}
	}

	var group mailGroupResponse
	var createdAt time.Time
	err = tx.QueryRowContext(c.Request.Context(), `
UPDATE mail_groups
SET name = $2, sort_order = $3, updated_at = NOW()
WHERE id = $1
RETURNING id, parent_id, name, system, sort_order, created_at
`, id, req.Name, sortOrder).Scan(&group.ID, &group.ParentID, &group.Name, &group.System, &group.SortOrder, &createdAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "分组名称已存在或编辑失败"})
		return
	}
	if err = tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "编辑分组失败"})
		return
	}
	group.CreatedAt = createdAt.Format(time.RFC3339)

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: group, Msg: "ok"})
}

func (s *appState) deleteMailGroup(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除分组失败"})
		return
	}
	defer db.Close()

	if mailGroupIsSystem(c.Request.Context(), db, id) {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "系统分组不能删除"})
		return
	}

	var parentID int
	var sortOrder int
	if err := db.QueryRowContext(c.Request.Context(), `SELECT parent_id, sort_order FROM mail_groups WHERE id = $1`, id).Scan(&parentID, &sortOrder); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "分组不存在"})
		return
	}

	var childCount int
	if err := db.QueryRowContext(c.Request.Context(), `SELECT COUNT(*) FROM mail_groups WHERE parent_id = $1`, id).Scan(&childCount); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除分组失败"})
		return
	}
	if childCount > 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "该分组下有子分组，不能删除"})
		return
	}

	var accountCount int
	if err := db.QueryRowContext(c.Request.Context(), `SELECT COUNT(*) FROM mail_accounts WHERE group_id = $1`, id).Scan(&accountCount); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除分组失败"})
		return
	}
	if accountCount > 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "该分组下有邮箱，不能删除"})
		return
	}

	result, err := db.ExecContext(c.Request.Context(), `DELETE FROM mail_groups WHERE id = $1`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除分组失败"})
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "分组不存在"})
		return
	}
	if _, err := db.ExecContext(c.Request.Context(), `UPDATE mail_groups SET sort_order = sort_order - 1 WHERE parent_id = $1 AND system = FALSE AND sort_order > $2`, parentID, sortOrder); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除分组失败"})
		return
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
}

func (s *appState) listMailServers(c *gin.Context) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "获取服务器失败"})
		return
	}
	defer db.Close()

	rows, err := db.QueryContext(c.Request.Context(), `
SELECT id, name, imap_host, smtp_host, created_at
FROM mail_servers
ORDER BY id ASC
`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "获取服务器失败"})
		return
	}
	defer rows.Close()

	items := []mailServerResponse{}
	for rows.Next() {
		var item mailServerResponse
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.Name, &item.ImapHost, &item.SMTPHost, &createdAt); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "获取服务器失败"})
			return
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		items = append(items, item)
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: items, Msg: "ok"})
}

func (s *appState) createMailServer(c *gin.Context) {
	var req saveMailServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.ImapHost = strings.TrimSpace(req.ImapHost)
	req.SMTPHost = strings.TrimSpace(req.SMTPHost)
	if req.Name == "" || req.ImapHost == "" || req.SMTPHost == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "服务器名称和地址不能为空"})
		return
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "添加服务器失败"})
		return
	}
	defer db.Close()

	var item mailServerResponse
	var createdAt time.Time
	err = db.QueryRowContext(c.Request.Context(), `
INSERT INTO mail_servers (name, imap_host, smtp_host)
VALUES ($1, $2, $3)
RETURNING id, name, imap_host, smtp_host, created_at
`, req.Name, req.ImapHost, req.SMTPHost).Scan(&item.ID, &item.Name, &item.ImapHost, &item.SMTPHost, &createdAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "服务器名称已存在或添加失败"})
		return
	}
	item.CreatedAt = createdAt.Format(time.RFC3339)

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: item, Msg: "ok"})
}

func (s *appState) updateMailServer(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	var req saveMailServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.ImapHost = strings.TrimSpace(req.ImapHost)
	req.SMTPHost = strings.TrimSpace(req.SMTPHost)
	if req.Name == "" || req.ImapHost == "" || req.SMTPHost == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "服务器名称和地址不能为空"})
		return
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "编辑服务器失败"})
		return
	}
	defer db.Close()

	var item mailServerResponse
	var createdAt time.Time
	err = db.QueryRowContext(c.Request.Context(), `
UPDATE mail_servers
SET name = $2, imap_host = $3, smtp_host = $4, updated_at = NOW()
WHERE id = $1
RETURNING id, name, imap_host, smtp_host, created_at
`, id, req.Name, req.ImapHost, req.SMTPHost).Scan(&item.ID, &item.Name, &item.ImapHost, &item.SMTPHost, &createdAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "服务器名称已存在或编辑失败"})
		return
	}
	item.CreatedAt = createdAt.Format(time.RFC3339)

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: item, Msg: "ok"})
}

func (s *appState) deleteMailServer(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除服务器失败"})
		return
	}
	defer db.Close()

	var accountCount int
	if err := db.QueryRowContext(c.Request.Context(), `SELECT COUNT(*) FROM mail_accounts WHERE server_id = $1`, id).Scan(&accountCount); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除服务器失败"})
		return
	}
	if accountCount > 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "该服务器下有邮箱，不能删除"})
		return
	}

	result, err := db.ExecContext(c.Request.Context(), `DELETE FROM mail_servers WHERE id = $1`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除服务器失败"})
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "服务器不存在"})
		return
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
}

func (s *appState) listMailAccounts(c *gin.Context) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "获取邮箱账号失败"})
		return
	}
	defer db.Close()

	search := strings.TrimSpace(c.Query("search"))
	groupID, _ := strconv.Atoi(c.Query("group_id"))
	page, pageSize, offset := parseListPage(c, 20, 500)
	where := []string{"1 = 1"}
	args := []interface{}{}
	if groupID > 0 {
		where = append(where, accountGroupTreeWhere("a", "mail_groups", len(args)+1))
		args = append(args, groupID)
	}
	if search != "" {
		where = append(where, fmt.Sprintf("(a.email ILIKE $%d OR a.remark ILIKE $%d)", len(args)+1, len(args)+1))
		args = append(args, "%"+search+"%")
	}
	if queryBool(c, "exclude_card_key_bound") {
		where = append(where, `NOT EXISTS (
			SELECT 1
			FROM card_keys ck
			WHERE TRIM(ck.bound_email) <> ''
			  AND LOWER(TRIM(ck.bound_email)) = LOWER(TRIM(a.email))
		)`)
	}
	whereSQL := strings.Join(where, " AND ")

	var total, normal int
	if err := db.QueryRowContext(c.Request.Context(), `
SELECT COUNT(*),
       COUNT(*) FILTER (WHERE LOWER(a.status) IN ('active', 'normal', 'ok', 'success'))
FROM mail_accounts a
WHERE `+whereSQL, args...).Scan(&total, &normal); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "鑾峰彇閭璐﹀彿澶辫触"})
		return
	}

	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := normalizeSortOrder(c.Query("sort_order"))
	orderClause := "a.created_at " + sortOrder + ", a.id " + sortOrder
	switch sortBy {
	case "group":
		orderClause = "COALESCE(g.name, '') " + sortOrder + ", a.id " + sortOrder
	case "email":
		orderClause = "a.email " + sortOrder + ", a.id " + sortOrder
	case "server":
		orderClause = "COALESCE(s.name, '') " + sortOrder + ", a.imap_host " + sortOrder + ", a.smtp_host " + sortOrder + ", a.id " + sortOrder
	case "status":
		orderClause = "a.status " + sortOrder + ", a.id " + sortOrder
	case "remark":
		orderClause = "a.remark " + sortOrder + ", a.id " + sortOrder
	case "id":
		orderClause = "a.id " + sortOrder
	}

	limitIndex := len(args) + 1
	offsetIndex := len(args) + 2
	queryArgs := append([]interface{}{}, args...)
	queryArgs = append(queryArgs, pageSize, offset)

	rows, err := db.QueryContext(c.Request.Context(), `
SELECT a.id, a.group_id, COALESCE(g.name, ''), a.email, a.server_id, COALESCE(s.name, ''), a.imap_host, a.smtp_host,
       a.imap_protocol, a.imap_port, a.imap_ssl, a.smtp_protocol, a.smtp_port, a.smtp_ssl, a.remark, a.status, a.status_reason, a.created_at
FROM mail_accounts a
LEFT JOIN mail_groups g ON g.id = a.group_id
LEFT JOIN mail_servers s ON s.id = a.server_id
WHERE `+whereSQL+`
ORDER BY `+orderClause+`
LIMIT $`+strconv.Itoa(limitIndex)+` OFFSET $`+strconv.Itoa(offsetIndex)+`
`, queryArgs...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "获取邮箱账号失败"})
		return
	}
	defer rows.Close()

	items := []mailAccountResponse{}
	for rows.Next() {
		var item mailAccountResponse
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.GroupID, &item.GroupName, &item.Email, &item.ServerID, &item.ServerName, &item.ImapHost, &item.SMTPHost, &item.ImapProtocol, &item.ImapPort, &item.ImapSSL, &item.SMTPProtocol, &item.SMTPPort, &item.SMTPSSL, &item.Remark, &item.Status, &item.StatusReason, &createdAt); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "获取邮箱账号失败"})
			return
		}
		item.CreatedAt = createdAt.Format("2006/01/02 15:04:05")
		items = append(items, item)
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: mailAccountListResponse{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Pages:    calculatePages(total, pageSize),
		Normal:   normal,
		Error:    total - normal,
	}, Msg: "ok"})
}

func (s *appState) exportMailDataZip(c *gin.Context) {
	var req mailDataExportRequest
	_ = c.ShouldBindJSON(&req)
	req.Password = strings.TrimSpace(req.Password)
	if req.Password == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请输入导出密码"})
		return
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "导出邮箱数据失败"})
		return
	}
	defer db.Close()

	filename := fmt.Sprintf("mail-data-%s.zip", time.Now().Format("20060102-150405"))
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if err := writeEncryptedMailDataZip(c.Request.Context(), c.Writer, db, accountExportSelector{IDs: req.IDs, Filter: req.Filter}, req.Password); err != nil {
		return
	}
}

func (s *appState) importMailDataZip(c *gin.Context) {
	password := strings.TrimSpace(c.PostForm("password"))
	if password == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请输入导入密码"})
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请选择 ZIP 数据文件"})
		return
	}

	payload, err := readEncryptedMailDataZip(file, password)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	if len(payload.Groups) == 0 && len(payload.Accounts) == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "ZIP 文件中没有可导入的数据"})
		return
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "导入邮箱数据失败"})
		return
	}
	defer db.Close()

	tx, err := db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "导入邮箱数据失败"})
		return
	}
	defer tx.Rollback()

	groupIDMap, groupCount, err := importMailGroups(c.Request.Context(), tx, payload.Groups, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	accountCount, err := importMailAccounts(c.Request.Context(), tx, payload.Accounts, groupIDMap, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	if _, err := tx.ExecContext(c.Request.Context(), `SELECT setval(pg_get_serial_sequence('mail_groups', 'id'), GREATEST(COALESCE((SELECT MAX(id) FROM mail_groups), 2), 2), true)`); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "导入分组失败"})
		return
	}
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "导入邮箱数据失败"})
		return
	}

	c.JSON(http.StatusOK, apiResponse{
		Code: 0,
		Data: mailDataImportResult{Groups: groupCount, Accounts: accountCount},
		Msg:  "ok",
	})
}

func (s *appState) createMailDataExportTask(c *gin.Context) {
	var req mailDataExportRequest
	_ = c.ShouldBindJSON(&req)
	req.Password = strings.TrimSpace(req.Password)
	if req.Password == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请输入导出密码"})
		return
	}
	task := s.tasks.create("mail_export", 0, "导出任务已创建")
	go s.runMailDataExportTask(task.ID, req)
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: task, Msg: "ok"})
}

func (s *appState) runMailDataExportTask(taskID string, req mailDataExportRequest) {
	ctx := context.Background()
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	defer db.Close()

	total, err := countAccountExportRows(ctx, db, "mail_accounts", accountExportSelector{IDs: req.IDs, Filter: req.Filter})
	if err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	s.tasks.update(taskID, func(task *backgroundTask) {
		task.Total = total
		task.Message = "正在生成导出 ZIP"
	})

	path, err := taskZipPath(taskID, "mail-data-export")
	if err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	file, err := os.Create(path)
	if err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	writeErr := writeEncryptedMailDataZip(ctx, file, db, accountExportSelector{IDs: req.IDs, Filter: req.Filter}, req.Password)
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		s.tasks.fail(taskID, writeErr)
		return
	}
	if closeErr != nil {
		_ = os.Remove(path)
		s.tasks.fail(taskID, closeErr)
		return
	}

	filename := fmt.Sprintf("mail-data-%s.zip", time.Now().Format("20060102-150405"))
	s.tasks.update(taskID, func(task *backgroundTask) {
		task.Done = total
		task.Success = total
		task.Message = "导出完成"
	})
	s.tasks.setResult(taskID, path, filename, "导出完成")
	s.tasks.finish(taskID)
}

func (s *appState) createMailDataImportTask(c *gin.Context) {
	password := strings.TrimSpace(c.PostForm("password"))
	if password == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请输入导入密码"})
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请选择 ZIP 数据文件"})
		return
	}

	task := s.tasks.create("mail_import", 0, "导入任务已创建")
	path, err := saveUploadedTaskZip(file, task.ID, "mail-data-import")
	if err != nil {
		s.tasks.fail(task.ID, err)
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "保存上传文件失败"})
		return
	}
	go s.runMailDataImportTask(task.ID, path, password)
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: task, Msg: "ok"})
}

func (s *appState) runMailDataImportTask(taskID string, path string, password string) {
	ctx := context.Background()
	defer os.Remove(path)

	payload, err := readEncryptedMailDataZipPath(path, password)
	if err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	if len(payload.Groups) == 0 && len(payload.Accounts) == 0 {
		s.tasks.fail(taskID, fmt.Errorf("ZIP 文件中没有可导入的数据"))
		return
	}
	total := len(payload.Groups) + len(payload.Accounts)
	s.tasks.update(taskID, func(task *backgroundTask) {
		task.Total = total
		task.Message = "正在导入邮箱数据"
	})

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	defer tx.Rollback()

	processed := 0
	reportProgress := func(message string) {
		processed++
		if !shouldReportTaskProgress(processed, total) {
			return
		}
		done := processed
		s.tasks.update(taskID, func(task *backgroundTask) {
			task.Done = done
			task.Success = done
			task.Message = message
		})
	}

	groupIDMap, groupCount, err := importMailGroups(ctx, tx, payload.Groups, func() {
		reportProgress("正在导入邮箱分组")
	})
	if err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	accountCount, err := importMailAccounts(ctx, tx, payload.Accounts, groupIDMap, func() {
		reportProgress("正在导入邮箱账号")
	})
	if err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	if _, err := tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('mail_groups', 'id'), GREATEST(COALESCE((SELECT MAX(id) FROM mail_groups), 2), 2), true)`); err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.tasks.fail(taskID, err)
		return
	}

	message := fmt.Sprintf("导入完成：%d 个邮箱，%d 个分组", accountCount, groupCount)
	s.tasks.update(taskID, func(task *backgroundTask) {
		task.Done = total
		task.Success = total
		task.Message = message
	})
	s.tasks.finish(taskID)
}

func (s *appState) createMailAccount(c *gin.Context) {
	var req saveMailAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	item, err := createMailAccountRecord(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: item, Msg: "ok"})
}

func (s *appState) batchCreateMailAccounts(c *gin.Context) {
	var req batchMailAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}

	lines := strings.Split(req.Content, "\n")
	items := []mailAccountResponse{}
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "----")
		if len(parts) != 2 {
			c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "批量邮箱格式错误，请使用 账号----密码"})
			return
		}
		item, err := createMailAccountRecord(c.Request.Context(), saveMailAccountRequest{
			Email:        strings.TrimSpace(parts[0]),
			Password:     strings.TrimSpace(parts[1]),
			GroupID:      req.GroupID,
			ServerID:     req.ServerID,
			ImapHost:     req.ImapHost,
			SMTPHost:     req.SMTPHost,
			ImapProtocol: req.ImapProtocol,
			ImapPort:     req.ImapPort,
			ImapSSL:      req.ImapSSL,
			SMTPProtocol: req.SMTPProtocol,
			SMTPPort:     req.SMTPPort,
			SMTPSSL:      req.SMTPSSL,
		})
		if err != nil {
			c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
			return
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请输入批量邮箱内容"})
		return
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: items, Msg: "ok"})
}

func (s *appState) updateMailAccount(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	var req saveMailAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}

	item, err := updateMailAccountRecord(c.Request.Context(), id, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: item, Msg: "ok"})
}

func (s *appState) deleteMailAccount(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除邮箱失败"})
		return
	}
	defer db.Close()

	rowsAffected, err := deleteAccountRowsAndUnbindCardKeys(c.Request.Context(), db, "mail_accounts", []int{id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除邮箱失败"})
		return
	}
	if rowsAffected == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "邮箱账号不存在"})
		return
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
}

func (s *appState) batchMailAccountAction(c *gin.Context) {
	var req mailAccountBatchActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "璇锋眰鍙傛暟閿欒"})
		return
	}
	req.Action = strings.TrimSpace(req.Action)
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "鎵归噺鎿嶄綔澶辫触"})
		return
	}
	ids, err := resolveAccountIDs(c.Request.Context(), db, "mail_accounts", req.IDs, req.Filter)
	db.Close()
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "鎵归噺鎿嶄綔澶辫触"})
		return
	}
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "璇烽€夋嫨璐﹀彿"})
		return
	}

	switch req.Action {
	case "delete", "test":
		task := s.tasks.create("mail_"+req.Action, len(ids), "running")
		go s.runMailAccountBatchTask(task.ID, req, ids)
		c.JSON(http.StatusOK, apiResponse{Code: 0, Data: task, Msg: "ok"})
	default:
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "鏈煡鎵归噺鎿嶄綔"})
	}
}

func (s *appState) runMailAccountBatchTask(taskID string, req mailAccountBatchActionRequest, ids []int) {
	ctx := context.Background()
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		s.tasks.update(taskID, func(task *backgroundTask) {
			task.Status = "failed"
			task.Message = err.Error()
		})
		return
	}
	defer db.Close()

	if req.Action == "delete" {
		if err := s.deleteAccountsInBatches(ctx, db, taskID, "mail_accounts", ids); err != nil {
			s.tasks.fail(taskID, err)
			return
		}
		s.tasks.finish(taskID)
		return
	}

	workerCount := 4
	if len(ids) < workerCount {
		workerCount = len(ids)
	}
	if workerCount <= 0 {
		workerCount = 1
	}
	idCh := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range idCh {
				err := error(nil)
				switch req.Action {
				case "test":
					err = runMailAccountConnectivityTest(ctx, db, id, req.TestType)
				}
				s.tasks.recordProgress(taskID, err)
			}
		}()
	}
	for _, id := range ids {
		idCh <- id
	}
	close(idCh)
	wg.Wait()
	s.tasks.finish(taskID)
}

func runMailAccountConnectivityTest(ctx context.Context, db *sql.DB, id int, testType string) error {
	testType = strings.TrimSpace(testType)
	if testType == "" {
		testType = "all"
	}
	if testType != "all" && testType != "receive" && testType != "send" {
		return fmt.Errorf("娴嬭瘯绫诲瀷閿欒")
	}
	account, err := loadMailAccountTestConfig(ctx, db, id)
	if err != nil {
		return err
	}
	if err := testReceiveMailAccount(ctx, account); err != nil {
		updateMailAccountStatus(ctx, db, id, "error", err.Error())
		return err
	}
	updateMailAccountStatus(ctx, db, id, "normal", "")
	return nil
}

func (s *appState) testMailAccount(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	var req testMailAccountRequest
	_ = c.ShouldBindJSON(&req)
	testType := strings.TrimSpace(req.Type)
	if testType == "" {
		testType = "all"
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "测试失败"})
		return
	}
	defer db.Close()

	account, err := loadMailAccountTestConfig(c.Request.Context(), db, id)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "邮箱账号不存在"})
		return
	}

	if testType != "all" && testType != "receive" && testType != "send" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "测试类型错误"})
		return
	}
	if err := testReceiveMailAccount(c.Request.Context(), account); err != nil {
		updateMailAccountStatus(c.Request.Context(), db, id, "error", err.Error())
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "邮箱连接测试失败"})
		return
	}

	updateMailAccountStatus(c.Request.Context(), db, id, "normal", "")
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: gin.H{"message": "邮箱连接测试成功"}, Msg: "ok"})
}

func (s *appState) receiveMailMessages(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	var req receiveMailMessagesRequest
	_ = c.ShouldBindJSON(&req)
	if req.Limit <= 0 {
		req.Limit = 10
	}
	if req.Limit > 100 {
		req.Limit = 100
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "收取邮件失败"})
		return
	}
	defer db.Close()

	account, err := loadMailAccountTestConfig(c.Request.Context(), db, id)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "邮箱账号不存在"})
		return
	}

	messages, err := receiveMailHeaders(c.Request.Context(), account, req.Limit)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "收取邮件失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: messages, Msg: "ok"})
}

func (s *appState) receiveMailDetail(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req receiveMailDetailRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.UID <= 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取邮件失败"})
		return
	}
	defer db.Close()
	account, err := loadMailAccountTestConfig(c.Request.Context(), db, id)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "邮箱账号不存在"})
		return
	}
	detail, err := receiveIMAPMailDetail(c.Request.Context(), account, req.Mailbox, req.Folder, req.UID)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "读取邮件失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: detail, Msg: "ok"})
}

func (s *appState) sendMailMessage(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req sendMailMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "发送邮件失败"})
		return
	}
	defer db.Close()
	account, err := loadMailAccountTestConfig(c.Request.Context(), db, id)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "邮箱账号不存在"})
		return
	}
	if err := sendMailAccountMessage(c.Request.Context(), account, req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "发送邮件失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: gin.H{"message": "发送成功"}, Msg: "ok"})
}

func updateMailAccountRecord(ctx context.Context, id int, req saveMailAccountRequest) (mailAccountResponse, error) {
	item := mailAccountResponse{}
	req.Email = strings.TrimSpace(req.Email)
	req.Password = strings.TrimSpace(req.Password)
	req.ImapHost = strings.TrimSpace(req.ImapHost)
	req.SMTPHost = strings.TrimSpace(req.SMTPHost)
	req.ImapProtocol = strings.TrimSpace(req.ImapProtocol)
	req.SMTPProtocol = strings.TrimSpace(req.SMTPProtocol)
	req.Remark = strings.TrimSpace(req.Remark)
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		return item, fmt.Errorf("邮箱账号格式错误")
	}
	if req.GroupID <= 0 {
		req.GroupID = 2
	}
	if req.ImapProtocol == "" {
		req.ImapProtocol = "IMAP"
	}
	if req.SMTPProtocol == "" {
		req.SMTPProtocol = "SMTP(SSL)"
	}
	if req.ImapPort <= 0 {
		req.ImapPort = 993
	}
	if req.SMTPPort <= 0 {
		req.SMTPPort = 465
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return item, fmt.Errorf("保存邮箱失败")
	}
	defer db.Close()

	if !mailGroupExists(ctx, db, req.GroupID) {
		return item, fmt.Errorf("邮箱分组不存在")
	}
	if mailGroupHasChildren(ctx, db, req.GroupID) {
		return item, fmt.Errorf("该分组下有子分组，不能直接添加邮箱")
	}

	if req.ServerID > 0 {
		if err := db.QueryRowContext(ctx, `SELECT imap_host, smtp_host FROM mail_servers WHERE id = $1`, req.ServerID).Scan(&req.ImapHost, &req.SMTPHost); err != nil {
			return item, fmt.Errorf("服务器不存在")
		}
	}
	if req.ImapHost == "" || req.SMTPHost == "" {
		return item, fmt.Errorf("收发服务器地址不能为空")
	}

	query := `
UPDATE mail_accounts
SET group_id = $2, server_id = $3, email = $4, imap_host = $5, smtp_host = $6,
    imap_protocol = $7, imap_port = $8, imap_ssl = $9, smtp_protocol = $10, smtp_port = $11, smtp_ssl = $12,
    remark = $13, status_reason = '', updated_at = NOW()
`
	args := []interface{}{id, req.GroupID, req.ServerID, req.Email, req.ImapHost, req.SMTPHost, req.ImapProtocol, req.ImapPort, req.ImapSSL, req.SMTPProtocol, req.SMTPPort, req.SMTPSSL, req.Remark}
	if req.Password != "" {
		query += `, password = $14 WHERE id = $1 RETURNING id, group_id, email, server_id, imap_host, smtp_host, imap_protocol, imap_port, imap_ssl, smtp_protocol, smtp_port, smtp_ssl, remark, status, status_reason, created_at`
		args = append(args, req.Password)
	} else {
		query += ` WHERE id = $1 RETURNING id, group_id, email, server_id, imap_host, smtp_host, imap_protocol, imap_port, imap_ssl, smtp_protocol, smtp_port, smtp_ssl, remark, status, status_reason, created_at`
	}

	var createdAt time.Time
	err = db.QueryRowContext(ctx, query, args...).Scan(&item.ID, &item.GroupID, &item.Email, &item.ServerID, &item.ImapHost, &item.SMTPHost, &item.ImapProtocol, &item.ImapPort, &item.ImapSSL, &item.SMTPProtocol, &item.SMTPPort, &item.SMTPSSL, &item.Remark, &item.Status, &item.StatusReason, &createdAt)
	if err != nil {
		return item, fmt.Errorf("邮箱账号不存在或保存失败")
	}
	item.CreatedAt = createdAt.Format("2006/01/02 15:04:05")
	if err := db.QueryRowContext(ctx, `SELECT name FROM mail_groups WHERE id = $1`, item.GroupID).Scan(&item.GroupName); err != nil {
		item.GroupName = ""
	}
	if item.ServerID > 0 {
		_ = db.QueryRowContext(ctx, `SELECT name FROM mail_servers WHERE id = $1`, item.ServerID).Scan(&item.ServerName)
	}
	return item, nil
}

func testTCPAddress(host string, port int) error {
	host = strings.TrimSpace(host)
	if host == "" || port <= 0 {
		return fmt.Errorf("地址或端口为空")
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

func loadMailAccountTestConfig(ctx context.Context, db *sql.DB, id int) (mailAccountTestConfig, error) {
	var account mailAccountTestConfig
	err := db.QueryRowContext(ctx, `
SELECT email, password, imap_host, imap_protocol, imap_port, imap_ssl, smtp_host, smtp_protocol, smtp_port, smtp_ssl
FROM mail_accounts
WHERE id = $1
`, id).Scan(&account.Email, &account.Password, &account.ImapHost, &account.ImapProtocol, &account.ImapPort, &account.ImapSSL, &account.SMTPHost, &account.SMTPProtocol, &account.SMTPPort, &account.SMTPSSL)
	return account, err
}

func updateMailAccountStatus(ctx context.Context, db *sql.DB, id int, status string, reason string) {
	_, _ = db.ExecContext(ctx, `UPDATE mail_accounts SET status = $2, status_reason = $3, updated_at = NOW() WHERE id = $1`, id, status, strings.TrimSpace(reason))
}

func writeEncryptedMailDataZip(ctx context.Context, w io.Writer, db *sql.DB, selector accountExportSelector, password string) error {
	zipWriter := encryptedzip.NewWriter(w)
	defer zipWriter.Close()

	metaWriter, err := zipWriter.Encrypt("metadata.json", password)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(metaWriter).Encode(map[string]interface{}{
		"exported_at": time.Now().Format(time.RFC3339),
		"format":      "mail-data-zip",
		"version":     1,
	}); err != nil {
		return err
	}

	groupsWriter, err := zipWriter.Encrypt("groups.json", password)
	if err != nil {
		return err
	}
	if err := writeExportGroupsJSON(ctx, db, groupsWriter); err != nil {
		return err
	}

	accountsWriter, err := zipWriter.Encrypt("accounts.json", password)
	if err != nil {
		return err
	}
	if err := writeExportAccountsJSON(ctx, db, accountsWriter, selector); err != nil {
		return err
	}
	return nil
}

func writeExportGroupsJSON(ctx context.Context, db *sql.DB, w io.Writer) error {
	rows, err := db.QueryContext(ctx, `
SELECT id, parent_id, name, system, sort_order, created_at
FROM mail_groups
ORDER BY id ASC
`)
	if err != nil {
		return err
	}
	defer rows.Close()

	encoder := json.NewEncoder(w)
	if _, err := io.WriteString(w, "["); err != nil {
		return err
	}
	first := true
	for rows.Next() {
		var item mailDataGroup
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.ParentID, &item.Name, &item.System, &item.SortOrder, &createdAt); err != nil {
			return err
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		if !first {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		first = false
		if err := encoder.Encode(item); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = io.WriteString(w, "]")
	return err
}

func writeExportAccountsJSON(ctx context.Context, db *sql.DB, w io.Writer, selector accountExportSelector) error {
	encoder := json.NewEncoder(w)
	if _, err := io.WriteString(w, "["); err != nil {
		return err
	}
	first := true
	if len(selector.IDs) > 0 {
		ids := normalizePositiveIDs(selector.IDs)
		for start := 0; start < len(ids); start += exportIDBatchSize {
			end := start + exportIDBatchSize
			if end > len(ids) {
				end = len(ids)
			}
			placeholders, args := intPlaceholders(ids[start:end], 1)
			if placeholders == "" {
				continue
			}
			query := `
SELECT id, group_id, email, password, server_id, imap_host, smtp_host, imap_protocol, imap_port, imap_ssl,
       smtp_protocol, smtp_port, smtp_ssl, remark, status, status_reason, created_at
FROM mail_accounts
WHERE id IN (` + placeholders + `)
ORDER BY id ASC`
			if err := writeMailAccountRowsJSON(ctx, db, w, encoder, query, args, &first); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "]")
		return err
	}

	whereSQL, args, err := buildAccountFilterWhere("", "mail_accounts", selector.Filter, 1)
	if err != nil {
		return err
	}
	query := `
SELECT id, group_id, email, password, server_id, imap_host, smtp_host, imap_protocol, imap_port, imap_ssl,
       smtp_protocol, smtp_port, smtp_ssl, remark, status, status_reason, created_at
FROM mail_accounts
WHERE ` + whereSQL + `
ORDER BY id ASC`
	if err := writeMailAccountRowsJSON(ctx, db, w, encoder, query, args, &first); err != nil {
		return err
	}
	_, err = io.WriteString(w, "]")
	return err
}

func writeMailAccountRowsJSON(ctx context.Context, db *sql.DB, w io.Writer, encoder *json.Encoder, query string, args []interface{}, first *bool) error {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var item mailDataAccount
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.GroupID, &item.Email, &item.Password, &item.ServerID, &item.ImapHost, &item.SMTPHost, &item.ImapProtocol, &item.ImapPort, &item.ImapSSL, &item.SMTPProtocol, &item.SMTPPort, &item.SMTPSSL, &item.Remark, &item.Status, &item.StatusReason, &createdAt); err != nil {
			return err
		}
		item.Status = mailExportStatusText(item.Status)
		item.StatusReason = ""
		item.CreatedAt = createdAt.Format(time.RFC3339)
		if !*first {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		*first = false
		if err := encoder.Encode(item); err != nil {
			return err
		}
	}
	return rows.Err()
}

func readEncryptedMailDataZip(fileHeader *multipart.FileHeader, password string) (mailDataPayload, error) {
	var payload mailDataPayload
	file, err := fileHeader.Open()
	if err != nil {
		return payload, fmt.Errorf("读取 ZIP 文件失败")
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return payload, fmt.Errorf("读取 ZIP 文件失败")
	}
	reader, err := encryptedzip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return payload, fmt.Errorf("ZIP 文件格式错误")
	}

	for _, zipFile := range reader.File {
		switch zipFile.Name {
		case "groups.json":
			zipFile.SetPassword(password)
			rc, err := zipFile.Open()
			if err != nil {
				return payload, fmt.Errorf("ZIP 密码错误或文件损坏")
			}
			if err := json.NewDecoder(rc).Decode(&payload.Groups); err != nil {
				rc.Close()
				return payload, fmt.Errorf("分组数据格式错误")
			}
			rc.Close()
		case "accounts.json":
			zipFile.SetPassword(password)
			rc, err := zipFile.Open()
			if err != nil {
				return payload, fmt.Errorf("ZIP 密码错误或文件损坏")
			}
			if err := json.NewDecoder(rc).Decode(&payload.Accounts); err != nil {
				rc.Close()
				return payload, fmt.Errorf("邮箱账号数据格式错误")
			}
			rc.Close()
		case "metadata.json":
			zipFile.SetPassword(password)
			rc, err := zipFile.Open()
			if err != nil {
				return payload, fmt.Errorf("ZIP 密码错误或文件损坏")
			}
			var metadata map[string]interface{}
			_ = json.NewDecoder(rc).Decode(&metadata)
			rc.Close()
		}
	}
	if len(payload.Groups) == 0 && len(payload.Accounts) == 0 {
		return payload, fmt.Errorf("ZIP 文件中缺少 groups.json 或 accounts.json")
	}
	return payload, nil
}

func readEncryptedMailDataZipPath(path string, password string) (mailDataPayload, error) {
	var payload mailDataPayload
	file, err := os.Open(path)
	if err != nil {
		return payload, fmt.Errorf("读取 ZIP 文件失败")
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return payload, fmt.Errorf("读取 ZIP 文件失败")
	}
	reader, err := encryptedzip.NewReader(file, stat.Size())
	if err != nil {
		return payload, fmt.Errorf("ZIP 文件格式错误")
	}

	for _, zipFile := range reader.File {
		switch zipFile.Name {
		case "groups.json":
			zipFile.SetPassword(password)
			rc, err := zipFile.Open()
			if err != nil {
				return payload, fmt.Errorf("ZIP 密码错误或文件损坏")
			}
			if err := json.NewDecoder(rc).Decode(&payload.Groups); err != nil {
				rc.Close()
				return payload, fmt.Errorf("分组数据格式错误")
			}
			rc.Close()
		case "accounts.json":
			zipFile.SetPassword(password)
			rc, err := zipFile.Open()
			if err != nil {
				return payload, fmt.Errorf("ZIP 密码错误或文件损坏")
			}
			if err := json.NewDecoder(rc).Decode(&payload.Accounts); err != nil {
				rc.Close()
				return payload, fmt.Errorf("邮箱账号数据格式错误")
			}
			rc.Close()
		case "metadata.json":
			zipFile.SetPassword(password)
			rc, err := zipFile.Open()
			if err != nil {
				return payload, fmt.Errorf("ZIP 密码错误或文件损坏")
			}
			var metadata map[string]interface{}
			_ = json.NewDecoder(rc).Decode(&metadata)
			rc.Close()
		}
	}
	if len(payload.Groups) == 0 && len(payload.Accounts) == 0 {
		return payload, fmt.Errorf("ZIP 文件中缺少 groups.json 或 accounts.json")
	}
	return payload, nil
}

func queryExportMailGroups(ctx context.Context, db *sql.DB) ([]mailDataGroup, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, parent_id, name, system, sort_order, created_at
FROM mail_groups
ORDER BY id ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []mailDataGroup{}
	for rows.Next() {
		var item mailDataGroup
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.ParentID, &item.Name, &item.System, &item.SortOrder, &createdAt); err != nil {
			return nil, err
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		items = append(items, item)
	}
	return items, rows.Err()
}

func queryExportMailAccounts(ctx context.Context, db *sql.DB, selector accountExportSelector) ([]mailDataAccount, error) {
	items := []mailDataAccount{}
	if len(selector.IDs) > 0 {
		ids := normalizePositiveIDs(selector.IDs)
		for start := 0; start < len(ids); start += exportIDBatchSize {
			end := start + exportIDBatchSize
			if end > len(ids) {
				end = len(ids)
			}
			placeholders, args := intPlaceholders(ids[start:end], 1)
			if placeholders == "" {
				continue
			}
			query := `
SELECT id, group_id, email, password, server_id, imap_host, smtp_host, imap_protocol, imap_port, imap_ssl,
       smtp_protocol, smtp_port, smtp_ssl, remark, status, status_reason, created_at
FROM mail_accounts
WHERE id IN (` + placeholders + `)
ORDER BY id ASC`
			next, err := queryMailDataAccounts(ctx, db, query, args)
			if err != nil {
				return nil, err
			}
			items = append(items, next...)
		}
		return items, nil
	}

	whereSQL, args, err := buildAccountFilterWhere("", "mail_accounts", selector.Filter, 1)
	if err != nil {
		return nil, err
	}
	query := `
SELECT id, group_id, email, password, server_id, imap_host, smtp_host, imap_protocol, imap_port, imap_ssl,
       smtp_protocol, smtp_port, smtp_ssl, remark, status, status_reason, created_at
FROM mail_accounts
WHERE ` + whereSQL + `
ORDER BY id ASC`
	return queryMailDataAccounts(ctx, db, query, args)
}

func queryMailDataAccounts(ctx context.Context, db *sql.DB, query string, args []interface{}) ([]mailDataAccount, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []mailDataAccount{}
	for rows.Next() {
		var item mailDataAccount
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.GroupID, &item.Email, &item.Password, &item.ServerID, &item.ImapHost, &item.SMTPHost, &item.ImapProtocol, &item.ImapPort, &item.ImapSSL, &item.SMTPProtocol, &item.SMTPPort, &item.SMTPSSL, &item.Remark, &item.Status, &item.StatusReason, &createdAt); err != nil {
			return nil, err
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		items = append(items, item)
	}
	return items, rows.Err()
}

func importMailGroups(ctx context.Context, tx *sql.Tx, groups []mailDataGroup, onProgress func()) (map[int]int, int, error) {
	groupIDMap := map[int]int{}
	emptyTarget, err := mailImportTargetIsEmpty(ctx, tx)
	if err != nil {
		return groupIDMap, 0, err
	}
	if emptyTarget {
		if err := resetMailImportSequences(ctx, tx); err != nil {
			return groupIDMap, 0, err
		}
	}

	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].ID < groups[j].ID
	})

	count := 0
	for _, group := range groups {
		group.Name = strings.TrimSpace(group.Name)
		if group.ID <= 0 || group.Name == "" {
			if onProgress != nil {
				onProgress()
			}
			continue
		}
		if group.System {
			groupIDMap[group.ID] = defaultMailSystemGroupID(group.Name)
			count++
			if onProgress != nil {
				onProgress()
			}
			continue
		}
		parentID := group.ParentID
		if parentID > 0 {
			if mappedParentID, ok := groupIDMap[parentID]; ok {
				parentID = mappedParentID
			}
			if !mailGroupExistsTx(ctx, tx, parentID) {
				return groupIDMap, count, fmt.Errorf("导入分组 %s 失败：父级分组不存在", group.Name)
			}
			if mailGroupParentIDTx(ctx, tx, parentID) > 0 {
				return groupIDMap, count, fmt.Errorf("导入分组 %s 失败：子分组下不能继续添加子分组", group.Name)
			}
			if mailGroupHasAccountsTx(ctx, tx, parentID) {
				return groupIDMap, count, fmt.Errorf("导入分组 %s 失败：父级分组下已有邮箱，不能继续添加子分组", group.Name)
			}
		}

		actualID, err := upsertImportMailGroup(ctx, tx, group, parentID)
		if err != nil {
			return groupIDMap, count, err
		}
		groupIDMap[group.ID] = actualID
		count++
		if onProgress != nil {
			onProgress()
		}
	}
	if err := normalizeGroupSortOrdersTx(ctx, tx, "mail_groups"); err != nil {
		return groupIDMap, count, err
	}
	return groupIDMap, count, nil
}

func defaultMailSystemGroupID(name string) int {
	if strings.Contains(name, "全部") {
		return 1
	}
	return 2
}

func upsertImportMailGroup(ctx context.Context, tx *sql.Tx, group mailDataGroup, parentID int) (int, error) {
	var actualID int
	err := tx.QueryRowContext(ctx, `SELECT id FROM mail_groups WHERE parent_id = $1 AND name = $2`, parentID, group.Name).Scan(&actualID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE mail_groups SET parent_id = $2, name = $3, system = $4, sort_order = $5, updated_at = NOW() WHERE id = $1`, actualID, parentID, group.Name, group.System, group.SortOrder)
		return actualID, err
	}
	if err != sql.ErrNoRows {
		return 0, err
	}

	err = tx.QueryRowContext(ctx, `
INSERT INTO mail_groups (parent_id, name, system, sort_order)
VALUES ($1, $2, $3, $4)
RETURNING id
`, parentID, group.Name, group.System, group.SortOrder).Scan(&actualID)
	return actualID, err
}

func mailImportTargetIsEmpty(ctx context.Context, tx *sql.Tx) (bool, error) {
	var accountCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail_accounts`).Scan(&accountCount); err != nil {
		return false, err
	}
	var customGroupCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail_groups WHERE system = FALSE`).Scan(&customGroupCount); err != nil {
		return false, err
	}
	return accountCount == 0 && customGroupCount == 0, nil
}

func resetMailImportSequences(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('mail_accounts', 'id'), 1, false)`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('mail_groups', 'id'), GREATEST(COALESCE((SELECT MAX(id) FROM mail_groups), 2), 2), true)`)
	return err
}

func importMailAccounts(ctx context.Context, tx *sql.Tx, accounts []mailDataAccount, groupIDMap map[int]int, onProgress func()) (int, error) {
	count := 0
	for _, account := range accounts {
		account.Email = strings.TrimSpace(account.Email)
		account.Password = strings.TrimSpace(account.Password)
		account.ImapHost = strings.TrimSpace(account.ImapHost)
		account.SMTPHost = strings.TrimSpace(account.SMTPHost)
		account.ImapProtocol = strings.TrimSpace(account.ImapProtocol)
		account.SMTPProtocol = strings.TrimSpace(account.SMTPProtocol)
		account.Remark = strings.TrimSpace(account.Remark)
		account.Status = strings.TrimSpace(account.Status)
		account.StatusReason = strings.TrimSpace(account.StatusReason)
		if account.Email == "" || !strings.Contains(account.Email, "@") {
			if onProgress != nil {
				onProgress()
			}
			continue
		}
		if account.ImapProtocol == "" {
			account.ImapProtocol = "IMAP"
		}
		if account.SMTPProtocol == "" {
			account.SMTPProtocol = "SMTP(SSL)"
		}
		if account.ImapPort <= 0 {
			account.ImapPort = 993
		}
		if account.SMTPPort <= 0 {
			account.SMTPPort = 465
		}
		if account.Status == "" {
			account.Status = "active"
		}
		account.Status = normalizeMailImportStatus(account.Status)
		groupID := account.GroupID
		if mappedGroupID, ok := groupIDMap[account.GroupID]; ok {
			groupID = mappedGroupID
		}
		if groupID <= 0 {
			groupID = 2
		}
		if !mailGroupExistsTx(ctx, tx, groupID) {
			return count, fmt.Errorf("导入邮箱 %s 失败：分组不存在", account.Email)
		}
		if mailGroupHasChildrenTx(ctx, tx, groupID) {
			return count, fmt.Errorf("导入邮箱 %s 失败：该分组下有子分组，不能直接添加邮箱", account.Email)
		}

		createdAtArg, hasCreatedAt := parseMailExportTimeArg(account.CreatedAt)
		_, err := tx.ExecContext(ctx, `
INSERT INTO mail_accounts (group_id, server_id, email, password, imap_host, smtp_host, imap_protocol, imap_port, imap_ssl, smtp_protocol, smtp_port, smtp_ssl, remark, status, status_reason, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, CASE WHEN $16::boolean THEN $17::timestamptz ELSE NOW() END)
ON CONFLICT (group_id, email) DO UPDATE SET
	server_id = EXCLUDED.server_id,
	password = EXCLUDED.password,
	imap_host = EXCLUDED.imap_host,
	smtp_host = EXCLUDED.smtp_host,
	imap_protocol = EXCLUDED.imap_protocol,
	imap_port = EXCLUDED.imap_port,
	imap_ssl = EXCLUDED.imap_ssl,
	smtp_protocol = EXCLUDED.smtp_protocol,
	smtp_port = EXCLUDED.smtp_port,
	smtp_ssl = EXCLUDED.smtp_ssl,
	remark = EXCLUDED.remark,
	status = EXCLUDED.status,
	status_reason = EXCLUDED.status_reason,
	created_at = EXCLUDED.created_at,
	updated_at = NOW()
`, groupID, account.ServerID, account.Email, account.Password, account.ImapHost, account.SMTPHost, account.ImapProtocol, account.ImapPort, account.ImapSSL, account.SMTPProtocol, account.SMTPPort, account.SMTPSSL, account.Remark, account.Status, account.StatusReason, hasCreatedAt, createdAtArg)
		if err != nil {
			return count, fmt.Errorf("导入邮箱 %s 失败: %w", account.Email, err)
		}
		count++
		if onProgress != nil {
			onProgress()
		}
	}
	return count, nil
}

func mailExportStatusText(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "error" || status == "failed" || status == "fail" {
		return "错误"
	}
	return "正常"
}

func normalizeMailImportStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "错误", "error", "failed", "fail":
		return "error"
	default:
		return "normal"
	}
}

func parseMailExportTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006/01/02 15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func parseMailExportTimeArg(value string) (interface{}, bool) {
	parsed, ok := parseMailExportTime(value)
	if !ok {
		return nil, false
	}
	return parsed, true
}

func mailGroupExistsTx(ctx context.Context, tx *sql.Tx, id int) bool {
	var exists bool
	return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail_groups WHERE id = $1)`, id).Scan(&exists) == nil && exists
}

func mailGroupParentIDTx(ctx context.Context, tx *sql.Tx, id int) int {
	var parentID int
	if err := tx.QueryRowContext(ctx, `SELECT parent_id FROM mail_groups WHERE id = $1`, id).Scan(&parentID); err != nil {
		return 0
	}
	return parentID
}

func mailGroupHasChildrenTx(ctx context.Context, tx *sql.Tx, id int) bool {
	var exists bool
	return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail_groups WHERE parent_id = $1)`, id).Scan(&exists) == nil && exists
}

func mailGroupHasAccountsTx(ctx context.Context, tx *sql.Tx, id int) bool {
	var exists bool
	return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail_accounts WHERE group_id = $1)`, id).Scan(&exists) == nil && exists
}

func testReceiveMailAccount(ctx context.Context, account mailAccountTestConfig) error {
	if strings.TrimSpace(account.Email) == "" || strings.TrimSpace(account.Password) == "" {
		return fmt.Errorf("邮箱账号或密码为空")
	}
	protocol := strings.ToUpper(strings.TrimSpace(account.ImapProtocol))
	if protocol == "" || protocol == "IMAP" {
		return testIMAPLogin(ctx, account)
	}
	if protocol == "POP3" {
		return testPOP3Login(ctx, account)
	}
	return fmt.Errorf("不支持的收件协议: %s", account.ImapProtocol)
}

func testSendMailAccount(ctx context.Context, account mailAccountTestConfig) error {
	if strings.TrimSpace(account.Email) == "" || strings.TrimSpace(account.Password) == "" {
		return fmt.Errorf("邮箱账号或密码为空")
	}
	if strings.TrimSpace(account.SMTPHost) == "" || account.SMTPPort <= 0 {
		return fmt.Errorf("发件服务器地址或端口为空")
	}

	address := net.JoinHostPort(strings.TrimSpace(account.SMTPHost), strconv.Itoa(account.SMTPPort))
	smtpProtocol := strings.TrimSpace(account.SMTPProtocol)
	useStartTLS := strings.EqualFold(smtpProtocol, "SMTP(STARTTLS)")
	conn, err := dialMailServer(ctx, account.SMTPHost, account.SMTPPort, account.SMTPSSL && !useStartTLS)
	if err != nil {
		return err
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, strings.TrimSpace(account.SMTPHost))
	if err != nil {
		return err
	}
	defer client.Close()

	if useStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("服务器不支持 STARTTLS")
		}
		if err := client.StartTLS(mailTLSConfig(account.SMTPHost)); err != nil {
			return err
		}
	}

	if ok, _ := client.Extension("AUTH"); !ok {
		return fmt.Errorf("服务器不支持 SMTP AUTH")
	}
	auth := smtp.PlainAuth("", strings.TrimSpace(account.Email), account.Password, strings.TrimSpace(account.SMTPHost))
	if err := client.Auth(auth); err != nil {
		loginAuth := &smtpLoginAuth{username: strings.TrimSpace(account.Email), password: account.Password}
		if loginErr := client.Auth(loginAuth); loginErr != nil {
			return loginErr
		}
	}
	if err := client.Noop(); err != nil {
		return fmt.Errorf("%s: %w", address, err)
	}
	return client.Quit()
}

func sendMailAccountMessage(ctx context.Context, account mailAccountTestConfig, req sendMailMessageRequest) error {
	fromEmail := strings.TrimSpace(account.Email)
	if fromEmail == "" || strings.TrimSpace(account.Password) == "" {
		return fmt.Errorf("邮箱账号或密码为空")
	}
	if strings.TrimSpace(account.SMTPHost) == "" || account.SMTPPort <= 0 {
		return fmt.Errorf("发件服务器地址或端口为空")
	}
	if _, err := stdmail.ParseAddress(fromEmail); err != nil {
		return fmt.Errorf("发件人地址格式错误")
	}
	recipients, err := stdmail.ParseAddressList(strings.TrimSpace(req.Recipient))
	if err != nil || len(recipients) == 0 {
		return fmt.Errorf("收件人地址格式错误")
	}
	envelopeRecipients := make([]string, 0, len(recipients))
	toHeaders := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		address := strings.TrimSpace(recipient.Address)
		if address == "" {
			continue
		}
		envelopeRecipients = append(envelopeRecipients, address)
		toHeaders = append(toHeaders, recipient.String())
	}
	if len(envelopeRecipients) == 0 {
		return fmt.Errorf("收件人地址格式错误")
	}

	client, err := openMailAccountSMTPClient(ctx, account)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := client.Mail(fromEmail); err != nil {
		return err
	}
	for _, recipient := range envelopeRecipients {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	message, err := buildSMTPMessage(account, req, strings.Join(toHeaders, ", "))
	if err != nil {
		_ = writer.Close()
		return err
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func openMailAccountSMTPClient(ctx context.Context, account mailAccountTestConfig) (*smtp.Client, error) {
	smtpProtocol := strings.TrimSpace(account.SMTPProtocol)
	useStartTLS := strings.EqualFold(smtpProtocol, "SMTP(STARTTLS)")
	conn, err := dialMailServer(ctx, account.SMTPHost, account.SMTPPort, account.SMTPSSL && !useStartTLS)
	if err != nil {
		return nil, err
	}
	client, err := smtp.NewClient(conn, strings.TrimSpace(account.SMTPHost))
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	shouldClose := true
	defer func() {
		if shouldClose {
			_ = client.Close()
		}
	}()

	if useStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return nil, fmt.Errorf("服务器不支持 STARTTLS")
		}
		if err := client.StartTLS(mailTLSConfig(account.SMTPHost)); err != nil {
			return nil, err
		}
	}
	if ok, _ := client.Extension("AUTH"); !ok {
		return nil, fmt.Errorf("服务器不支持 SMTP AUTH")
	}
	auth := smtp.PlainAuth("", strings.TrimSpace(account.Email), account.Password, strings.TrimSpace(account.SMTPHost))
	if err := client.Auth(auth); err != nil {
		loginAuth := &smtpLoginAuth{username: strings.TrimSpace(account.Email), password: account.Password}
		if loginErr := client.Auth(loginAuth); loginErr != nil {
			return nil, loginErr
		}
	}
	shouldClose = false
	return client, nil
}

func buildSMTPMessage(account mailAccountTestConfig, req sendMailMessageRequest, toHeader string) ([]byte, error) {
	fromAddress := stdmail.Address{
		Name:    singleLineHeader(req.Nickname),
		Address: strings.TrimSpace(account.Email),
	}
	subject := mime.QEncoding.Encode("utf-8", singleLineHeader(req.Subject))
	var message bytes.Buffer
	message.WriteString("From: " + fromAddress.String() + "\r\n")
	message.WriteString("To: " + singleLineHeader(toHeader) + "\r\n")
	message.WriteString("Subject: " + subject + "\r\n")
	message.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	message.WriteString("MIME-Version: 1.0\r\n")
	message.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	message.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	message.WriteString("\r\n")
	encodedBody := quotedprintable.NewWriter(&message)
	if _, err := encodedBody.Write([]byte(req.Body)); err != nil {
		_ = encodedBody.Close()
		return nil, err
	}
	if err := encodedBody.Close(); err != nil {
		return nil, err
	}
	message.WriteString("\r\n")
	return message.Bytes(), nil
}

func singleLineHeader(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

func testIMAPLogin(ctx context.Context, account mailAccountTestConfig) error {
	conn, err := dialMailServer(ctx, account.ImapHost, account.ImapPort, account.ImapSSL)
	if err != nil {
		return err
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	if line, err := readMailLine(reader); err != nil {
		return err
	} else if !strings.HasPrefix(line, "* OK") {
		return fmt.Errorf("IMAP 服务响应异常: %s", strings.TrimSpace(line))
	}

	if err := writeMailLine(conn, "a001 LOGIN %s %s", imapQuoted(account.Email), imapQuoted(account.Password)); err != nil {
		return err
	}
	if err := readUntilTaggedOK(reader, "a001"); err != nil {
		return err
	}
	_ = writeMailLine(conn, "a002 LOGOUT")
	return nil
}

func testPOP3Login(ctx context.Context, account mailAccountTestConfig) error {
	conn, err := dialMailServer(ctx, account.ImapHost, account.ImapPort, account.ImapSSL)
	if err != nil {
		return err
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	if line, err := readMailLine(reader); err != nil {
		return err
	} else if !strings.HasPrefix(line, "+OK") {
		return fmt.Errorf("POP3 服务响应异常: %s", strings.TrimSpace(line))
	}
	if err := writeMailLine(conn, "USER %s", account.Email); err != nil {
		return err
	}
	if err := expectPOP3OK(reader); err != nil {
		return err
	}
	if err := writeMailLine(conn, "PASS %s", account.Password); err != nil {
		return err
	}
	if err := expectPOP3OK(reader); err != nil {
		return err
	}
	_ = writeMailLine(conn, "QUIT")
	return nil
}

func receiveMailHeaders(ctx context.Context, account mailAccountTestConfig, limit int) (receiveMailMessagesResponse, error) {
	protocol := strings.ToUpper(strings.TrimSpace(account.ImapProtocol))
	if protocol == "POP3" {
		inbox, err := receivePOP3Headers(ctx, account, limit)
		if err != nil {
			return receiveMailMessagesResponse{}, err
		}
		return receiveMailMessagesResponse{Inbox: inbox, Trash: []receivedMailMessage{}}, nil
	}
	inbox, err := receiveIMAPFolderHeaders(ctx, account, "INBOX", "inbox", limit)
	if err != nil {
		return receiveMailMessagesResponse{}, err
	}
	trash := []receivedMailMessage{}
	for _, folder := range discoverTrashMailboxes(ctx, account) {
		items, err := receiveIMAPFolderHeaders(ctx, account, folder, "trash", limit)
		if err == nil {
			trash = items
			break
		}
	}
	return receiveMailMessagesResponse{Inbox: inbox, Trash: trash}, nil
}

func receiveIMAPFolderHeaders(ctx context.Context, account mailAccountTestConfig, mailbox string, folder string, limit int) ([]receivedMailMessage, error) {
	conn, err := dialMailServer(ctx, account.ImapHost, account.ImapPort, account.ImapSSL)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	if line, err := readMailLine(reader); err != nil {
		return nil, err
	} else if !strings.HasPrefix(line, "* OK") {
		return nil, fmt.Errorf("IMAP 服务响应异常: %s", strings.TrimSpace(line))
	}
	if err := writeMailLine(conn, "a001 LOGIN %s %s", imapQuoted(account.Email), imapQuoted(account.Password)); err != nil {
		return nil, err
	}
	if err := readUntilTaggedOK(reader, "a001"); err != nil {
		return nil, err
	}
	if err := writeMailLine(conn, "a002 SELECT %s", imapMailboxName(mailbox)); err != nil {
		return nil, err
	}
	total, err := readSelectTotal(reader, "a002")
	if err != nil {
		return nil, err
	}
	if total <= 0 {
		_ = writeMailLine(conn, "a004 LOGOUT")
		return []receivedMailMessage{}, nil
	}
	start := total - limit + 1
	if start < 1 {
		start = 1
	}
	if err := writeMailLine(conn, "a003 FETCH %d:%d (UID BODY.PEEK[HEADER.FIELDS (SUBJECT FROM TO DATE)])", start, total); err != nil {
		return nil, err
	}
	items, err := readFetchHeaders(reader, "a003", folder, mailbox)
	if err != nil {
		return nil, err
	}
	_ = writeMailLine(conn, "a004 LOGOUT")
	return items, nil
}

type imapMailboxInfo struct {
	Name        string
	DecodedName string
	Flags       string
}

func discoverTrashMailboxes(ctx context.Context, account mailAccountTestConfig) []string {
	fallback := []string{"Trash", "Junk", "Spam", "Bulk Mail", "Bulk", "Deleted Messages", "Deleted Items", "垃圾箱", "垃圾邮件", "垃圾信", "广告邮件"}
	mailboxes, err := listIMAPMailboxes(ctx, account)
	if err != nil {
		return fallback
	}

	candidates := []string{}
	addCandidate := func(name string) {
		if strings.TrimSpace(name) == "" || strings.EqualFold(name, "INBOX") {
			return
		}
		for _, existing := range candidates {
			if existing == name {
				return
			}
		}
		candidates = append(candidates, name)
	}

	for _, mailbox := range mailboxes {
		flags := strings.ToLower(mailbox.Flags)
		if strings.Contains(flags, `\junk`) || strings.Contains(flags, `\spam`) {
			addCandidate(mailbox.Name)
		}
	}
	for _, mailbox := range mailboxes {
		if isLikelySpamMailbox(mailbox.DecodedName) || isLikelySpamMailbox(mailbox.Name) {
			addCandidate(mailbox.Name)
		}
	}
	for _, name := range fallback {
		addCandidate(name)
	}
	return candidates
}

func listIMAPMailboxes(ctx context.Context, account mailAccountTestConfig) ([]imapMailboxInfo, error) {
	conn, err := dialMailServer(ctx, account.ImapHost, account.ImapPort, account.ImapSSL)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	if line, err := readMailLine(reader); err != nil {
		return nil, err
	} else if !strings.HasPrefix(line, "* OK") {
		return nil, fmt.Errorf("IMAP 服务响应异常: %s", strings.TrimSpace(line))
	}
	if err := writeMailLine(conn, "a001 LOGIN %s %s", imapQuoted(account.Email), imapQuoted(account.Password)); err != nil {
		return nil, err
	}
	if err := readUntilTaggedOK(reader, "a001"); err != nil {
		return nil, err
	}
	if err := writeMailLine(conn, `a002 LIST "" "*"`); err != nil {
		return nil, err
	}
	mailboxes, err := readIMAPList(reader, "a002")
	_ = writeMailLine(conn, "a003 LOGOUT")
	return mailboxes, err
}

func readIMAPList(reader *bufio.Reader, tag string) ([]imapMailboxInfo, error) {
	mailboxes := []imapMailboxInfo{}
	for {
		line, err := readMailLine(reader)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(line, tag+" ") {
			if strings.HasPrefix(line, tag+" OK") {
				return mailboxes, nil
			}
			return nil, fmt.Errorf("%s", strings.TrimSpace(line))
		}
		if !strings.HasPrefix(strings.ToUpper(line), "* LIST ") {
			continue
		}
		info, ok := parseIMAPListLine(line)
		if ok {
			mailboxes = append(mailboxes, info)
		}
	}
}

func parseIMAPListLine(line string) (imapMailboxInfo, bool) {
	flagsStart := strings.Index(line, "(")
	flagsEnd := strings.Index(line, ")")
	if flagsStart < 0 || flagsEnd <= flagsStart {
		return imapMailboxInfo{}, false
	}
	name, ok := lastIMAPListAtom(line[flagsEnd+1:])
	if !ok || strings.TrimSpace(name) == "" {
		return imapMailboxInfo{}, false
	}
	return imapMailboxInfo{
		Name:        name,
		DecodedName: decodeIMAPMailboxName(name),
		Flags:       line[flagsStart+1 : flagsEnd],
	}, true
}

func lastIMAPListAtom(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if strings.HasSuffix(value, `"`) {
		end := len(value) - 1
		escaped := false
		for index := end - 1; index >= 0; index-- {
			char := value[index]
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == '"' {
				return unescapeIMAPQuoted(value[index+1 : end]), true
			}
		}
	}
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "", false
	}
	return strings.Trim(fields[len(fields)-1], `"`), true
}

func unescapeIMAPQuoted(value string) string {
	value = strings.ReplaceAll(value, `\"`, `"`)
	value = strings.ReplaceAll(value, `\\`, `\`)
	return value
}

func decodeIMAPMailboxName(value string) string {
	var builder strings.Builder
	for index := 0; index < len(value); {
		if value[index] != '&' {
			builder.WriteByte(value[index])
			index++
			continue
		}
		end := strings.IndexByte(value[index:], '-')
		if end < 0 {
			builder.WriteByte(value[index])
			index++
			continue
		}
		encoded := value[index+1 : index+end]
		if encoded == "" {
			builder.WriteByte('&')
		} else if decoded, ok := decodeIMAPModifiedUTF7Segment(encoded); ok {
			builder.WriteString(decoded)
		} else {
			builder.WriteString(value[index : index+end+1])
		}
		index += end + 1
	}
	return builder.String()
}

func decodeIMAPModifiedUTF7Segment(value string) (string, bool) {
	encoded := strings.ReplaceAll(value, ",", "/")
	padding := len(encoded) % 4
	if padding > 0 {
		encoded += strings.Repeat("=", 4-padding)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data)%2 != 0 {
		return "", false
	}
	units := make([]uint16, 0, len(data)/2)
	for index := 0; index < len(data); index += 2 {
		units = append(units, uint16(data[index])<<8|uint16(data[index+1]))
	}
	runes := utf16.Decode(units)
	return string(runes), utf8.ValidString(string(runes))
}

func isLikelySpamMailbox(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	return strings.Contains(normalized, "junk") ||
		strings.Contains(normalized, "spam") ||
		strings.Contains(normalized, "bulk") ||
		strings.Contains(normalized, "垃圾") ||
		strings.Contains(normalized, "广告") ||
		strings.Contains(normalized, "不受欢迎")
}

func receivePOP3Headers(ctx context.Context, account mailAccountTestConfig, limit int) ([]receivedMailMessage, error) {
	conn, err := dialMailServer(ctx, account.ImapHost, account.ImapPort, account.ImapSSL)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	if line, err := readMailLine(reader); err != nil {
		return nil, err
	} else if !strings.HasPrefix(line, "+OK") {
		return nil, fmt.Errorf("POP3 服务响应异常: %s", strings.TrimSpace(line))
	}
	if err := writeMailLine(conn, "USER %s", account.Email); err != nil {
		return nil, err
	}
	if err := expectPOP3OK(reader); err != nil {
		return nil, err
	}
	if err := writeMailLine(conn, "PASS %s", account.Password); err != nil {
		return nil, err
	}
	if err := expectPOP3OK(reader); err != nil {
		return nil, err
	}
	if err := writeMailLine(conn, "STAT"); err != nil {
		return nil, err
	}
	line, err := readMailLine(reader)
	if err != nil {
		return nil, err
	}
	parts := strings.Fields(line)
	total := 0
	if len(parts) >= 2 && strings.HasPrefix(line, "+OK") {
		total, _ = strconv.Atoi(parts[1])
	}
	start := total - limit + 1
	if start < 1 {
		start = 1
	}
	items := []receivedMailMessage{}
	for index := total; index >= start; index-- {
		if err := writeMailLine(conn, "TOP %d 0", index); err != nil {
			return nil, err
		}
		if err := expectPOP3OK(reader); err != nil {
			return nil, err
		}
		var builder strings.Builder
		for {
			line, err := readMailLine(reader)
			if err != nil {
				return nil, err
			}
			if line == "." {
				break
			}
			builder.WriteString(line)
			builder.WriteString("\r\n")
		}
		item := parseMailHeader(builder.String(), "inbox", "INBOX")
		item.UID = index
		items = append(items, item)
	}
	_ = writeMailLine(conn, "QUIT")
	return items, nil
}

func receiveIMAPMailDetail(ctx context.Context, account mailAccountTestConfig, mailbox string, folder string, uid int) (receiveMailDetailResponse, error) {
	if strings.TrimSpace(mailbox) == "" {
		mailbox = "INBOX"
	}
	if strings.TrimSpace(folder) == "" {
		folder = "inbox"
	}
	conn, err := dialMailServer(ctx, account.ImapHost, account.ImapPort, account.ImapSSL)
	if err != nil {
		return receiveMailDetailResponse{}, err
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	if line, err := readMailLine(reader); err != nil {
		return receiveMailDetailResponse{}, err
	} else if !strings.HasPrefix(line, "* OK") {
		return receiveMailDetailResponse{}, fmt.Errorf("IMAP 服务响应异常: %s", strings.TrimSpace(line))
	}
	if err := writeMailLine(conn, "a001 LOGIN %s %s", imapQuoted(account.Email), imapQuoted(account.Password)); err != nil {
		return receiveMailDetailResponse{}, err
	}
	if err := readUntilTaggedOK(reader, "a001"); err != nil {
		return receiveMailDetailResponse{}, err
	}
	if err := writeMailLine(conn, "a002 SELECT %s", imapMailboxName(mailbox)); err != nil {
		return receiveMailDetailResponse{}, err
	}
	if _, err := readSelectTotal(reader, "a002"); err != nil {
		return receiveMailDetailResponse{}, err
	}
	if err := writeMailLine(conn, "a003 UID FETCH %d (BODY.PEEK[])", uid); err != nil {
		return receiveMailDetailResponse{}, err
	}
	raw, err := readFetchLiteral(reader, "a003")
	if err != nil {
		return receiveMailDetailResponse{}, err
	}
	_ = writeMailLine(conn, "a004 LOGOUT")
	item := parseFullMailHeader(raw, folder, mailbox)
	item.UID = uid
	plain, html := parseMailBody(raw)
	return receiveMailDetailResponse{receivedMailMessage: item, Body: plain, HTML: html}, nil
}

func dialMailServer(ctx context.Context, host string, port int, useTLS bool) (net.Conn, error) {
	host = strings.TrimSpace(host)
	if host == "" || port <= 0 {
		return nil, fmt.Errorf("地址或端口为空")
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	var (
		conn net.Conn
		err  error
	)
	// Mail account IMAP, POP3, and SMTP traffic share the IMAP proxy switch.
	rawConn, err := dialTCPWithProxy(ctx, proxyScopeIMAP, host, port, 10*time.Second)
	if err != nil {
		return nil, err
	}
	if useTLS {
		tlsConn := tls.Client(rawConn, mailTLSConfig(host))
		if err = tlsConn.HandshakeContext(ctx); err != nil {
			_ = rawConn.Close()
			return nil, fmt.Errorf("%s: %w", address, err)
		}
		conn = tlsConn
	} else {
		conn = rawConn
	}
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	return conn, nil
}

func mailTLSConfig(host string) *tls.Config {
	return &tls.Config{ServerName: strings.TrimSpace(host), MinVersion: tls.VersionTLS12}
}

func readMailLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func writeMailLine(conn net.Conn, format string, args ...interface{}) error {
	_, err := fmt.Fprintf(conn, format+"\r\n", args...)
	return err
}

func readUntilTaggedOK(reader *bufio.Reader, tag string) error {
	for {
		line, err := readMailLine(reader)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(line, tag+" ") {
			continue
		}
		if strings.HasPrefix(line, tag+" OK") {
			return nil
		}
		return fmt.Errorf("%s", strings.TrimSpace(line))
	}
}

func readSelectTotal(reader *bufio.Reader, tag string) (int, error) {
	total := 0
	for {
		line, err := readMailLine(reader)
		if err != nil {
			return 0, err
		}
		if strings.HasPrefix(line, "* ") && strings.Contains(line, " EXISTS") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				total, _ = strconv.Atoi(parts[1])
			}
			continue
		}
		if !strings.HasPrefix(line, tag+" ") {
			continue
		}
		if strings.HasPrefix(line, tag+" OK") {
			return total, nil
		}
		return 0, fmt.Errorf("%s", strings.TrimSpace(line))
	}
}

func readFetchHeaders(reader *bufio.Reader, tag string, folder string, mailbox string) ([]receivedMailMessage, error) {
	items := []receivedMailMessage{}
	literalPattern := regexp.MustCompile(`\{(\d+)\}$`)
	uidPattern := regexp.MustCompile(`UID\s+(\d+)`)
	for {
		line, err := readMailLine(reader)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(line, tag+" ") {
			if strings.HasPrefix(line, tag+" OK") {
				sortReceivedMailMessages(items)
				return items, nil
			}
			return nil, fmt.Errorf("%s", strings.TrimSpace(line))
		}
		matches := literalPattern.FindStringSubmatch(line)
		if len(matches) != 2 {
			continue
		}
		uid := 0
		if uidMatches := uidPattern.FindStringSubmatch(line); len(uidMatches) == 2 {
			uid, _ = strconv.Atoi(uidMatches[1])
		}
		size, _ := strconv.Atoi(matches[1])
		buffer := make([]byte, size)
		if _, err := io.ReadFull(reader, buffer); err != nil {
			return nil, err
		}
		item := parseMailHeader(string(buffer), folder, mailbox)
		item.UID = uid
		items = append(items, item)
		for {
			next, err := readMailLine(reader)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(next) == ")" {
				break
			}
			if strings.HasPrefix(next, tag+" ") {
				if strings.HasPrefix(next, tag+" OK") {
					sortReceivedMailMessages(items)
					return items, nil
				}
				return nil, fmt.Errorf("%s", strings.TrimSpace(next))
			}
		}
	}
}

func readFetchLiteral(reader *bufio.Reader, tag string) ([]byte, error) {
	literalPattern := regexp.MustCompile(`\{(\d+)\}$`)
	for {
		line, err := readMailLine(reader)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(line, tag+" ") {
			if strings.HasPrefix(line, tag+" OK") {
				return nil, fmt.Errorf("邮件内容为空")
			}
			return nil, fmt.Errorf("%s", strings.TrimSpace(line))
		}
		matches := literalPattern.FindStringSubmatch(line)
		if len(matches) != 2 {
			continue
		}
		size, _ := strconv.Atoi(matches[1])
		buffer := make([]byte, size)
		if _, err := io.ReadFull(reader, buffer); err != nil {
			return nil, err
		}
		for {
			next, err := readMailLine(reader)
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(next, tag+" ") {
				if strings.HasPrefix(next, tag+" OK") {
					return buffer, nil
				}
				return nil, fmt.Errorf("%s", strings.TrimSpace(next))
			}
		}
	}
}

func expectPOP3OK(reader *bufio.Reader) error {
	line, err := readMailLine(reader)
	if err != nil {
		return err
	}
	if strings.HasPrefix(line, "+OK") {
		return nil
	}
	return fmt.Errorf("%s", strings.TrimSpace(line))
}

func imapQuoted(value string) string {
	escaped := strings.ReplaceAll(value, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	return `"` + escaped + `"`
}

func imapMailboxName(value string) string {
	if strings.EqualFold(value, "INBOX") {
		return "INBOX"
	}
	return imapQuoted(value)
}

func parseMailHeader(raw string, folder string, mailbox string) receivedMailMessage {
	message, err := stdmail.ReadMessage(strings.NewReader(raw + "\r\n\r\n"))
	if err != nil || message == nil {
		return receivedMailMessage{Folder: folder, Mailbox: mailbox, Subject: "无标题"}
	}
	return mailMessageFromHeader(message.Header, folder, mailbox)
}

func parseFullMailHeader(raw []byte, folder string, mailbox string) receivedMailMessage {
	message, err := stdmail.ReadMessage(bytes.NewReader(raw))
	if err != nil || message == nil {
		return receivedMailMessage{Folder: folder, Mailbox: mailbox, Subject: "无标题"}
	}
	return mailMessageFromHeader(message.Header, folder, mailbox)
}

func mailMessageFromHeader(header stdmail.Header, folder string, mailbox string) receivedMailMessage {
	subject := decodeMailHeader(header.Get("Subject"))
	if strings.TrimSpace(subject) == "" {
		subject = "无标题"
	}
	dateText := strings.TrimSpace(header.Get("Date"))
	parsedTime, _ := parseMailDate(dateText)
	timestamp := int64(0)
	if !parsedTime.IsZero() {
		timestamp = parsedTime.Unix()
	}
	return receivedMailMessage{
		Folder:    folder,
		Mailbox:   mailbox,
		Subject:   subject,
		From:      decodeMailHeader(header.Get("From")),
		To:        decodeMailHeader(header.Get("To")),
		Time:      formatMailTime(parsedTime, dateText),
		Timestamp: timestamp,
	}
}

func decodeMailHeader(value string) string {
	decoded, err := new(mime.WordDecoder).DecodeHeader(strings.TrimSpace(value))
	if err != nil {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(decoded)
}

func extractEmailAddress(value string) string {
	decoded := decodeMailHeader(value)
	addresses, err := stdmail.ParseAddressList(decoded)
	if err != nil || len(addresses) == 0 {
		return decoded
	}
	return addresses[0].Address
}

func parseMailBody(raw []byte) (string, string) {
	message, err := stdmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return strings.TrimSpace(string(raw)), ""
	}
	contentType := message.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(contentType)
	transferEncoding := strings.ToLower(strings.TrimSpace(message.Header.Get("Content-Transfer-Encoding")))
	bodyBytes, _ := io.ReadAll(message.Body)
	if strings.HasPrefix(mediaType, "multipart/") {
		return parseMultipartMailBody(mediaType, params["boundary"], bodyBytes)
	}
	decoded := decodeMailBodyBytes(bodyBytes, transferEncoding)
	if strings.EqualFold(mediaType, "text/html") {
		return "", string(decoded)
	}
	return strings.TrimSpace(string(decoded)), ""
}

func parseMultipartMailBody(mediaType string, boundary string, bodyBytes []byte) (string, string) {
	if boundary == "" {
		return strings.TrimSpace(string(bodyBytes)), ""
	}
	reader := multipart.NewReader(bytes.NewReader(bodyBytes), boundary)
	var plainParts []string
	var htmlParts []string
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		partMediaType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		encoding := strings.ToLower(strings.TrimSpace(part.Header.Get("Content-Transfer-Encoding")))
		partBytes, _ := io.ReadAll(part)
		decoded := string(decodeMailBodyBytes(partBytes, encoding))
		if strings.HasPrefix(partMediaType, "multipart/") {
			nestedRaw := []byte("Content-Type: " + part.Header.Get("Content-Type") + "\r\n\r\n" + string(partBytes))
			nestedPlain, nestedHTML := parseMailBody(nestedRaw)
			if nestedPlain != "" {
				plainParts = append(plainParts, nestedPlain)
			}
			if nestedHTML != "" {
				htmlParts = append(htmlParts, nestedHTML)
			}
			continue
		}
		if strings.EqualFold(partMediaType, "text/html") {
			htmlParts = append(htmlParts, decoded)
		} else if strings.EqualFold(partMediaType, "text/plain") {
			plainParts = append(plainParts, strings.TrimSpace(decoded))
		}
	}
	return strings.TrimSpace(strings.Join(plainParts, "\n\n")), strings.TrimSpace(strings.Join(htmlParts, "\n\n"))
}

func decodeMailBodyBytes(value []byte, encoding string) []byte {
	switch encoding {
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(value)))
		if err == nil {
			return decoded
		}
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(string(value))))
		if err == nil {
			return decoded
		}
	}
	return value
}

func parseMailDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	candidates := []string{value, stripMailDateTrailingComment(value)}
	layouts := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"2 Jan 2006 15:04:05 -0700",
		"02 Jan 2006 15:04:05 -0700",
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if parsed, err := stdmail.ParseDate(candidate); err == nil {
			return parsed, nil
		}
		for _, layout := range layouts {
			if parsed, err := time.Parse(layout, candidate); err == nil {
				return parsed, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("invalid date")
}

func formatMailTime(value time.Time, fallback string) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format("2006/01/02 15:04:05")
}

func stripMailDateTrailingComment(value string) string {
	value = strings.TrimSpace(value)
	for strings.HasSuffix(value, ")") {
		start := strings.LastIndex(value, "(")
		if start < 0 {
			break
		}
		value = strings.TrimSpace(value[:start])
	}
	return value
}

func sortReceivedMailMessages(items []receivedMailMessage) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].Timestamp > items[i].Timestamp {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

func (a *smtpLoginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	a.step = 0
	return "LOGIN", nil, nil
}

func (a *smtpLoginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(string(fromServer))
	if strings.Contains(prompt, "username") || a.step == 0 {
		a.step++
		return []byte(a.username), nil
	}
	a.step++
	return []byte(a.password), nil
}

func createMailAccountRecord(ctx context.Context, req saveMailAccountRequest) (mailAccountResponse, error) {
	item := mailAccountResponse{}
	req.Email = strings.TrimSpace(req.Email)
	req.Password = strings.TrimSpace(req.Password)
	req.ImapHost = strings.TrimSpace(req.ImapHost)
	req.SMTPHost = strings.TrimSpace(req.SMTPHost)
	req.ImapProtocol = strings.TrimSpace(req.ImapProtocol)
	req.SMTPProtocol = strings.TrimSpace(req.SMTPProtocol)
	req.Remark = strings.TrimSpace(req.Remark)
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		return item, fmt.Errorf("邮箱账号格式错误")
	}
	if req.Password == "" {
		return item, fmt.Errorf("邮箱密码不能为空")
	}
	if req.GroupID <= 0 {
		req.GroupID = 2
	}
	if req.ImapProtocol == "" {
		req.ImapProtocol = "IMAP"
	}
	if req.SMTPProtocol == "" {
		req.SMTPProtocol = "SMTP(SSL)"
	}
	if req.ImapPort <= 0 {
		req.ImapPort = 993
	}
	if req.SMTPPort <= 0 {
		req.SMTPPort = 465
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return item, fmt.Errorf("保存邮箱失败")
	}
	defer db.Close()

	if !mailGroupExists(ctx, db, req.GroupID) {
		return item, fmt.Errorf("邮箱分组不存在")
	}
	if mailGroupHasChildren(ctx, db, req.GroupID) {
		return item, fmt.Errorf("该分组下有子分组，不能直接添加邮箱")
	}

	if req.ServerID > 0 {
		if err := db.QueryRowContext(ctx, `SELECT imap_host, smtp_host FROM mail_servers WHERE id = $1`, req.ServerID).Scan(&req.ImapHost, &req.SMTPHost); err != nil {
			return item, fmt.Errorf("服务器不存在")
		}
	}
	if req.ImapHost == "" || req.SMTPHost == "" {
		return item, fmt.Errorf("收发服务器地址不能为空")
	}

	var createdAt time.Time
	err = db.QueryRowContext(ctx, `
INSERT INTO mail_accounts (group_id, server_id, email, password, imap_host, smtp_host, imap_protocol, imap_port, imap_ssl, smtp_protocol, smtp_port, smtp_ssl, remark, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'active')
RETURNING id, created_at
`, req.GroupID, req.ServerID, req.Email, req.Password, req.ImapHost, req.SMTPHost, req.ImapProtocol, req.ImapPort, req.ImapSSL, req.SMTPProtocol, req.SMTPPort, req.SMTPSSL, req.Remark).Scan(&item.ID, &createdAt)
	if err != nil {
		return item, fmt.Errorf("邮箱已存在或保存失败")
	}

	item.GroupID = req.GroupID
	item.Email = req.Email
	item.ServerID = req.ServerID
	item.ImapHost = req.ImapHost
	item.SMTPHost = req.SMTPHost
	item.ImapProtocol = req.ImapProtocol
	item.ImapPort = req.ImapPort
	item.ImapSSL = req.ImapSSL
	item.SMTPProtocol = req.SMTPProtocol
	item.SMTPPort = req.SMTPPort
	item.SMTPSSL = req.SMTPSSL
	item.Remark = req.Remark
	item.Status = "active"
	item.StatusReason = ""
	item.CreatedAt = createdAt.Format("2006/01/02 15:04:05")
	if err := db.QueryRowContext(ctx, `SELECT name FROM mail_groups WHERE id = $1`, req.GroupID).Scan(&item.GroupName); err != nil {
		item.GroupName = ""
	}
	if req.ServerID > 0 {
		_ = db.QueryRowContext(ctx, `SELECT name FROM mail_servers WHERE id = $1`, req.ServerID).Scan(&item.ServerName)
	}
	return item, nil
}

func mailGroupExists(ctx context.Context, db *sql.DB, id int) bool {
	var exists bool
	return db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail_groups WHERE id = $1)`, id).Scan(&exists) == nil && exists
}

func mailGroupIsSystem(ctx context.Context, db *sql.DB, id int) bool {
	var system bool
	return db.QueryRowContext(ctx, `SELECT system FROM mail_groups WHERE id = $1`, id).Scan(&system) == nil && system
}

func mailGroupParentID(ctx context.Context, db *sql.DB, id int) int {
	var parentID int
	if err := db.QueryRowContext(ctx, `SELECT parent_id FROM mail_groups WHERE id = $1`, id).Scan(&parentID); err != nil {
		return 0
	}
	return parentID
}

func mailGroupHasChildren(ctx context.Context, db *sql.DB, id int) bool {
	var exists bool
	return db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail_groups WHERE parent_id = $1)`, id).Scan(&exists) == nil && exists
}

func mailGroupHasAccounts(ctx context.Context, db *sql.DB, id int) bool {
	var exists bool
	return db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail_accounts WHERE group_id = $1)`, id).Scan(&exists) == nil && exists
}

func (s *appState) updateUser(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	var req saveUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u8bf7\u6c42\u53c2\u6570\u9519\u8bef"})
		return
	}

	update := s.db.User.UpdateOneID(id)
	if req.Username != "" {
		update.SetUsername(req.Username)
	}
	update.SetEmail(req.Email)
	update.SetBalance(req.Balance)
	if req.Enabled != nil {
		update.SetEnabled(*req.Enabled)
	}
	if req.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u66f4\u65b0\u7528\u6237\u5931\u8d25"})
			return
		}
		update.SetPasswordHash(string(hash))
	}

	item, err := update.Save(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u66f4\u65b0\u7528\u6237\u5931\u8d25"})
		return
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: toAdminUser(item), Msg: "ok"})
}

func (s *appState) updateUserStatus(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	var req updateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u8bf7\u6c42\u53c2\u6570\u9519\u8bef"})
		return
	}

	enabled := req.Status != "disabled"
	item, err := s.db.User.UpdateOneID(id).SetEnabled(enabled).Save(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u66f4\u65b0\u7528\u6237\u72b6\u6001\u5931\u8d25"})
		return
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: toAdminUser(item), Msg: "ok"})
}

func (s *appState) updateUserBalance(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	var req updateBalanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u8bf7\u6c42\u53c2\u6570\u9519\u8bef"})
		return
	}
	if req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u91d1\u989d\u5fc5\u987b\u5927\u4e8e 0"})
		return
	}

	current, err := s.db.User.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u7528\u6237\u4e0d\u5b58\u5728"})
		return
	}

	next := current.Balance + req.Amount
	if req.Type == "deduct" {
		next = current.Balance - req.Amount
	}
	if next < 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u4f59\u989d\u4e0d\u8db3"})
		return
	}

	item, err := current.Update().SetBalance(next).Save(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u4f59\u989d\u66f4\u65b0\u5931\u8d25"})
		return
	}
	if err := saveBalanceRecord(c.Request.Context(), id, req.Type, req.Amount, next, req.Remark); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u4f59\u989d\u8bb0\u5f55\u4fdd\u5b58\u5931\u8d25"})
		return
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: toAdminUser(item), Msg: "ok"})
}

func saveBalanceRecord(ctx context.Context, userID int, recordType string, amount float64, balanceAfter float64, remark string) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.ExecContext(ctx, `
INSERT INTO balance_records (user_id, type, amount, balance_after, remark, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
`, userID, recordType, amount, balanceAfter, strings.TrimSpace(remark), time.Now())
	return err
}

func (s *appState) listUserBalanceRecords(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	current, err := s.db.User.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u7528\u6237\u4e0d\u5b58\u5728"})
		return
	}

	recordType := c.Query("type")
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u83b7\u53d6\u5145\u503c\u8bb0\u5f55\u5931\u8d25"})
		return
	}
	defer db.Close()

	query := `
SELECT id, user_id, type, amount, balance_after, remark, created_at
FROM balance_records
WHERE user_id = $1
`
	args := []interface{}{id}
	if recordType == "deposit" || recordType == "deduct" {
		query += " AND type = $2"
		args = append(args, recordType)
	}
	query += " ORDER BY created_at DESC, id DESC"

	rows, err := db.QueryContext(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u83b7\u53d6\u5145\u503c\u8bb0\u5f55\u5931\u8d25"})
		return
	}
	defer rows.Close()

	records := []balanceRecordResponse{}
	for rows.Next() {
		var record balanceRecordResponse
		var createdAt time.Time
		if err := rows.Scan(&record.ID, &record.UserID, &record.Type, &record.Amount, &record.BalanceAfter, &record.Remark, &createdAt); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "\u83b7\u53d6\u5145\u503c\u8bb0\u5f55\u5931\u8d25"})
			return
		}
		record.CreatedAt = createdAt.Format(time.RFC3339)
		records = append(records, record)
	}

	c.JSON(http.StatusOK, apiResponse{
		Code: 0,
		Data: gin.H{
			"user":    toAdminUser(current),
			"records": records,
		},
		Msg: "ok",
	})
}

func (s *appState) deleteUser(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	if id == 1 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u521d\u59cb\u7ba1\u7406\u5458\u4e0d\u80fd\u5220\u9664"})
		return
	}
	item, err := s.db.User.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u7528\u6237\u4e0d\u5b58\u5728"})
		return
	}
	if item.Role == "admin" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u7ba1\u7406\u5458\u4e0d\u80fd\u5220\u9664"})
		return
	}

	if err := deleteUserAndCompactIDs(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u5220\u9664\u7528\u6237\u5931\u8d25"})
		return
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
}

func deleteUserAndCompactIDs(ctx context.Context, id int) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM balance_records WHERE user_id = $1`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE balance_records SET user_id = user_id - 1 WHERE user_id > $1`, id); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE users SET id = -id WHERE id > $1`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET id = (-id) - 1 WHERE id < 0`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('users', 'id'), GREATEST(COALESCE((SELECT MAX(id) FROM users), 1), 1), true)`); err != nil {
		return err
	}

	return tx.Commit()
}

func valueOrDefault(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func siteLogoOrDefault(value string) string {
	if strings.TrimSpace(value) == "" || value == defaultPublicLogoPath {
		return defaultPublicLogoDataURL()
	}
	return value
}

func defaultPublicLogoDataURL() string {
	defaultPublicLogoOnce.Do(func() {
		defaultPublicLogoValue = defaultPublicLogoPath
		for _, path := range defaultPublicLogoCandidates() {
			data, err := os.ReadFile(path)
			if err == nil && len(data) > 0 {
				defaultPublicLogoValue = "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
				return
			}
		}
	})
	return defaultPublicLogoValue
}

func defaultPublicLogoCandidates() []string {
	candidates := []string{
		filepath.Clean("../qianduan/public/logo.png"),
		filepath.Clean("qianduan/public/logo.png"),
		filepath.Clean("public/logo.png"),
		filepath.Clean("logo.png"),
	}

	if executable, err := os.Executable(); err == nil {
		executableDir := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(executableDir, "public", "logo.png"),
			filepath.Join(executableDir, "logo.png"),
			filepath.Join(executableDir, "..", "qianduan", "public", "logo.png"),
		)
	}

	return candidates
}

func defaultSystemSettings() map[string]interface{} {
	return map[string]interface{}{
		"site_name":                     "\u90ae\u7bb1\u7ba1\u7406\u7cfb\u7edf",
		"site_logo":                     defaultPublicLogoDataURL(),
		"site_subtitle":                 "\u6279\u91cf\u8d26\u53f7\u4e0e\u4efb\u52a1\u7ba1\u7406\u5e73\u53f0",
		"table_default_page_size":       20,
		"table_page_size_options":       []interface{}{10, 20, 50, 100},
		"card_key_log_cleanup_days":     "",
		"backup_schedule_enabled":       false,
		"backup_schedule_frequency":     "daily",
		"backup_schedule_time":          "03:00",
		"backup_schedule_interval_days": 1,
		"backup_schedule_weekday":       1,
		"backup_schedule_month_day":     1,
		"backup_schedule_retain_count":  3,
		"backup_webdav_enabled":         false,
		"backup_webdav_url":             "",
		"backup_webdav_username":        "",
		"backup_webdav_password":        "",
		"backup_webdav_remote_dir":      "/MailPlus",
	}
}

func (s *appState) readSettings(ctx context.Context) (map[string]interface{}, error) {
	defaults := defaultSystemSettings()
	settings, err := s.db.SystemSetting.Query().All(ctx)
	if err != nil {
		return nil, err
	}

	result := map[string]interface{}{}
	for key, value := range defaults {
		result[key] = value
	}
	for _, item := range settings {
		if fallback, ok := defaults[item.Key]; ok {
			result[item.Key] = parseSettingValue(item.Value, fallback)
		}
	}
	if logo, ok := result["site_logo"].(string); ok {
		result["site_logo"] = siteLogoOrDefault(logo)
	}

	return result, nil
}

func settingValueToString(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		bytes, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(bytes)
	}
}

func parseSettingValue(value string, fallback interface{}) interface{} {
	switch fallback.(type) {
	case bool:
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
		var parsed bool
		if err := json.Unmarshal([]byte(value), &parsed); err == nil {
			return parsed
		}
	case int:
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
		var parsed int
		if err := json.Unmarshal([]byte(value), &parsed); err == nil {
			return parsed
		}
	case []interface{}, map[string]interface{}:
		var parsed interface{}
		if err := json.Unmarshal([]byte(value), &parsed); err == nil {
			return parsed
		}
	default:
		return value
	}
	return fallback
}

func parsePositiveInt(value string, fallback int) int {
	result, err := strconv.Atoi(value)
	if err != nil || result <= 0 {
		return fallback
	}
	return result
}

func parseListPage(c *gin.Context, defaultPageSize int, maxPageSize int) (int, int, int) {
	page := parsePositiveInt(c.Query("page"), 1)
	pageSize := parsePositiveInt(c.Query("page_size"), defaultPageSize)
	if maxPageSize > 0 && pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	return page, pageSize, (page - 1) * pageSize
}

func calculatePages(total int, pageSize int) int {
	if total <= 0 || pageSize <= 0 {
		return 0
	}
	return (total + pageSize - 1) / pageSize
}

func normalizeSortOrder(value string) string {
	if strings.EqualFold(value, "desc") {
		return "DESC"
	}
	return "ASC"
}

func queryBool(c *gin.Context, key string) bool {
	value := strings.TrimSpace(c.Query(key))
	return value == "1" || strings.EqualFold(value, "true") || strings.EqualFold(value, "yes") || strings.EqualFold(value, "on")
}

func (s *appState) listTasks(c *gin.Context) {
	limit := parsePositiveInt(c.Query("limit"), 20)
	limit = clampInt(limit, 1, 100)
	tasks, err := s.tasks.list(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取任务列表失败"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: tasks, Msg: "ok"})
}

func parseIDParam(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "\u65e0\u6548\u7684 ID"})
		return 0, false
	}
	return id, true
}

func (s *appState) getTask(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	task, ok := s.tasks.get(id)
	if !ok {
		c.JSON(http.StatusNotFound, apiResponse{Code: 404, Msg: "任务不存在"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: task, Msg: "ok"})
}

func (s *appState) downloadTaskResult(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	task, ok := s.tasks.get(id)
	if !ok {
		c.JSON(http.StatusNotFound, apiResponse{Code: 404, Msg: "任务不存在"})
		return
	}
	if task.Status != "success" || task.ResultPath == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "任务结果尚未生成"})
		return
	}
	if !taskPathInTempDir(task.ResultPath) {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "任务结果路径无效"})
		return
	}
	if _, err := os.Stat(task.ResultPath); err != nil {
		c.JSON(http.StatusNotFound, apiResponse{Code: 404, Msg: "任务结果文件不存在"})
		return
	}
	filename := filepath.Base(task.ResultPath)
	if strings.TrimSpace(task.FileName) != "" {
		filename = filepath.Base(task.FileName)
	}
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	c.File(task.ResultPath)
	cleanupAfter := time.Now().Add(taskResultCleanupDelay)
	s.tasks.markResultDownloaded(task.ID, cleanupAfter)
}

func (s *appState) deleteTask(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if err := s.tasks.delete(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, apiResponse{Code: 404, Msg: "任务不存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "清理任务失败"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
}

func (s *appState) clearTaskResult(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if err := s.tasks.delete(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, apiResponse{Code: 404, Msg: "任务不存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "清理任务结果失败"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
}

func taskTempDir() (string, error) {
	dir := filepath.Join(os.TempDir(), "mail-admin-tasks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func taskZipPath(taskID string, prefix string) (string, error) {
	dir, err := taskTempDir()
	if err != nil {
		return "", err
	}
	safePrefix := strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(strings.TrimSpace(prefix))
	if safePrefix == "" {
		safePrefix = "task"
	}
	return filepath.Join(dir, fmt.Sprintf("%s-%s.zip", safePrefix, taskID)), nil
}

func saveUploadedTaskZip(fileHeader *multipart.FileHeader, taskID string, prefix string) (string, error) {
	source, err := fileHeader.Open()
	if err != nil {
		return "", err
	}
	defer source.Close()

	path, err := taskZipPath(taskID, prefix+"-upload")
	if err != nil {
		return "", err
	}
	destination, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer destination.Close()
	if _, err := io.Copy(destination, source); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func cleanupExpiredTaskResults(ctx context.Context, db *sql.DB) error {
	completedCutoff := time.Now().Add(-taskCompletedMaxAge)
	rows, err := db.QueryContext(ctx, `
SELECT id, result_path
FROM background_tasks
WHERE status <> 'running'
  AND (
    (result_path <> '' AND result_cleanup_after IS NOT NULL AND result_cleanup_after <= NOW())
    OR updated_at < $1
  )
`, completedCutoff)
	if err != nil {
		return err
	}
	return deleteBackgroundTaskRows(ctx, db, rows)
}

func cleanupStaleBackgroundTasks(ctx context.Context, db *sql.DB) error {
	staleCutoff := time.Now().Add(-taskStaleMaxAge)
	rows, err := db.QueryContext(ctx, `
SELECT id, result_path
FROM background_tasks
WHERE updated_at < $1
`, staleCutoff)
	if err != nil {
		return err
	}
	return deleteBackgroundTaskRows(ctx, db, rows)
}

func deleteBackgroundTaskRows(ctx context.Context, db *sql.DB, rows *sql.Rows) error {
	defer rows.Close()
	for rows.Next() {
		var id string
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			return err
		}
		if err := removeTaskResultFile(path); err != nil {
			continue
		}
		_, _ = db.ExecContext(ctx, `
DELETE FROM background_tasks
WHERE id = $1
`, id)
	}
	return rows.Err()
}

func removeTaskResultFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if !taskPathInTempDir(path) {
		return fmt.Errorf("任务结果路径无效")
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func taskPathInTempDir(path string) bool {
	dir, err := taskTempDir()
	if err != nil {
		return false
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absDir, absPath)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func accountGroupTable(accountTable string) (string, error) {
	switch accountTable {
	case "mail_accounts":
		return "mail_groups", nil
	case "outlook_accounts":
		return "outlook_groups", nil
	default:
		return "", fmt.Errorf("invalid table")
	}
}

func accountGroupTreeWhere(alias string, groupTable string, placeholder int) string {
	prefix := alias
	if prefix != "" {
		prefix += "."
	}
	return fmt.Sprintf(`%sgroup_id IN (
	WITH RECURSIVE group_tree AS (
		SELECT id FROM %s WHERE id = $%d
		UNION ALL
		SELECT g.id
		FROM %s g
		JOIN group_tree gt ON g.parent_id = gt.id
	)
	SELECT id FROM group_tree
)`, prefix, groupTable, placeholder, groupTable)
}

func buildAccountFilterWhere(alias string, accountTable string, filter accountListFilter, start int) (string, []interface{}, error) {
	where := []string{"1 = 1"}
	args := []interface{}{}
	prefix := alias
	if prefix != "" {
		prefix += "."
	}
	groupTable, err := accountGroupTable(accountTable)
	if err != nil {
		return "", nil, err
	}
	if filter.GroupID > 0 {
		where = append(where, accountGroupTreeWhere(alias, groupTable, start+len(args)))
		args = append(args, filter.GroupID)
	}
	search := strings.TrimSpace(filter.Search)
	if search != "" {
		where = append(where, fmt.Sprintf("(%semail ILIKE $%d OR %sremark ILIKE $%d)", prefix, start+len(args), prefix, start+len(args)))
		args = append(args, "%"+search+"%")
	}
	return strings.Join(where, " AND "), args, nil
}

func queryIDsByFilter(ctx context.Context, db *sql.DB, table string, filter accountListFilter) ([]int, error) {
	if table != "mail_accounts" && table != "outlook_accounts" {
		return nil, fmt.Errorf("invalid table")
	}
	whereSQL, args, err := buildAccountFilterWhere("", table, filter, 1)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id FROM `+table+` WHERE `+whereSQL+` ORDER BY id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func resolveAccountIDs(ctx context.Context, db *sql.DB, table string, ids []int, filter accountListFilter) ([]int, error) {
	if len(ids) > 0 {
		return normalizePositiveIDs(ids), nil
	}
	return queryIDsByFilter(ctx, db, table, filter)
}

func normalizePositiveIDs(ids []int) []int {
	result := []int{}
	seen := map[int]bool{}
	for _, id := range ids {
		if id > 0 && !seen[id] {
			result = append(result, id)
			seen[id] = true
		}
	}
	sort.Ints(result)
	return result
}

func (s *appState) deleteAccountsInBatches(ctx context.Context, db *sql.DB, taskID string, table string, ids []int) error {
	if table != "mail_accounts" && table != "outlook_accounts" {
		return fmt.Errorf("invalid table")
	}
	for start := 0; start < len(ids); start += batchDeleteSize {
		end := start + batchDeleteSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders, _ := intPlaceholders(batch, 1)
		if placeholders == "" {
			continue
		}
		if _, err := deleteAccountRowsAndUnbindCardKeys(ctx, db, table, batch); err != nil {
			return err
		}
		s.tasks.recordBatchSuccess(taskID, len(batch))
	}
	return nil
}

func deleteAccountRowsAndUnbindCardKeys(ctx context.Context, db *sql.DB, table string, ids []int) (int64, error) {
	if table != "mail_accounts" && table != "outlook_accounts" {
		return 0, fmt.Errorf("invalid table")
	}
	placeholders, args := intPlaceholders(ids, 1)
	if placeholders == "" {
		return 0, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
UPDATE card_keys ck
SET bound_email = '',
    updated_at = NOW()
WHERE TRIM(ck.bound_email) <> ''
  AND EXISTS (
	SELECT 1
	FROM `+table+` a
	WHERE a.id IN (`+placeholders+`)
	  AND LOWER(TRIM(a.email)) = LOWER(TRIM(ck.bound_email))
  )
`, args...); err != nil {
		return 0, err
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return 0, err
	}
	rowsAffected, _ := result.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return rowsAffected, nil
}

func countAccountExportRows(ctx context.Context, db *sql.DB, table string, selector accountExportSelector) (int, error) {
	if table != "mail_accounts" && table != "outlook_accounts" {
		return 0, fmt.Errorf("invalid table")
	}
	if len(selector.IDs) > 0 {
		ids := normalizePositiveIDs(selector.IDs)
		total := 0
		for start := 0; start < len(ids); start += exportIDBatchSize {
			end := start + exportIDBatchSize
			if end > len(ids) {
				end = len(ids)
			}
			placeholders, args := intPlaceholders(ids[start:end], 1)
			if placeholders == "" {
				continue
			}
			var count int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE id IN (`+placeholders+`)`, args...).Scan(&count); err != nil {
				return 0, err
			}
			total += count
		}
		return total, nil
	}
	whereSQL, args, err := buildAccountFilterWhere("", table, selector.Filter, 1)
	if err != nil {
		return 0, err
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *appState) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := strings.TrimSpace(c.GetHeader("Authorization"))
		if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, apiResponse{Code: 401, Msg: "\u8bf7\u5148\u767b\u5f55"})
			return
		}

		token := strings.TrimSpace(header[len("Bearer "):])
		session, ok := s.sessions.get(token)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, apiResponse{Code: 401, Msg: "\u767b\u5f55\u5df2\u8fc7\u671f\uff0c\u8bf7\u91cd\u65b0\u767b\u5f55"})
			return
		}

		c.Set("user_id", session.UserID)
		c.Next()
	}
}

func (s *appState) adminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		current, ok := s.currentUser(c)
		if !ok {
			c.Abort()
			return
		}
		if !current.Enabled || current.Role != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, apiResponse{Code: 403, Msg: "Admin permission required"})
			return
		}
		if userMustChangePassword(c.Request.Context(), current.ID) {
			c.AbortWithStatusJSON(http.StatusForbidden, apiResponse{Code: 403, Msg: "Password change required"})
			return
		}
		c.Next()
	}
}

func (s *appState) currentUser(c *gin.Context) (*ent.User, bool) {
	idValue, exists := c.Get("user_id")
	id, ok := idValue.(int)
	if !exists || !ok || id <= 0 {
		c.JSON(http.StatusUnauthorized, apiResponse{Code: 401, Msg: "\u8bf7\u5148\u767b\u5f55"})
		return nil, false
	}
	item, err := s.db.User.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusUnauthorized, apiResponse{Code: 401, Msg: "\u8bf7\u5148\u767b\u5f55"})
		return nil, false
	}
	return item, true
}

func userStatus(item *ent.User) string {
	if item.Enabled {
		return "active"
	}
	return "disabled"
}

func toAuthUser(item *ent.User) authUser {
	return authUser{
		ID:        item.ID,
		Username:  item.Username,
		Email:     item.Email,
		AvatarURL: item.AvatarURL,
		Balance:   item.Balance,
		Role:      item.Role,
		Status:    userStatus(item),
		CreatedAt: item.CreatedAt.Format(time.RFC3339),
	}
}

func toAdminUser(item *ent.User) adminUserResponse {
	return adminUserResponse{
		ID:        item.ID,
		Username:  item.Username,
		Email:     item.Email,
		AvatarURL: item.AvatarURL,
		Balance:   item.Balance,
		Role:      item.Role,
		Status:    userStatus(item),
		CreatedAt: item.CreatedAt.Format(time.RFC3339),
	}
}

func newToken() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(bytes)
}

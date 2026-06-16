package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	encryptedzip "github.com/alexmullins/zip"
	"github.com/gin-gonic/gin"
)

const (
	defaultOutlookClientID = "9e5f94bc-e8a4-4e73-b8be-63364c29d753"
	microsoftTokenURL      = "https://login.microsoftonline.com/common/oauth2/v2.0/token"
	microsoftAuthURL       = "https://login.microsoftonline.com/common/oauth2/v2.0/authorize"
	graphAPIBaseURL        = "https://graph.microsoft.com/v1.0"
)

type outlookGroupResponse struct {
	ID        int    `json:"id"`
	ParentID  int    `json:"parent_id"`
	Name      string `json:"name"`
	System    bool   `json:"system"`
	SortOrder int    `json:"sort_order"`
	Count     int    `json:"count"`
	CreatedAt string `json:"created_at"`
}

type outlookAccountResponse struct {
	ID                 int    `json:"id"`
	GroupID            int    `json:"group_id"`
	GroupName          string `json:"group_name"`
	Email              string `json:"email"`
	Password           string `json:"password,omitempty"`
	ClientID           string `json:"client_id"`
	RefreshTokenMasked string `json:"refresh_token"`
	Remark             string `json:"remark"`
	Status             string `json:"status"`
	StatusReason       string `json:"status_reason"`
	LastTokenRefreshAt string `json:"last_token_refresh_at"`
	CreatedAt          string `json:"created_at"`
}

type outlookAccountListResponse struct {
	Items    []outlookAccountResponse `json:"items"`
	Total    int                      `json:"total"`
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
	Pages    int                      `json:"pages"`
	Normal   int                      `json:"normal"`
	Error    int                      `json:"error"`
}

type saveOutlookAccountRequest struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	ClientID     string `json:"client_id"`
	RefreshToken string `json:"refresh_token"`
	GroupID      int    `json:"group_id"`
	Remark       string `json:"remark"`
	Status       string `json:"status"`
}

type batchOutlookAccountRequest struct {
	Content string `json:"content"`
	GroupID int    `json:"group_id"`
}

type outlookBatchActionRequest struct {
	Action  string            `json:"action"`
	IDs     []int             `json:"ids"`
	Filter  accountListFilter `json:"filter"`
	GroupID int               `json:"group_id"`
}

type outlookDataExportRequest struct {
	IDs      []int             `json:"ids"`
	Filter   accountListFilter `json:"filter"`
	Password string            `json:"password"`
}

type outlookDataGroup struct {
	ID        int    `json:"id"`
	ParentID  int    `json:"parent_id"`
	Name      string `json:"name"`
	System    bool   `json:"system"`
	SortOrder int    `json:"sort_order"`
	CreatedAt string `json:"created_at"`
}

type outlookDataAccount struct {
	ID                 int    `json:"id"`
	GroupID            int    `json:"group_id"`
	Email              string `json:"email"`
	Password           string `json:"password"`
	ClientID           string `json:"client_id"`
	RefreshToken       string `json:"refresh_token"`
	Remark             string `json:"remark"`
	Status             string `json:"status"`
	StatusReason       string `json:"status_reason"`
	LastTokenRefreshAt string `json:"last_token_refresh_at"`
	CreatedAt          string `json:"created_at"`
}

type outlookDataPayload struct {
	ExportedAt string               `json:"exported_at"`
	Groups     []outlookDataGroup   `json:"groups"`
	Accounts   []outlookDataAccount `json:"accounts"`
}

type outlookStoredAccount struct {
	ID           int
	Email        string
	Password     string
	ClientID     string
	RefreshToken string
	GroupID      int
	Remark       string
	Status       string
}

type outlookTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

type outlookOAuthSession struct {
	ClientID     string
	RefreshToken string
	ExpiresAt    time.Time
	Complete     bool
}

type outlookOAuthStore struct {
	mu       sync.Mutex
	sessions map[string]outlookOAuthSession
}

func newOutlookOAuthStore() *outlookOAuthStore {
	return &outlookOAuthStore{sessions: map[string]outlookOAuthSession{}}
}

func (s *outlookOAuthStore) start(state string, clientID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	s.sessions[state] = outlookOAuthSession{ClientID: clientID, ExpiresAt: time.Now().Add(10 * time.Minute)}
}

func (s *outlookOAuthStore) clientID(state string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	return s.sessions[state].ClientID
}

func (s *outlookOAuthStore) complete(state string, refreshToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[state]
	if !ok {
		return
	}
	session.RefreshToken = refreshToken
	session.Complete = true
	session.ExpiresAt = time.Now().Add(2 * time.Minute)
	s.sessions[state] = session
}

func (s *outlookOAuthStore) poll(state string) (outlookOAuthSession, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	session, ok := s.sessions[state]
	if !ok {
		return outlookOAuthSession{}, "not_found"
	}
	if !session.Complete {
		return session, "pending"
	}
	delete(s.sessions, state)
	return session, "success"
}

func (s *outlookOAuthStore) cleanupLocked() {
	now := time.Now()
	for state, session := range s.sessions {
		if now.After(session.ExpiresAt) {
			delete(s.sessions, state)
		}
	}
}

type outlookGraphEmailAddress struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type outlookGraphRecipient struct {
	EmailAddress outlookGraphEmailAddress `json:"emailAddress"`
}

type outlookGraphBody struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

type outlookGraphMessage struct {
	ID               string                  `json:"id"`
	Subject          string                  `json:"subject"`
	From             outlookGraphRecipient   `json:"from"`
	ToRecipients     []outlookGraphRecipient `json:"toRecipients"`
	CCRecipients     []outlookGraphRecipient `json:"ccRecipients"`
	ReceivedDateTime string                  `json:"receivedDateTime"`
	BodyPreview      string                  `json:"bodyPreview"`
	Body             outlookGraphBody        `json:"body"`
	IsRead           bool                    `json:"isRead"`
	HasAttachments   bool                    `json:"hasAttachments"`
}

type outlookMessageResponse struct {
	ID             string `json:"id"`
	Folder         string `json:"folder"`
	Subject        string `json:"subject"`
	From           string `json:"from"`
	To             string `json:"to"`
	CC             string `json:"cc"`
	Time           string `json:"time"`
	Timestamp      int64  `json:"timestamp"`
	BodyPreview    string `json:"body_preview"`
	Body           string `json:"body"`
	HTML           string `json:"html"`
	IsRead         bool   `json:"is_read"`
	HasAttachments bool   `json:"has_attachments"`
}

type outlookMessageListResponse struct {
	Items []outlookMessageResponse `json:"items"`
	Total int                      `json:"total"`
	Error string                   `json:"error,omitempty"`
}

type outlookMessageDetailsRequest struct {
	IDs []string `json:"ids"`
}

func ensureOutlookManagementTables(ctx context.Context) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err = db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS outlook_groups (
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
	if _, err = db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS outlook_groups_name_parent_idx ON outlook_groups (parent_id, name)`); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, `ALTER TABLE outlook_groups ADD COLUMN IF NOT EXISTS sort_order INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, `
INSERT INTO outlook_groups (id, parent_id, name, system)
VALUES (1, 0, '全部微软邮箱', TRUE)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, parent_id = 0, system = TRUE
`); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, `
INSERT INTO outlook_groups (id, parent_id, name, system)
VALUES (2, 0, '默认分组', TRUE)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, parent_id = 0, system = TRUE
`); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('outlook_groups', 'id'), GREATEST(COALESCE((SELECT MAX(id) FROM outlook_groups), 2), 2), true)`); err != nil {
		return err
	}
	if err = normalizeGroupSortOrders(ctx, db, "outlook_groups"); err != nil {
		return err
	}

	if _, err = db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS outlook_accounts (
	id SERIAL PRIMARY KEY,
	group_id INTEGER NOT NULL DEFAULT 2,
	email TEXT NOT NULL,
	password TEXT NOT NULL DEFAULT '',
	client_id TEXT NOT NULL,
	refresh_token TEXT NOT NULL,
	remark TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active',
	status_reason TEXT NOT NULL DEFAULT '',
	last_token_refresh_at TIMESTAMP WITH TIME ZONE,
	created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
)
`); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, `ALTER TABLE outlook_accounts ADD COLUMN IF NOT EXISTS status_reason TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	indexStatements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS outlook_accounts_group_email_unique_idx ON outlook_accounts (group_id, email)`,
		`DROP INDEX IF EXISTS outlook_accounts_email_idx`,
		`CREATE INDEX IF NOT EXISTS outlook_accounts_group_id_idx ON outlook_accounts (group_id)`,
		`CREATE INDEX IF NOT EXISTS outlook_accounts_status_idx ON outlook_accounts (status)`,
		`CREATE INDEX IF NOT EXISTS outlook_accounts_created_at_id_idx ON outlook_accounts (created_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS outlook_accounts_group_created_at_id_idx ON outlook_accounts (group_id, created_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS outlook_accounts_group_id_id_idx ON outlook_accounts (group_id, id)`,
		`CREATE INDEX IF NOT EXISTS outlook_accounts_client_id_id_idx ON outlook_accounts (client_id, id)`,
		`CREATE INDEX IF NOT EXISTS outlook_accounts_group_client_id_id_idx ON outlook_accounts (group_id, client_id, id)`,
		`CREATE INDEX IF NOT EXISTS outlook_accounts_status_id_idx ON outlook_accounts (status, id)`,
		`CREATE INDEX IF NOT EXISTS outlook_accounts_group_status_id_idx ON outlook_accounts (group_id, status, id)`,
		`CREATE INDEX IF NOT EXISTS outlook_accounts_group_email_id_idx ON outlook_accounts (group_id, email, id)`,
	}
	for _, statement := range indexStatements {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	if _, err = db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS pg_trgm`); err == nil {
		_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS outlook_accounts_email_trgm_idx ON outlook_accounts USING gin (email gin_trgm_ops)`)
		_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS outlook_accounts_remark_trgm_idx ON outlook_accounts USING gin (remark gin_trgm_ops)`)
	}
	return nil
}

func (s *appState) listOutlookGroups(c *gin.Context) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "获取微软邮箱分组失败"})
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
		  AND LOWER(TRIM(ck.bound_email)) = LOWER(TRIM(outlook_accounts.email))
	)`
	}

	rows, err := db.QueryContext(c.Request.Context(), `
SELECT g.id, g.parent_id, g.name, g.system, g.sort_order, COALESCE(stats.count, 0) AS count, g.created_at
FROM outlook_groups g
LEFT JOIN (
	SELECT group_id, COUNT(*) AS count
	FROM outlook_accounts
	`+accountCountWhere+`
	GROUP BY group_id
) stats ON stats.group_id = g.id
ORDER BY system DESC, sort_order ASC, id ASC
`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "获取微软邮箱分组失败"})
		return
	}
	defer rows.Close()

	items := []outlookGroupResponse{}
	for rows.Next() {
		var item outlookGroupResponse
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.ParentID, &item.Name, &item.System, &item.SortOrder, &item.Count, &createdAt); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "获取微软邮箱分组失败"})
			return
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		items = append(items, item)
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: items, Msg: "ok"})
}

func (s *appState) createOutlookGroup(c *gin.Context) {
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
	if req.ParentID > 0 && !outlookGroupExists(c.Request.Context(), db, req.ParentID) {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "父级分组不存在"})
		return
	}
	if req.ParentID > 0 && outlookGroupParentID(c.Request.Context(), db, req.ParentID) > 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "子分组下不能继续添加子分组"})
		return
	}
	if req.ParentID > 0 && outlookGroupHasAccounts(c.Request.Context(), db, req.ParentID) {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "该分组下已有邮箱，不能继续添加子分组"})
		return
	}

	tx, err := db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "创建分组失败"})
		return
	}
	defer tx.Rollback()

	if err = normalizeGroupSortOrdersTx(c.Request.Context(), tx, "outlook_groups"); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "创建分组失败"})
		return
	}

	var siblingCount int
	if err = tx.QueryRowContext(c.Request.Context(), `SELECT COUNT(*) FROM outlook_groups WHERE parent_id = $1 AND system = FALSE`, req.ParentID).Scan(&siblingCount); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "创建分组失败"})
		return
	}
	sortOrder := normalizeRequestedGroupSortOrder(req.SortOrder, siblingCount+1, siblingCount+1)
	if _, err = tx.ExecContext(c.Request.Context(), `UPDATE outlook_groups SET sort_order = sort_order + 1 WHERE parent_id = $1 AND system = FALSE AND sort_order >= $2`, req.ParentID, sortOrder); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "创建分组失败"})
		return
	}

	var item outlookGroupResponse
	var createdAt time.Time
	err = tx.QueryRowContext(c.Request.Context(), `
INSERT INTO outlook_groups (parent_id, name, system, sort_order)
VALUES ($1, $2, FALSE, $3)
RETURNING id, parent_id, name, system, sort_order, created_at
`, req.ParentID, req.Name, sortOrder).Scan(&item.ID, &item.ParentID, &item.Name, &item.System, &item.SortOrder, &createdAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "分组名称已存在或创建失败"})
		return
	}
	if err = tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "创建分组失败"})
		return
	}
	item.CreatedAt = createdAt.Format(time.RFC3339)
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: item, Msg: "ok"})
}

func (s *appState) updateOutlookGroup(c *gin.Context) {
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

	if err = normalizeGroupSortOrdersTx(c.Request.Context(), tx, "outlook_groups"); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "编辑分组失败"})
		return
	}

	var parentID int
	var currentSortOrder int
	var system bool
	if err = tx.QueryRowContext(c.Request.Context(), `SELECT parent_id, sort_order, system FROM outlook_groups WHERE id = $1`, id).Scan(&parentID, &currentSortOrder, &system); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "分组不存在"})
		return
	}
	if system {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "系统分组不能编辑"})
		return
	}

	var siblingCount int
	if err = tx.QueryRowContext(c.Request.Context(), `SELECT COUNT(*) FROM outlook_groups WHERE parent_id = $1 AND system = FALSE`, parentID).Scan(&siblingCount); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "编辑分组失败"})
		return
	}
	sortOrder := normalizeRequestedGroupSortOrder(req.SortOrder, siblingCount, currentSortOrder)
	if sortOrder < currentSortOrder {
		if _, err = tx.ExecContext(c.Request.Context(), `UPDATE outlook_groups SET sort_order = sort_order + 1 WHERE parent_id = $1 AND system = FALSE AND id <> $2 AND sort_order >= $3 AND sort_order < $4`, parentID, id, sortOrder, currentSortOrder); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "编辑分组失败"})
			return
		}
	} else if sortOrder > currentSortOrder {
		if _, err = tx.ExecContext(c.Request.Context(), `UPDATE outlook_groups SET sort_order = sort_order - 1 WHERE parent_id = $1 AND system = FALSE AND id <> $2 AND sort_order <= $3 AND sort_order > $4`, parentID, id, sortOrder, currentSortOrder); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "编辑分组失败"})
			return
		}
	}

	var item outlookGroupResponse
	var createdAt time.Time
	err = tx.QueryRowContext(c.Request.Context(), `
UPDATE outlook_groups
SET name = $2, sort_order = $3, updated_at = NOW()
WHERE id = $1
RETURNING id, parent_id, name, system, sort_order, created_at
`, id, req.Name, sortOrder).Scan(&item.ID, &item.ParentID, &item.Name, &item.System, &item.SortOrder, &createdAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "分组名称已存在或编辑失败"})
		return
	}
	if err = tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "编辑分组失败"})
		return
	}
	item.CreatedAt = createdAt.Format(time.RFC3339)
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: item, Msg: "ok"})
}

func (s *appState) deleteOutlookGroup(c *gin.Context) {
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
	if outlookGroupIsSystem(c.Request.Context(), db, id) {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "系统分组不能删除"})
		return
	}
	var parentID int
	var sortOrder int
	if err := db.QueryRowContext(c.Request.Context(), `SELECT parent_id, sort_order FROM outlook_groups WHERE id = $1`, id).Scan(&parentID, &sortOrder); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "分组不存在"})
		return
	}
	if outlookGroupHasChildren(c.Request.Context(), db, id) {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "该分组下有子分组，不能删除"})
		return
	}
	var accountCount int
	if err := db.QueryRowContext(c.Request.Context(), `SELECT COUNT(*) FROM outlook_accounts WHERE group_id = $1`, id).Scan(&accountCount); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除分组失败"})
		return
	}
	if accountCount > 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "该分组下有微软邮箱，不能删除"})
		return
	}
	result, err := db.ExecContext(c.Request.Context(), `DELETE FROM outlook_groups WHERE id = $1`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除分组失败"})
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "分组不存在"})
		return
	}
	if _, err := db.ExecContext(c.Request.Context(), `UPDATE outlook_groups SET sort_order = sort_order - 1 WHERE parent_id = $1 AND system = FALSE AND sort_order > $2`, parentID, sortOrder); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除分组失败"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
}

func (s *appState) listOutlookAccounts(c *gin.Context) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "获取微软邮箱账号失败"})
		return
	}
	defer db.Close()

	search := strings.TrimSpace(c.Query("search"))
	groupID, _ := strconv.Atoi(c.Query("group_id"))
	page, pageSize, offset := parseListPage(c, 20, 500)
	where := []string{"1 = 1"}
	args := []interface{}{}
	if groupID > 0 {
		where = append(where, accountGroupTreeWhere("a", "outlook_groups", len(args)+1))
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
FROM outlook_accounts a
WHERE `+whereSQL, args...).Scan(&total, &normal); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "鑾峰彇寰蒋閭璐﹀彿澶辫触"})
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
	case "client":
		orderClause = "a.client_id " + sortOrder + ", a.id " + sortOrder
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
SELECT a.id, a.group_id, COALESCE(g.name, ''), a.email, a.client_id, a.refresh_token, a.remark,
       a.status, a.status_reason, a.last_token_refresh_at, a.created_at
FROM outlook_accounts a
LEFT JOIN outlook_groups g ON g.id = a.group_id
WHERE `+whereSQL+`
ORDER BY `+orderClause+`
LIMIT $`+strconv.Itoa(limitIndex)+` OFFSET $`+strconv.Itoa(offsetIndex)+`
`, queryArgs...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "获取微软邮箱账号失败"})
		return
	}
	defer rows.Close()

	items := []outlookAccountResponse{}
	for rows.Next() {
		item, err := scanOutlookAccountResponse(rows)
		if err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "获取微软邮箱账号失败"})
			return
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: outlookAccountListResponse{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Pages:    calculatePages(total, pageSize),
		Normal:   normal,
		Error:    total - normal,
	}, Msg: "ok"})
}

func (s *appState) createOutlookAccount(c *gin.Context) {
	var req saveOutlookAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	item, err := createOutlookAccountRecord(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: item, Msg: "ok"})
}

func (s *appState) batchCreateOutlookAccounts(c *gin.Context) {
	var req batchOutlookAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	lines := strings.Split(req.Content, "\n")
	items := []outlookAccountResponse{}
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "----")
		if len(parts) < 4 {
			c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "批量格式错误，请使用 邮箱----密码----client_id----refresh_token"})
			return
		}
		item, err := createOutlookAccountRecord(c.Request.Context(), saveOutlookAccountRequest{
			Email:        strings.TrimSpace(parts[0]),
			Password:     strings.TrimSpace(parts[1]),
			ClientID:     strings.TrimSpace(parts[2]),
			RefreshToken: strings.TrimSpace(strings.Join(parts[3:], "----")),
			GroupID:      req.GroupID,
		})
		if err != nil {
			c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
			return
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请输入批量账号内容"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: items, Msg: "ok"})
}

func (s *appState) updateOutlookAccount(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req saveOutlookAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	item, err := updateOutlookAccountRecord(c.Request.Context(), id, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: item, Msg: "ok"})
}

func (s *appState) deleteOutlookAccount(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除微软邮箱失败"})
		return
	}
	defer db.Close()
	rowsAffected, err := deleteAccountRowsAndUnbindCardKeys(c.Request.Context(), db, "outlook_accounts", []int{id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除微软邮箱失败"})
		return
	}
	if rowsAffected == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "微软邮箱账号不存在"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
}

func (s *appState) batchOutlookAccountAction(c *gin.Context) {
	var req outlookBatchActionRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请选择账号"})
		return
	}
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "批量操作失败"})
		return
	}
	defer db.Close()
	_, args := intPlaceholders(req.IDs, 1)
	if len(args) == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请选择账号"})
		return
	}

	switch req.Action {
	case "delete":
		_, err = deleteAccountRowsAndUnbindCardKeys(c.Request.Context(), db, "outlook_accounts", req.IDs)
	case "move":
		if req.GroupID <= 0 || !outlookGroupExists(c.Request.Context(), db, req.GroupID) || outlookGroupHasChildren(c.Request.Context(), db, req.GroupID) {
			c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "目标分组不可用"})
			return
		}
		if duplicateEmail, err := outlookMoveDuplicateEmail(c.Request.Context(), db, req.GroupID, req.IDs); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "批量操作失败"})
			return
		} else if duplicateEmail != "" {
			c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "目标分组下已存在邮箱：" + duplicateEmail})
			return
		}
		validIDCount := len(args)
		args = append([]interface{}{req.GroupID}, args...)
		_, err = db.ExecContext(c.Request.Context(), `UPDATE outlook_accounts SET group_id = $1, updated_at = NOW() WHERE id IN (`+shiftedPlaceholders(validIDCount, 2)+`)`, args...)
	default:
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "未知批量操作"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "批量操作失败"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
}

func (s *appState) batchOutlookAccountActionV2(c *gin.Context) {
	var req outlookBatchActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "批量操作失败"})
		return
	}
	defer db.Close()
	ids, err := resolveAccountIDs(c.Request.Context(), db, "outlook_accounts", req.IDs, req.Filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "批量操作失败"})
		return
	}
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请选择账号"})
		return
	}

	switch req.Action {
	case "delete", "test":
		task := s.tasks.create("outlook_"+req.Action, len(ids), "running")
		go s.runOutlookAccountBatchTask(task.ID, req, ids)
		c.JSON(http.StatusOK, apiResponse{Code: 0, Data: task, Msg: "ok"})
		return
	case "move":
		if req.GroupID <= 0 || !outlookGroupExists(c.Request.Context(), db, req.GroupID) || outlookGroupHasChildren(c.Request.Context(), db, req.GroupID) {
			c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "目标分组不可用"})
			return
		}
		if duplicateEmail, err := outlookMoveDuplicateEmail(c.Request.Context(), db, req.GroupID, ids); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "批量操作失败"})
			return
		} else if duplicateEmail != "" {
			c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "目标分组下已存在邮箱：" + duplicateEmail})
			return
		}
		placeholders, args := intPlaceholders(ids, 2)
		if len(args) == 0 {
			c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请选择账号"})
			return
		}
		args = append([]interface{}{req.GroupID}, args...)
		if _, err := db.ExecContext(c.Request.Context(), `UPDATE outlook_accounts SET group_id = $1, updated_at = NOW() WHERE id IN (`+placeholders+`)`, args...); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "批量操作失败"})
			return
		}
		c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
	default:
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "未知批量操作"})
	}
}

func (s *appState) runOutlookAccountBatchTask(taskID string, req outlookBatchActionRequest, ids []int) {
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
		if err := s.deleteAccountsInBatches(ctx, db, taskID, "outlook_accounts", ids); err != nil {
			s.tasks.fail(taskID, err)
			return
		}
		s.tasks.finish(taskID)
		return
	}

	workerCount := 3
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
					err = runOutlookAccountConnectivityTest(ctx, db, id)
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

func (s *appState) exportOutlookDataZip(c *gin.Context) {
	var req outlookDataExportRequest
	_ = c.ShouldBindJSON(&req)
	req.Password = strings.TrimSpace(req.Password)
	if req.Password == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请输入导出密码"})
		return
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "导出微软邮箱数据失败"})
		return
	}
	defer db.Close()

	filename := fmt.Sprintf("outlook-data-%s.zip", time.Now().Format("20060102-150405"))
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if err := writeEncryptedOutlookDataZip(c.Request.Context(), c.Writer, db, accountExportSelector{IDs: req.IDs, Filter: req.Filter}, req.Password); err != nil {
		return
	}
}

func (s *appState) importOutlookDataZip(c *gin.Context) {
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
	payload, err := readEncryptedOutlookDataZip(file, password)
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
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "导入微软邮箱数据失败"})
		return
	}
	defer db.Close()
	tx, err := db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "导入微软邮箱数据失败"})
		return
	}
	defer tx.Rollback()

	groupIDMap, groupCount, err := importOutlookGroups(c.Request.Context(), tx, payload.Groups, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	accountCount, err := importOutlookAccounts(c.Request.Context(), tx, payload.Accounts, groupIDMap, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	if _, err := tx.ExecContext(c.Request.Context(), `SELECT setval(pg_get_serial_sequence('outlook_groups', 'id'), GREATEST(COALESCE((SELECT MAX(id) FROM outlook_groups), 2), 2), true)`); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "导入分组失败"})
		return
	}
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "导入微软邮箱数据失败"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: mailDataImportResult{Groups: groupCount, Accounts: accountCount}, Msg: "ok"})
}

func (s *appState) createOutlookDataExportTask(c *gin.Context) {
	var req outlookDataExportRequest
	_ = c.ShouldBindJSON(&req)
	req.Password = strings.TrimSpace(req.Password)
	if req.Password == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请输入导出密码"})
		return
	}
	task := s.tasks.create("outlook_export", 0, "导出任务已创建")
	go s.runOutlookDataExportTask(task.ID, req)
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: task, Msg: "ok"})
}

func (s *appState) runOutlookDataExportTask(taskID string, req outlookDataExportRequest) {
	ctx := context.Background()
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	defer db.Close()

	total, err := countAccountExportRows(ctx, db, "outlook_accounts", accountExportSelector{IDs: req.IDs, Filter: req.Filter})
	if err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	s.tasks.update(taskID, func(task *backgroundTask) {
		task.Total = total
		task.Message = "正在生成导出 ZIP"
	})

	path, err := taskZipPath(taskID, "outlook-data-export")
	if err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	file, err := os.Create(path)
	if err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	writeErr := writeEncryptedOutlookDataZip(ctx, file, db, accountExportSelector{IDs: req.IDs, Filter: req.Filter}, req.Password)
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

	filename := fmt.Sprintf("outlook-data-%s.zip", time.Now().Format("20060102-150405"))
	s.tasks.update(taskID, func(task *backgroundTask) {
		task.Done = total
		task.Success = total
		task.Message = "导出完成"
	})
	s.tasks.setResult(taskID, path, filename, "导出完成")
	s.tasks.finish(taskID)
}

func (s *appState) createOutlookDataImportTask(c *gin.Context) {
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

	task := s.tasks.create("outlook_import", 0, "导入任务已创建")
	path, err := saveUploadedTaskZip(file, task.ID, "outlook-data-import")
	if err != nil {
		s.tasks.fail(task.ID, err)
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "保存上传文件失败"})
		return
	}
	go s.runOutlookDataImportTask(task.ID, path, password)
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: task, Msg: "ok"})
}

func (s *appState) runOutlookDataImportTask(taskID string, path string, password string) {
	ctx := context.Background()
	defer os.Remove(path)

	payload, err := readEncryptedOutlookDataZipPath(path, password)
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
		task.Message = "正在导入微软邮箱数据"
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

	groupIDMap, groupCount, err := importOutlookGroups(ctx, tx, payload.Groups, func() {
		reportProgress("正在导入微软邮箱分组")
	})
	if err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	accountCount, err := importOutlookAccounts(ctx, tx, payload.Accounts, groupIDMap, func() {
		reportProgress("正在导入微软邮箱账号")
	})
	if err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	if _, err := tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('outlook_groups', 'id'), GREATEST(COALESCE((SELECT MAX(id) FROM outlook_groups), 2), 2), true)`); err != nil {
		s.tasks.fail(taskID, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.tasks.fail(taskID, err)
		return
	}

	message := fmt.Sprintf("导入完成：%d 个微软邮箱，%d 个分组", accountCount, groupCount)
	s.tasks.update(taskID, func(task *backgroundTask) {
		task.Done = total
		task.Success = total
		task.Message = message
	})
	s.tasks.finish(taskID)
}

func (s *appState) testOutlookAccount(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	db, account, ok := loadOutlookAccountForRequest(c, id)
	if !ok {
		return
	}
	defer db.Close()
	token, err := refreshOutlookAccessToken(c.Request.Context(), db, account)
	if err != nil || token == "" {
		updateOutlookAccountStatus(c.Request.Context(), db, id, "error", errorText(err, "Graph API 授权失败"))
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "Graph API 连接失败: " + errorText(err, "授权失败")})
		return
	}
	updateOutlookAccountStatus(c.Request.Context(), db, id, "normal", "")
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: gin.H{"message": "Graph API 连接正常"}, Msg: "ok"})
}

func (s *appState) listOutlookMessages(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	folder := normalizeOutlookFolder(c.DefaultQuery("folder", "inbox"))
	top := parsePositiveInt(c.DefaultQuery("top", "20"), 20)
	if top > 100 {
		top = 100
	}
	skip := parsePositiveInt(c.DefaultQuery("skip", "0"), 0)
	keyword := strings.TrimSpace(c.Query("keyword"))

	db, account, ok := loadOutlookAccountForRequest(c, id)
	if !ok {
		return
	}
	defer db.Close()
	if strings.EqualFold(account.Status, "disabled") {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "该账号已停用"})
		return
	}
	token, err := refreshOutlookAccessToken(c.Request.Context(), db, account)
	if err != nil {
		updateOutlookAccountStatus(c.Request.Context(), db, id, "error", err.Error())
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "Graph API 授权失败: " + err.Error()})
		return
	}
	items, err := fetchOutlookMessages(c.Request.Context(), token, folder, top, skip, keyword)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "获取邮件失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: outlookMessageListResponse{Items: items, Total: len(items)}, Msg: "ok"})
}

func (s *appState) getOutlookMessageDetails(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req outlookMessageDetailsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	ids := make([]string, 0, len(req.IDs))
	seen := map[string]bool{}
	for _, messageID := range req.IDs {
		messageID = strings.TrimSpace(messageID)
		if messageID != "" && !seen[messageID] {
			seen[messageID] = true
			ids = append(ids, messageID)
		}
	}
	if len(ids) == 0 {
		c.JSON(http.StatusOK, apiResponse{Code: 0, Data: []outlookMessageResponse{}, Msg: "ok"})
		return
	}

	db, account, ok := loadOutlookAccountForRequest(c, id)
	if !ok {
		return
	}
	defer db.Close()
	token, err := refreshOutlookAccessToken(c.Request.Context(), db, account)
	if err != nil {
		updateOutlookAccountStatus(c.Request.Context(), db, id, "error", err.Error())
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "Graph API 授权失败: " + err.Error()})
		return
	}
	items, err := fetchOutlookMessageDetails(c.Request.Context(), token, ids)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "读取邮件详情失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: items, Msg: "ok"})
}

func (s *appState) getOutlookMessageDetail(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	messageID := strings.TrimSpace(c.Param("messageID"))
	if messageID == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "邮件 ID 不能为空"})
		return
	}
	db, account, ok := loadOutlookAccountForRequest(c, id)
	if !ok {
		return
	}
	defer db.Close()
	token, err := refreshOutlookAccessToken(c.Request.Context(), db, account)
	if err != nil {
		updateOutlookAccountStatus(c.Request.Context(), db, id, "error", err.Error())
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "Graph API 授权失败: " + err.Error()})
		return
	}
	item, err := fetchOutlookMessageDetail(c.Request.Context(), token, messageID)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "读取邮件详情失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: item, Msg: "ok"})
}

func (s *appState) outlookOAuthAuthorize(c *gin.Context) {
	clientID := strings.TrimSpace(c.Query("client_id"))
	if clientID == "" {
		clientID = defaultOutlookClientID
	}
	oauthState := newToken()
	s.outlookOAuth.start(oauthState, clientID)
	redirectURI := outlookCallbackURL(c.Request)
	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("response_type", "code")
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", "Mail.ReadWrite offline_access")
	params.Set("response_mode", "query")
	params.Set("state", oauthState)
	loginHint := strings.TrimSpace(c.Query("login_hint"))
	if loginHint != "" {
		params.Set("login_hint", loginHint)
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: gin.H{"url": microsoftAuthURL + "?" + params.Encode(), "client_id": clientID, "redirect_uri": redirectURI, "state": oauthState}, Msg: "ok"})
}

func (s *appState) outlookOAuthExchange(c *gin.Context) {
	var req struct {
		Code        string `json:"code"`
		ClientID    string `json:"client_id"`
		RedirectURI string `json:"redirect_uri"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	if req.Code == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "授权码不能为空"})
		return
	}
	clientID := strings.TrimSpace(req.ClientID)
	if clientID == "" {
		clientID = defaultOutlookClientID
	}
	redirectURI := strings.TrimSpace(req.RedirectURI)
	if redirectURI == "" {
		redirectURI = "https://localhost"
	}
	token, err := exchangeOutlookAuthCode(c.Request.Context(), clientID, req.Code, redirectURI)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "换取 token 失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: gin.H{"client_id": clientID, "refresh_token": token.RefreshToken}, Msg: "ok"})
}

func (s *appState) outlookOAuthCallback(c *gin.Context) {
	if errMsg := c.Query("error"); errMsg != "" {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(outlookCallbackPage(false, "授权失败: "+valueOrDefault(c.Query("error_description"), errMsg), nil)))
		return
	}
	code := strings.TrimSpace(c.Query("code"))
	if code == "" {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(outlookCallbackPage(false, "未收到授权码", nil)))
		return
	}
	oauthState := strings.TrimSpace(c.Query("state"))
	clientID := s.outlookOAuth.clientID(oauthState)
	if clientID == "" {
		clientID = oauthState
	}
	if clientID == "" {
		clientID = defaultOutlookClientID
	}
	token, err := exchangeOutlookAuthCode(c.Request.Context(), clientID, code, outlookCallbackURL(c.Request))
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(outlookCallbackPage(false, "换取 token 失败: "+err.Error(), nil)))
		return
	}
	if oauthState != "" {
		s.outlookOAuth.complete(oauthState, token.RefreshToken)
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(outlookCallbackPage(true, "", gin.H{"client_id": clientID, "refresh_token": token.RefreshToken})))
}

func (s *appState) outlookOAuthResult(c *gin.Context) {
	oauthState := strings.TrimSpace(c.Query("state"))
	if oauthState == "" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "OAuth state 不能为空"})
		return
	}
	session, status := s.outlookOAuth.poll(oauthState)
	data := gin.H{"status": status}
	if status == "success" {
		data["client_id"] = session.ClientID
		data["refresh_token"] = session.RefreshToken
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: data, Msg: "ok"})
}

func createOutlookAccountRecord(ctx context.Context, req saveOutlookAccountRequest) (outlookAccountResponse, error) {
	var item outlookAccountResponse
	req.Email = strings.TrimSpace(req.Email)
	req.Password = strings.TrimSpace(req.Password)
	req.ClientID = strings.TrimSpace(req.ClientID)
	req.RefreshToken = strings.TrimSpace(req.RefreshToken)
	req.Remark = strings.TrimSpace(req.Remark)
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		return item, fmt.Errorf("微软邮箱格式错误")
	}
	if req.ClientID == "" {
		req.ClientID = defaultOutlookClientID
	}
	if req.RefreshToken == "" {
		return item, fmt.Errorf("Refresh Token 不能为空")
	}
	if req.GroupID <= 0 {
		req.GroupID = 2
	}
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return item, fmt.Errorf("保存微软邮箱失败")
	}
	defer db.Close()
	if !outlookGroupExists(ctx, db, req.GroupID) {
		return item, fmt.Errorf("微软邮箱分组不存在")
	}
	if outlookGroupHasChildren(ctx, db, req.GroupID) {
		return item, fmt.Errorf("该分组下有子分组，不能直接添加邮箱")
	}
	var createdAt time.Time
	err = db.QueryRowContext(ctx, `
INSERT INTO outlook_accounts (group_id, email, password, client_id, refresh_token, remark, status)
VALUES ($1, $2, $3, $4, $5, $6, 'active')
RETURNING id, created_at
`, req.GroupID, req.Email, req.Password, req.ClientID, req.RefreshToken, req.Remark).Scan(&item.ID, &createdAt)
	if err != nil {
		return item, fmt.Errorf("微软邮箱已存在或保存失败")
	}
	item.GroupID = req.GroupID
	item.Email = req.Email
	item.ClientID = req.ClientID
	item.RefreshTokenMasked = maskToken(req.RefreshToken)
	item.Remark = req.Remark
	item.Status = "active"
	item.CreatedAt = createdAt.Format("2006/01/02 15:04:05")
	_ = db.QueryRowContext(ctx, `SELECT name FROM outlook_groups WHERE id = $1`, req.GroupID).Scan(&item.GroupName)
	return item, nil
}

func updateOutlookAccountRecord(ctx context.Context, id int, req saveOutlookAccountRequest) (outlookAccountResponse, error) {
	var item outlookAccountResponse
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return item, fmt.Errorf("保存微软邮箱失败")
	}
	defer db.Close()
	existing, err := loadOutlookAccount(ctx, db, id)
	if err != nil {
		return item, fmt.Errorf("微软邮箱账号不存在")
	}
	req.Email = strings.TrimSpace(valueOrDefault(req.Email, existing.Email))
	req.Password = strings.TrimSpace(req.Password)
	req.ClientID = strings.TrimSpace(valueOrDefault(req.ClientID, existing.ClientID))
	req.RefreshToken = strings.TrimSpace(req.RefreshToken)
	req.Remark = strings.TrimSpace(req.Remark)
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		return item, fmt.Errorf("微软邮箱格式错误")
	}
	if req.GroupID <= 0 {
		req.GroupID = existing.GroupID
	}
	if !outlookGroupExists(ctx, db, req.GroupID) {
		return item, fmt.Errorf("微软邮箱分组不存在")
	}
	if outlookGroupHasChildren(ctx, db, req.GroupID) {
		return item, fmt.Errorf("该分组下有子分组，不能直接添加邮箱")
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = existing.Status
	}

	query := `
UPDATE outlook_accounts
SET group_id = $2, email = $3, client_id = $4, remark = $5, status = $6, status_reason = '', updated_at = NOW()
`
	args := []interface{}{id, req.GroupID, req.Email, req.ClientID, req.Remark, status}
	nextIndex := 7
	if req.Password != "" {
		query += fmt.Sprintf(", password = $%d", nextIndex)
		args = append(args, req.Password)
		nextIndex++
	}
	if req.RefreshToken != "" {
		query += fmt.Sprintf(", refresh_token = $%d", nextIndex)
		args = append(args, req.RefreshToken)
	}
	query += ` WHERE id = $1`
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return item, fmt.Errorf("微软邮箱已存在或保存失败")
	}
	item, err = queryOutlookAccountResponse(ctx, db, id)
	if err != nil {
		return item, fmt.Errorf("读取微软邮箱失败")
	}
	return item, nil
}

func loadOutlookAccountForRequest(c *gin.Context, id int) (*sql.DB, outlookStoredAccount, bool) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取微软邮箱失败"})
		return nil, outlookStoredAccount{}, false
	}
	account, err := loadOutlookAccount(c.Request.Context(), db, id)
	if err != nil {
		db.Close()
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "微软邮箱账号不存在"})
		return nil, outlookStoredAccount{}, false
	}
	return db, account, true
}

func loadOutlookAccount(ctx context.Context, db *sql.DB, id int) (outlookStoredAccount, error) {
	var item outlookStoredAccount
	err := db.QueryRowContext(ctx, `
SELECT id, email, password, client_id, refresh_token, group_id, remark, status
FROM outlook_accounts
WHERE id = $1
`, id).Scan(&item.ID, &item.Email, &item.Password, &item.ClientID, &item.RefreshToken, &item.GroupID, &item.Remark, &item.Status)
	return item, err
}

func queryOutlookAccountResponse(ctx context.Context, db *sql.DB, id int) (outlookAccountResponse, error) {
	row := db.QueryRowContext(ctx, `
SELECT a.id, a.group_id, COALESCE(g.name, ''), a.email, a.client_id, a.refresh_token, a.remark,
       a.status, a.status_reason, a.last_token_refresh_at, a.created_at
FROM outlook_accounts a
LEFT JOIN outlook_groups g ON g.id = a.group_id
WHERE a.id = $1
`, id)
	return scanOutlookAccountResponse(row)
}

type sqlScanner interface {
	Scan(dest ...interface{}) error
}

func scanOutlookAccountResponse(scanner sqlScanner) (outlookAccountResponse, error) {
	var item outlookAccountResponse
	var createdAt time.Time
	var lastRefresh sql.NullTime
	var refreshToken string
	if err := scanner.Scan(&item.ID, &item.GroupID, &item.GroupName, &item.Email, &item.ClientID, &refreshToken, &item.Remark, &item.Status, &item.StatusReason, &lastRefresh, &createdAt); err != nil {
		return item, err
	}
	item.RefreshTokenMasked = maskToken(refreshToken)
	if lastRefresh.Valid {
		item.LastTokenRefreshAt = lastRefresh.Time.Format("2006/01/02 15:04:05")
	}
	item.CreatedAt = createdAt.Format("2006/01/02 15:04:05")
	return item, nil
}

func refreshOutlookAccessToken(ctx context.Context, db *sql.DB, account outlookStoredAccount) (string, error) {
	form := url.Values{}
	form.Set("client_id", account.ClientID)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", account.RefreshToken)
	form.Set("scope", "https://graph.microsoft.com/.default")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, microsoftTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := doOutlookHTTPRequest(ctx, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var token outlookTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || token.AccessToken == "" {
		msg := token.ErrorDesc
		if msg == "" {
			msg = token.Error
		}
		if msg == "" {
			msg = fmt.Sprintf("token request failed: %d", resp.StatusCode)
		}
		return "", fmt.Errorf("%s", maskLongTokenText(msg))
	}
	if token.RefreshToken != "" && token.RefreshToken != account.RefreshToken {
		_, _ = db.ExecContext(ctx, `UPDATE outlook_accounts SET refresh_token = $2, status = 'active', status_reason = '', last_token_refresh_at = NOW(), updated_at = NOW() WHERE id = $1`, account.ID, token.RefreshToken)
	} else {
		_, _ = db.ExecContext(ctx, `UPDATE outlook_accounts SET status = 'active', status_reason = '', last_token_refresh_at = NOW(), updated_at = NOW() WHERE id = $1`, account.ID)
	}
	return token.AccessToken, nil
}

func exchangeOutlookAuthCode(ctx context.Context, clientID string, code string, redirectURI string) (outlookTokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("scope", "Mail.ReadWrite offline_access")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, microsoftTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return outlookTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := doOutlookHTTPRequest(ctx, req)
	if err != nil {
		return outlookTokenResponse{}, err
	}
	defer resp.Body.Close()
	var token outlookTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return token, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || token.RefreshToken == "" {
		msg := token.ErrorDesc
		if msg == "" {
			msg = token.Error
		}
		if msg == "" {
			msg = fmt.Sprintf("token exchange failed: %d", resp.StatusCode)
		}
		return token, fmt.Errorf("%s", maskLongTokenText(msg))
	}
	return token, nil
}

func fetchOutlookMessages(ctx context.Context, accessToken string, folder string, top int, skip int, keyword string) ([]outlookMessageResponse, error) {
	if folder == "all" {
		inbox, inboxErr := fetchOutlookMessages(ctx, accessToken, "inbox", top, 0, keyword)
		junk, junkErr := fetchOutlookMessages(ctx, accessToken, "junkemail", top, 0, keyword)
		if inboxErr != nil && junkErr != nil {
			return nil, inboxErr
		}
		items := append(inbox, junk...)
		sortOutlookMessages(items)
		if len(items) > top {
			items = items[:top]
		}
		return items, nil
	}

	u, _ := url.Parse(graphAPIBaseURL + "/me/mailFolders/" + url.PathEscape(folder) + "/messages")
	query := u.Query()
	query.Set("$top", strconv.Itoa(top))
	query.Set("$orderby", "receivedDateTime desc")
	query.Set("$select", "id,subject,from,toRecipients,receivedDateTime,bodyPreview,isRead,hasAttachments")
	if keyword != "" {
		query.Set("$search", `"`+keyword+`"`)
	} else {
		query.Set("$skip", strconv.Itoa(skip))
	}
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Prefer", `outlook.body-content-type="text"`)
	if keyword != "" {
		req.Header.Set("ConsistencyLevel", "eventual")
	}
	resp, err := doOutlookHTTPRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, graphHTTPError(resp)
	}
	var payload struct {
		Value []outlookGraphMessage `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	items := make([]outlookMessageResponse, 0, len(payload.Value))
	for _, message := range payload.Value {
		items = append(items, outlookMessageFromGraph(message, folder))
	}
	return items, nil
}

func fetchOutlookMessageDetails(ctx context.Context, accessToken string, messageIDs []string) ([]outlookMessageResponse, error) {
	items := make([]outlookMessageResponse, 0, len(messageIDs))
	for start := 0; start < len(messageIDs); start += 20 {
		end := start + 20
		if end > len(messageIDs) {
			end = len(messageIDs)
		}
		chunk, err := fetchOutlookMessageDetailsBatch(ctx, accessToken, messageIDs[start:end])
		if err != nil {
			return nil, err
		}
		items = append(items, chunk...)
	}
	return items, nil
}

func fetchOutlookMessageDetailsBatch(ctx context.Context, accessToken string, messageIDs []string) ([]outlookMessageResponse, error) {
	requests := make([]map[string]interface{}, 0, len(messageIDs))
	for index, messageID := range messageIDs {
		query := url.Values{}
		query.Set("$select", "id,subject,from,toRecipients,ccRecipients,receivedDateTime,body,bodyPreview,isRead,hasAttachments")
		requests = append(requests, map[string]interface{}{
			"id":      strconv.Itoa(index),
			"method":  http.MethodGet,
			"url":     "/me/messages/" + url.PathEscape(messageID) + "?" + query.Encode(),
			"headers": map[string]string{"Prefer": `outlook.body-content-type="html"`},
		})
	}
	body, err := json.Marshal(map[string]interface{}{"requests": requests})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphAPIBaseURL+"/$batch", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", `outlook.body-content-type="html"`)
	resp, err := doOutlookHTTPRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, graphHTTPError(resp)
	}
	var payload struct {
		Responses []struct {
			ID     string              `json:"id"`
			Status int                 `json:"status"`
			Body   outlookGraphMessage `json:"body"`
		} `json:"responses"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	items := make([]outlookMessageResponse, 0, len(payload.Responses))
	for _, response := range payload.Responses {
		if response.Status < 200 || response.Status >= 300 || response.Body.ID == "" {
			continue
		}
		item := outlookMessageFromGraph(response.Body, "")
		if strings.EqualFold(response.Body.Body.ContentType, "html") {
			item.HTML = response.Body.Body.Content
		} else {
			item.Body = response.Body.Body.Content
		}
		items = append(items, item)
	}
	return items, nil
}

func fetchOutlookMessageDetail(ctx context.Context, accessToken string, messageID string) (outlookMessageResponse, error) {
	u, _ := url.Parse(graphAPIBaseURL + "/me/messages/" + url.PathEscape(messageID))
	query := u.Query()
	query.Set("$select", "id,subject,from,toRecipients,ccRecipients,receivedDateTime,body,bodyPreview,isRead,hasAttachments")
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return outlookMessageResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Prefer", `outlook.body-content-type="html"`)
	resp, err := doOutlookHTTPRequest(ctx, req)
	if err != nil {
		return outlookMessageResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return outlookMessageResponse{}, graphHTTPError(resp)
	}
	var message outlookGraphMessage
	if err := json.NewDecoder(resp.Body).Decode(&message); err != nil {
		return outlookMessageResponse{}, err
	}
	item := outlookMessageFromGraph(message, "")
	if strings.EqualFold(message.Body.ContentType, "html") {
		item.HTML = message.Body.Content
	} else {
		item.Body = message.Body.Content
	}
	return item, nil
}

func outlookMessageFromGraph(message outlookGraphMessage, folder string) outlookMessageResponse {
	parsedTime, _ := time.Parse(time.RFC3339, message.ReceivedDateTime)
	return outlookMessageResponse{
		ID:             message.ID,
		Folder:         folder,
		Subject:        valueOrDefault(message.Subject, "无标题"),
		From:           formatOutlookAddress(message.From.EmailAddress),
		To:             formatOutlookRecipients(message.ToRecipients),
		CC:             formatOutlookRecipients(message.CCRecipients),
		Time:           formatMailTime(parsedTime, message.ReceivedDateTime),
		Timestamp:      parsedTime.Unix(),
		BodyPreview:    message.BodyPreview,
		IsRead:         message.IsRead,
		HasAttachments: message.HasAttachments,
	}
}

func updateOutlookAccountStatus(ctx context.Context, db *sql.DB, id int, status string, reason string) {
	_, _ = db.ExecContext(ctx, `UPDATE outlook_accounts SET status = $2, status_reason = $3, updated_at = NOW() WHERE id = $1`, id, status, strings.TrimSpace(reason))
}

func runOutlookAccountConnectivityTest(ctx context.Context, db *sql.DB, id int) error {
	account, err := loadOutlookAccount(ctx, db, id)
	if err != nil {
		return err
	}
	token, err := refreshOutlookAccessToken(ctx, db, account)
	if err != nil || token == "" {
		text := errorText(err, "Graph API 授权失败")
		updateOutlookAccountStatus(ctx, db, id, "error", text)
		return fmt.Errorf("%s", text)
	}
	updateOutlookAccountStatus(ctx, db, id, "normal", "")
	return nil
}

func writeEncryptedOutlookDataZip(ctx context.Context, w io.Writer, db *sql.DB, selector accountExportSelector, password string) error {
	zipWriter := encryptedzip.NewWriter(w)
	defer zipWriter.Close()

	metaWriter, err := zipWriter.Encrypt("metadata.json", password)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(metaWriter).Encode(map[string]interface{}{
		"exported_at": time.Now().Format(time.RFC3339),
		"format":      "outlook-data-zip",
		"version":     1,
	}); err != nil {
		return err
	}

	groupsWriter, err := zipWriter.Encrypt("groups.json", password)
	if err != nil {
		return err
	}
	if err := writeExportOutlookGroupsJSON(ctx, db, groupsWriter); err != nil {
		return err
	}

	accountsWriter, err := zipWriter.Encrypt("accounts.json", password)
	if err != nil {
		return err
	}
	return writeExportOutlookAccountsJSON(ctx, db, accountsWriter, selector)
}

func writeExportOutlookGroupsJSON(ctx context.Context, db *sql.DB, w io.Writer) error {
	rows, err := db.QueryContext(ctx, `
SELECT id, parent_id, name, system, sort_order, created_at
FROM outlook_groups
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
		var item outlookDataGroup
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

func writeExportOutlookAccountsJSON(ctx context.Context, db *sql.DB, w io.Writer, selector accountExportSelector) error {
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
SELECT id, group_id, email, password, client_id, refresh_token, remark, status, status_reason, last_token_refresh_at, created_at
FROM outlook_accounts
WHERE id IN (` + placeholders + `)
ORDER BY id ASC`
			if err := writeOutlookAccountRowsJSON(ctx, db, w, encoder, query, args, &first); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "]")
		return err
	}

	whereSQL, args, err := buildAccountFilterWhere("", "outlook_accounts", selector.Filter, 1)
	if err != nil {
		return err
	}
	query := `
SELECT id, group_id, email, password, client_id, refresh_token, remark, status, status_reason, last_token_refresh_at, created_at
FROM outlook_accounts
WHERE ` + whereSQL + `
ORDER BY id ASC`
	if err := writeOutlookAccountRowsJSON(ctx, db, w, encoder, query, args, &first); err != nil {
		return err
	}
	_, err = io.WriteString(w, "]")
	return err
}

func writeOutlookAccountRowsJSON(ctx context.Context, db *sql.DB, w io.Writer, encoder *json.Encoder, query string, args []interface{}, first *bool) error {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var item outlookDataAccount
		var lastRefresh sql.NullTime
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.GroupID, &item.Email, &item.Password, &item.ClientID, &item.RefreshToken, &item.Remark, &item.Status, &item.StatusReason, &lastRefresh, &createdAt); err != nil {
			return err
		}
		if lastRefresh.Valid {
			item.LastTokenRefreshAt = lastRefresh.Time.Format(time.RFC3339)
		}
		item.Status = outlookExportStatusText(item.Status)
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

func readEncryptedOutlookDataZip(fileHeader *multipart.FileHeader, password string) (outlookDataPayload, error) {
	var payload outlookDataPayload
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
				return payload, fmt.Errorf("微软邮箱账号数据格式错误")
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

func readEncryptedOutlookDataZipPath(path string, password string) (outlookDataPayload, error) {
	var payload outlookDataPayload
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
				return payload, fmt.Errorf("微软邮箱账号数据格式错误")
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

func importOutlookGroups(ctx context.Context, tx *sql.Tx, groups []outlookDataGroup, onProgress func()) (map[int]int, int, error) {
	groupIDMap := map[int]int{}
	emptyTarget, err := outlookImportTargetIsEmpty(ctx, tx)
	if err != nil {
		return groupIDMap, 0, err
	}
	if emptyTarget {
		if err := resetOutlookImportSequences(ctx, tx); err != nil {
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
			groupIDMap[group.ID] = defaultOutlookSystemGroupID(group.Name)
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
			if !outlookGroupExistsTx(ctx, tx, parentID) {
				return groupIDMap, count, fmt.Errorf("导入分组 %s 失败：父级分组不存在", group.Name)
			}
			if outlookGroupParentIDTx(ctx, tx, parentID) > 0 {
				return groupIDMap, count, fmt.Errorf("导入分组 %s 失败：子分组下不能继续添加子分组", group.Name)
			}
			if outlookGroupHasAccountsTx(ctx, tx, parentID) {
				return groupIDMap, count, fmt.Errorf("导入分组 %s 失败：父级分组下已有邮箱，不能继续添加子分组", group.Name)
			}
		}

		actualID, err := upsertImportOutlookGroup(ctx, tx, group, parentID)
		if err != nil {
			return groupIDMap, count, err
		}
		groupIDMap[group.ID] = actualID
		count++
		if onProgress != nil {
			onProgress()
		}
	}
	if err := normalizeGroupSortOrdersTx(ctx, tx, "outlook_groups"); err != nil {
		return groupIDMap, count, err
	}
	return groupIDMap, count, nil
}

func defaultOutlookSystemGroupID(name string) int {
	if strings.Contains(name, "全部") {
		return 1
	}
	return 2
}

func upsertImportOutlookGroup(ctx context.Context, tx *sql.Tx, group outlookDataGroup, parentID int) (int, error) {
	var actualID int
	err := tx.QueryRowContext(ctx, `SELECT id FROM outlook_groups WHERE parent_id = $1 AND name = $2`, parentID, group.Name).Scan(&actualID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE outlook_groups SET parent_id = $2, name = $3, system = $4, sort_order = $5, updated_at = NOW() WHERE id = $1`, actualID, parentID, group.Name, group.System, group.SortOrder)
		return actualID, err
	}
	if err != sql.ErrNoRows {
		return 0, err
	}

	err = tx.QueryRowContext(ctx, `
INSERT INTO outlook_groups (parent_id, name, system, sort_order)
VALUES ($1, $2, $3, $4)
RETURNING id
`, parentID, group.Name, group.System, group.SortOrder).Scan(&actualID)
	return actualID, err
}

func outlookImportTargetIsEmpty(ctx context.Context, tx *sql.Tx) (bool, error) {
	var accountCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM outlook_accounts`).Scan(&accountCount); err != nil {
		return false, err
	}
	var customGroupCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM outlook_groups WHERE system = FALSE`).Scan(&customGroupCount); err != nil {
		return false, err
	}
	return accountCount == 0 && customGroupCount == 0, nil
}

func resetOutlookImportSequences(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('outlook_accounts', 'id'), 1, false)`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('outlook_groups', 'id'), GREATEST(COALESCE((SELECT MAX(id) FROM outlook_groups), 2), 2), true)`)
	return err
}

func importOutlookAccounts(ctx context.Context, tx *sql.Tx, accounts []outlookDataAccount, groupIDMap map[int]int, onProgress func()) (int, error) {
	count := 0
	for _, account := range accounts {
		account.Email = strings.TrimSpace(account.Email)
		account.Password = strings.TrimSpace(account.Password)
		account.ClientID = strings.TrimSpace(account.ClientID)
		account.RefreshToken = strings.TrimSpace(account.RefreshToken)
		account.Remark = strings.TrimSpace(account.Remark)
		account.Status = strings.TrimSpace(account.Status)
		account.StatusReason = strings.TrimSpace(account.StatusReason)
		if account.Email == "" || !strings.Contains(account.Email, "@") {
			if onProgress != nil {
				onProgress()
			}
			continue
		}
		if account.ClientID == "" {
			account.ClientID = defaultOutlookClientID
		}
		if account.Status == "" {
			account.Status = "active"
		}
		account.Status = normalizeOutlookImportStatus(account.Status)
		groupID := account.GroupID
		if mappedGroupID, ok := groupIDMap[account.GroupID]; ok {
			groupID = mappedGroupID
		}
		if groupID <= 0 {
			groupID = 2
		}
		if !outlookGroupExistsTx(ctx, tx, groupID) {
			return count, fmt.Errorf("导入微软邮箱 %s 失败：分组不存在", account.Email)
		}
		if outlookGroupHasChildrenTx(ctx, tx, groupID) {
			return count, fmt.Errorf("导入微软邮箱 %s 失败：该分组下有子分组，不能直接添加邮箱", account.Email)
		}

		lastRefreshArg, hasLastRefresh := parseOutlookExportTimeArg(account.LastTokenRefreshAt)
		createdAtArg, hasCreatedAt := parseOutlookExportTimeArg(account.CreatedAt)
		_, err := tx.ExecContext(ctx, `
INSERT INTO outlook_accounts (group_id, email, password, client_id, refresh_token, remark, status, status_reason, last_token_refresh_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, CASE WHEN $9::boolean THEN $10::timestamptz ELSE NULL END, CASE WHEN $11::boolean THEN $12::timestamptz ELSE NOW() END)
ON CONFLICT (group_id, email) DO UPDATE SET
	password = EXCLUDED.password,
	client_id = EXCLUDED.client_id,
	refresh_token = EXCLUDED.refresh_token,
	remark = EXCLUDED.remark,
	status = EXCLUDED.status,
	status_reason = EXCLUDED.status_reason,
	last_token_refresh_at = EXCLUDED.last_token_refresh_at,
	created_at = EXCLUDED.created_at,
	updated_at = NOW()
`, groupID, account.Email, account.Password, account.ClientID, account.RefreshToken, account.Remark, account.Status, account.StatusReason, hasLastRefresh, lastRefreshArg, hasCreatedAt, createdAtArg)
		if err != nil {
			return count, fmt.Errorf("导入微软邮箱 %s 失败: %w", account.Email, err)
		}
		count++
		if onProgress != nil {
			onProgress()
		}
	}
	return count, nil
}

func outlookGroupExistsTx(ctx context.Context, tx *sql.Tx, id int) bool {
	var exists bool
	return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM outlook_groups WHERE id = $1)`, id).Scan(&exists) == nil && exists
}

func outlookGroupParentIDTx(ctx context.Context, tx *sql.Tx, id int) int {
	var parentID int
	if err := tx.QueryRowContext(ctx, `SELECT parent_id FROM outlook_groups WHERE id = $1`, id).Scan(&parentID); err != nil {
		return 0
	}
	return parentID
}

func outlookGroupHasChildrenTx(ctx context.Context, tx *sql.Tx, id int) bool {
	var exists bool
	return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM outlook_groups WHERE parent_id = $1)`, id).Scan(&exists) == nil && exists
}

func outlookGroupHasAccountsTx(ctx context.Context, tx *sql.Tx, id int) bool {
	var exists bool
	return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM outlook_accounts WHERE group_id = $1)`, id).Scan(&exists) == nil && exists
}

func outlookGroupExists(ctx context.Context, db *sql.DB, id int) bool {
	var exists bool
	return db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM outlook_groups WHERE id = $1)`, id).Scan(&exists) == nil && exists
}

func outlookGroupIsSystem(ctx context.Context, db *sql.DB, id int) bool {
	var system bool
	return db.QueryRowContext(ctx, `SELECT system FROM outlook_groups WHERE id = $1`, id).Scan(&system) == nil && system
}

func outlookGroupParentID(ctx context.Context, db *sql.DB, id int) int {
	var parentID int
	if err := db.QueryRowContext(ctx, `SELECT parent_id FROM outlook_groups WHERE id = $1`, id).Scan(&parentID); err != nil {
		return 0
	}
	return parentID
}

func outlookGroupHasChildren(ctx context.Context, db *sql.DB, id int) bool {
	var exists bool
	return db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM outlook_groups WHERE parent_id = $1)`, id).Scan(&exists) == nil && exists
}

func outlookGroupHasAccounts(ctx context.Context, db *sql.DB, id int) bool {
	var exists bool
	return db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM outlook_accounts WHERE group_id = $1)`, id).Scan(&exists) == nil && exists
}

func normalizeOutlookFolder(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "all":
		return "all"
	case "junk", "junkemail", "trash":
		return "junkemail"
	case "deleted", "deleteditems", "deleted_items":
		return "deleteditems"
	default:
		return "inbox"
	}
}

func formatOutlookAddress(address outlookGraphEmailAddress) string {
	if address.Name != "" && address.Address != "" && address.Name != address.Address {
		return fmt.Sprintf("%s <%s>", address.Name, address.Address)
	}
	return valueOrDefault(address.Address, address.Name)
}

func formatOutlookRecipients(items []outlookGraphRecipient) string {
	values := make([]string, 0, len(items))
	for _, item := range items {
		if text := formatOutlookAddress(item.EmailAddress); text != "" {
			values = append(values, text)
		}
	}
	return strings.Join(values, ", ")
}

func sortOutlookMessages(items []outlookMessageResponse) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].Timestamp > items[i].Timestamp {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

func graphHTTPError(resp *http.Response) error {
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	msg := payload.Error.Message
	if msg == "" {
		msg = fmt.Sprintf("Graph API HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("%s", maskLongTokenText(msg))
}

func intPlaceholders(ids []int, start int) (string, []interface{}) {
	parts := []string{}
	args := []interface{}{}
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("$%d", start+len(args)))
		args = append(args, id)
	}
	return strings.Join(parts, ","), args
}

func shiftedPlaceholders(count int, start int) string {
	parts := make([]string, 0, count)
	for i := 0; i < count; i++ {
		parts = append(parts, fmt.Sprintf("$%d", start+i))
	}
	return strings.Join(parts, ",")
}

func outlookMoveDuplicateEmail(ctx context.Context, db *sql.DB, groupID int, ids []int) (string, error) {
	placeholders, idArgs := intPlaceholders(ids, 2)
	if len(idArgs) == 0 {
		return "", nil
	}
	args := append([]interface{}{groupID}, idArgs...)
	query := `
WITH moving AS (
	SELECT id, email
	FROM outlook_accounts
	WHERE id IN (` + placeholders + `)
)
SELECT moving.email
FROM moving
JOIN outlook_accounts existing
	ON existing.group_id = $1
	AND existing.email = moving.email
	AND existing.id <> moving.id
LIMIT 1
`
	var email string
	err := db.QueryRowContext(ctx, query, args...).Scan(&email)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return email, err
}

func maskToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 12 {
		return strings.Repeat("*", len(value))
	}
	return value[:6] + strings.Repeat("*", 6) + value[len(value)-6:]
}

func maskLongTokenText(value string) string {
	return regexpLongToken.ReplaceAllStringFunc(value, maskToken)
}

var regexpLongToken = regexp.MustCompile(`[A-Za-z0-9._~+/=-]{40,}`)

func outlookExportStatusText(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "error" || status == "failed" || status == "fail" {
		return "错误"
	}
	return "正常"
}

func normalizeOutlookImportStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "错误", "error", "failed", "fail":
		return "error"
	default:
		return "normal"
	}
}

func parseOutlookExportTime(value string) (time.Time, bool) {
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

func parseOutlookExportTimeArg(value string) (interface{}, bool) {
	parsed, ok := parseOutlookExportTime(value)
	if !ok {
		return nil, false
	}
	return parsed, true
}

func errorText(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	return err.Error()
}

func outlookCallbackURL(req *http.Request) string {
	scheme := req.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if req.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := req.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = req.Host
	}
	return scheme + "://" + host + "/api/admin/outlook-oauth/callback"
}

func outlookCallbackPage(success bool, errText string, data gin.H) string {
	dataJSON := "null"
	if data != nil {
		bytes, _ := json.Marshal(data)
		dataJSON = string(bytes)
	}
	title := "授权失败"
	body := html.EscapeString(errText)
	className := "error"
	if success {
		title = "授权成功"
		body = "正在自动填入账号凭证，请稍候..."
		className = "success"
	}
	return `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Microsoft OAuth</title>
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#0f172a;color:#e2e8f0;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.card{width:min(460px,calc(100vw - 32px));border:1px solid #334155;border-radius:12px;background:#1e293b;padding:28px;text-align:center;box-shadow:0 18px 40px rgba(0,0,0,.28)}
.success{color:#22c55e}.error{color:#ef4444}p{color:#94a3b8;font-size:14px;line-height:1.7}
</style></head><body><div class="card"><h2 class="` + className + `">` + title + `</h2><p>` + body + `</p></div>
<script>
var result={success:` + strconv.FormatBool(success) + `,data:` + dataJSON + `,error:` + strconv.Quote(errText) + `};
function notifyOpener(){
  if(!window.opener || window.opener.closed){return false}
  try{
    window.opener.postMessage({type:'outlook-oauth-callback',success:result.success,data:result.data,error:result.error},'*');
    return true;
  }catch(e){return false}
}
var sent=notifyOpener();
var tries=0;
var timer=setInterval(function(){
  tries++;
  sent=notifyOpener()||sent;
  if(sent||tries>=3){
    clearInterval(timer);
    if(result.success){setTimeout(function(){window.close()},500)}
  }
},300);
if(result.success){setTimeout(function(){window.close()},1200)}
</script></body></html>`
}

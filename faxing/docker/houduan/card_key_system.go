package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	cardKeyStatusUnused   = "unused"
	cardKeyStatusUsed     = "used"
	cardKeyStatusDisabled = "disabled"

	cardKeyGeneratedLength      = 15
	cardKeyGenerateMaxAttempts  = 100
	cardKeyGeneratedCharSet     = "0123456789abcdefghijklmnopqrstuvwxyz"
	cardKeyBatchGenerateMaxSize = 1000
)

type cardKeyGroupResponse struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
	Count     int    `json:"count"`
	CreatedAt string `json:"created_at"`
}

type saveCardKeyGroupRequest struct {
	Name      string `json:"name"`
	SortOrder *int   `json:"sort_order"`
}

type cardKeyResponse struct {
	ID            int     `json:"id"`
	GroupID       int     `json:"group_id"`
	GroupName     string  `json:"group_name"`
	Key           string  `json:"key"`
	Amount        float64 `json:"amount"`
	Status        string  `json:"status"`
	UsedBy        string  `json:"used_by"`
	UsedAt        string  `json:"used_at"`
	UsageLimit    int     `json:"usage_limit"`
	UsedCount     int     `json:"used_count"`
	MailDays      int     `json:"mail_days"`
	MailDaysBlank bool    `json:"mail_days_blank"`
	MailKeyword   string  `json:"mail_keyword"`
	BoundEmail    string  `json:"bound_email"`
	Remark        string  `json:"remark"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

type cardKeyListResponse struct {
	Items    []cardKeyResponse `json:"items"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
	Pages    int               `json:"pages"`
	Unused   int               `json:"unused"`
	Used     int               `json:"used"`
	Disabled int               `json:"disabled"`
}

type cardKeyUseLogResponse struct {
	ID          int    `json:"id"`
	CardKey     string `json:"card_key"`
	BoundEmail  string `json:"bound_email"`
	MailSubject string `json:"mail_subject"`
	UserIP      string `json:"user_ip"`
	UsedAt      string `json:"used_at"`
}

type cardKeyUseLogListResponse struct {
	Items    []cardKeyUseLogResponse `json:"items"`
	Total    int                     `json:"total"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"page_size"`
	Pages    int                     `json:"pages"`
}

type saveCardKeyRequest struct {
	GroupID       int     `json:"group_id"`
	Key           string  `json:"key"`
	Amount        float64 `json:"amount"`
	Status        string  `json:"status"`
	UsedBy        string  `json:"used_by"`
	UsageLimit    int     `json:"usage_limit"`
	MailDays      int     `json:"mail_days"`
	MailDaysBlank bool    `json:"mail_days_blank"`
	MailKeyword   string  `json:"mail_keyword"`
	BoundEmail    string  `json:"bound_email"`
	Remark        string  `json:"remark"`
}

type batchCardKeyRequest struct {
	GroupID       int     `json:"group_id"`
	Count         int     `json:"count"`
	Content       string  `json:"content"`
	Amount        float64 `json:"amount"`
	Status        string  `json:"status"`
	UsageLimit    int     `json:"usage_limit"`
	MailDays      int     `json:"mail_days"`
	MailDaysBlank bool    `json:"mail_days_blank"`
	MailKeyword   string  `json:"mail_keyword"`
	BoundEmail    string  `json:"bound_email"`
	Remark        string  `json:"remark"`
}

type cardKeyListFilter struct {
	GroupID int    `json:"group_id"`
	Search  string `json:"search"`
	Status  string `json:"status"`
}

type cardKeyBatchActionRequest struct {
	Action string            `json:"action"`
	IDs    []int             `json:"ids"`
	Filter cardKeyListFilter `json:"filter"`
	Status string            `json:"status"`
}

func ensureCardKeySystemTables(ctx context.Context) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS card_key_groups (
	id SERIAL PRIMARY KEY,
	name TEXT NOT NULL,
	sort_order INTEGER NOT NULL DEFAULT 0,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
`); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS card_keys (
	id SERIAL PRIMARY KEY,
	group_id INTEGER NOT NULL DEFAULT 0,
	key TEXT NOT NULL,
	amount NUMERIC(12, 2) NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'unused',
	used_by TEXT NOT NULL DEFAULT '',
	used_at TIMESTAMPTZ,
	usage_limit INTEGER NOT NULL DEFAULT 1,
	mail_days INTEGER NOT NULL DEFAULT 1,
	mail_days_blank BOOLEAN NOT NULL DEFAULT FALSE,
	mail_keyword TEXT NOT NULL DEFAULT '',
	bound_email TEXT NOT NULL DEFAULT '',
	remark TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
`); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS card_key_use_logs (
	id SERIAL PRIMARY KEY,
	card_key_id INTEGER NOT NULL DEFAULT 0,
	card_key TEXT NOT NULL DEFAULT '',
	bound_email TEXT NOT NULL DEFAULT '',
	mail_subject TEXT NOT NULL DEFAULT '',
	user_ip TEXT NOT NULL DEFAULT '',
	used_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
`); err != nil {
		return err
	}

	statements := []string{
		`ALTER TABLE card_keys ADD COLUMN IF NOT EXISTS group_id INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE card_keys ADD COLUMN IF NOT EXISTS usage_limit INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE card_keys ADD COLUMN IF NOT EXISTS mail_days INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE card_keys ALTER COLUMN mail_days SET DEFAULT 1`,
		`ALTER TABLE card_keys ADD COLUMN IF NOT EXISTS mail_days_blank BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE card_keys ADD COLUMN IF NOT EXISTS mail_keyword TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE card_keys ADD COLUMN IF NOT EXISTS bound_email TEXT NOT NULL DEFAULT ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS card_key_groups_name_idx ON card_key_groups (name)`,
		`CREATE INDEX IF NOT EXISTS card_key_groups_sort_order_id_idx ON card_key_groups (sort_order, id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS card_keys_key_idx ON card_keys (key)`,
		`CREATE INDEX IF NOT EXISTS card_keys_group_id_idx ON card_keys (group_id)`,
		`CREATE INDEX IF NOT EXISTS card_keys_group_created_at_id_idx ON card_keys (group_id, created_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS card_keys_status_idx ON card_keys (status)`,
		`CREATE INDEX IF NOT EXISTS card_keys_created_at_id_idx ON card_keys (created_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS card_keys_amount_idx ON card_keys (amount)`,
		`CREATE INDEX IF NOT EXISTS card_keys_usage_limit_idx ON card_keys (usage_limit)`,
		`CREATE INDEX IF NOT EXISTS card_keys_bound_email_idx ON card_keys (bound_email)`,
		`ALTER TABLE card_key_use_logs ADD COLUMN IF NOT EXISTS card_key_id INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE card_key_use_logs ADD COLUMN IF NOT EXISTS card_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE card_key_use_logs ADD COLUMN IF NOT EXISTS bound_email TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE card_key_use_logs ADD COLUMN IF NOT EXISTS mail_subject TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE card_key_use_logs ADD COLUMN IF NOT EXISTS user_ip TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE card_key_use_logs ADD COLUMN IF NOT EXISTS used_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`,
		`CREATE INDEX IF NOT EXISTS card_key_use_logs_used_at_id_idx ON card_key_use_logs (used_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS card_key_use_logs_card_key_idx ON card_key_use_logs (card_key)`,
		`CREATE INDEX IF NOT EXISTS card_key_use_logs_bound_email_idx ON card_key_use_logs (bound_email)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	if _, err := db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS pg_trgm`); err == nil {
		_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS card_keys_key_trgm_idx ON card_keys USING gin (key gin_trgm_ops)`)
		_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS card_keys_bound_email_trgm_idx ON card_keys USING gin (bound_email gin_trgm_ops)`)
		_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS card_keys_remark_trgm_idx ON card_keys USING gin (remark gin_trgm_ops)`)
		_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS card_key_use_logs_card_key_trgm_idx ON card_key_use_logs USING gin (card_key gin_trgm_ops)`)
		_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS card_key_use_logs_bound_email_trgm_idx ON card_key_use_logs USING gin (bound_email gin_trgm_ops)`)
	}
	return normalizeCardKeyGroupSortOrders(ctx, db)
}

func normalizeCardKeyGroupSortOrders(ctx context.Context, exec sqlExecer) error {
	_, err := exec.ExecContext(ctx, `
WITH ranked AS (
	SELECT id, ROW_NUMBER() OVER (
		ORDER BY
			CASE WHEN sort_order > 0 THEN sort_order ELSE 2147483647 END,
			id
	) AS new_sort_order
	FROM card_key_groups
)
UPDATE card_key_groups AS g
SET sort_order = ranked.new_sort_order
FROM ranked
WHERE g.id = ranked.id AND g.sort_order <> ranked.new_sort_order
`)
	return err
}

func (s *appState) listCardKeyGroups(c *gin.Context) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取卡密分组失败"})
		return
	}
	defer db.Close()

	rows, err := db.QueryContext(c.Request.Context(), `
SELECT g.id, g.name, g.sort_order, COUNT(k.id), g.created_at
FROM card_key_groups g
LEFT JOIN card_keys k ON k.group_id = g.id
GROUP BY g.id, g.name, g.sort_order, g.created_at
ORDER BY g.sort_order ASC, g.id ASC
`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取卡密分组失败"})
		return
	}
	defer rows.Close()

	groups := []cardKeyGroupResponse{}
	for rows.Next() {
		var group cardKeyGroupResponse
		var createdAt time.Time
		if err := rows.Scan(&group.ID, &group.Name, &group.SortOrder, &group.Count, &createdAt); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取卡密分组失败"})
			return
		}
		group.CreatedAt = createdAt.Format(time.RFC3339)
		groups = append(groups, group)
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: groups, Msg: "ok"})
}

func (s *appState) createCardKeyGroup(c *gin.Context) {
	var req saveCardKeyGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	group, err := saveCardKeyGroup(c.Request.Context(), 0, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: group, Msg: "ok"})
}

func (s *appState) updateCardKeyGroup(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req saveCardKeyGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	group, err := saveCardKeyGroup(c.Request.Context(), id, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: group, Msg: "ok"})
}

func (s *appState) deleteCardKeyGroup(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除卡密分组失败"})
		return
	}
	defer db.Close()

	tx, err := db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除卡密分组失败"})
		return
	}
	defer tx.Rollback()

	var sortOrder int
	if err := tx.QueryRowContext(c.Request.Context(), `SELECT sort_order FROM card_key_groups WHERE id = $1`, id).Scan(&sortOrder); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "卡密分组不存在"})
		return
	}
	var keyCount int
	if err := tx.QueryRowContext(c.Request.Context(), `SELECT COUNT(*) FROM card_keys WHERE group_id = $1`, id).Scan(&keyCount); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除卡密分组失败"})
		return
	}
	if keyCount > 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "分组下存在卡密，无法删除"})
		return
	}
	if _, err := tx.ExecContext(c.Request.Context(), `DELETE FROM card_key_groups WHERE id = $1`, id); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除卡密分组失败"})
		return
	}
	if _, err := tx.ExecContext(c.Request.Context(), `UPDATE card_key_groups SET sort_order = sort_order - 1 WHERE sort_order > $1`, sortOrder); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除卡密分组失败"})
		return
	}
	if err := normalizeCardKeyGroupSortOrders(c.Request.Context(), tx); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除卡密分组失败"})
		return
	}
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除卡密分组失败"})
		return
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
}

func saveCardKeyGroup(ctx context.Context, id int, req saveCardKeyGroupRequest) (cardKeyGroupResponse, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return cardKeyGroupResponse{}, fmt.Errorf("分组名称不能为空")
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return cardKeyGroupResponse{}, fmt.Errorf("保存卡密分组失败")
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return cardKeyGroupResponse{}, fmt.Errorf("保存卡密分组失败")
	}
	defer tx.Rollback()

	if err := normalizeCardKeyGroupSortOrders(ctx, tx); err != nil {
		return cardKeyGroupResponse{}, fmt.Errorf("保存卡密分组失败")
	}

	if id > 0 {
		var currentSortOrder int
		if err := tx.QueryRowContext(ctx, `SELECT sort_order FROM card_key_groups WHERE id = $1`, id).Scan(&currentSortOrder); err != nil {
			return cardKeyGroupResponse{}, fmt.Errorf("卡密分组不存在")
		}
		var groupCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM card_key_groups`).Scan(&groupCount); err != nil {
			return cardKeyGroupResponse{}, fmt.Errorf("保存卡密分组失败")
		}
		sortOrder := normalizeRequestedGroupSortOrder(req.SortOrder, groupCount, currentSortOrder)
		if sortOrder < currentSortOrder {
			if _, err := tx.ExecContext(ctx, `UPDATE card_key_groups SET sort_order = sort_order + 1 WHERE id <> $1 AND sort_order >= $2 AND sort_order < $3`, id, sortOrder, currentSortOrder); err != nil {
				return cardKeyGroupResponse{}, fmt.Errorf("保存卡密分组失败")
			}
		} else if sortOrder > currentSortOrder {
			if _, err := tx.ExecContext(ctx, `UPDATE card_key_groups SET sort_order = sort_order - 1 WHERE id <> $1 AND sort_order <= $2 AND sort_order > $3`, id, sortOrder, currentSortOrder); err != nil {
				return cardKeyGroupResponse{}, fmt.Errorf("保存卡密分组失败")
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE card_key_groups SET name = $2, sort_order = $3, updated_at = NOW() WHERE id = $1`, id, req.Name, sortOrder); err != nil {
			return cardKeyGroupResponse{}, fmt.Errorf("分组名称已存在或保存失败")
		}
	} else {
		var groupCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM card_key_groups`).Scan(&groupCount); err != nil {
			return cardKeyGroupResponse{}, fmt.Errorf("保存卡密分组失败")
		}
		sortOrder := normalizeRequestedGroupSortOrder(req.SortOrder, groupCount+1, groupCount+1)
		if _, err := tx.ExecContext(ctx, `UPDATE card_key_groups SET sort_order = sort_order + 1 WHERE sort_order >= $1`, sortOrder); err != nil {
			return cardKeyGroupResponse{}, fmt.Errorf("保存卡密分组失败")
		}
		if err := tx.QueryRowContext(ctx, `
INSERT INTO card_key_groups (name, sort_order)
VALUES ($1, $2)
RETURNING id
`, req.Name, sortOrder).Scan(&id); err != nil {
			return cardKeyGroupResponse{}, fmt.Errorf("分组名称已存在或保存失败")
		}
	}

	if err := normalizeCardKeyGroupSortOrders(ctx, tx); err != nil {
		return cardKeyGroupResponse{}, fmt.Errorf("保存卡密分组失败")
	}
	if err := tx.Commit(); err != nil {
		return cardKeyGroupResponse{}, fmt.Errorf("保存卡密分组失败")
	}

	group, err := getCardKeyGroup(ctx, db, id)
	if err != nil {
		return cardKeyGroupResponse{}, fmt.Errorf("读取卡密分组失败")
	}
	return group, nil
}

func getCardKeyGroup(ctx context.Context, db *sql.DB, id int) (cardKeyGroupResponse, error) {
	var group cardKeyGroupResponse
	var createdAt time.Time
	err := db.QueryRowContext(ctx, `
SELECT g.id, g.name, g.sort_order, COUNT(k.id), g.created_at
FROM card_key_groups g
LEFT JOIN card_keys k ON k.group_id = g.id
WHERE g.id = $1
GROUP BY g.id, g.name, g.sort_order, g.created_at
`, id).Scan(&group.ID, &group.Name, &group.SortOrder, &group.Count, &createdAt)
	if err != nil {
		return group, err
	}
	group.CreatedAt = createdAt.Format(time.RFC3339)
	return group, nil
}

func (s *appState) listCardKeyUseLogs(c *gin.Context) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取卡密日志失败"})
		return
	}
	defer db.Close()

	_ = s.clearExpiredCardKeyUseLogs(c.Request.Context())

	search := strings.TrimSpace(c.Query("search"))
	page, pageSize, offset := parseListPage(c, 20, 500)
	whereSQL := "1 = 1"
	args := []interface{}{}
	if search != "" {
		whereSQL = "(card_key ILIKE $1 OR bound_email ILIKE $1)"
		args = append(args, "%"+search+"%")
	}

	var total int
	if err := db.QueryRowContext(c.Request.Context(), `SELECT COUNT(*) FROM card_key_use_logs WHERE `+whereSQL, args...).Scan(&total); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取卡密日志失败"})
		return
	}

	limitIndex := len(args) + 1
	offsetIndex := len(args) + 2
	queryArgs := append([]interface{}{}, args...)
	queryArgs = append(queryArgs, pageSize, offset)
	rows, err := db.QueryContext(c.Request.Context(), `
SELECT id, card_key, bound_email, mail_subject, user_ip, used_at
FROM card_key_use_logs
WHERE `+whereSQL+`
ORDER BY used_at DESC, id DESC
LIMIT $`+strconv.Itoa(limitIndex)+` OFFSET $`+strconv.Itoa(offsetIndex)+`
`, queryArgs...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取卡密日志失败"})
		return
	}
	defer rows.Close()

	items := []cardKeyUseLogResponse{}
	for rows.Next() {
		var item cardKeyUseLogResponse
		var usedAt time.Time
		if err := rows.Scan(&item.ID, &item.CardKey, &item.BoundEmail, &item.MailSubject, &item.UserIP, &usedAt); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取卡密日志失败"})
			return
		}
		item.UsedAt = formatBeijingTime(usedAt)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取卡密日志失败"})
		return
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: cardKeyUseLogListResponse{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Pages:    calculatePages(total, pageSize),
	}, Msg: "ok"})
}

func (s *appState) clearCardKeyUseLogs(c *gin.Context) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "清空卡密日志失败"})
		return
	}
	defer db.Close()

	result, err := db.ExecContext(c.Request.Context(), `DELETE FROM card_key_use_logs`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "清空卡密日志失败"})
		return
	}
	count, _ := result.RowsAffected()
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: gin.H{"count": count}, Msg: "ok"})
}

func (s *appState) listCardKeys(c *gin.Context) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取卡密失败"})
		return
	}
	defer db.Close()

	search := strings.TrimSpace(c.Query("search"))
	status := normalizeCardKeyStatus(c.Query("status"))
	groupID, _ := strconv.Atoi(c.Query("group_id"))
	page, pageSize, offset := parseListPage(c, 20, 500)
	whereSQL, args := buildCardKeyWhere("k", cardKeyListFilter{GroupID: groupID, Search: search, Status: status}, 1)

	var total int
	if err := db.QueryRowContext(c.Request.Context(), `SELECT COUNT(*) FROM card_keys k WHERE `+whereSQL, args...).Scan(&total); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取卡密失败"})
		return
	}

	var unused, used, disabled int
	if err := db.QueryRowContext(c.Request.Context(), `
SELECT COUNT(*) FILTER (WHERE status = 'unused'),
       COUNT(*) FILTER (WHERE status = 'used'),
       COUNT(*) FILTER (WHERE status = 'disabled')
FROM card_keys k
WHERE `+whereSQL, args...).Scan(&unused, &used, &disabled); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取卡密统计失败"})
		return
	}

	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := normalizeSortOrder(c.Query("sort_order"))
	orderClause := "k.created_at " + sortOrder + ", k.id " + sortOrder
	switch sortBy {
	case "id":
		orderClause = "k.id " + sortOrder
	case "group":
		orderClause = "COALESCE(g.name, '') " + sortOrder + ", k.id " + sortOrder
	case "key":
		orderClause = "k.key " + sortOrder + ", k.id " + sortOrder
	case "amount":
		orderClause = "k.amount " + sortOrder + ", k.id " + sortOrder
	case "usage_limit":
		orderClause = "k.usage_limit " + sortOrder + ", k.id " + sortOrder
	case "status":
		orderClause = "k.status " + sortOrder + ", k.id " + sortOrder
	case "used_by":
		orderClause = "k.used_by " + sortOrder + ", k.id " + sortOrder
	case "used_at":
		orderClause = "k.used_at " + sortOrder + " NULLS LAST, k.id " + sortOrder
	case "bound_email":
		orderClause = "k.bound_email " + sortOrder + ", k.id " + sortOrder
	case "mail_filter":
		orderClause = "k.mail_keyword " + sortOrder + ", k.mail_days " + sortOrder + ", k.id " + sortOrder
	case "remark":
		orderClause = "k.remark " + sortOrder + ", k.id " + sortOrder
	case "created_at":
		orderClause = "k.created_at " + sortOrder + ", k.id " + sortOrder
	}

	limitIndex := len(args) + 1
	offsetIndex := len(args) + 2
	queryArgs := append([]interface{}{}, args...)
	queryArgs = append(queryArgs, pageSize, offset)
	rows, err := db.QueryContext(c.Request.Context(), `
SELECT k.id, k.group_id, COALESCE(g.name, ''), k.key, k.amount, k.status, k.used_by, k.used_at, k.usage_limit, COALESCE(access.used_count, 0), k.mail_days, k.mail_days_blank, k.mail_keyword, k.bound_email, k.remark, k.created_at, k.updated_at
FROM card_keys k
LEFT JOIN card_key_groups g ON g.id = k.group_id
LEFT JOIN (
	SELECT card_key_id, COALESCE(SUM(charged_count), 0) AS used_count
	FROM card_key_mail_access_records
	GROUP BY card_key_id
) access ON access.card_key_id = k.id
WHERE `+whereSQL+`
ORDER BY `+orderClause+`
LIMIT $`+strconv.Itoa(limitIndex)+` OFFSET $`+strconv.Itoa(offsetIndex)+`
`, queryArgs...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取卡密失败"})
		return
	}
	defer rows.Close()

	items := []cardKeyResponse{}
	for rows.Next() {
		item, err := scanCardKey(rows)
		if err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取卡密失败"})
			return
		}
		items = append(items, item)
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: cardKeyListResponse{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Pages:    calculatePages(total, pageSize),
		Unused:   unused,
		Used:     used,
		Disabled: disabled,
	}, Msg: "ok"})
}

func (s *appState) createCardKey(c *gin.Context) {
	var req saveCardKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	item, err := saveCardKey(c.Request.Context(), 0, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: item, Msg: "ok"})
}

func (s *appState) batchCreateCardKeys(c *gin.Context) {
	var req batchCardKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	count := req.Count
	if count <= 0 && strings.TrimSpace(req.Content) != "" {
		for _, rawLine := range strings.Split(req.Content, "\n") {
			if strings.TrimSpace(rawLine) != "" {
				count++
			}
		}
	}
	if count <= 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请输入生成数量"})
		return
	}
	if count > cardKeyBatchGenerateMaxSize {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: fmt.Sprintf("一次最多生成 %d 个卡密", cardKeyBatchGenerateMaxSize)})
		return
	}
	if strings.TrimSpace(req.BoundEmail) != "" && count > 1 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "同一个邮箱只能绑定一个卡密"})
		return
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "批量生成卡密失败"})
		return
	}
	defer db.Close()

	itemReq := saveCardKeyRequest{
		GroupID:       req.GroupID,
		Amount:        req.Amount,
		Status:        req.Status,
		UsageLimit:    req.UsageLimit,
		MailDays:      req.MailDays,
		MailDaysBlank: req.MailDaysBlank,
		MailKeyword:   req.MailKeyword,
		BoundEmail:    req.BoundEmail,
		Remark:        req.Remark,
	}
	items := []cardKeyResponse{}
	for i := 0; i < count; i++ {
		item, err := saveCardKeyWithDB(c.Request.Context(), db, 0, itemReq)
		if err != nil {
			c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
			return
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: items, Msg: "ok"})
}

func (s *appState) updateCardKey(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req saveCardKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	item, err := saveCardKey(c.Request.Context(), id, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: item, Msg: "ok"})
}

func (s *appState) deleteCardKey(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除卡密失败"})
		return
	}
	defer db.Close()
	result, err := db.ExecContext(c.Request.Context(), `DELETE FROM card_keys WHERE id = $1`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除卡密失败"})
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "卡密不存在"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
}

func (s *appState) batchCardKeyAction(c *gin.Context) {
	var req cardKeyBatchActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	req.Action = strings.TrimSpace(req.Action)

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "批量操作卡密失败"})
		return
	}
	defer db.Close()

	ids, err := resolveCardKeyIDs(c.Request.Context(), db, req.IDs, req.Filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "批量操作卡密失败"})
		return
	}
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "没有可操作的卡密"})
		return
	}

	placeholders, args := intPlaceholders(ids, 1)
	switch req.Action {
	case "delete":
		if _, err := db.ExecContext(c.Request.Context(), `DELETE FROM card_keys WHERE id IN (`+placeholders+`)`, args...); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "批量删除卡密失败"})
			return
		}
	case "status":
		status := normalizeCardKeyStatus(req.Status)
		if status == "" {
			c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "卡密状态不正确"})
			return
		}
		args = append(args, status)
		statusIndex := len(args)
		if _, err := db.ExecContext(c.Request.Context(), `
UPDATE card_keys
SET status = $`+strconv.Itoa(statusIndex)+`,
    used_by = CASE WHEN $`+strconv.Itoa(statusIndex)+` = 'used' THEN used_by ELSE '' END,
    used_at = CASE WHEN $`+strconv.Itoa(statusIndex)+` = 'used' THEN COALESCE(used_at, NOW()) ELSE NULL END,
    updated_at = NOW()
WHERE id IN (`+placeholders+`)
`, args...); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "批量更新卡密失败"})
			return
		}
	case "unbind_email":
		result, err := db.ExecContext(c.Request.Context(), `
UPDATE card_keys
SET bound_email = '',
    updated_at = NOW()
WHERE id IN (`+placeholders+`)
  AND TRIM(bound_email) <> ''
`, args...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "批量解绑邮箱失败"})
			return
		}
		rowsAffected, _ := result.RowsAffected()
		c.JSON(http.StatusOK, apiResponse{Code: 0, Data: gin.H{
			"count":   rowsAffected,
			"skipped": int64(len(ids)) - rowsAffected,
		}, Msg: "ok"})
		return
	default:
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "不支持的批量操作"})
		return
	}

	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: gin.H{"count": len(ids)}, Msg: "ok"})
}

func saveCardKey(ctx context.Context, id int, req saveCardKeyRequest) (cardKeyResponse, error) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return cardKeyResponse{}, fmt.Errorf("保存卡密失败")
	}
	defer db.Close()
	return saveCardKeyWithDB(ctx, db, id, req)
}

func saveCardKeyWithDB(ctx context.Context, db *sql.DB, id int, req saveCardKeyRequest) (cardKeyResponse, error) {
	item := cardKeyResponse{}
	var err error
	req.Key = strings.TrimSpace(req.Key)
	req.Status = normalizeCardKeyStatus(req.Status)
	req.UsedBy = strings.TrimSpace(req.UsedBy)
	req.MailKeyword = strings.TrimSpace(req.MailKeyword)
	req.BoundEmail = strings.TrimSpace(req.BoundEmail)
	req.Remark = strings.TrimSpace(req.Remark)
	if req.GroupID <= 0 {
		return item, fmt.Errorf("请选择卡密分组")
	}
	if id > 0 && req.Key == "" {
		return item, fmt.Errorf("卡密不能为空")
	}
	if req.Amount < 0 {
		return item, fmt.Errorf("卡密面值不能小于 0")
	}
	if req.UsageLimit <= 0 {
		req.UsageLimit = 1
	}
	if req.MailDays < 0 {
		req.MailDays = 0
	}
	if req.MailDays != 0 {
		req.MailDaysBlank = false
	}
	if req.Status == "" {
		req.Status = cardKeyStatusUnused
	}
	if req.Status != cardKeyStatusUsed {
		req.UsedBy = ""
	}
	if !cardKeyGroupExists(ctx, db, req.GroupID) {
		return item, fmt.Errorf("卡密分组不存在")
	}
	if req.BoundEmail != "" {
		available, err := cardKeyBoundEmailAvailable(ctx, db, req.BoundEmail, id)
		if err != nil {
			return item, fmt.Errorf("检查绑定邮箱失败")
		}
		if !available {
			return item, fmt.Errorf("邮箱已绑定到其他卡密")
		}
	}

	if id > 0 {
		row := db.QueryRowContext(ctx, `
WITH saved AS (
	UPDATE card_keys
	SET group_id = $2,
	    key = $3,
	    amount = $4,
	    status = $5,
	    used_by = $6,
	    used_at = CASE WHEN $5 = 'used' THEN COALESCE(used_at, NOW()) ELSE NULL END,
	    usage_limit = $7,
	    mail_days = $8,
	    mail_days_blank = $9,
	    mail_keyword = $10,
	    bound_email = $11,
	    remark = $12,
	    updated_at = NOW()
	WHERE id = $1
	RETURNING id, group_id, key, amount, status, used_by, used_at, usage_limit, mail_days, mail_days_blank, mail_keyword, bound_email, remark, created_at, updated_at
)
SELECT saved.id, saved.group_id, COALESCE(g.name, ''), saved.key, saved.amount, saved.status, saved.used_by, saved.used_at, saved.usage_limit, COALESCE(access.used_count, 0), saved.mail_days, saved.mail_days_blank, saved.mail_keyword, saved.bound_email, saved.remark, saved.created_at, saved.updated_at
FROM saved
LEFT JOIN card_key_groups g ON g.id = saved.group_id
LEFT JOIN (
	SELECT card_key_id, COALESCE(SUM(charged_count), 0) AS used_count
	FROM card_key_mail_access_records
	GROUP BY card_key_id
) access ON access.card_key_id = saved.id
`, id, req.GroupID, req.Key, req.Amount, req.Status, req.UsedBy, req.UsageLimit, req.MailDays, req.MailDaysBlank, req.MailKeyword, req.BoundEmail, req.Remark)
		item, err = scanCardKey(row)
		if err != nil {
			return item, fmt.Errorf("卡密不存在、已存在或保存失败")
		}
		return item, nil
	}

	attempts := 1
	if req.Key == "" {
		attempts = cardKeyGenerateMaxAttempts
	}
	for attempt := 0; attempt < attempts; attempt++ {
		key := req.Key
		if key == "" {
			key, err = generateCardKeyValue()
			if err != nil {
				return item, fmt.Errorf("生成卡密失败")
			}
		}
		item, inserted, err := insertCardKey(ctx, db, req, key)
		if err != nil {
			return item, err
		}
		if inserted {
			return item, nil
		}
		if req.Key != "" {
			return item, fmt.Errorf("卡密已存在或保存失败")
		}
	}

	return item, fmt.Errorf("生成卡密失败，请重试")
}

func insertCardKey(ctx context.Context, db *sql.DB, req saveCardKeyRequest, key string) (cardKeyResponse, bool, error) {
	item := cardKeyResponse{}
	row := db.QueryRowContext(ctx, `
WITH saved AS (
	INSERT INTO card_keys (group_id, key, amount, status, used_by, used_at, usage_limit, mail_days, mail_days_blank, mail_keyword, bound_email, remark)
	VALUES ($1, $2, $3, $4, $5, CASE WHEN $4 = 'used' THEN NOW() ELSE NULL END, $6, $7, $8, $9, $10, $11)
	ON CONFLICT (key) DO NOTHING
	RETURNING id, group_id, key, amount, status, used_by, used_at, usage_limit, mail_days, mail_days_blank, mail_keyword, bound_email, remark, created_at, updated_at
)
SELECT saved.id, saved.group_id, COALESCE(g.name, ''), saved.key, saved.amount, saved.status, saved.used_by, saved.used_at, saved.usage_limit, 0, saved.mail_days, saved.mail_days_blank, saved.mail_keyword, saved.bound_email, saved.remark, saved.created_at, saved.updated_at
FROM saved
LEFT JOIN card_key_groups g ON g.id = saved.group_id
`, req.GroupID, key, req.Amount, req.Status, req.UsedBy, req.UsageLimit, req.MailDays, req.MailDaysBlank, req.MailKeyword, req.BoundEmail, req.Remark)
	item, err := scanCardKey(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return item, false, nil
		}
		return item, false, fmt.Errorf("卡密保存失败")
	}
	return item, true, nil
}

func scanCardKey(scanner sqlScanner) (cardKeyResponse, error) {
	var item cardKeyResponse
	var usedAt sql.NullTime
	var createdAt, updatedAt time.Time
	err := scanner.Scan(&item.ID, &item.GroupID, &item.GroupName, &item.Key, &item.Amount, &item.Status, &item.UsedBy, &usedAt, &item.UsageLimit, &item.UsedCount, &item.MailDays, &item.MailDaysBlank, &item.MailKeyword, &item.BoundEmail, &item.Remark, &createdAt, &updatedAt)
	if err != nil {
		return item, err
	}
	if usedAt.Valid {
		item.UsedAt = usedAt.Time.Format("2006/01/02 15:04:05")
	}
	item.CreatedAt = createdAt.Format("2006/01/02 15:04:05")
	item.UpdatedAt = updatedAt.Format("2006/01/02 15:04:05")
	return item, nil
}

func normalizeCardKeyStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "unused", "active", "normal":
		if strings.TrimSpace(value) == "" {
			return ""
		}
		return cardKeyStatusUnused
	case "used":
		return cardKeyStatusUsed
	case "disabled", "disable", "inactive":
		return cardKeyStatusDisabled
	default:
		return ""
	}
}

func generateCardKeyValue() (string, error) {
	var builder strings.Builder
	builder.Grow(cardKeyGeneratedLength)
	max := big.NewInt(int64(len(cardKeyGeneratedCharSet)))
	for i := 0; i < cardKeyGeneratedLength; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		builder.WriteByte(cardKeyGeneratedCharSet[n.Int64()])
	}
	return builder.String(), nil
}

func buildCardKeyWhere(alias string, filter cardKeyListFilter, start int) (string, []interface{}) {
	where := []string{"1 = 1"}
	args := []interface{}{}
	prefix := alias
	if prefix != "" {
		prefix += "."
	}
	if filter.GroupID > 0 {
		where = append(where, fmt.Sprintf("%sgroup_id = $%d", prefix, start+len(args)))
		args = append(args, filter.GroupID)
	}
	status := normalizeCardKeyStatus(filter.Status)
	if status != "" {
		where = append(where, fmt.Sprintf("%sstatus = $%d", prefix, start+len(args)))
		args = append(args, status)
	}
	search := strings.TrimSpace(filter.Search)
	if search != "" {
		where = append(where, fmt.Sprintf("(%skey ILIKE $%d OR %sbound_email ILIKE $%d OR %sremark ILIKE $%d)", prefix, start+len(args), prefix, start+len(args), prefix, start+len(args)))
		args = append(args, "%"+search+"%")
	}
	return strings.Join(where, " AND "), args
}

func parseCardKeyBatchLine(line string, defaults batchCardKeyRequest) (saveCardKeyRequest, error) {
	parts := strings.Split(line, "----")
	req := saveCardKeyRequest{
		GroupID: defaults.GroupID,
		Key:     strings.TrimSpace(parts[0]),
		Amount:  defaults.Amount,
		Status:  defaults.Status,
		Remark:  defaults.Remark,
	}
	if len(parts) > 1 {
		amountText := strings.TrimSpace(parts[1])
		if amountText != "" {
			amount, err := strconv.ParseFloat(amountText, 64)
			if err != nil {
				return req, fmt.Errorf("批量卡密面值格式错误")
			}
			req.Amount = amount
		}
	}
	if len(parts) > 2 {
		req.Remark = strings.TrimSpace(parts[2])
	}
	if len(parts) > 3 {
		return req, fmt.Errorf("批量卡密格式错误，请使用 卡密 或 卡密----面值----备注")
	}
	return req, nil
}

func queryCardKeyIDsByFilter(ctx context.Context, db *sql.DB, filter cardKeyListFilter) ([]int, error) {
	whereSQL, args := buildCardKeyWhere("", filter, 1)
	rows, err := db.QueryContext(ctx, `SELECT id FROM card_keys WHERE `+whereSQL+` ORDER BY id ASC`, args...)
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

func resolveCardKeyIDs(ctx context.Context, db *sql.DB, ids []int, filter cardKeyListFilter) ([]int, error) {
	if len(ids) > 0 {
		return normalizePositiveIDs(ids), nil
	}
	return queryCardKeyIDsByFilter(ctx, db, filter)
}

func cardKeyGroupExists(ctx context.Context, db *sql.DB, id int) bool {
	var exists bool
	return db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM card_key_groups WHERE id = $1)`, id).Scan(&exists) == nil && exists
}

func cardKeyBoundEmailAvailable(ctx context.Context, db *sql.DB, email string, currentID int) (bool, error) {
	var existingID int
	err := db.QueryRowContext(ctx, `
SELECT id
FROM card_keys
WHERE TRIM(bound_email) <> ''
  AND LOWER(TRIM(bound_email)) = LOWER(TRIM($1))
  AND id <> $2
LIMIT 1
`, email, currentID).Scan(&existingID)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

func recordCardKeyUseAttempt(ctx context.Context, db *sql.DB, cardKeyID int, cardKey string, email string, subject string, userIP string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
UPDATE card_keys
SET used_at = NOW(), updated_at = NOW()
WHERE id = $1
`, cardKeyID); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO card_key_use_logs (card_key_id, card_key, bound_email, mail_subject, user_ip, used_at)
VALUES ($1, $2, $3, $4, $5, NOW())
`, cardKeyID, strings.TrimSpace(cardKey), strings.TrimSpace(email), strings.TrimSpace(subject), strings.TrimSpace(userIP)); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *appState) runCardKeyUseLogCleanupLoop(stop <-chan struct{}) {
	_ = s.clearExpiredCardKeyUseLogs(context.Background())
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = s.clearExpiredCardKeyUseLogs(context.Background())
		case <-stop:
			return
		}
	}
}

func (s *appState) clearExpiredCardKeyUseLogs(ctx context.Context) error {
	settings, err := s.readSettings(ctx)
	if err != nil {
		return err
	}
	days := normalizeCardKeyLogCleanupDays(settings["card_key_log_cleanup_days"])
	if days <= 0 {
		return nil
	}
	return cleanupCardKeyUseLogs(ctx, days)
}

func cleanupCardKeyUseLogs(ctx context.Context, days int) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()
	return cleanupCardKeyUseLogsWithDB(ctx, db, days)
}

func cleanupCardKeyUseLogsWithDB(ctx context.Context, db *sql.DB, days int) error {
	if days <= 0 {
		return nil
	}
	_, err := db.ExecContext(ctx, `
DELETE FROM card_key_use_logs
WHERE used_at <= NOW() - ($1::int * INTERVAL '1 day')
`, days)
	return err
}

func normalizeCardKeyLogCleanupDays(value interface{}) int {
	switch typed := value.(type) {
	case string:
		value = strings.TrimSpace(typed)
	case int:
		if typed > 0 {
			return typed
		}
		return 0
	case int64:
		if typed > 0 {
			return int(typed)
		}
		return 0
	case float64:
		if typed > 0 {
			return int(typed)
		}
		return 0
	default:
		return 0
	}

	text := value.(string)
	if text == "" {
		return 0
	}
	days, err := strconv.Atoi(text)
	if err != nil || days <= 0 {
		return 0
	}
	return days
}

func formatBeijingTime(value time.Time) string {
	return value.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006/01/02 15:04:05")
}

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	xnetproxy "golang.org/x/net/proxy"
)

const (
	proxyScopeIMAP    = "imap"
	proxyScopeOutlook = "outlook"

	proxyProtocolHTTP   = "http"
	proxyProtocolSOCKS5 = "socks5"
	proxyProtocolVMess  = "vmess"
	proxyProtocolVLESS  = "vless"
)

var defaultProxyRuntime = newProxyRuntime()

type proxyNode struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Protocol     string `json:"protocol"`
	Address      string `json:"address"`
	Port         int    `json:"port"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	UUID         string `json:"uuid"`
	AlterID      int    `json:"alter_id"`
	Security     string `json:"security"`
	Encryption   string `json:"encryption"`
	Transport    string `json:"transport"`
	TLS          string `json:"tls"`
	SNI          string `json:"sni"`
	Path         string `json:"path"`
	HostHeader   string `json:"host_header"`
	Flow         string `json:"flow"`
	Fingerprint  string `json:"fingerprint"`
	PublicKey    string `json:"public_key"`
	ShortID      string `json:"short_id"`
	SpiderX      string `json:"spider_x"`
	Enabled      bool   `json:"enabled"`
	LocalPort    int    `json:"local_port"`
	Status       string `json:"status"`
	StatusReason string `json:"status_reason"`
	LatencyMS    int    `json:"latency_ms"`
	LastTestedAt string `json:"last_tested_at"`
	Remark       string `json:"remark"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type proxyNodeListResponse struct {
	Items       []proxyNode `json:"items"`
	Total       int         `json:"total"`
	Page        int         `json:"page"`
	PageSize    int         `json:"page_size"`
	Pages       int         `json:"pages"`
	Normal      int         `json:"normal"`
	Error       int         `json:"error"`
	StatsTotal  int         `json:"stats_total"`
	StatsNormal int         `json:"stats_normal"`
	StatsError  int         `json:"stats_error"`
}

type saveProxyNodeRequest struct {
	ImportURL   string `json:"import_url"`
	Name        string `json:"name"`
	Protocol    string `json:"protocol"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	UUID        string `json:"uuid"`
	AlterID     int    `json:"alter_id"`
	Security    string `json:"security"`
	Encryption  string `json:"encryption"`
	Transport   string `json:"transport"`
	TLS         string `json:"tls"`
	SNI         string `json:"sni"`
	Path        string `json:"path"`
	HostHeader  string `json:"host_header"`
	Flow        string `json:"flow"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"public_key"`
	ShortID     string `json:"short_id"`
	SpiderX     string `json:"spider_x"`
	Enabled     *bool  `json:"enabled"`
	Remark      string `json:"remark"`
}

type importProxyNodesRequest struct {
	Nodes []saveProxyNodeRequest `json:"nodes"`
}

type importProxyNodesResponse struct {
	Count int `json:"count"`
}

type proxySetting struct {
	Scope       string `json:"scope"`
	Enabled     bool   `json:"enabled"`
	ProxyNodeID int    `json:"proxy_node_id"`
	UpdatedAt   string `json:"updated_at"`
}

type proxySettingsResponse struct {
	IMAP    proxySetting `json:"imap"`
	Outlook proxySetting `json:"outlook"`
}

type updateProxySettingsRequest struct {
	IMAP    proxySettingInput `json:"imap"`
	Outlook proxySettingInput `json:"outlook"`
}

type proxySettingInput struct {
	Enabled     bool `json:"enabled"`
	ProxyNodeID int  `json:"proxy_node_id"`
}

type proxyEndpoint struct {
	Protocol string
	Address  string
	Port     int
	Username string
	Password string
	NodeID   int
}

type proxyRuntime struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	configPath string
	configHash string
	lastError  string
	startedAt  time.Time
}

func newProxyRuntime() *proxyRuntime {
	return &proxyRuntime{}
}

func ensureProxySystemTables(ctx context.Context) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS proxy_nodes (
	id SERIAL PRIMARY KEY,
	name TEXT NOT NULL,
	protocol TEXT NOT NULL,
	address TEXT NOT NULL DEFAULT '',
	port INTEGER NOT NULL DEFAULT 0,
	username TEXT NOT NULL DEFAULT '',
	password TEXT NOT NULL DEFAULT '',
	uuid TEXT NOT NULL DEFAULT '',
	alter_id INTEGER NOT NULL DEFAULT 0,
	security TEXT NOT NULL DEFAULT '',
	encryption TEXT NOT NULL DEFAULT '',
	transport TEXT NOT NULL DEFAULT 'tcp',
	tls TEXT NOT NULL DEFAULT '',
	sni TEXT NOT NULL DEFAULT '',
	path TEXT NOT NULL DEFAULT '',
	host_header TEXT NOT NULL DEFAULT '',
	flow TEXT NOT NULL DEFAULT '',
	fingerprint TEXT NOT NULL DEFAULT '',
	public_key TEXT NOT NULL DEFAULT '',
	short_id TEXT NOT NULL DEFAULT '',
	spider_x TEXT NOT NULL DEFAULT '',
	enabled BOOLEAN NOT NULL DEFAULT TRUE,
	local_port INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'unchecked',
	status_reason TEXT NOT NULL DEFAULT '',
	latency_ms INTEGER NOT NULL DEFAULT 0,
	last_tested_at TIMESTAMPTZ,
	remark TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
`); err != nil {
		return err
	}

	for _, stmt := range []string{
		`ALTER TABLE proxy_nodes ADD COLUMN IF NOT EXISTS fingerprint TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE proxy_nodes ADD COLUMN IF NOT EXISTS public_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE proxy_nodes ADD COLUMN IF NOT EXISTS short_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE proxy_nodes ADD COLUMN IF NOT EXISTS spider_x TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS proxy_nodes_name_idx ON proxy_nodes (name)`,
		`CREATE INDEX IF NOT EXISTS proxy_nodes_address_idx ON proxy_nodes (address)`,
		`CREATE INDEX IF NOT EXISTS proxy_nodes_protocol_idx ON proxy_nodes (protocol)`,
		`CREATE INDEX IF NOT EXISTS proxy_nodes_enabled_idx ON proxy_nodes (enabled)`,
		`CREATE INDEX IF NOT EXISTS proxy_nodes_status_idx ON proxy_nodes (status)`,
		`CREATE INDEX IF NOT EXISTS proxy_nodes_latency_idx ON proxy_nodes (latency_ms)`,
		`CREATE INDEX IF NOT EXISTS proxy_nodes_created_at_idx ON proxy_nodes (created_at)`,
		`CREATE INDEX IF NOT EXISTS proxy_nodes_updated_at_idx ON proxy_nodes (updated_at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS proxy_nodes_local_port_idx ON proxy_nodes (local_port) WHERE local_port > 0`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS proxy_settings (
	scope TEXT PRIMARY KEY,
	enabled BOOLEAN NOT NULL DEFAULT FALSE,
	proxy_node_id INTEGER NOT NULL DEFAULT 0,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
`); err != nil {
		return err
	}

	for _, scope := range []string{proxyScopeIMAP, proxyScopeOutlook} {
		if _, err := db.ExecContext(ctx, `
INSERT INTO proxy_settings (scope, enabled, proxy_node_id)
VALUES ($1, FALSE, 0)
ON CONFLICT (scope) DO NOTHING
`, scope); err != nil {
			return err
		}
	}
	return nil
}

func parseProxyNodeIDFilter(value string) []int {
	ids := []int{}
	seen := map[int]struct{}{}
	for _, part := range strings.Split(value, ",") {
		id, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		ids = append(ids, id)
		seen[id] = struct{}{}
		if len(ids) >= 200 {
			break
		}
	}
	return ids
}

func (s *appState) listProxyNodes(c *gin.Context) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取代理节点失败"})
		return
	}
	defer db.Close()

	search := strings.TrimSpace(c.Query("search"))
	ids := parseProxyNodeIDFilter(c.Query("ids"))
	page, pageSize, offset := parseListPage(c, 20, 500)
	where := []string{"1 = 1"}
	args := []interface{}{}
	if len(ids) > 0 {
		placeholders := make([]string, 0, len(ids))
		for _, id := range ids {
			args = append(args, id)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		where = append(where, "id IN ("+strings.Join(placeholders, ", ")+")")
	}
	if search != "" {
		searchIndex := len(args) + 1
		where = append(where, fmt.Sprintf("(CAST(id AS TEXT) ILIKE $%d OR name ILIKE $%d OR protocol ILIKE $%d OR address ILIKE $%d OR CAST(port AS TEXT) ILIKE $%d OR username ILIKE $%d OR status ILIKE $%d OR remark ILIKE $%d)", searchIndex, searchIndex, searchIndex, searchIndex, searchIndex, searchIndex, searchIndex, searchIndex))
		args = append(args, "%"+search+"%")
	}
	whereSQL := strings.Join(where, " AND ")

	var total, normal, errorCount int
	if err := db.QueryRowContext(c.Request.Context(), `
SELECT COUNT(*),
       COUNT(*) FILTER (WHERE status = 'normal'),
       COUNT(*) FILTER (WHERE status = 'error')
FROM proxy_nodes
WHERE `+whereSQL, args...).Scan(&total, &normal, &errorCount); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取代理节点失败"})
		return
	}

	var statsTotal, statsNormal, statsError int
	if err := db.QueryRowContext(c.Request.Context(), `
SELECT COUNT(*),
       COUNT(*) FILTER (WHERE status = 'normal'),
       COUNT(*) FILTER (WHERE status = 'error')
FROM proxy_nodes
`).Scan(&statsTotal, &statsNormal, &statsError); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "璇诲彇浠ｇ悊鑺傜偣澶辫触"})
		return
	}

	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := normalizeSortOrder(c.Query("sort_order"))
	orderClause := "created_at " + sortOrder + ", id " + sortOrder
	switch sortBy {
	case "id":
		orderClause = "id " + sortOrder
	case "name":
		orderClause = "name " + sortOrder + ", id " + sortOrder
	case "protocol":
		orderClause = "protocol " + sortOrder + ", id " + sortOrder
	case "address":
		orderClause = "address " + sortOrder + ", port " + sortOrder + ", id " + sortOrder
	case "status":
		orderClause = "status " + sortOrder + ", id " + sortOrder
	case "latency":
		orderClause = "latency_ms " + sortOrder + ", id " + sortOrder
	case "created_at":
		orderClause = "created_at " + sortOrder + ", id " + sortOrder
	case "updated_at":
		orderClause = "updated_at " + sortOrder + ", id " + sortOrder
	}

	limitIndex := len(args) + 1
	offsetIndex := len(args) + 2
	queryArgs := append([]interface{}{}, args...)
	queryArgs = append(queryArgs, pageSize, offset)
	nodes, err := listProxyNodesPage(c.Request.Context(), db, whereSQL, orderClause, limitIndex, offsetIndex, queryArgs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取代理节点失败"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: proxyNodeListResponse{
		Items:       nodes,
		Total:       total,
		Page:        page,
		PageSize:    pageSize,
		Pages:       calculatePages(total, pageSize),
		Normal:      normal,
		Error:       errorCount,
		StatsTotal:  statsTotal,
		StatsNormal: statsNormal,
		StatsError:  statsError,
	}, Msg: "ok"})
}

func (s *appState) createProxyNode(c *gin.Context) {
	var req saveProxyNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	node, err := saveProxyNode(c.Request.Context(), 0, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	_ = s.proxies.ensureForSelected(c.Request.Context())
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: node, Msg: "ok"})
}

func (s *appState) importProxyNodes(c *gin.Context) {
	var req importProxyNodesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "璇锋眰鍙傛暟閿欒"})
		return
	}
	count, err := createProxyNodesBatch(c.Request.Context(), req.Nodes)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	_ = s.proxies.ensureForSelected(c.Request.Context())
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: importProxyNodesResponse{Count: count}, Msg: "ok"})
}

func (s *appState) updateProxyNode(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req saveProxyNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	node, err := saveProxyNode(c.Request.Context(), id, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	_ = s.proxies.ensureForSelected(c.Request.Context())
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: node, Msg: "ok"})
}

func (s *appState) deleteProxyNode(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除代理节点失败"})
		return
	}
	defer db.Close()
	if _, err := db.ExecContext(c.Request.Context(), `DELETE FROM proxy_nodes WHERE id = $1`, id); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "删除代理节点失败"})
		return
	}
	_, _ = db.ExecContext(c.Request.Context(), `UPDATE proxy_settings SET enabled = FALSE, proxy_node_id = 0, updated_at = NOW() WHERE proxy_node_id = $1`, id)
	_ = s.proxies.ensureForSelected(c.Request.Context())
	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok"})
}

func (s *appState) testProxyNode(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "测试代理失败"})
		return
	}
	defer db.Close()
	node, err := getProxyNode(c.Request.Context(), db, id)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "代理节点不存在"})
		return
	}
	if err := s.proxies.ensureForSelected(c.Request.Context(), id); err != nil {
		updateProxyNodeTestStatus(c.Request.Context(), db, id, "error", err.Error(), 0)
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "代理核心启动失败: " + err.Error()})
		return
	}
	defer func() {
		_ = s.proxies.ensureForSelected(context.Background())
	}()
	start := time.Now()
	endpoint, err := proxyEndpointForNode(c.Request.Context(), node)
	if err == nil {
		err = testProxyEndpoint(c.Request.Context(), endpoint)
	}
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		updateProxyNodeTestStatus(c.Request.Context(), db, id, "error", err.Error(), 0)
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "代理测试失败: " + err.Error()})
		return
	}
	updateProxyNodeTestStatus(c.Request.Context(), db, id, "normal", "", latency)
	node, _ = getProxyNode(c.Request.Context(), db, id)
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: node, Msg: "ok"})
}

func (s *appState) getProxySettings(c *gin.Context) {
	settings, err := loadProxySettings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取代理设置失败"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: settings, Msg: "ok"})
}

func (s *appState) updateProxySettings(c *gin.Context) {
	var req updateProxySettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	if err := validateProxySettingsForUpdate(c.Request.Context(), s.proxies, req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	if err := saveProxySettings(c.Request.Context(), req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	if err := s.proxies.ensureForSelected(c.Request.Context()); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "代理核心启动失败: " + err.Error()})
		return
	}
	settings, _ := loadProxySettings(c.Request.Context())
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: settings, Msg: "ok"})
}

func (s *appState) getProxyRuntime(c *gin.Context) {
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: s.proxies.snapshot(), Msg: "ok"})
}

func listProxyNodesPage(ctx context.Context, db *sql.DB, whereSQL string, orderClause string, limitIndex int, offsetIndex int, args []interface{}) ([]proxyNode, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, name, protocol, address, port, username, password, uuid, alter_id, security, encryption, transport, tls, sni, path, host_header,
       flow, fingerprint, public_key, short_id, spider_x, enabled, local_port, status, status_reason, latency_ms, last_tested_at, remark, created_at, updated_at
FROM proxy_nodes
WHERE `+whereSQL+`
ORDER BY `+orderClause+`
LIMIT $`+strconv.Itoa(limitIndex)+` OFFSET $`+strconv.Itoa(offsetIndex)+`
`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []proxyNode{}
	for rows.Next() {
		node, err := scanProxyNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func getProxyNode(ctx context.Context, db *sql.DB, id int) (proxyNode, error) {
	row := db.QueryRowContext(ctx, `
SELECT id, name, protocol, address, port, username, password, uuid, alter_id, security, encryption, transport, tls, sni, path, host_header,
       flow, fingerprint, public_key, short_id, spider_x, enabled, local_port, status, status_reason, latency_ms, last_tested_at, remark, created_at, updated_at
FROM proxy_nodes
WHERE id = $1
`, id)
	return scanProxyNode(row)
}

func scanProxyNode(scanner sqlScanner) (proxyNode, error) {
	var node proxyNode
	var lastTested sql.NullTime
	var createdAt, updatedAt time.Time
	err := scanner.Scan(
		&node.ID, &node.Name, &node.Protocol, &node.Address, &node.Port, &node.Username, &node.Password, &node.UUID, &node.AlterID,
		&node.Security, &node.Encryption, &node.Transport, &node.TLS, &node.SNI, &node.Path, &node.HostHeader, &node.Flow,
		&node.Fingerprint, &node.PublicKey, &node.ShortID, &node.SpiderX, &node.Enabled, &node.LocalPort, &node.Status,
		&node.StatusReason, &node.LatencyMS, &lastTested, &node.Remark, &createdAt, &updatedAt,
	)
	if err != nil {
		return node, err
	}
	if lastTested.Valid {
		node.LastTestedAt = lastTested.Time.Format(time.RFC3339)
	}
	node.CreatedAt = createdAt.Format(time.RFC3339)
	node.UpdatedAt = updatedAt.Format(time.RFC3339)
	return node, nil
}

func saveProxyNode(ctx context.Context, id int, req saveProxyNodeRequest) (proxyNode, error) {
	req, err := prepareProxyNodeRequest(req)
	if err != nil {
		return proxyNode{}, err
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return proxyNode{}, err
	}
	defer db.Close()

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	localPort := 0
	if isXrayProxyProtocol(req.Protocol) {
		localPort, err = proxyLocalPortForSave(ctx, db, id)
		if err != nil {
			return proxyNode{}, err
		}
	}

	if id > 0 {
		var existing proxyNode
		existing, err = getProxyNode(ctx, db, id)
		if err != nil {
			return proxyNode{}, fmt.Errorf("代理节点不存在")
		}
		if localPort == 0 && isXrayProxyProtocol(existing.Protocol) && !isXrayProxyProtocol(req.Protocol) {
			localPort = 0
		} else if localPort == 0 {
			localPort = existing.LocalPort
		}
		_, err = db.ExecContext(ctx, `
UPDATE proxy_nodes
SET name = $2, protocol = $3, address = $4, port = $5, username = $6, password = $7, uuid = $8, alter_id = $9,
    security = $10, encryption = $11, transport = $12, tls = $13, sni = $14, path = $15, host_header = $16,
    flow = $17, fingerprint = $18, public_key = $19, short_id = $20, spider_x = $21, enabled = $22, local_port = $23,
    remark = $24, status = 'unchecked', status_reason = '', latency_ms = 0, last_tested_at = NULL, updated_at = NOW()
WHERE id = $1
`, id, req.Name, req.Protocol, req.Address, req.Port, req.Username, req.Password, req.UUID, req.AlterID, req.Security, req.Encryption, req.Transport, req.TLS, req.SNI, req.Path, req.HostHeader, req.Flow, req.Fingerprint, req.PublicKey, req.ShortID, req.SpiderX, enabled, localPort, req.Remark)
		if err != nil {
			return proxyNode{}, err
		}
		return getProxyNode(ctx, db, id)
	}

	var nodeID int
	err = db.QueryRowContext(ctx, `
INSERT INTO proxy_nodes (name, protocol, address, port, username, password, uuid, alter_id, security, encryption, transport, tls, sni, path, host_header,
                         flow, fingerprint, public_key, short_id, spider_x, enabled, local_port, remark)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
RETURNING id
`, req.Name, req.Protocol, req.Address, req.Port, req.Username, req.Password, req.UUID, req.AlterID, req.Security, req.Encryption, req.Transport, req.TLS, req.SNI, req.Path, req.HostHeader, req.Flow, req.Fingerprint, req.PublicKey, req.ShortID, req.SpiderX, enabled, localPort, req.Remark).Scan(&nodeID)
	if err != nil {
		return proxyNode{}, err
	}
	return getProxyNode(ctx, db, nodeID)
}

func prepareProxyNodeRequest(req saveProxyNodeRequest) (saveProxyNodeRequest, error) {
	if strings.TrimSpace(req.ImportURL) != "" {
		parsed, err := parseProxyShareLink(req.ImportURL)
		if err != nil {
			return saveProxyNodeRequest{}, err
		}
		req = mergeProxyRequest(parsed, req)
	}
	normalizeProxyRequest(&req)
	if err := validateProxyRequest(req); err != nil {
		return saveProxyNodeRequest{}, err
	}
	return req, nil
}

func createProxyNodesBatch(ctx context.Context, requests []saveProxyNodeRequest) (int, error) {
	if len(requests) == 0 {
		return 0, fmt.Errorf("没有可导入的代理节点")
	}
	if len(requests) > 5000 {
		return 0, fmt.Errorf("一次最多导入 5000 个代理节点")
	}

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return 0, err
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	usedPorts, err := loadUsedProxyLocalPorts(ctx, tx)
	if err != nil {
		return 0, err
	}
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO proxy_nodes (name, protocol, address, port, username, password, uuid, alter_id, security, encryption, transport, tls, sni, path, host_header,
                         flow, fingerprint, public_key, short_id, spider_x, enabled, local_port, remark)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	for index, raw := range requests {
		req, err := prepareProxyNodeRequest(raw)
		if err != nil {
			return 0, fmt.Errorf("第 %d 个节点导入失败: %w", index+1, err)
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		localPort := 0
		if isXrayProxyProtocol(req.Protocol) {
			localPort, err = nextProxyLocalPort(usedPorts)
			if err != nil {
				return 0, err
			}
		}
		if _, err := stmt.ExecContext(ctx, req.Name, req.Protocol, req.Address, req.Port, req.Username, req.Password, req.UUID, req.AlterID, req.Security, req.Encryption, req.Transport, req.TLS, req.SNI, req.Path, req.HostHeader, req.Flow, req.Fingerprint, req.PublicKey, req.ShortID, req.SpiderX, enabled, localPort, req.Remark); err != nil {
			return 0, fmt.Errorf("第 %d 个节点保存失败: %w", index+1, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(requests), nil
}

func normalizeProxyRequest(req *saveProxyNodeRequest) {
	req.Name = strings.TrimSpace(req.Name)
	req.Protocol = normalizeProxyProtocol(req.Protocol)
	req.Address = strings.TrimSpace(req.Address)
	req.Username = strings.TrimSpace(req.Username)
	req.Password = strings.TrimSpace(req.Password)
	req.UUID = strings.TrimSpace(req.UUID)
	req.Security = strings.TrimSpace(req.Security)
	req.Encryption = strings.TrimSpace(req.Encryption)
	req.Transport = strings.ToLower(strings.TrimSpace(req.Transport))
	req.TLS = strings.ToLower(strings.TrimSpace(req.TLS))
	req.SNI = strings.TrimSpace(req.SNI)
	req.Path = strings.TrimSpace(req.Path)
	req.HostHeader = strings.TrimSpace(req.HostHeader)
	req.Flow = strings.TrimSpace(req.Flow)
	req.Fingerprint = strings.TrimSpace(req.Fingerprint)
	req.PublicKey = strings.TrimSpace(req.PublicKey)
	req.ShortID = strings.TrimSpace(req.ShortID)
	req.SpiderX = strings.TrimSpace(req.SpiderX)
	req.Remark = strings.TrimSpace(req.Remark)
	if req.Transport == "" {
		req.Transport = "tcp"
	}
	if req.Security == "" && req.Protocol == proxyProtocolVMess {
		req.Security = "auto"
	}
	if req.Encryption == "" && req.Protocol == proxyProtocolVLESS {
		req.Encryption = "none"
	}
	if req.Name == "" && req.Address != "" {
		req.Name = req.Protocol + "://" + req.Address
	}
}

func validateProxyRequest(req saveProxyNodeRequest) error {
	if req.Name == "" {
		return fmt.Errorf("请填写代理名称")
	}
	if !isSupportedProxyProtocol(req.Protocol) {
		return fmt.Errorf("不支持的代理协议")
	}
	if req.Address == "" || req.Port <= 0 || req.Port > 65535 {
		return fmt.Errorf("请填写有效的代理地址和端口")
	}
	if isXrayProxyProtocol(req.Protocol) && req.UUID == "" {
		return fmt.Errorf("VMess/VLESS 节点必须填写 UUID")
	}
	return nil
}

func proxyLocalPortForSave(ctx context.Context, db *sql.DB, id int) (int, error) {
	if id > 0 {
		var port int
		if err := db.QueryRowContext(ctx, `SELECT local_port FROM proxy_nodes WHERE id = $1`, id).Scan(&port); err == nil && port > 0 {
			return port, nil
		}
	}
	used, err := loadUsedProxyLocalPorts(ctx, db)
	if err != nil {
		return 0, err
	}
	return nextProxyLocalPort(used)
}

type proxyLocalPortQuerier interface {
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
}

func loadUsedProxyLocalPorts(ctx context.Context, querier proxyLocalPortQuerier) (map[int]bool, error) {
	used := map[int]bool{}
	rows, err := querier.QueryContext(ctx, `SELECT local_port FROM proxy_nodes WHERE local_port > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var port int
		if err := rows.Scan(&port); err == nil && port > 0 {
			used[port] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return used, nil
}

func nextProxyLocalPort(used map[int]bool) (int, error) {
	for port := 31001; port <= 31999; port++ {
		if !used[port] {
			used[port] = true
			return port, nil
		}
	}
	return 0, fmt.Errorf("可用本地代理端口不足")
}

func updateProxyNodeTestStatus(ctx context.Context, db *sql.DB, id int, status string, reason string, latency int) {
	_, _ = db.ExecContext(ctx, `
UPDATE proxy_nodes
SET status = $2, status_reason = $3, latency_ms = $4, last_tested_at = NOW(), updated_at = NOW()
WHERE id = $1
`, id, status, strings.TrimSpace(reason), latency)
}

func loadProxySettings(ctx context.Context) (proxySettingsResponse, error) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return proxySettingsResponse{}, err
	}
	defer db.Close()
	result := proxySettingsResponse{}
	settings := map[string]proxySetting{}
	rows, err := db.QueryContext(ctx, `SELECT scope, enabled, proxy_node_id, updated_at FROM proxy_settings`)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item proxySetting
		var updatedAt time.Time
		if err := rows.Scan(&item.Scope, &item.Enabled, &item.ProxyNodeID, &updatedAt); err != nil {
			return result, err
		}
		item.UpdatedAt = updatedAt.Format(time.RFC3339)
		settings[item.Scope] = item
	}
	result.IMAP = settings[proxyScopeIMAP]
	result.Outlook = settings[proxyScopeOutlook]
	if result.IMAP.Scope == "" {
		result.IMAP.Scope = proxyScopeIMAP
	}
	if result.Outlook.Scope == "" {
		result.Outlook.Scope = proxyScopeOutlook
	}
	return result, rows.Err()
}

func saveProxySettings(ctx context.Context, req updateProxySettingsRequest) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()
	for _, item := range []struct {
		scope string
		input proxySettingInput
	}{
		{proxyScopeIMAP, req.IMAP},
		{proxyScopeOutlook, req.Outlook},
	} {
		if item.input.Enabled {
			if item.input.ProxyNodeID <= 0 {
				return fmt.Errorf("%s 代理未选择节点", item.scope)
			}
			if _, err := getProxyNode(ctx, db, item.input.ProxyNodeID); err != nil {
				return fmt.Errorf("代理节点不存在")
			}
		}
		if _, err := db.ExecContext(ctx, `
INSERT INTO proxy_settings (scope, enabled, proxy_node_id, updated_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (scope) DO UPDATE SET enabled = EXCLUDED.enabled, proxy_node_id = EXCLUDED.proxy_node_id, updated_at = NOW()
`, item.scope, item.input.Enabled, item.input.ProxyNodeID); err != nil {
			return err
		}
	}
	return nil
}

func validateProxySettingsForUpdate(ctx context.Context, runtime *proxyRuntime, req updateProxySettingsRequest) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	existingSettings := map[string]proxySettingInput{}
	rows, err := db.QueryContext(ctx, `SELECT scope, enabled, proxy_node_id FROM proxy_settings`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var scope string
		var setting proxySettingInput
		if err := rows.Scan(&scope, &setting.Enabled, &setting.ProxyNodeID); err != nil {
			_ = rows.Close()
			return err
		}
		existingSettings[scope] = setting
	}
	if err := rows.Close(); err != nil {
		return err
	}

	type proxySettingCheck struct {
		scope string
		input proxySettingInput
		node  proxyNode
		test  bool
	}

	checks := []proxySettingCheck{}
	for _, item := range []struct {
		scope string
		input proxySettingInput
	}{
		{proxyScopeIMAP, req.IMAP},
		{proxyScopeOutlook, req.Outlook},
	} {
		check := proxySettingCheck{
			scope: item.scope,
			input: item.input,
		}
		if item.input.Enabled {
			if item.input.ProxyNodeID <= 0 {
				return fmt.Errorf("%s 代理未选择节点", item.scope)
			}
			node, err := getProxyNode(ctx, db, item.input.ProxyNodeID)
			if err != nil {
				return fmt.Errorf("代理节点不存在")
			}
			if !node.Enabled {
				return fmt.Errorf("%s 代理节点已停用", item.scope)
			}
			check.node = node
			existing := existingSettings[item.scope]
			check.test = !existing.Enabled || existing.ProxyNodeID != item.input.ProxyNodeID
		}
		checks = append(checks, check)
	}

	testChecks := []proxySettingCheck{}
	extraNodeIDs := []int{}
	for _, check := range checks {
		if check.input.Enabled && check.test {
			testChecks = append(testChecks, check)
			extraNodeIDs = append(extraNodeIDs, check.node.ID)
		}
	}
	if len(testChecks) == 0 {
		return nil
	}

	if err := runtime.ensureForSelected(ctx, extraNodeIDs...); err != nil {
		for _, check := range testChecks {
			if isXrayProxyProtocol(check.node.Protocol) {
				updateProxyNodeTestStatus(ctx, db, check.node.ID, "error", err.Error(), 0)
			}
		}
		return fmt.Errorf("%s 代理节点测试失败: %s", testChecks[0].scope, err.Error())
	}

	tested := map[int]error{}
	for _, check := range testChecks {
		if err, ok := tested[check.node.ID]; ok {
			if err != nil {
				return fmt.Errorf("%s 代理节点测试失败: %s", check.scope, err.Error())
			}
			continue
		}

		start := time.Now()
		endpoint, err := proxyEndpointForNode(ctx, check.node)
		if err == nil {
			err = testProxyEndpoint(ctx, endpoint)
		}
		if err != nil {
			updateProxyNodeTestStatus(ctx, db, check.node.ID, "error", err.Error(), 0)
			tested[check.node.ID] = err
			return fmt.Errorf("%s 代理节点测试失败: %s", check.scope, err.Error())
		}

		latency := int(time.Since(start).Milliseconds())
		updateProxyNodeTestStatus(ctx, db, check.node.ID, "normal", "", latency)
		tested[check.node.ID] = nil
	}
	return nil
}

func activeProxyEndpoint(ctx context.Context, scope string) (proxyEndpoint, bool, error) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return proxyEndpoint{}, false, err
	}
	defer db.Close()
	var enabled bool
	var nodeID int
	if err := db.QueryRowContext(ctx, `SELECT enabled, proxy_node_id FROM proxy_settings WHERE scope = $1`, scope).Scan(&enabled, &nodeID); err != nil {
		return proxyEndpoint{}, false, err
	}
	if !enabled || nodeID <= 0 {
		return proxyEndpoint{}, false, nil
	}
	node, err := getProxyNode(ctx, db, nodeID)
	if err != nil {
		return proxyEndpoint{}, false, err
	}
	if !node.Enabled {
		return proxyEndpoint{}, false, fmt.Errorf("代理节点已停用")
	}
	if err := defaultProxyRuntime.ensureForSelected(ctx); err != nil {
		return proxyEndpoint{}, false, err
	}
	endpoint, err := proxyEndpointForNode(ctx, node)
	if err != nil {
		return proxyEndpoint{}, false, err
	}
	return endpoint, true, nil
}

func proxyEndpointForNode(ctx context.Context, node proxyNode) (proxyEndpoint, error) {
	if isXrayProxyProtocol(node.Protocol) {
		if node.LocalPort <= 0 {
			return proxyEndpoint{}, fmt.Errorf("VMess/VLESS 节点缺少本地端口")
		}
		return proxyEndpoint{Protocol: proxyProtocolSOCKS5, Address: "127.0.0.1", Port: node.LocalPort, NodeID: node.ID}, nil
	}
	return proxyEndpoint{Protocol: node.Protocol, Address: node.Address, Port: node.Port, Username: node.Username, Password: node.Password, NodeID: node.ID}, nil
}

func dialTCPWithProxy(ctx context.Context, scope string, host string, port int, timeout time.Duration) (net.Conn, error) {
	host = strings.TrimSpace(host)
	if host == "" || port <= 0 {
		return nil, fmt.Errorf("地址或端口为空")
	}
	endpoint, ok, err := activeProxyEndpoint(ctx, scope)
	if err != nil {
		return nil, err
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	if !ok {
		dialer := &net.Dialer{Timeout: timeout}
		return dialer.DialContext(ctx, "tcp", address)
	}
	return dialTCPThroughEndpoint(ctx, endpoint, "tcp", address, timeout)
}

func dialTCPThroughEndpoint(ctx context.Context, endpoint proxyEndpoint, network string, address string, timeout time.Duration) (net.Conn, error) {
	switch endpoint.Protocol {
	case proxyProtocolSOCKS5:
		auth := &xnetproxy.Auth{User: endpoint.Username, Password: endpoint.Password}
		if endpoint.Username == "" && endpoint.Password == "" {
			auth = nil
		}
		base := &contextProxyDialer{ctx: ctx, timeout: timeout}
		dialer, err := xnetproxy.SOCKS5("tcp", net.JoinHostPort(endpoint.Address, strconv.Itoa(endpoint.Port)), auth, base)
		if err != nil {
			return nil, err
		}
		return dialer.Dial(network, address)
	case proxyProtocolHTTP:
		return dialHTTPConnectProxy(ctx, endpoint, address, timeout)
	default:
		return nil, fmt.Errorf("不支持的代理协议: %s", endpoint.Protocol)
	}
}

type contextProxyDialer struct {
	ctx     context.Context
	timeout time.Duration
}

func (d *contextProxyDialer) Dial(network string, address string) (net.Conn, error) {
	ctx := d.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	dialer := &net.Dialer{Timeout: d.timeout}
	return dialer.DialContext(ctx, network, address)
}

func dialHTTPConnectProxy(ctx context.Context, endpoint proxyEndpoint, target string, timeout time.Duration) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(endpoint.Address, strconv.Itoa(endpoint.Port)))
	if err != nil {
		return nil, err
	}
	request := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if endpoint.Username != "" || endpoint.Password != "" {
		token := base64.StdEncoding.EncodeToString([]byte(endpoint.Username + ":" + endpoint.Password))
		request += "Proxy-Authorization: Basic " + token + "\r\n"
	}
	request += "\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		_ = conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = conn.Close()
		return nil, fmt.Errorf("HTTP 代理 CONNECT 失败: %s", resp.Status)
	}
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func httpClientForProxyScope(ctx context.Context, scope string) (*http.Client, error) {
	endpoint, ok, err := activeProxyEndpoint(ctx, scope)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if ok {
		if endpoint.Protocol == proxyProtocolHTTP {
			proxyURL := &url.URL{Scheme: "http", Host: net.JoinHostPort(endpoint.Address, strconv.Itoa(endpoint.Port))}
			if endpoint.Username != "" || endpoint.Password != "" {
				proxyURL.User = url.UserPassword(endpoint.Username, endpoint.Password)
			}
			transport.Proxy = http.ProxyURL(proxyURL)
		} else {
			transport.Proxy = nil
			transport.DialContext = func(ctx context.Context, network string, address string) (net.Conn, error) {
				return dialTCPThroughEndpoint(ctx, endpoint, network, address, 15*time.Second)
			}
		}
	}
	return &http.Client{Transport: transport, Timeout: 60 * time.Second}, nil
}

func doOutlookHTTPRequest(ctx context.Context, req *http.Request) (*http.Response, error) {
	client, err := httpClientForProxyScope(ctx, proxyScopeOutlook)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

func testProxyEndpoint(ctx context.Context, endpoint proxyEndpoint) error {
	target := strings.TrimSpace(os.Getenv("PROXY_TEST_TARGET"))
	if target == "" {
		target = "www.microsoft.com:443"
	}
	host, port, err := splitProxyTestTarget(target)
	if err != nil {
		return err
	}
	target = net.JoinHostPort(host, port)
	conn, err := dialTCPThroughEndpoint(ctx, endpoint, "tcp", target, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if port != "443" {
		return nil
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	tlsConn := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = tlsConn.Close()
		return fmt.Errorf("TLS 握手失败: %w", err)
	}
	_ = tlsConn.Close()
	return nil
}

func splitProxyTestTarget(target string) (string, string, error) {
	host, port, err := net.SplitHostPort(target)
	if err == nil {
		if strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
			return "", "", fmt.Errorf("代理测试目标无效")
		}
		return strings.Trim(host, "[]"), port, nil
	}
	if strings.Contains(target, "://") {
		u, parseErr := url.Parse(target)
		if parseErr != nil || u.Hostname() == "" {
			return "", "", fmt.Errorf("代理测试目标无效")
		}
		port = u.Port()
		if port == "" {
			port = "443"
		}
		return u.Hostname(), port, nil
	}
	if strings.Contains(target, ":") {
		return "", "", fmt.Errorf("代理测试目标无效，请使用 host:port")
	}
	return target, "443", nil
}

func (r *proxyRuntime) ensureForSelected(ctx context.Context, extraNodeIDs ...int) error {
	nodes, err := selectedXrayNodes(ctx, extraNodeIDs...)
	if err != nil {
		r.setLastError(err)
		return err
	}
	if len(nodes) == 0 {
		r.stop()
		return nil
	}
	config, err := buildXrayConfig(nodes)
	if err != nil {
		r.setLastError(err)
		return err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(config))

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil && r.configHash == hash && r.processRunningLocked() {
		return nil
	}
	r.stopLocked()

	bin, err := xrayBinaryPath()
	if err != nil {
		r.lastError = err.Error()
		return err
	}
	configPath, err := writeXrayConfig(config)
	if err != nil {
		r.lastError = err.Error()
		return err
	}
	cmd := exec.CommandContext(context.Background(), bin, "run", "-config", configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		r.lastError = err.Error()
		return err
	}
	r.cmd = cmd
	r.configPath = configPath
	r.configHash = hash
	r.lastError = ""
	r.startedAt = time.Now()
	go func() {
		_ = cmd.Wait()
	}()
	time.Sleep(250 * time.Millisecond)
	if !r.processRunningLocked() {
		err := fmt.Errorf("xray 进程启动后立即退出")
		r.lastError = err.Error()
		return err
	}
	return nil
}

func (r *proxyRuntime) setLastError(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastError = err.Error()
}

func (r *proxyRuntime) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
}

func (r *proxyRuntime) stopLocked() {
	if r.cmd != nil && r.cmd.Process != nil && r.processRunningLocked() {
		_ = r.cmd.Process.Kill()
	}
	r.cmd = nil
	r.configHash = ""
	r.configPath = ""
	r.lastError = ""
	r.startedAt = time.Time{}
}

func (r *proxyRuntime) processRunningLocked() bool {
	return r.cmd != nil && r.cmd.Process != nil && r.cmd.ProcessState == nil
}

func (r *proxyRuntime) snapshot() gin.H {
	r.mu.Lock()
	defer r.mu.Unlock()
	bin, binErr := xrayBinaryPath()
	data := gin.H{
		"running":     r.processRunningLocked(),
		"config_path": r.configPath,
		"last_error":  r.lastError,
		"platform":    runtime.GOOS + "-" + runtime.GOARCH,
		"xray_bin":    bin,
	}
	if !r.startedAt.IsZero() {
		data["started_at"] = r.startedAt.Format(time.RFC3339)
	}
	if binErr != nil {
		data["xray_error"] = binErr.Error()
	}
	return data
}

func selectedXrayNodes(ctx context.Context, extraNodeIDs ...int) ([]proxyNode, error) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ids := map[int]bool{}
	rows, err := db.QueryContext(ctx, `SELECT proxy_node_id FROM proxy_settings WHERE enabled = TRUE AND proxy_node_id > 0`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil && id > 0 {
			ids[id] = true
		}
	}
	_ = rows.Close()
	for _, id := range extraNodeIDs {
		if id > 0 {
			ids[id] = true
		}
	}
	nodes := []proxyNode{}
	for id := range ids {
		node, err := getProxyNode(ctx, db, id)
		if err != nil || !node.Enabled || !isXrayProxyProtocol(node.Protocol) {
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func buildXrayConfig(nodes []proxyNode) ([]byte, error) {
	inbounds := []map[string]interface{}{}
	outbounds := []map[string]interface{}{}
	rules := []map[string]interface{}{}
	for _, node := range nodes {
		if node.LocalPort <= 0 {
			return nil, fmt.Errorf("节点 %s 缺少本地端口", node.Name)
		}
		inTag := fmt.Sprintf("in-%d", node.ID)
		outTag := fmt.Sprintf("out-%d", node.ID)
		inbounds = append(inbounds, map[string]interface{}{
			"tag":      inTag,
			"listen":   "127.0.0.1",
			"port":     node.LocalPort,
			"protocol": "socks",
			"settings": map[string]interface{}{"auth": "noauth", "udp": true},
		})
		outbound, err := xrayOutboundForNode(node, outTag)
		if err != nil {
			return nil, err
		}
		outbounds = append(outbounds, outbound)
		rules = append(rules, map[string]interface{}{"type": "field", "inboundTag": []string{inTag}, "outboundTag": outTag})
	}
	outbounds = append(outbounds, map[string]interface{}{"tag": "direct", "protocol": "freedom"})
	config := map[string]interface{}{
		"log":       map[string]interface{}{"loglevel": "warning"},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"routing":   map[string]interface{}{"rules": rules},
	}
	return json.MarshalIndent(config, "", "  ")
}

func xrayOutboundForNode(node proxyNode, tag string) (map[string]interface{}, error) {
	stream := xrayStreamSettings(node)
	if node.Protocol == proxyProtocolVMess {
		user := map[string]interface{}{"id": node.UUID, "alterId": node.AlterID, "security": valueOrDefault(node.Security, "auto")}
		return map[string]interface{}{
			"tag":      tag,
			"protocol": "vmess",
			"settings": map[string]interface{}{
				"vnext": []map[string]interface{}{{"address": node.Address, "port": node.Port, "users": []map[string]interface{}{user}}},
			},
			"streamSettings": stream,
		}, nil
	}
	if node.Protocol == proxyProtocolVLESS {
		user := map[string]interface{}{"id": node.UUID, "encryption": valueOrDefault(node.Encryption, "none")}
		if node.Flow != "" {
			user["flow"] = node.Flow
		}
		return map[string]interface{}{
			"tag":      tag,
			"protocol": "vless",
			"settings": map[string]interface{}{
				"vnext": []map[string]interface{}{{"address": node.Address, "port": node.Port, "users": []map[string]interface{}{user}}},
			},
			"streamSettings": stream,
		}, nil
	}
	return nil, fmt.Errorf("不支持的 xray 协议: %s", node.Protocol)
}

func xrayStreamSettings(node proxyNode) map[string]interface{} {
	network := valueOrDefault(strings.ToLower(node.Transport), "tcp")
	security := strings.ToLower(node.TLS)
	if security == "none" || security == "false" {
		security = ""
	}
	stream := map[string]interface{}{"network": network}
	if security != "" {
		stream["security"] = security
	}
	if security == "tls" {
		tlsSettings := map[string]interface{}{}
		if node.SNI != "" {
			tlsSettings["serverName"] = node.SNI
		}
		if node.Fingerprint != "" {
			tlsSettings["fingerprint"] = node.Fingerprint
		}
		stream["tlsSettings"] = tlsSettings
	}
	if security == "reality" {
		reality := map[string]interface{}{}
		if node.SNI != "" {
			reality["serverName"] = node.SNI
		}
		if node.Fingerprint != "" {
			reality["fingerprint"] = node.Fingerprint
		}
		if node.PublicKey != "" {
			reality["publicKey"] = node.PublicKey
		}
		if node.ShortID != "" {
			reality["shortId"] = node.ShortID
		}
		if node.SpiderX != "" {
			reality["spiderX"] = node.SpiderX
		}
		stream["realitySettings"] = reality
	}
	if network == "ws" {
		headers := map[string]interface{}{}
		if node.HostHeader != "" {
			headers["Host"] = node.HostHeader
		}
		stream["wsSettings"] = map[string]interface{}{"path": valueOrDefault(node.Path, "/"), "headers": headers}
	}
	if network == "grpc" {
		stream["grpcSettings"] = map[string]interface{}{"serviceName": strings.TrimPrefix(node.Path, "/")}
	}
	return stream
}

func writeXrayConfig(config []byte) (string, error) {
	dir := filepath.Join(os.TempDir(), "mail-admin-xray")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "config.json")
	return path, os.WriteFile(path, config, 0600)
}

func xrayBinaryPath() (string, error) {
	if value := strings.TrimSpace(os.Getenv("XRAY_BIN")); value != "" {
		if fileExists(value) {
			return value, nil
		}
		return value, fmt.Errorf("XRAY_BIN 指向的文件不存在: %s", value)
	}
	platform, err := xrayPlatformDir()
	if err != nil {
		return "", err
	}
	name := "xray"
	if runtime.GOOS == "windows" {
		name = "xray.exe"
	}
	path := filepath.Join(".", "bin", "xray", platform, name)
	if fileExists(path) {
		return path, nil
	}
	abs, _ := filepath.Abs(path)
	return abs, fmt.Errorf("未找到当前平台的 xray 核心，请放置到 %s，或设置 XRAY_BIN", abs)
}

func xrayPlatformDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		if runtime.GOARCH == "amd64" {
			return "windows-amd64", nil
		}
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			return "linux-amd64", nil
		case "arm64":
			return "linux-arm64", nil
		case "arm":
			return "linux-armv7", nil
		}
	}
	return "", fmt.Errorf("当前平台暂未内置 xray 路径: %s-%s", runtime.GOOS, runtime.GOARCH)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func normalizeProxyShareLink(raw string) string {
	value := strings.TrimSpace(raw)
	wrappers := [][2]string{
		{"(", ")"},
		{"（", "）"},
		{"[", "]"},
		{"【", "】"},
		{"<", ">"},
		{"《", "》"},
		{"\"", "\""},
		{"'", "'"},
	}
	changed := true
	for changed {
		changed = false
		for _, wrapper := range wrappers {
			if strings.HasPrefix(value, wrapper[0]) && strings.HasSuffix(value, wrapper[1]) {
				value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, wrapper[0]), wrapper[1]))
				changed = true
			}
		}
	}
	return value
}

func unescapeProxyURLPart(value string) string {
	unescaped, err := url.PathUnescape(value)
	if err != nil {
		return value
	}
	return unescaped
}

func decodePlainProxyAuth(username string, password string) (string, string) {
	if username == "" || password != "" {
		return username, password
	}
	data, err := decodeBase64Loose(username)
	if err != nil {
		return username, password
	}
	decoded := string(data)
	separatorIndex := strings.Index(decoded, ":")
	if separatorIndex < 0 {
		return username, password
	}
	return decoded[:separatorIndex], decoded[separatorIndex+1:]
}

func parseProxyShareLink(raw string) (saveProxyNodeRequest, error) {
	raw = normalizeProxyShareLink(raw)
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "vmess://"):
		return parseVMessLink(raw)
	case strings.HasPrefix(lower, "vless://"):
		return parseVLESSLink(raw)
	case strings.HasPrefix(lower, "socks5://"), strings.HasPrefix(lower, "socks://"), strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		return parsePlainProxyURL(raw)
	default:
		return saveProxyNodeRequest{}, fmt.Errorf("不支持的代理链接")
	}
}

func parseVMessLink(raw string) (saveProxyNodeRequest, error) {
	body := strings.TrimPrefix(raw, "vmess://")
	data, err := decodeBase64Loose(body)
	if err != nil {
		return saveProxyNodeRequest{}, fmt.Errorf("VMess 链接解析失败")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return saveProxyNodeRequest{}, fmt.Errorf("VMess 配置不是有效 JSON")
	}
	port, _ := strconv.Atoi(fmt.Sprint(payload["port"]))
	alterID, _ := strconv.Atoi(fmt.Sprint(payload["aid"]))
	tlsValue := strings.TrimSpace(fmt.Sprint(payload["tls"]))
	req := saveProxyNodeRequest{
		Name:        mapStringValue(payload, "ps"),
		Protocol:    proxyProtocolVMess,
		Address:     mapStringValue(payload, "add"),
		Port:        port,
		UUID:        mapStringValue(payload, "id"),
		AlterID:     alterID,
		Security:    valueOrDefault(mapStringValue(payload, "scy"), "auto"),
		Transport:   valueOrDefault(mapStringValue(payload, "net"), "tcp"),
		TLS:         tlsValue,
		SNI:         mapStringValue(payload, "sni"),
		Path:        mapStringValue(payload, "path"),
		HostHeader:  mapStringValue(payload, "host"),
		Fingerprint: mapStringValue(payload, "fp"),
	}
	return req, nil
}

func parseVLESSLink(raw string) (saveProxyNodeRequest, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return saveProxyNodeRequest{}, fmt.Errorf("VLESS 链接解析失败")
	}
	port, _ := strconv.Atoi(u.Port())
	query := u.Query()
	req := saveProxyNodeRequest{
		Name:        strings.TrimSpace(u.Fragment),
		Protocol:    proxyProtocolVLESS,
		Address:     u.Hostname(),
		Port:        port,
		UUID:        u.User.Username(),
		Encryption:  valueOrDefault(query.Get("encryption"), "none"),
		Transport:   valueOrDefault(query.Get("type"), "tcp"),
		TLS:         query.Get("security"),
		SNI:         query.Get("sni"),
		Path:        query.Get("path"),
		HostHeader:  query.Get("host"),
		Flow:        query.Get("flow"),
		Fingerprint: query.Get("fp"),
		PublicKey:   query.Get("pbk"),
		ShortID:     query.Get("sid"),
		SpiderX:     query.Get("spx"),
	}
	return req, nil
}

func parsePlainProxyURL(raw string) (saveProxyNodeRequest, error) {
	raw = normalizeProxyShareLink(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return saveProxyNodeRequest{}, fmt.Errorf("代理链接解析失败")
	}
	port, _ := strconv.Atoi(u.Port())
	protocol := normalizeProxyProtocol(u.Scheme)
	username := ""
	password := ""
	if u.User != nil {
		username = unescapeProxyURLPart(u.User.Username())
		password, _ = u.User.Password()
		password = unescapeProxyURLPart(password)
	}
	if protocol == proxyProtocolSOCKS5 {
		username, password = decodePlainProxyAuth(username, password)
	}
	return saveProxyNodeRequest{
		Name:     strings.TrimSpace(u.Fragment),
		Protocol: protocol,
		Address:  u.Hostname(),
		Port:     port,
		Username: username,
		Password: password,
	}, nil
}

func mergeProxyRequest(parsed saveProxyNodeRequest, override saveProxyNodeRequest) saveProxyNodeRequest {
	importURL := override.ImportURL
	if strings.TrimSpace(override.Name) != "" {
		parsed.Name = override.Name
	}
	if strings.TrimSpace(override.Protocol) != "" {
		parsed.Protocol = override.Protocol
	}
	if strings.TrimSpace(override.Address) != "" {
		parsed.Address = override.Address
	}
	if override.Port > 0 {
		parsed.Port = override.Port
	}
	if strings.TrimSpace(override.Username) != "" {
		parsed.Username = override.Username
	}
	if strings.TrimSpace(override.Password) != "" {
		parsed.Password = override.Password
	}
	if strings.TrimSpace(override.UUID) != "" {
		parsed.UUID = override.UUID
	}
	if override.AlterID > 0 {
		parsed.AlterID = override.AlterID
	}
	if strings.TrimSpace(override.Security) != "" {
		parsed.Security = override.Security
	}
	if strings.TrimSpace(override.Encryption) != "" {
		parsed.Encryption = override.Encryption
	}
	if strings.TrimSpace(override.Transport) != "" {
		parsed.Transport = override.Transport
	}
	if strings.TrimSpace(override.TLS) != "" {
		parsed.TLS = override.TLS
	}
	if strings.TrimSpace(override.SNI) != "" {
		parsed.SNI = override.SNI
	}
	if strings.TrimSpace(override.Path) != "" {
		parsed.Path = override.Path
	}
	if strings.TrimSpace(override.HostHeader) != "" {
		parsed.HostHeader = override.HostHeader
	}
	if strings.TrimSpace(override.Flow) != "" {
		parsed.Flow = override.Flow
	}
	if strings.TrimSpace(override.Fingerprint) != "" {
		parsed.Fingerprint = override.Fingerprint
	}
	if strings.TrimSpace(override.PublicKey) != "" {
		parsed.PublicKey = override.PublicKey
	}
	if strings.TrimSpace(override.ShortID) != "" {
		parsed.ShortID = override.ShortID
	}
	if strings.TrimSpace(override.SpiderX) != "" {
		parsed.SpiderX = override.SpiderX
	}
	if override.Enabled != nil {
		parsed.Enabled = override.Enabled
	}
	if strings.TrimSpace(override.Remark) != "" {
		parsed.Remark = override.Remark
	}
	parsed.ImportURL = importURL
	return parsed
}

func decodeBase64Loose(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\n", "")
	value = strings.ReplaceAll(value, "\r", "")
	if data, err := base64.StdEncoding.DecodeString(value); err == nil {
		return data, nil
	}
	if data, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return data, nil
	}
	if data, err := base64.URLEncoding.DecodeString(value); err == nil {
		return data, nil
	}
	return base64.RawURLEncoding.DecodeString(value)
}

func normalizeProxyProtocol(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "socks", "socks5":
		return proxyProtocolSOCKS5
	case "https", "http":
		return proxyProtocolHTTP
	case "vmess":
		return proxyProtocolVMess
	case "vless":
		return proxyProtocolVLESS
	default:
		return value
	}
}

func isSupportedProxyProtocol(value string) bool {
	switch normalizeProxyProtocol(value) {
	case proxyProtocolHTTP, proxyProtocolSOCKS5, proxyProtocolVMess, proxyProtocolVLESS:
		return true
	default:
		return false
	}
}

func isXrayProxyProtocol(value string) bool {
	value = normalizeProxyProtocol(value)
	return value == proxyProtocolVMess || value == proxyProtocolVLESS
}

func mapStringValue(values map[string]interface{}, key string) string {
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

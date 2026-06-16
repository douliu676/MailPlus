package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	stdhtml "html"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	xhtml "golang.org/x/net/html"
)

const (
	publicMailCooldownSeconds  = 40
	publicMailFetchLimit       = 50
	publicMailReturnLimit      = 20
	publicMailOutlookScanLimit = 500
)

type publicMailInfoResponse struct {
	Key             string `json:"key"`
	Status          string `json:"status"`
	BoundEmail      string `json:"bound_email"`
	HasBoundEmail   bool   `json:"has_bound_email"`
	UsageLimit      int    `json:"usage_limit"`
	UsedCount       int    `json:"used_count"`
	Remaining       int    `json:"remaining"`
	MailDays        int    `json:"mail_days"`
	MailKeyword     string `json:"mail_keyword"`
	CooldownSeconds int    `json:"cooldown_seconds"`
}

type publicMailMessagesRequest struct {
	Email string `json:"email"`
}

type publicMailMessage struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Folder      string `json:"folder"`
	UID         int    `json:"-"`
	Mailbox     string `json:"-"`
	RemoteID    string `json:"-"`
	Subject     string `json:"subject"`
	From        string `json:"from"`
	To          string `json:"to"`
	Time        string `json:"time"`
	Timestamp   int64  `json:"timestamp"`
	BodyPreview string `json:"body_preview"`
	Body        string `json:"body"`
	HTML        string `json:"html"`
}

type publicMailMessagesResponse struct {
	Email           string              `json:"email"`
	Messages        []publicMailMessage `json:"messages"`
	MessageItem     *publicMailMessage  `json:"message_item"`
	Charged         bool                `json:"charged"`
	Repeated        bool                `json:"repeated"`
	UsedCount       int                 `json:"used_count"`
	Remaining       int                 `json:"remaining"`
	WaitSeconds     int                 `json:"wait_seconds"`
	CooldownSeconds int                 `json:"cooldown_seconds"`
	Message         string              `json:"message"`
}

type publicCardKey struct {
	ID          int
	Key         string
	Status      string
	UsageLimit  int
	MailDays    int
	MailKeyword string
	BoundEmail  string
}

type publicMailAccessRecord struct {
	LastRequestAt  sql.NullTime
	LastMessageKey string
	ChargedCount   int
}

func ensurePublicMailTables(ctx context.Context) error {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err = db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS card_key_mail_access_records (
	id SERIAL PRIMARY KEY,
	card_key_id INTEGER NOT NULL REFERENCES card_keys(id) ON DELETE CASCADE,
	email TEXT NOT NULL,
	client_key TEXT NOT NULL,
	client_ip TEXT NOT NULL DEFAULT '',
	last_request_at TIMESTAMPTZ,
	last_message_key TEXT NOT NULL DEFAULT '',
	charged_count INTEGER NOT NULL DEFAULT 0,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	UNIQUE(card_key_id, email, client_key)
)
`); err != nil {
		return err
	}

	statements := []string{
		`ALTER TABLE card_key_mail_access_records ADD COLUMN IF NOT EXISTS client_ip TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE card_key_mail_access_records ADD COLUMN IF NOT EXISTS last_request_at TIMESTAMPTZ`,
		`ALTER TABLE card_key_mail_access_records ADD COLUMN IF NOT EXISTS last_message_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE card_key_mail_access_records ADD COLUMN IF NOT EXISTS charged_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE card_key_mail_access_records ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`,
		`ALTER TABLE card_key_mail_access_records ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`,
		`CREATE INDEX IF NOT EXISTS card_key_mail_access_card_idx ON card_key_mail_access_records (card_key_id)`,
		`CREATE INDEX IF NOT EXISTS card_key_mail_access_lookup_idx ON card_key_mail_access_records (card_key_id, email, client_key)`,
		`CREATE INDEX IF NOT EXISTS card_key_mail_access_request_idx ON card_key_mail_access_records (last_request_at)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *appState) getPublicMailInfo(c *gin.Context) {
	card, ok := s.loadPublicCardKeyForRequest(c)
	if !ok {
		return
	}
	usedCount, err := publicMailUsedCount(c.Request.Context(), card.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "查询卡密失败"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: publicMailInfo(card, usedCount), Msg: "ok"})
}

func (s *appState) getPublicMailMessages(c *gin.Context) {
	var req publicMailMessagesRequest
	_ = c.ShouldBindJSON(&req)
	response, status, message, data, ok := s.fetchPublicMailMessagesForRequest(c, req.Email)
	if !ok {
		result := apiResponse{Code: status, Msg: message}
		if data != nil {
			result.Data = data
		}
		c.JSON(status, result)
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: response, Msg: "ok"})
}

func (s *appState) getPublicMailAll(c *gin.Context) {
	if publicMailPlainRequested(c) {
		s.getPublicMailPlain(c)
		return
	}
	s.streamPublicMailPage(c)
}

func (s *appState) getPublicMailPlain(c *gin.Context) {
	response, status, message, _, ok := s.fetchPublicMailMessagesForRequest(c, publicMailRequestEmail(c))
	if !ok {
		writePublicMailPlain(c, status, message)
		return
	}
	writePublicMailPlain(c, http.StatusOK, publicMailResponsePlainText(response))
}

func (s *appState) fetchPublicMailMessagesForRequest(c *gin.Context, requestedEmail string) (publicMailMessagesResponse, int, string, gin.H, bool) {
	card, status, message, ok := loadPublicCardKeyForGinRequest(c)
	if !ok {
		return publicMailMessagesResponse{}, status, message, nil, false
	}

	email := strings.ToLower(strings.TrimSpace(requestedEmail))
	if card.BoundEmail != "" {
		if email != "" && !strings.EqualFold(email, card.BoundEmail) {
			return publicMailMessagesResponse{}, http.StatusBadRequest, "链接中的邮箱与卡密绑定邮箱不匹配", nil, false
		}
		email = card.BoundEmail
	}
	if email == "" || !strings.Contains(email, "@") {
		return publicMailMessagesResponse{}, http.StatusBadRequest, "请输入邮箱地址", nil, false
	}

	clientKey := publicMailClientKey(c, email)
	clientIP := c.ClientIP()

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return publicMailMessagesResponse{}, http.StatusInternalServerError, "获取邮件失败", nil, false
	}
	defer db.Close()

	ctx := c.Request.Context()
	record, err := getPublicMailAccessRecord(ctx, db, card.ID, email, clientKey)
	if err != nil {
		return publicMailMessagesResponse{}, http.StatusInternalServerError, "获取邮件失败", nil, false
	}
	if wait := publicMailCooldownWait(record.LastRequestAt); wait > 0 {
		return publicMailMessagesResponse{}, http.StatusTooManyRequests, fmt.Sprintf("请等待 %d 秒后再试", wait), gin.H{"wait_seconds": wait}, false
	}
	if err := touchPublicMailAccessRecord(ctx, db, card.ID, email, clientKey, clientIP); err != nil {
		return publicMailMessagesResponse{}, http.StatusInternalServerError, "获取邮件失败", nil, false
	}

	usedCount, err := publicMailUsedCountWithDB(ctx, db, card.ID)
	if err != nil {
		return publicMailMessagesResponse{}, http.StatusInternalServerError, "获取邮件失败", nil, false
	}
	if publicMailRemaining(card, usedCount) <= 0 {
		return publicMailMessagesResponse{}, http.StatusForbidden, "卡密使用次数已用完", nil, false
	}

	messages, err := fetchPublicMailMessages(ctx, db, card, email)
	if err != nil {
		return publicMailMessagesResponse{}, http.StatusBadRequest, "获取邮件失败: " + err.Error(), nil, false
	}

	latestKey := ""
	var latestMessage *publicMailMessage
	if len(messages) > 0 {
		latestKey = messages[0].ID
		latestMessage = &messages[0]
	}
	charged := false
	repeated := latestKey != "" && latestKey == record.LastMessageKey
	if latestKey != "" && !repeated {
		usedCount, charged, err = chargePublicMailUsage(ctx, db, card, email, clientKey, latestKey, usedCount)
		if err != nil {
			return publicMailMessagesResponse{}, http.StatusForbidden, err.Error(), nil, false
		}
	}

	responseMessage := "未获取到符合条件的邮件"
	if len(messages) > 0 {
		responseMessage = "获取邮件成功"
		if repeated {
			responseMessage = "获取到同一封邮件，未扣除次数"
		} else if charged {
			responseMessage = "获取到新邮件，已扣除一次"
		}
	}
	latestSubject := ""
	if latestMessage != nil {
		latestSubject = latestMessage.Subject
	}
	_ = recordCardKeyUseAttempt(ctx, db, card.ID, card.Key, email, latestSubject, clientIP)
	return publicMailMessagesResponse{
		Email:           email,
		Messages:        messages,
		MessageItem:     latestMessage,
		Charged:         charged,
		Repeated:        repeated,
		UsedCount:       usedCount,
		Remaining:       publicMailRemaining(card, usedCount),
		WaitSeconds:     publicMailCooldownSeconds,
		CooldownSeconds: publicMailCooldownSeconds,
		Message:         responseMessage,
	}, http.StatusOK, "", nil, true
}

func (s *appState) loadPublicCardKeyForRequest(c *gin.Context) (publicCardKey, bool) {
	card, status, message, ok := loadPublicCardKeyForGinRequest(c)
	if !ok {
		c.JSON(status, apiResponse{Code: status, Msg: message})
		return publicCardKey{}, false
	}
	return card, true
}

func loadPublicCardKeyForGinRequest(c *gin.Context) (publicCardKey, int, string, bool) {
	key := publicMailRouteKey(c)
	if key == "" {
		return publicCardKey{}, http.StatusBadRequest, "卡密不能为空", false
	}
	card, err := loadPublicCardKey(c.Request.Context(), key)
	if err != nil {
		return publicCardKey{}, http.StatusNotFound, "卡密不存在", false
	}
	if strings.EqualFold(card.Status, cardKeyStatusDisabled) {
		return publicCardKey{}, http.StatusForbidden, "卡密已禁用", false
	}
	return card, http.StatusOK, "", true
}

func publicMailRouteKey(c *gin.Context) string {
	key := strings.TrimSpace(c.Param("key"))
	if decoded, err := url.PathUnescape(key); err == nil {
		key = decoded
	}
	if !strings.HasPrefix(key, "keys=") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(key, "keys="))
}

func publicMailRequestEmail(c *gin.Context) string {
	if email := strings.TrimSpace(c.Query("email")); email != "" {
		return email
	}
	email := strings.Trim(strings.TrimSpace(c.Param("email")), "/")
	if decoded, err := url.PathUnescape(email); err == nil {
		email = decoded
	}
	return strings.TrimSpace(email)
}

func publicMailPlainRequested(c *gin.Context) bool {
	if value := strings.ToLower(strings.TrimSpace(c.Query("plain"))); value == "1" || value == "true" {
		return true
	}
	accept := strings.ToLower(c.GetHeader("Accept"))
	return !strings.Contains(accept, "text/html")
}

func writePublicMailPlain(c *gin.Context, status int, text string) {
	c.Data(status, "text/plain; charset=utf-8", []byte(text))
}

func publicMailResponsePlainText(response publicMailMessagesResponse) string {
	if response.MessageItem == nil {
		return "暂无邮件..."
	}
	text := publicMailMessagePlainText(*response.MessageItem)
	if text == "" {
		return "暂无邮件..."
	}
	return text
}

func publicMailMessagePlainText(message publicMailMessage) string {
	return mailPlainMessageText(message.Subject, message.From, message.To, message.Time, message.Timestamp, message.Body, message.HTML, message.BodyPreview)
}

func mailPlainMessageText(subject string, from string, to string, displayTime string, timestamp int64, body string, html string, preview string) string {
	bodyText := mailPlainBodyText(body, html, preview)
	if bodyText == "" {
		bodyText = "暂无正文"
	}
	return mailPlainHeaderText(subject, from, to, displayTime, timestamp) + "\n\n" + bodyText
}

func mailPlainBodyText(body string, html string, preview string) string {
	for _, value := range []string{
		body,
		publicMailHTMLToText(html),
		preview,
	} {
		if text := publicMailCleanText(value); text != "" {
			return text
		}
	}
	return ""
}

func mailPlainHeaderText(subject string, from string, to string, displayTime string, timestamp int64) string {
	return strings.Join([]string{
		"标题：" + mailPlainFieldValue(subject),
		"发件人：" + mailPlainFieldValue(from),
		"收件人：" + mailPlainFieldValue(to),
		"时间：" + mailPlainFieldValue(mailPlainDisplayTime(displayTime, timestamp)),
	}, "\n")
}

func mailPlainFieldValue(value string) string {
	value = publicMailCleanText(value)
	if value == "" {
		return "-"
	}
	return value
}

func mailPlainDisplayTime(value string, timestamp int64) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	if timestamp > 0 {
		return time.Unix(timestamp, 0).Local().Format("2006-01-02 15:04:05")
	}
	return ""
}

func publicMailHTMLToText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	tokenizer := xhtml.NewTokenizer(strings.NewReader(value))
	var builder strings.Builder
	skipDepth := 0
	for {
		tokenType := tokenizer.Next()
		switch tokenType {
		case xhtml.ErrorToken:
			return publicMailCleanText(builder.String())
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			nameBytes, _ := tokenizer.TagName()
			name := strings.ToLower(string(nameBytes))
			if name == "script" || name == "style" {
				if tokenType == xhtml.StartTagToken {
					skipDepth++
				}
				continue
			}
			if skipDepth > 0 {
				continue
			}
			if name == "br" || publicMailHTMLBlockTag(name) {
				builder.WriteByte('\n')
			}
			if name == "li" {
				builder.WriteString("- ")
			}
		case xhtml.EndTagToken:
			nameBytes, _ := tokenizer.TagName()
			name := strings.ToLower(string(nameBytes))
			if name == "script" || name == "style" {
				if skipDepth > 0 {
					skipDepth--
				}
				continue
			}
			if skipDepth > 0 {
				continue
			}
			if publicMailHTMLBlockTag(name) {
				builder.WriteByte('\n')
			}
		case xhtml.TextToken:
			if skipDepth > 0 {
				continue
			}
			builder.WriteString(stdhtml.UnescapeString(string(tokenizer.Text())))
		}
	}
}

func publicMailHTMLBlockTag(name string) bool {
	switch name {
	case "address", "article", "aside", "blockquote", "div", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "li", "main", "p", "section", "table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul", "ol":
		return true
	default:
		return false
	}
}

func publicMailCleanText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Split(value, "\n")
	cleanLines := make([]string, 0, len(lines))
	lastBlank := true
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if !lastBlank {
				cleanLines = append(cleanLines, "")
			}
			lastBlank = true
			continue
		}
		cleanLines = append(cleanLines, line)
		lastBlank = false
	}
	return strings.TrimSpace(strings.Join(cleanLines, "\n"))
}

func (s *appState) streamPublicMailPage(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(http.StatusOK)
	_, _ = c.Writer.Write([]byte(publicMailLoadingPageStart()))
	c.Writer.Flush()

	response, _, message, _, ok := s.fetchPublicMailMessagesForRequest(c, publicMailRequestEmail(c))
	if ok {
		message = publicMailResponsePlainText(response)
	}
	_, _ = c.Writer.Write([]byte(publicMailResultScript(message)))
	_, _ = c.Writer.Write([]byte("</body>\n</html>"))
	c.Writer.Flush()
}

func publicMailLoadingPageStart() string {
	return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>API取件</title>
<style>
html,body{margin:0;min-height:100%;background:#fff;color:#000;font:14px/1.4 Arial,"Microsoft YaHei",sans-serif}
#state{box-sizing:border-box;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:16px;color:#334155;font-weight:700;text-align:center}
#result{box-sizing:border-box;margin:8px;white-space:pre-wrap;word-break:break-word;font:inherit}
</style>
</head>
<body>
<div id="state">正在收取邮件...</div>
<pre id="result" hidden></pre>
<script>
function show(text){
  var state = document.getElementById('state');
  var result = document.getElementById('result');
  state.hidden = true;
  result.hidden = false;
  result.textContent = text || '暂无邮件...';
}
</script>
`
}

func publicMailResultScript(text string) string {
	payload, err := json.Marshal(text)
	if err != nil {
		payload = []byte(`"获取邮件失败"`)
	}
	return "<script>show(" + string(payload) + ");</script>\n"
}

func loadPublicCardKey(ctx context.Context, key string) (publicCardKey, error) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return publicCardKey{}, err
	}
	defer db.Close()

	var card publicCardKey
	err = db.QueryRowContext(ctx, `
SELECT id, key, status, usage_limit, mail_days, mail_keyword, bound_email
FROM card_keys
WHERE key = $1
`, key).Scan(&card.ID, &card.Key, &card.Status, &card.UsageLimit, &card.MailDays, &card.MailKeyword, &card.BoundEmail)
	if card.UsageLimit <= 0 {
		card.UsageLimit = 1
	}
	if card.MailDays < 0 {
		card.MailDays = 0
	}
	card.MailKeyword = strings.TrimSpace(card.MailKeyword)
	card.BoundEmail = strings.ToLower(strings.TrimSpace(card.BoundEmail))
	return card, err
}

func publicMailInfo(card publicCardKey, usedCount int) publicMailInfoResponse {
	return publicMailInfoResponse{
		Key:             card.Key,
		Status:          card.Status,
		BoundEmail:      card.BoundEmail,
		HasBoundEmail:   card.BoundEmail != "",
		UsageLimit:      card.UsageLimit,
		UsedCount:       usedCount,
		Remaining:       publicMailRemaining(card, usedCount),
		MailDays:        card.MailDays,
		MailKeyword:     card.MailKeyword,
		CooldownSeconds: publicMailCooldownSeconds,
	}
}

func publicMailRemaining(card publicCardKey, usedCount int) int {
	remaining := card.UsageLimit - usedCount
	if remaining < 0 {
		return 0
	}
	return remaining
}

func publicMailUsedCount(ctx context.Context, cardKeyID int) (int, error) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return 0, err
	}
	defer db.Close()
	return publicMailUsedCountWithDB(ctx, db, cardKeyID)
}

func publicMailUsedCountWithDB(ctx context.Context, db *sql.DB, cardKeyID int) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(charged_count), 0) FROM card_key_mail_access_records WHERE card_key_id = $1`, cardKeyID).Scan(&count)
	return count, err
}

func getPublicMailAccessRecord(ctx context.Context, db *sql.DB, cardKeyID int, email string, clientKey string) (publicMailAccessRecord, error) {
	var record publicMailAccessRecord
	err := db.QueryRowContext(ctx, `
SELECT last_request_at, last_message_key, charged_count
FROM card_key_mail_access_records
WHERE card_key_id = $1 AND email = $2 AND client_key = $3
`, cardKeyID, email, clientKey).Scan(&record.LastRequestAt, &record.LastMessageKey, &record.ChargedCount)
	if err == sql.ErrNoRows {
		return record, nil
	}
	return record, err
}

func touchPublicMailAccessRecord(ctx context.Context, db *sql.DB, cardKeyID int, email string, clientKey string, clientIP string) error {
	_, err := db.ExecContext(ctx, `
INSERT INTO card_key_mail_access_records (card_key_id, email, client_key, client_ip, last_request_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (card_key_id, email, client_key)
DO UPDATE SET client_ip = EXCLUDED.client_ip, last_request_at = NOW(), updated_at = NOW()
`, cardKeyID, email, clientKey, clientIP)
	return err
}

func chargePublicMailUsage(ctx context.Context, db *sql.DB, card publicCardKey, email string, clientKey string, latestMessageKey string, usedCount int) (int, bool, error) {
	if usedCount >= card.UsageLimit {
		return usedCount, false, fmt.Errorf("卡密使用次数已用完")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return usedCount, false, fmt.Errorf("记录使用次数失败")
	}
	defer tx.Rollback()

	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(charged_count), 0) FROM card_key_mail_access_records WHERE card_key_id = $1`, card.ID).Scan(&usedCount); err != nil {
		return usedCount, false, fmt.Errorf("记录使用次数失败")
	}
	if usedCount >= card.UsageLimit {
		return usedCount, false, fmt.Errorf("卡密使用次数已用完")
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE card_key_mail_access_records
SET last_message_key = $4, charged_count = charged_count + 1, updated_at = NOW()
WHERE card_key_id = $1 AND email = $2 AND client_key = $3
`, card.ID, email, clientKey, latestMessageKey); err != nil {
		return usedCount, false, fmt.Errorf("记录使用次数失败")
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE card_keys
SET status = $2, used_by = $3, used_at = COALESCE(used_at, NOW()), updated_at = NOW()
WHERE id = $1
`, card.ID, cardKeyStatusUsed, email); err != nil {
		return usedCount, false, fmt.Errorf("记录使用次数失败")
	}
	if err := tx.Commit(); err != nil {
		return usedCount, false, fmt.Errorf("记录使用次数失败")
	}
	return usedCount + 1, true, nil
}

func publicMailCooldownWait(lastRequest sql.NullTime) int {
	if !lastRequest.Valid {
		return 0
	}
	elapsed := time.Since(lastRequest.Time)
	wait := publicMailCooldownSeconds - int(elapsed.Seconds())
	if wait < 0 {
		return 0
	}
	if wait == 0 {
		return 1
	}
	return wait
}

func publicMailClientKey(c *gin.Context, email string) string {
	raw := strings.Join([]string{c.ClientIP(), c.GetHeader("User-Agent"), strings.ToLower(strings.TrimSpace(email))}, "|")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func fetchPublicMailMessages(ctx context.Context, db *sql.DB, card publicCardKey, email string) ([]publicMailMessage, error) {
	if account, err := loadPublicMailAccountByEmail(ctx, db, email); err == nil {
		messages, err := receiveMailHeaders(ctx, account, publicMailFetchLimit)
		if err != nil {
			return nil, err
		}
		items := make([]publicMailMessage, 0, len(messages.Inbox)+len(messages.Trash))
		for _, message := range append(messages.Inbox, messages.Trash...) {
			item := publicMailMessage{
				ID:        publicIMAPMessageKey(email, message),
				Source:    "imap",
				Folder:    message.Folder,
				UID:       message.UID,
				Mailbox:   message.Mailbox,
				Subject:   message.Subject,
				From:      message.From,
				To:        message.To,
				Time:      message.Time,
				Timestamp: message.Timestamp,
			}
			if publicMailMessageMatches(card, item) {
				items = append(items, item)
			}
		}
		sortPublicMailMessages(items)
		return hydrateLatestPublicIMAPMessage(ctx, account, limitPublicMailMessages(items)), nil
	}

	account, err := loadPublicOutlookAccountByEmail(ctx, db, email)
	if err != nil {
		return nil, fmt.Errorf("邮箱账号不存在")
	}
	if strings.EqualFold(account.Status, "disabled") {
		return nil, fmt.Errorf("邮箱账号已停用")
	}
	token, err := refreshOutlookAccessToken(ctx, db, account)
	if err != nil {
		return nil, fmt.Errorf("微软邮箱授权失败: %s", err.Error())
	}
	outlookItems, err := fetchPublicOutlookMessages(ctx, token, card)
	if err != nil {
		return nil, err
	}
	items := make([]publicMailMessage, 0, len(outlookItems))
	for _, message := range outlookItems {
		item := publicMailMessage{
			ID:          publicOutlookMessageKey(email, message),
			Source:      "outlook",
			Folder:      message.Folder,
			RemoteID:    message.ID,
			Subject:     message.Subject,
			From:        message.From,
			To:          message.To,
			Time:        message.Time,
			Timestamp:   message.Timestamp,
			BodyPreview: message.BodyPreview,
		}
		if publicMailMessageMatches(card, item) {
			items = append(items, item)
		}
	}
	sortPublicMailMessages(items)
	return hydrateLatestPublicOutlookMessage(ctx, token, limitPublicMailMessages(items)), nil
}

func fetchPublicOutlookMessages(ctx context.Context, token string, card publicCardKey) ([]outlookMessageResponse, error) {
	items := []outlookMessageResponse{}
	var firstErr error
	successCount := 0
	for _, folder := range []string{"inbox", "junkemail"} {
		folderItems, err := fetchPublicOutlookFolderMessages(ctx, token, folder, card)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		successCount++
		items = append(items, folderItems...)
	}
	if successCount == 0 && firstErr != nil {
		return nil, firstErr
	}
	sortOutlookMessages(items)
	if len(items) > publicMailReturnLimit {
		items = items[:publicMailReturnLimit]
	}
	return items, nil
}

func fetchPublicOutlookFolderMessages(ctx context.Context, token string, folder string, card publicCardKey) ([]outlookMessageResponse, error) {
	items := []outlookMessageResponse{}
	keyword := strings.TrimSpace(card.MailKeyword)
	for skip := 0; skip < publicMailOutlookScanLimit; skip += publicMailFetchLimit {
		top := publicMailFetchLimit
		if remaining := publicMailOutlookScanLimit - skip; remaining < top {
			top = remaining
		}
		page, err := fetchOutlookMessages(ctx, token, folder, top, skip, "")
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		for _, message := range page {
			if publicMailMessageMatches(card, publicMailMessage{Subject: message.Subject, Timestamp: message.Timestamp}) {
				items = append(items, message)
			}
		}
		if keyword == "" || len(items) > 0 || publicOutlookPagePastMailDays(card, page) || len(page) < top {
			break
		}
	}
	return items, nil
}

func publicOutlookPagePastMailDays(card publicCardKey, items []outlookMessageResponse) bool {
	if card.MailDays <= 0 || len(items) == 0 {
		return false
	}
	cutoff := time.Now().Add(-time.Duration(card.MailDays) * 24 * time.Hour).Unix()
	oldest := int64(0)
	for _, item := range items {
		if item.Timestamp <= 0 {
			continue
		}
		if oldest == 0 || item.Timestamp < oldest {
			oldest = item.Timestamp
		}
	}
	return oldest > 0 && oldest < cutoff
}

func hydrateLatestPublicIMAPMessage(ctx context.Context, account mailAccountTestConfig, items []publicMailMessage) []publicMailMessage {
	if len(items) == 0 {
		return items
	}
	latest := items[0]
	var detail receiveMailDetailResponse
	var err error
	if strings.EqualFold(strings.TrimSpace(account.ImapProtocol), "POP3") {
		detail, err = receivePOP3MailDetail(ctx, account, latest.UID)
	} else {
		detail, err = receiveIMAPMailDetail(ctx, account, latest.Mailbox, latest.Folder, latest.UID)
	}
	if err == nil {
		latest.Subject = valueOrDefault(detail.Subject, latest.Subject)
		latest.From = valueOrDefault(detail.From, latest.From)
		latest.To = valueOrDefault(detail.To, latest.To)
		latest.Time = valueOrDefault(detail.Time, latest.Time)
		if detail.Timestamp > 0 {
			latest.Timestamp = detail.Timestamp
		}
		latest.Body = detail.Body
		latest.HTML = detail.HTML
	}
	return []publicMailMessage{latest}
}

func hydrateLatestPublicOutlookMessage(ctx context.Context, token string, items []publicMailMessage) []publicMailMessage {
	if len(items) == 0 || strings.TrimSpace(items[0].RemoteID) == "" {
		return items
	}
	latest := items[0]
	detail, err := fetchOutlookMessageDetail(ctx, token, latest.RemoteID)
	if err == nil {
		latest.Subject = valueOrDefault(detail.Subject, latest.Subject)
		latest.From = valueOrDefault(detail.From, latest.From)
		latest.To = valueOrDefault(detail.To, latest.To)
		latest.Time = valueOrDefault(detail.Time, latest.Time)
		if detail.Timestamp > 0 {
			latest.Timestamp = detail.Timestamp
		}
		latest.BodyPreview = valueOrDefault(detail.BodyPreview, latest.BodyPreview)
		latest.Body = detail.Body
		latest.HTML = detail.HTML
	}
	return []publicMailMessage{latest}
}

func receivePOP3MailDetail(ctx context.Context, account mailAccountTestConfig, uid int) (receiveMailDetailResponse, error) {
	if uid <= 0 {
		return receiveMailDetailResponse{}, fmt.Errorf("邮件编号无效")
	}
	conn, err := dialMailServer(ctx, account.ImapHost, account.ImapPort, account.ImapSSL)
	if err != nil {
		return receiveMailDetailResponse{}, err
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	if line, err := readMailLine(reader); err != nil {
		return receiveMailDetailResponse{}, err
	} else if !strings.HasPrefix(line, "+OK") {
		return receiveMailDetailResponse{}, fmt.Errorf("POP3 服务响应异常: %s", strings.TrimSpace(line))
	}
	if err := writeMailLine(conn, "USER %s", account.Email); err != nil {
		return receiveMailDetailResponse{}, err
	}
	if err := expectPOP3OK(reader); err != nil {
		return receiveMailDetailResponse{}, err
	}
	if err := writeMailLine(conn, "PASS %s", account.Password); err != nil {
		return receiveMailDetailResponse{}, err
	}
	if err := expectPOP3OK(reader); err != nil {
		return receiveMailDetailResponse{}, err
	}
	if err := writeMailLine(conn, "RETR %d", uid); err != nil {
		return receiveMailDetailResponse{}, err
	}
	if err := expectPOP3OK(reader); err != nil {
		return receiveMailDetailResponse{}, err
	}

	var builder strings.Builder
	for {
		line, err := readMailLine(reader)
		if err != nil {
			return receiveMailDetailResponse{}, err
		}
		if line == "." {
			break
		}
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}
		builder.WriteString(line)
		builder.WriteString("\r\n")
	}
	_ = writeMailLine(conn, "QUIT")

	raw := []byte(builder.String())
	item := parseFullMailHeader(raw, "inbox", "INBOX")
	item.UID = uid
	plain, html := parseMailBody(raw)
	return receiveMailDetailResponse{receivedMailMessage: item, Body: plain, HTML: html}, nil
}

func loadPublicMailAccountByEmail(ctx context.Context, db *sql.DB, email string) (mailAccountTestConfig, error) {
	var account mailAccountTestConfig
	err := db.QueryRowContext(ctx, `
SELECT email, password, imap_host, imap_protocol, imap_port, imap_ssl, smtp_host, smtp_protocol, smtp_port, smtp_ssl
FROM mail_accounts
WHERE LOWER(email) = LOWER($1) AND LOWER(status) <> 'disabled'
ORDER BY CASE WHEN LOWER(status) IN ('active', 'normal', 'ok', 'success') THEN 0 ELSE 1 END, id ASC
LIMIT 1
`, email).Scan(&account.Email, &account.Password, &account.ImapHost, &account.ImapProtocol, &account.ImapPort, &account.ImapSSL, &account.SMTPHost, &account.SMTPProtocol, &account.SMTPPort, &account.SMTPSSL)
	return account, err
}

func loadPublicOutlookAccountByEmail(ctx context.Context, db *sql.DB, email string) (outlookStoredAccount, error) {
	var account outlookStoredAccount
	err := db.QueryRowContext(ctx, `
SELECT id, email, password, client_id, refresh_token, group_id, remark, status
FROM outlook_accounts
WHERE LOWER(email) = LOWER($1) AND LOWER(status) <> 'disabled'
ORDER BY CASE WHEN LOWER(status) IN ('active', 'normal', 'ok', 'success') THEN 0 ELSE 1 END, id ASC
LIMIT 1
`, email).Scan(&account.ID, &account.Email, &account.Password, &account.ClientID, &account.RefreshToken, &account.GroupID, &account.Remark, &account.Status)
	return account, err
}

func publicMailMessageMatches(card publicCardKey, item publicMailMessage) bool {
	if card.MailDays > 0 {
		cutoff := time.Now().Add(-time.Duration(card.MailDays) * 24 * time.Hour).Unix()
		if item.Timestamp <= 0 || item.Timestamp < cutoff {
			return false
		}
	}
	keyword := strings.ToLower(strings.TrimSpace(card.MailKeyword))
	if keyword == "" {
		return true
	}
	return strings.Contains(strings.ToLower(item.Subject), keyword)
}

func publicIMAPMessageKey(email string, message receivedMailMessage) string {
	parts := []string{"imap", strings.ToLower(email), message.Folder, message.Mailbox, fmt.Sprintf("%d", message.UID), fmt.Sprintf("%d", message.Timestamp), message.Subject}
	return strings.Join(parts, "|")
}

func publicOutlookMessageKey(email string, message outlookMessageResponse) string {
	return strings.Join([]string{"outlook", strings.ToLower(email), message.Folder, message.ID}, "|")
}

func sortPublicMailMessages(items []publicMailMessage) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Timestamp > items[j].Timestamp
	})
}

func limitPublicMailMessages(items []publicMailMessage) []publicMailMessage {
	if len(items) > publicMailReturnLimit {
		return items[:publicMailReturnLimit]
	}
	return items
}

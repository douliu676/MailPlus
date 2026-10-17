package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	stdmail "net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"mail-admin/houduan/ent"
	"mail-admin/houduan/ent/systemsetting"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

const quickMailKeySettingKey = "quick_mail_key_hash"

type quickMailKeyResponse struct {
	Configured bool `json:"configured"`
}

type saveQuickMailKeyRequest struct {
	Key string `json:"key"`
}

type quickMailReceiveRequest struct {
	Email       string `json:"email"`
	Limit       int    `json:"limit"`
	AdminKey    string `json:"admin_key"`
	Key         string `json:"key"`
	IncludeBody *bool  `json:"include_body"`
}

func (req quickMailReceiveRequest) wantsBody() bool {
	return req.IncludeBody == nil || *req.IncludeBody
}

type quickMailDetailRequest struct {
	quickMailReceiveRequest
	UID         int    `json:"uid"`
	UIDValidity uint32 `json:"uid_validity"`
	Mailbox     string `json:"mailbox"`
	Folder      string `json:"folder"`
	MessageID   string `json:"message_id"`
}

type quickMailMessage struct {
	ID             string `json:"id"`
	Source         string `json:"source"`
	UID            int    `json:"uid,omitempty"`
	UIDValidity    uint32 `json:"uid_validity,omitempty"`
	MessageID      string `json:"message_id,omitempty"`
	Folder         string `json:"folder"`
	Mailbox        string `json:"mailbox,omitempty"`
	Subject        string `json:"subject"`
	From           string `json:"from"`
	To             string `json:"to"`
	CC             string `json:"cc,omitempty"`
	Time           string `json:"time"`
	Timestamp      int64  `json:"timestamp"`
	Body           string `json:"body"`
	HTML           string `json:"html"`
	BodyPreview    string `json:"body_preview,omitempty"`
	IsRead         bool   `json:"is_read,omitempty"`
	HasAttachments bool   `json:"has_attachments,omitempty"`
	BodyLoaded     bool   `json:"body_loaded"`
}

type quickMailReceiveResponse struct {
	Inbox    []quickMailMessage `json:"inbox"`
	Trash    []quickMailMessage `json:"trash"`
	Warnings []string           `json:"warnings,omitempty"`
}

func quickMailRequestContext(c *gin.Context, timeout time.Duration) context.CancelFunc {
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	c.Request = c.Request.WithContext(ctx)
	return cancel
}

func writeQuickMailError(c *gin.Context, operation string, err error) {
	status := http.StatusBadRequest
	message := operation + ": " + err.Error()
	var networkError net.Error
	if errors.Is(c.Request.Context().Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
		status, message = http.StatusGatewayTimeout, operation+"超时，请稍后重试"
	} else if errors.Is(c.Request.Context().Err(), context.Canceled) {
		status, message = http.StatusRequestTimeout, "请求已取消"
	}
	c.JSON(status, apiResponse{Code: status, Msg: message})
}

func (s *appState) getQuickMailKey(c *gin.Context) {
	configured, err := s.quickMailKeyConfigured(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "读取快速取件秘钥失败"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: quickMailKeyResponse{Configured: configured}, Msg: "ok"})
}

func (s *appState) updateQuickMailKey(c *gin.Context) {
	var req saveQuickMailKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}

	key := strings.TrimSpace(req.Key)
	if key == "" {
		if err := s.deleteSystemSetting(c.Request.Context(), quickMailKeySettingKey); err != nil {
			c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "保存快速取件秘钥失败"})
			return
		}
		c.JSON(http.StatusOK, apiResponse{Code: 0, Data: quickMailKeyResponse{Configured: false}, Msg: "ok"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(key), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "保存快速取件秘钥失败"})
		return
	}
	if err := s.saveSystemSetting(c.Request.Context(), quickMailKeySettingKey, string(hash)); err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "保存快速取件秘钥失败"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: quickMailKeyResponse{Configured: true}, Msg: "ok"})
}

func (s *appState) quickMailReceiveIMAP(c *gin.Context) {
	cancel := quickMailRequestContext(c, quickMailReceiveTimeout)
	defer cancel()
	var req quickMailReceiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	if !s.authorizeQuickMailRequest(c, req.quickMailKey()) {
		return
	}

	email, ok := normalizeQuickMailEmail(req.Email)
	if !ok {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "输入有误"})
		return
	}
	limit := normalizeQuickMailLimit(req.Limit)

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "收取邮件失败"})
		return
	}
	defer db.Close()

	account, err := loadPublicMailAccountByEmail(c.Request.Context(), db, email)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "输入有误"})
		return
	}
	started := time.Now()
	messages, warnings, err := receiveQuickIMAPHeaders(c.Request.Context(), account, limit)
	if err != nil {
		writeQuickMailError(c, "收取邮件", err)
		return
	}
	log.Printf("quick-mail imap headers: inbox=%d trash=%d elapsed=%s", len(messages.Inbox), len(messages.Trash), time.Since(started).Round(time.Millisecond))
	inbox := quickIMAPMessages(account.Email, messages.Inbox)
	trash := quickIMAPMessages(account.Email, messages.Trash)
	// POP3 sequence numbers can change between sessions. Keep its complete-response
	// behaviour instead of using them as identifiers for a later lazy request.
	if req.wantsBody() || strings.EqualFold(strings.TrimSpace(account.ImapProtocol), "POP3") {
		items := append(append([]receivedMailMessage{}, messages.Inbox...), messages.Trash...)
		details, detailErr := quickIMAPMessagesWithDetails(c.Request.Context(), account, account.Email, items)
		if detailErr != nil {
			writeQuickMailError(c, "读取邮件内容", detailErr)
			return
		}
		inbox, trash = details[:len(messages.Inbox)], details[len(messages.Inbox):]
	}

	c.JSON(http.StatusOK, apiResponse{
		Code: 0,
		Data: quickMailReceiveResponse{
			Inbox:    inbox,
			Trash:    trash,
			Warnings: warnings,
		},
		Msg: "ok",
	})
}

func (s *appState) quickMailReceiveOutlook(c *gin.Context) {
	cancel := quickMailRequestContext(c, quickMailReceiveTimeout)
	defer cancel()
	var req quickMailReceiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return
	}
	if !s.authorizeQuickMailRequest(c, req.quickMailKey()) {
		return
	}

	email, ok := normalizeQuickMailEmail(req.Email)
	if !ok {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "输入有误"})
		return
	}
	limit := normalizeQuickMailLimit(req.Limit)

	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "收取邮件失败"})
		return
	}
	defer db.Close()

	account, err := loadPublicOutlookAccountByEmail(c.Request.Context(), db, email)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "输入有误"})
		return
	}
	token, err := refreshOutlookAccessToken(c.Request.Context(), db, account)
	if err != nil {
		updateOutlookAccountStatus(c.Request.Context(), db, account.ID, "error", err.Error())
		writeQuickMailError(c, "收取邮件", err)
		return
	}

	inbox, err := fetchOutlookMessages(c.Request.Context(), token, "inbox", limit, 0, "")
	if err != nil {
		writeQuickMailError(c, "收取邮件", err)
		return
	}
	var warnings []string
	trash, err := fetchOutlookMessages(c.Request.Context(), token, "junkemail", limit, 0, "")
	if err != nil {
		if req.wantsBody() {
			writeQuickMailError(c, "收取邮件", err)
			return
		}
		trash = []outlookMessageResponse{}
		warnings = []string{"收件箱已获取，垃圾箱读取失败，可重新获取"}
	}
	inboxMessages := quickOutlookMessages(account.Email, inbox)
	trashMessages := quickOutlookMessages(account.Email, trash)
	if req.wantsBody() {
		items := append(append([]outlookMessageResponse{}, inbox...), trash...)
		details, detailErr := quickOutlookMessagesWithDetails(c.Request.Context(), token, account.Email, items)
		if detailErr != nil {
			writeQuickMailError(c, "读取邮件内容", detailErr)
			return
		}
		inboxMessages, trashMessages = details[:len(inbox)], details[len(inbox):]
	}

	c.JSON(http.StatusOK, apiResponse{
		Code: 0,
		Data: quickMailReceiveResponse{
			Inbox:    inboxMessages,
			Trash:    trashMessages,
			Warnings: warnings,
		},
		Msg: "ok",
	})
}

func (s *appState) getQuickMailIMAPPlain(c *gin.Context) {
	cancel := quickMailRequestContext(c, quickMailReceiveTimeout)
	defer cancel()
	if !s.authorizeQuickMailPlainRequest(c, quickMailRouteKey(c)) {
		return
	}
	email, limit, ok := quickMailPlainRouteParams(c)
	if !ok {
		return
	}
	messages, err := fetchQuickIMAPPlainMessages(c.Request.Context(), email, limit)
	if err != nil {
		writeQuickMailPlain(c, http.StatusBadRequest, err.Error())
		return
	}
	writeQuickMailPlain(c, http.StatusOK, quickMailMessagesPlainText(messages))
}

func (s *appState) getQuickMailOutlookPlain(c *gin.Context) {
	cancel := quickMailRequestContext(c, quickMailReceiveTimeout)
	defer cancel()
	if !s.authorizeQuickMailPlainRequest(c, quickMailRouteKey(c)) {
		return
	}
	email, limit, ok := quickMailPlainRouteParams(c)
	if !ok {
		return
	}
	messages, err := fetchQuickOutlookPlainMessages(c.Request.Context(), email, limit)
	if err != nil {
		writeQuickMailPlain(c, http.StatusBadRequest, err.Error())
		return
	}
	writeQuickMailPlain(c, http.StatusOK, quickMailMessagesPlainText(messages))
}

func (req quickMailReceiveRequest) quickMailKey() string {
	if strings.TrimSpace(req.Key) != "" {
		return req.Key
	}
	return req.AdminKey
}

func quickMailRouteKey(c *gin.Context) string {
	key := strings.TrimSpace(c.Param("key"))
	if decoded, err := url.PathUnescape(key); err == nil {
		key = decoded
	}
	if !strings.HasPrefix(key, "keys=") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(key, "keys="))
}

func quickMailPlainRouteParams(c *gin.Context) (string, int, bool) {
	email := strings.TrimSpace(c.Param("email"))
	if decoded, err := url.PathUnescape(email); err == nil {
		email = decoded
	}
	normalizedEmail, ok := normalizeQuickMailEmail(email)
	if !ok {
		writeQuickMailPlain(c, http.StatusBadRequest, "输入有误")
		return "", 0, false
	}

	limitValue := strings.TrimSpace(c.Param("limit"))
	if limitValue == "" {
		return normalizedEmail, 1, true
	}
	if decoded, err := url.PathUnescape(limitValue); err == nil {
		limitValue = decoded
	}
	limit, err := strconv.Atoi(limitValue)
	if err != nil || limit <= 0 {
		writeQuickMailPlain(c, http.StatusBadRequest, "取件数量有误")
		return "", 0, false
	}
	return normalizedEmail, normalizeQuickMailLimit(limit), true
}

func (s *appState) authorizeQuickMailRequest(c *gin.Context, key string) bool {
	if userID, ok := s.optionalAdminAuthUserID(c); ok {
		c.Set("user_id", userID)
		return true
	}

	key = strings.TrimSpace(key)
	if key == "" {
		c.Header(authErrorHeader, authErrorQuickMailKey)
		c.JSON(http.StatusUnauthorized, apiResponse{Code: 401, Msg: "请输入秘钥"})
		return false
	}
	matched, err := s.quickMailKeyMatches(c.Request.Context(), key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "校验秘钥失败"})
		return false
	}
	if !matched {
		c.Header(authErrorHeader, authErrorQuickMailKey)
		c.JSON(http.StatusUnauthorized, apiResponse{Code: 401, Msg: "秘钥错误"})
		return false
	}
	return true
}

func (s *appState) authorizeQuickMailPlainRequest(c *gin.Context, key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		c.Header(authErrorHeader, authErrorQuickMailKey)
		writeQuickMailPlain(c, http.StatusUnauthorized, "请输入秘钥")
		return false
	}
	matched, err := s.quickMailKeyMatches(c.Request.Context(), key)
	if err != nil {
		writeQuickMailPlain(c, http.StatusInternalServerError, "校验秘钥失败")
		return false
	}
	if !matched {
		c.Header(authErrorHeader, authErrorQuickMailKey)
		writeQuickMailPlain(c, http.StatusUnauthorized, "秘钥错误")
		return false
	}
	return true
}

func (s *appState) optionalAdminAuthUserID(c *gin.Context) (int, bool) {
	header := strings.TrimSpace(c.GetHeader("Authorization"))
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return 0, false
	}
	token := strings.TrimSpace(header[len("Bearer "):])
	session, ok := s.sessions.get(token)
	if !ok {
		return 0, false
	}
	current, err := s.db.User.Get(c.Request.Context(), session.UserID)
	if err != nil || !current.Enabled || current.Role != "admin" {
		return 0, false
	}
	if userMustChangePassword(c.Request.Context(), current.ID) {
		return 0, false
	}
	return session.UserID, true
}

func (s *appState) quickMailKeyConfigured(ctx context.Context) (bool, error) {
	value, err := s.readSystemSetting(ctx, quickMailKeySettingKey)
	if ent.IsNotFound(err) {
		return false, nil
	}
	return strings.TrimSpace(value) != "", err
}

func (s *appState) quickMailKeyMatches(ctx context.Context, key string) (bool, error) {
	value, err := s.readSystemSetting(ctx, quickMailKeySettingKey)
	if ent.IsNotFound(err) || strings.TrimSpace(value) == "" {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bcrypt.CompareHashAndPassword([]byte(value), []byte(key)) == nil, nil
}

func (s *appState) readSystemSetting(ctx context.Context, key string) (string, error) {
	item, err := s.db.SystemSetting.Query().Where(systemsetting.Key(key)).Only(ctx)
	if err != nil {
		return "", err
	}
	return item.Value, nil
}

func fetchQuickIMAPPlainMessages(ctx context.Context, email string, limit int) ([]quickMailMessage, error) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return nil, fmt.Errorf("收取邮件失败")
	}
	defer db.Close()

	account, err := loadPublicMailAccountByEmail(ctx, db, email)
	if err != nil {
		return nil, fmt.Errorf("邮箱账号不存在或已停用")
	}
	headers, _, err := receiveQuickIMAPHeaders(ctx, account, limit)
	if err != nil {
		return nil, fmt.Errorf("收取邮件失败: %s", err.Error())
	}

	items := make([]receivedMailMessage, 0, len(headers.Inbox)+len(headers.Trash))
	items = append(items, headers.Inbox...)
	items = append(items, headers.Trash...)
	sortReceivedMailMessages(items)
	if len(items) > limit {
		items = items[:limit]
	}

	return quickIMAPMessagesWithDetails(ctx, account, email, items)
}

func fetchQuickOutlookPlainMessages(ctx context.Context, email string, limit int) ([]quickMailMessage, error) {
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		return nil, fmt.Errorf("收取邮件失败")
	}
	defer db.Close()

	account, err := loadPublicOutlookAccountByEmail(ctx, db, email)
	if err != nil {
		return nil, fmt.Errorf("邮箱账号不存在或已停用")
	}
	token, err := refreshOutlookAccessToken(ctx, db, account)
	if err != nil {
		updateOutlookAccountStatus(ctx, db, account.ID, "error", err.Error())
		return nil, fmt.Errorf("收取邮件失败: %s", err.Error())
	}

	items, err := fetchOutlookMessages(ctx, token, "all", limit, 0, "")
	if err != nil {
		return nil, fmt.Errorf("收取邮件失败: %s", err.Error())
	}
	messageIDs := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) != "" {
			messageIDs = append(messageIDs, item.ID)
		}
	}

	detailsByID := map[string]outlookMessageResponse{}
	if len(messageIDs) > 0 {
		details, err := fetchOutlookMessageDetails(ctx, token, messageIDs)
		if err != nil {
			return nil, fmt.Errorf("读取邮件内容失败: %s", err.Error())
		}
		for _, detail := range details {
			if strings.TrimSpace(detail.ID) != "" {
				detailsByID[detail.ID] = detail
			}
		}
	}

	messages := make([]quickMailMessage, 0, len(items))
	for _, item := range items {
		if detail, ok := detailsByID[item.ID]; ok {
			item = mergeOutlookMessageDetail(item, detail)
		}
		messages = append(messages, quickOutlookMessages(email, []outlookMessageResponse{item})...)
	}
	return messages, nil
}

func quickIMAPDetailMessage(email string, header receivedMailMessage, detail receiveMailDetailResponse) quickMailMessage {
	uid := detail.UID
	if uid <= 0 {
		uid = header.UID
	}
	timestamp := detail.Timestamp
	if timestamp <= 0 {
		timestamp = header.Timestamp
	}
	return quickMailMessage{
		ID:          publicIMAPMessageKey(email, header),
		Source:      "imap",
		UID:         uid,
		UIDValidity: detail.UIDValidity,
		Folder:      valueOrDefault(detail.Folder, header.Folder),
		Mailbox:     valueOrDefault(detail.Mailbox, header.Mailbox),
		Subject:     valueOrDefault(detail.Subject, header.Subject),
		From:        valueOrDefault(detail.From, header.From),
		To:          valueOrDefault(detail.To, header.To),
		Time:        valueOrDefault(detail.Time, header.Time),
		Timestamp:   timestamp,
		Body:        detail.Body,
		HTML:        detail.HTML,
		BodyLoaded:  true,
	}
}

func mergeOutlookMessageDetail(summary outlookMessageResponse, detail outlookMessageResponse) outlookMessageResponse {
	return outlookMessageResponse{
		ID:             valueOrDefault(detail.ID, summary.ID),
		Folder:         valueOrDefault(summary.Folder, detail.Folder),
		Subject:        valueOrDefault(detail.Subject, summary.Subject),
		From:           valueOrDefault(detail.From, summary.From),
		To:             valueOrDefault(detail.To, summary.To),
		CC:             valueOrDefault(detail.CC, summary.CC),
		Time:           valueOrDefault(detail.Time, summary.Time),
		Timestamp:      firstPositiveInt64(detail.Timestamp, summary.Timestamp),
		BodyPreview:    valueOrDefault(detail.BodyPreview, summary.BodyPreview),
		Body:           valueOrDefault(detail.Body, summary.Body),
		HTML:           valueOrDefault(detail.HTML, summary.HTML),
		IsRead:         detail.IsRead || summary.IsRead,
		HasAttachments: detail.HasAttachments || summary.HasAttachments,
	}
}

func firstPositiveInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func quickMailMessagesPlainText(messages []quickMailMessage) string {
	if len(messages) == 0 {
		return "暂无邮件..."
	}
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		text := quickMailMessagePlainText(message)
		if text == "" {
			text = "暂无邮件..."
		}
		parts = append(parts, text)
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts, "\n\n------------------------------\n\n")
}

func quickMailMessagePlainText(message quickMailMessage) string {
	return mailPlainMessageText(message.Subject, message.From, message.To, message.Time, message.Timestamp, message.Body, message.HTML, message.BodyPreview)
}

func writeQuickMailPlain(c *gin.Context, status int, text string) {
	c.Data(status, "text/plain; charset=utf-8", []byte(text))
}

func (s *appState) saveSystemSetting(ctx context.Context, key string, value string) error {
	existing, err := s.db.SystemSetting.Query().Where(systemsetting.Key(key)).Only(ctx)
	if err == nil {
		_, err = existing.Update().SetValue(value).Save(ctx)
		return err
	}
	if !ent.IsNotFound(err) {
		return err
	}
	_, err = s.db.SystemSetting.Create().SetKey(key).SetValue(value).Save(ctx)
	return err
}

func (s *appState) deleteSystemSetting(ctx context.Context, key string) error {
	_, err := s.db.SystemSetting.Delete().Where(systemsetting.Key(key)).Exec(ctx)
	return err
}

func normalizeQuickMailEmail(value string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(value))
	if email == "" {
		return "", false
	}
	address, err := stdmail.ParseAddress(email)
	if err != nil || address.Address != email || strings.TrimSpace(address.Name) != "" {
		return "", false
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || !strings.Contains(parts[1], ".") {
		return "", false
	}
	return email, true
}

func normalizeQuickMailLimit(value int) int {
	if value <= 0 {
		return 1
	}
	if value > 100 {
		return 100
	}
	return value
}

func quickIMAPMessagesWithDetails(ctx context.Context, account mailAccountTestConfig, email string, items []receivedMailMessage) ([]quickMailMessage, error) {
	workers := quickMailConcurrency
	if strings.EqualFold(strings.TrimSpace(account.ImapProtocol), "POP3") {
		workers = 1
	}
	details, err := collectQuickMailDetails(ctx, items, workers, func(workerCtx context.Context) (quickMailDetailSession, error) {
		if workers == 1 && strings.EqualFold(strings.TrimSpace(account.ImapProtocol), "POP3") {
			session, err := openQuickPOP3Session(workerCtx, account)
			if err != nil {
				return nil, err
			}
			return session, nil
		}
		session, err := openQuickIMAPSession(workerCtx, account)
		if err != nil {
			return nil, err
		}
		return session, nil
	})
	if err != nil {
		return nil, err
	}
	result := make([]quickMailMessage, len(items))
	for i, item := range items {
		result[i] = quickIMAPDetailMessage(email, item, details[i])
	}
	return result, nil
}

func quickOutlookMessagesWithDetails(ctx context.Context, accessToken string, email string, items []outlookMessageResponse) ([]quickMailMessage, error) {
	messageIDs := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		messageID := strings.TrimSpace(item.ID)
		if messageID != "" && !seen[messageID] {
			seen[messageID] = true
			messageIDs = append(messageIDs, messageID)
		}
	}
	if len(messageIDs) == 0 {
		return quickOutlookMessages(email, items), nil
	}

	details, err := fetchOutlookMessageDetails(ctx, accessToken, messageIDs)
	if err != nil {
		return nil, err
	}
	detailsByID := map[string]outlookMessageResponse{}
	for _, detail := range details {
		if strings.TrimSpace(detail.ID) != "" {
			detailsByID[detail.ID] = detail
		}
	}

	hydrated := make([]outlookMessageResponse, 0, len(items))
	for _, item := range items {
		if detail, ok := detailsByID[item.ID]; ok {
			item = mergeOutlookMessageDetail(item, detail)
		}
		hydrated = append(hydrated, item)
	}
	result := quickOutlookMessages(email, hydrated)
	for i := range result {
		if _, ok := detailsByID[items[i].ID]; !ok {
			return nil, fmt.Errorf("部分邮件正文未能读取，请重试")
		}
		result[i].BodyLoaded = true
	}
	return result, nil
}

func quickIMAPMessages(email string, items []receivedMailMessage) []quickMailMessage {
	result := make([]quickMailMessage, 0, len(items))
	for _, item := range items {
		result = append(result, quickMailMessage{
			ID:          publicIMAPMessageKey(email, item),
			Source:      "imap",
			UID:         item.UID,
			UIDValidity: item.UIDValidity,
			Folder:      item.Folder,
			Mailbox:     item.Mailbox,
			Subject:     item.Subject,
			From:        item.From,
			To:          item.To,
			Time:        item.Time,
			Timestamp:   item.Timestamp,
		})
	}
	return result
}

func quickOutlookMessages(email string, items []outlookMessageResponse) []quickMailMessage {
	result := make([]quickMailMessage, 0, len(items))
	for _, item := range items {
		folder := item.Folder
		if normalizeOutlookFolder(folder) == "junkemail" {
			folder = "trash"
		}
		body := item.Body
		if strings.TrimSpace(body) == "" {
			body = item.BodyPreview
		}
		result = append(result, quickMailMessage{
			ID:             publicOutlookMessageKey(email, item),
			MessageID:      item.ID,
			Source:         "outlook",
			Folder:         folder,
			Subject:        item.Subject,
			From:           item.From,
			To:             item.To,
			CC:             item.CC,
			Time:           item.Time,
			Timestamp:      item.Timestamp,
			Body:           body,
			HTML:           item.HTML,
			BodyPreview:    item.BodyPreview,
			IsRead:         item.IsRead,
			HasAttachments: item.HasAttachments,
		})
	}
	return result
}

func parsePublicTablePageSize(value string, fallback int) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	parsed := parsePositiveInt(value, fallback)
	if parsed <= 0 {
		return fallback
	}
	return parsed
}

func parsePublicTablePageSizeOptions(value string, defaultPageSize int) []int {
	values := []int{}
	var rawItems []interface{}
	if strings.TrimSpace(value) != "" {
		if err := jsonUnmarshalString(value, &rawItems); err == nil {
			for _, item := range rawItems {
				switch typed := item.(type) {
				case float64:
					values = append(values, int(typed))
				case int:
					values = append(values, typed)
				case string:
					values = append(values, parsePositiveInt(typed, 0))
				}
			}
		}
	}
	if len(values) == 0 {
		values = []int{10, 20, 50, 100}
	}
	if defaultPageSize > 0 {
		values = append(values, defaultPageSize)
	}
	seen := map[int]bool{}
	result := []int{}
	for _, item := range values {
		if item > 0 && !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	sort.Ints(result)
	if len(result) == 0 {
		return []int{defaultPageSize}
	}
	return result
}

func jsonUnmarshalString(value string, target interface{}) error {
	if err := json.Unmarshal([]byte(value), target); err != nil {
		return fmt.Errorf("parse json: %w", err)
	}
	return nil
}

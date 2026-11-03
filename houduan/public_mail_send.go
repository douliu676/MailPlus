package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	stdmail "net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

type publicMailSendRequest struct {
	Email     string `json:"email"`
	RequestID string `json:"request_id"`
	sendMailMessageRequest
}

type publicMailSendResponse struct {
	publicMailQuota
	OperationID string `json:"operation_id"`
	RequestID   string `json:"request_id"`
	Email       string `json:"email"`
	Recipient   string `json:"recipient"`
	Subject     string `json:"subject"`
	Status      string `json:"status"`
	Charged     bool   `json:"charged"`
	Replayed    bool   `json:"replayed"`
	Message     string `json:"message"`
	Error       string `json:"error"`
}

func makePublicMailSendResponse(op publicMailOperation, quota publicMailQuota, replayed bool) publicMailSendResponse {
	return publicMailSendResponse{
		publicMailQuota: quota, OperationID: op.ID, RequestID: op.RequestID,
		Email: op.Email, Recipient: op.Recipient, Subject: op.Subject, Status: op.Status,
		Charged: op.Status == "success", Replayed: replayed, Message: publicMailSendMessage(op, quota), Error: op.Error,
	}
}

func validPublicMailRequestID(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_') {
			return false
		}
	}
	return true
}

func (s *appState) sendPublicMail(c *gin.Context) {
	card, ok := s.loadPublicCardKeyForRequest(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	var req publicMailSendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "发件参数错误或邮件过大"})
		return
	}
	if !validPublicMailRequestID(req.RequestID) {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "发件请求标识无效，请刷新页面后重试"})
		return
	}
	email, err := resolvePublicMailEmail(card, req.Email)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: err.Error()})
		return
	}
	recipients, err := stdmail.ParseAddressList(strings.TrimSpace(req.Recipient))
	if err != nil || len(recipients) == 0 {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请填写有效的收件人邮箱"})
		return
	}
	addresses := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		addresses = append(addresses, recipient.Address)
	}
	// The fingerprint binds the retry identifier to exactly this message.
	payload, _ := json.Marshal([]string{email, req.Nickname, req.Recipient, req.Subject, req.Body})
	fingerprint := sha256.Sum256(payload)

	// Once submitted, closing the composer/browser must not cancel settlement.
	// Both the transport and the following independent settlement are bounded.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), publicMailOperationTimeout)
	defer cancel()
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "发件服务暂时不可用"})
		return
	}
	defer db.Close()
	op, _, quota, created, err := beginPublicMailOperation(ctx, db, card, publicMailOperation{
		RequestID: req.RequestID, Kind: "send", Fingerprint: hex.EncodeToString(fingerprint[:]),
		Email: email, Recipient: strings.Join(addresses, ", "), Subject: req.Subject, UserIP: c.ClientIP(),
	})
	if err != nil {
		status, message, data := publicMailOperationErrorResponse(err)
		c.JSON(status, apiResponse{Code: status, Msg: message, Data: data})
		return
	}
	if created {
		status, detail := "success", ""
		if sendErr := sendPublicMailMessage(ctx, db, email, req.sendMailMessageRequest); sendErr != nil {
			status, detail = "failed", maskLongTokenText(sendErr.Error())
			var uncertain *uncertainMailSendError
			if errors.As(sendErr, &uncertain) {
				status = "unknown"
			}
		}
		settleCtx, settleCancel := context.WithTimeout(context.Background(), 10*time.Second)
		finished, currentQuota, settleErr := finishPublicMailSend(settleCtx, db, op.ID, status, detail, false)
		settleCancel()
		if settleErr == nil {
			op, quota = finished, currentQuota
		} else {
			// The durable running record keeps its reservation. Recovery turns it
			// into unknown; neither a timeout nor a database outage permits a retry.
			op.Status, op.Error = "unknown", "发送结果暂时无法结算，请勿重复发送"
		}
	}
	result := makePublicMailSendResponse(op, quota, !created)
	if op.Status == "failed" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: result.Message, Data: result})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok", Data: result})
}

func (s *appState) getPublicMailSendStatus(c *gin.Context) {
	card, ok := s.loadPublicCardKeyForRequest(c)
	if !ok {
		return
	}
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "查询发送结果失败"})
		return
	}
	defer db.Close()
	_, quota, err := loadPublicMailSnapshot(c.Request.Context(), db, card)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "查询发送结果失败"})
		return
	}
	op, err := scanPublicMailOperation(db.QueryRowContext(c.Request.Context(), `SELECT `+publicMailOperationColumns+` FROM card_key_mail_operations WHERE card_key_id=$1 AND request_id=$2 AND operation_type='send'`, card.ID, c.Param("requestID")))
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, apiResponse{Code: 404, Msg: "发件请求尚未登记，请稍后查询"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "查询发送结果失败"})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok", Data: makePublicMailSendResponse(op, quota, true)})
}

// An administrator can settle a genuinely ambiguous delivery after checking
// the provider's records. This never sends another email.
func (s *appState) resolvePublicMailSend(c *gin.Context) {
	var req struct {
		Status string `json:"status"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Status != "success" && req.Status != "failed" {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请选择确认成功或确认失败"})
		return
	}
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiResponse{Code: 500, Msg: "确认发送结果失败"})
		return
	}
	defer db.Close()
	detail := "管理员核对后确认发送失败"
	if req.Status == "success" {
		detail = "管理员核对后确认发送成功"
	}
	op, quota, err := finishPublicMailSend(c.Request.Context(), db, c.Param("operationID"), req.Status, detail, true)
	if err != nil {
		status, message, data := publicMailOperationErrorResponse(err)
		c.JSON(status, apiResponse{Code: status, Msg: message, Data: data})
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Msg: "ok", Data: makePublicMailSendResponse(op, quota, true)})
}

type uncertainMailSendError struct{ cause error }

func (e *uncertainMailSendError) Error() string { return "发送结果待确认：" + e.cause.Error() }
func (e *uncertainMailSendError) Unwrap() error { return e.cause }

func classifySMTPCompletionError(err error) error {
	var reply *textproto.Error
	if errors.As(err, &reply) && reply.Code >= 400 && reply.Code < 600 {
		return err // The server explicitly rejected the completed DATA command.
	}
	return &uncertainMailSendError{cause: err}
}

type smtpContextClient struct {
	*smtp.Client
	stop func() bool
	once sync.Once
}

func newSMTPContextClient(ctx context.Context, conn net.Conn, host string) (*smtpContextClient, error) {
	deadline := time.Now().Add(20 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	// net/smtp identifies implicit TLS with a concrete *tls.Conn assertion.
	// Keep the original connection intact; attach lifecycle cleanup to the
	// SMTP client instead of hiding TLS behind a net.Conn wrapper.
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		stop()
		_ = conn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return &smtpContextClient{Client: client, stop: stop}, nil
}

func (c *smtpContextClient) Close() error {
	var err error
	c.once.Do(func() { c.stop(); err = c.Client.Close() })
	return err
}

func sendPublicMailMessage(ctx context.Context, db *sql.DB, email string, req sendMailMessageRequest) error {
	account, err := loadPublicMailAccountByEmail(ctx, db, email)
	if err == nil {
		return sendMailAccountMessage(ctx, account, req)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return errors.New("查询发件邮箱失败")
	}
	outlook, err := loadPublicOutlookAccountByEmail(ctx, db, email)
	if err != nil {
		return errors.New("邮箱账号不存在或已停用")
	}
	token, err := refreshOutlookAccessToken(ctx, db, outlook)
	if err != nil {
		return fmt.Errorf("微软邮箱授权失败：%w", err)
	}
	client, err := httpClientForProxyScope(ctx, proxyScopeOutlook)
	if err != nil {
		return err
	}
	return submitOutlookMail(ctx, client, graphAPIBaseURL+"/me/sendMail", email, token, req)
}

func submitOutlookMail(ctx context.Context, client *http.Client, endpoint, email, token string, mail sendMailMessageRequest) error {
	addresses, err := stdmail.ParseAddressList(strings.TrimSpace(mail.Recipient))
	if err != nil || len(addresses) == 0 {
		return errors.New("收件人地址格式错误")
	}
	recipients := make([]gin.H, 0, len(addresses))
	for _, address := range addresses {
		recipients = append(recipients, gin.H{"emailAddress": gin.H{"address": address.Address, "name": address.Name}})
	}
	body, err := json.Marshal(gin.H{"message": gin.H{
		"subject": mail.Subject, "body": gin.H{"contentType": "Text", "content": mail.Body},
		"from":         gin.H{"emailAddress": gin.H{"address": email, "name": mail.Nickname}},
		"toRecipients": recipients,
	}, "saveToSentItems": true})
	if err != nil {
		return err
	}
	var started atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteHeaders: func() { started.Store(true) }})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	sendClient := *client
	sendClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := sendClient.Do(request)
	if err != nil {
		if started.Load() {
			return &uncertainMailSendError{cause: err}
		}
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	var result struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&result)
	message := maskLongTokenText(result.Error.Message)
	if message == "" {
		message = fmt.Sprintf("微软发件接口返回 %d", response.StatusCode)
	}
	if response.StatusCode == http.StatusForbidden {
		message = "微软邮箱未获准发件，请检查 Mail.Send 授权：" + message
	}
	err = errors.New(message)
	if response.StatusCode >= 500 || response.StatusCode == http.StatusRequestTimeout {
		return &uncertainMailSendError{cause: err}
	}
	return err
}

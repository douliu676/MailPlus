package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func (s *appState) bindQuickMailDetail(c *gin.Context, outlook bool) (quickMailDetailRequest, bool) {
	var req quickMailDetailRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "请求参数错误"})
		return req, false
	}
	// Lazy detail reads have the same authorization requirements as collection.
	if !s.authorizeQuickMailRequest(c, req.quickMailKey()) {
		return req, false
	}
	var valid bool
	req.Email, valid = normalizeQuickMailEmail(req.Email)
	if !valid || !validQuickMailDetail(req, outlook) {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "邮件参数无效，请重新获取列表"})
		return req, false
	}
	return req, true
}

func validQuickMailDetail(req quickMailDetailRequest, outlook bool) bool {
	if req.Folder != "inbox" && req.Folder != "trash" {
		return false
	}
	if outlook {
		return strings.TrimSpace(req.MessageID) != "" && len(req.MessageID) <= 4096 && !strings.ContainsAny(req.MessageID, "\r\n\x00")
	}
	return req.UID > 0 && uint64(req.UID) <= uint64(^uint32(0)) && len(req.Mailbox) <= 1024 && !strings.ContainsAny(req.Mailbox, "\r\n\x00")
}

func (s *appState) quickMailDetailIMAP(c *gin.Context) {
	cancel := quickMailRequestContext(c, quickMailDetailTimeout)
	defer cancel()
	req, ok := s.bindQuickMailDetail(c, false)
	if !ok {
		return
	}
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		writeQuickMailError(c, "读取邮件正文", err)
		return
	}
	defer db.Close()
	account, err := loadPublicMailAccountByEmail(c.Request.Context(), db, req.Email)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "邮箱账号不存在或已停用"})
		return
	}
	if strings.EqualFold(strings.TrimSpace(account.ImapProtocol), "POP3") {
		writeQuickMailError(c, "读取邮件正文", fmt.Errorf("请重新获取 POP3 邮件列表"))
		return
	}
	session, err := openQuickIMAPSession(c.Request.Context(), account)
	if err != nil {
		writeQuickMailError(c, "读取邮件正文", err)
		return
	}
	defer session.Close()
	detail, err := session.readDetail(receivedMailMessage{
		UID: req.UID, UIDValidity: req.UIDValidity, Mailbox: req.Mailbox, Folder: req.Folder,
	})
	if err != nil {
		writeQuickMailError(c, "读取邮件正文", err)
		return
	}
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: quickIMAPDetailMessage(account.Email, detail.receivedMailMessage, detail), Msg: "ok"})
}

func (s *appState) quickMailDetailOutlook(c *gin.Context) {
	cancel := quickMailRequestContext(c, quickMailDetailTimeout)
	defer cancel()
	req, ok := s.bindQuickMailDetail(c, true)
	if !ok {
		return
	}
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		writeQuickMailError(c, "读取邮件正文", err)
		return
	}
	defer db.Close()
	account, err := loadPublicOutlookAccountByEmail(c.Request.Context(), db, req.Email)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiResponse{Code: 400, Msg: "邮箱账号不存在或已停用"})
		return
	}
	access, err := refreshOutlookAccessToken(c.Request.Context(), db, account)
	if err != nil {
		writeQuickMailError(c, "读取邮件正文", err)
		return
	}
	detail, err := fetchOutlookMessageDetail(c.Request.Context(), access, req.MessageID)
	if err != nil {
		writeQuickMailError(c, "读取邮件正文", err)
		return
	}
	detail.Folder = req.Folder
	message := quickOutlookMessages(account.Email, []outlookMessageResponse{detail})[0]
	message.BodyLoaded = true
	c.JSON(http.StatusOK, apiResponse{Code: 0, Data: message, Msg: "ok"})
}

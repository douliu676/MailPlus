package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	quickMailReceiveTimeout = 90 * time.Second
	quickMailDetailTimeout  = 30 * time.Second
	quickMailIOTimeout      = 30 * time.Second
	quickMailConcurrency    = 2
)

// Account-scoped limits also cover simultaneous requests from multiple tabs.
var quickMailConnections = struct {
	sync.Mutex
	accounts map[string]*quickMailConnectionSlots
}{accounts: make(map[string]*quickMailConnectionSlots)}

type quickMailConnectionSlots struct {
	permits chan struct{}
	users   int
}

func acquireQuickMailConnection(ctx context.Context, account mailAccountTestConfig) (func(), error) {
	limit := quickMailConcurrency
	if strings.EqualFold(strings.TrimSpace(account.ImapProtocol), "POP3") {
		limit = 1 // POP3 servers commonly lock a mailbox for the whole session.
	}
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%d", strings.ToLower(account.Email), strings.ToLower(account.ImapHost), account.ImapPort, limit)
	quickMailConnections.Lock()
	slots := quickMailConnections.accounts[key]
	if slots == nil {
		slots = &quickMailConnectionSlots{permits: make(chan struct{}, limit)}
		quickMailConnections.accounts[key] = slots
	}
	slots.users++
	quickMailConnections.Unlock()
	dropUser := func() {
		quickMailConnections.Lock()
		defer quickMailConnections.Unlock()
		slots.users--
		if slots.users == 0 {
			delete(quickMailConnections.accounts, key)
		}
	}
	select {
	case slots.permits <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-slots.permits; dropUser() }) }, nil
	case <-ctx.Done():
		dropUser()
		return nil, ctx.Err()
	}
}

type quickMailConn struct {
	net.Conn
	stop    func() bool
	release func()
	once    sync.Once
}

func (c *quickMailConn) Close() error {
	var err error
	c.once.Do(func() {
		c.stop()
		err = c.Conn.Close()
		c.release()
	})
	return err
}

func openQuickMailConnection(ctx context.Context, account mailAccountTestConfig) (net.Conn, error) {
	release, err := acquireQuickMailConnection(ctx, account)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, err := dialMailServer(dialCtx, account.ImapHost, account.ImapPort, account.ImapSSL)
	cancel()
	if err != nil {
		release()
		return nil, err
	}
	return &quickMailConn{
		Conn: conn, release: release,
		stop: context.AfterFunc(ctx, func() { _ = conn.Close() }),
	}, nil
}

func setQuickMailDeadline(ctx context.Context, conn net.Conn) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(quickMailIOTimeout)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	return conn.SetDeadline(deadline)
}

type quickIMAPSession struct {
	ctx         context.Context
	conn        net.Conn
	reader      *bufio.Reader
	stop        func() bool
	sequence    int
	mailbox     string
	total       int
	uidValidity uint32
}

func openQuickIMAPSession(ctx context.Context, account mailAccountTestConfig) (*quickIMAPSession, error) {
	conn, err := openQuickMailConnection(ctx, account)
	if err != nil {
		return nil, err
	}
	return newQuickIMAPSession(ctx, conn, account)
}

// Each session owns its connection and is used by one worker only.
func newQuickIMAPSession(ctx context.Context, conn net.Conn, account mailAccountTestConfig) (*quickIMAPSession, error) {
	session := &quickIMAPSession{ctx: ctx, conn: conn, reader: bufio.NewReader(conn)}
	session.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
	if err := setQuickMailDeadline(ctx, conn); err != nil {
		_ = session.Close()
		return nil, err
	}
	line, err := readMailLine(session.reader)
	if err == nil && strings.HasPrefix(line, "* PREAUTH") {
		return session, nil
	}
	if err == nil && !strings.HasPrefix(line, "* OK") {
		err = fmt.Errorf("IMAP 服务响应异常")
	}
	if err == nil {
		var tag string
		tag, err = session.command("LOGIN %s %s", imapQuoted(account.Email), imapQuoted(account.Password))
		if err == nil {
			err = readUntilTaggedOK(session.reader, tag)
		}
	}
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	return session, nil
}

func (s *quickIMAPSession) Close() error {
	s.stop()
	return s.conn.Close()
}

func (s *quickIMAPSession) command(format string, args ...interface{}) (string, error) {
	if err := setQuickMailDeadline(s.ctx, s.conn); err != nil {
		return "", err
	}
	s.sequence++
	tag := fmt.Sprintf("q%06d", s.sequence)
	return tag, writeMailLine(s.conn, tag+" "+format, args...)
}

type quickIMAPStatusError struct{ message string }

func (e *quickIMAPStatusError) Error() string { return e.message }

var quickIMAPUIDValidityPattern = regexp.MustCompile(`(?i)\[UIDVALIDITY\s+(\d+)\]`)

func (s *quickIMAPSession) selectMailbox(mailbox string) error {
	if mailbox == "" {
		mailbox = "INBOX"
	}
	if strings.ContainsAny(mailbox, "\r\n\x00") {
		return fmt.Errorf("邮箱文件夹无效")
	}
	if s.mailbox == mailbox {
		return nil
	}
	s.mailbox, s.total, s.uidValidity = "", 0, 0
	tag, err := s.command("EXAMINE %s", imapMailboxName(mailbox))
	if err != nil {
		return err
	}
	for {
		line, err := readMailLine(s.reader)
		if err != nil {
			return err
		}
		if match := quickIMAPUIDValidityPattern.FindStringSubmatch(line); len(match) == 2 {
			value, _ := strconv.ParseUint(match[1], 10, 32)
			s.uidValidity = uint32(value)
		}
		if strings.HasPrefix(line, "* ") && strings.HasSuffix(line, " EXISTS") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				s.total, _ = strconv.Atoi(fields[1])
			}
		}
		if strings.HasPrefix(line, tag+" ") {
			if !strings.HasPrefix(line, tag+" OK") {
				return &quickIMAPStatusError{message: line}
			}
			s.mailbox = mailbox
			return nil
		}
	}
}

func (s *quickIMAPSession) headers(mailbox, folder string, limit int) ([]receivedMailMessage, error) {
	if err := s.selectMailbox(mailbox); err != nil {
		return nil, err
	}
	if s.total == 0 {
		return []receivedMailMessage{}, nil
	}
	start := max(1, s.total-limit+1)
	tag, err := s.command("FETCH %d:%d (UID BODY.PEEK[HEADER.FIELDS (SUBJECT FROM TO DATE)])", start, s.total)
	if err != nil {
		return nil, err
	}
	items, err := readFetchHeaders(s.reader, tag, folder, mailbox)
	for i := range items {
		items[i].UIDValidity = s.uidValidity
	}
	return items, err
}

func (s *quickIMAPSession) readDetail(item receivedMailMessage) (receiveMailDetailResponse, error) {
	if item.UID <= 0 {
		return receiveMailDetailResponse{}, fmt.Errorf("邮件编号无效")
	}
	if err := s.selectMailbox(item.Mailbox); err != nil {
		return receiveMailDetailResponse{}, err
	}
	if item.UIDValidity != 0 && item.UIDValidity != s.uidValidity {
		return receiveMailDetailResponse{}, fmt.Errorf("邮箱列表已变化，请重新获取邮件列表")
	}
	tag, err := s.command("UID FETCH %d (BODY.PEEK[])", item.UID)
	if err != nil {
		return receiveMailDetailResponse{}, err
	}
	raw, err := readFetchLiteral(s.reader, tag)
	if err != nil {
		return receiveMailDetailResponse{}, err
	}
	header := parseFullMailHeader(raw, item.Folder, s.mailbox)
	header.UID, header.UIDValidity = item.UID, s.uidValidity
	body, html := parseMailBody(raw)
	return receiveMailDetailResponse{receivedMailMessage: header, Body: body, HTML: html}, nil
}

func receiveQuickIMAPHeaders(ctx context.Context, account mailAccountTestConfig, limit int) (receiveMailMessagesResponse, []string, error) {
	if strings.EqualFold(strings.TrimSpace(account.ImapProtocol), "POP3") {
		result, err := receiveMailHeaders(ctx, account, limit)
		return result, nil, err
	}
	session, err := openQuickIMAPSession(ctx, account)
	if err != nil {
		return receiveMailMessagesResponse{}, nil, err
	}
	defer session.Close()
	return receiveQuickIMAPHeadersFromSession(session, limit)
}

func receiveQuickIMAPHeadersFromSession(session *quickIMAPSession, limit int) (receiveMailMessagesResponse, []string, error) {
	inbox, err := session.headers("INBOX", "inbox", limit)
	if err != nil {
		return receiveMailMessagesResponse{}, nil, err
	}
	result := receiveMailMessagesResponse{Inbox: inbox, Trash: []receivedMailMessage{}}
	tag, err := session.command(`LIST "" "*"`)
	if err != nil {
		return result, []string{"收件箱已获取，垃圾箱读取失败，可重新获取"}, nil
	}
	mailboxes, err := readIMAPList(session.reader, tag)
	if err != nil {
		return result, []string{"收件箱已获取，垃圾箱列表读取失败，可重新获取"}, nil
	}
	// Prefer the provider's junk folder, as the original folder discovery did.
	candidates := quickIMAPTrashCandidates(mailboxes)
	for _, mailbox := range candidates {
		items, err := session.headers(mailbox, "trash", limit)
		if err == nil {
			result.Trash = items
			return result, nil, nil
		}
		var statusError *quickIMAPStatusError
		if !errors.As(err, &statusError) {
			return result, []string{"收件箱已获取，垃圾箱读取失败，可重新获取"}, nil
		}
	}
	if len(candidates) > 0 {
		return result, []string{"收件箱已获取，垃圾箱暂时无法读取，可重新获取"}, nil
	}
	return result, nil, nil
}

func quickIMAPTrashCandidates(mailboxes []imapMailboxInfo) []string {
	var candidates []string
	seen := make(map[string]bool)
	add := func(mailbox imapMailboxInfo) {
		if seen[mailbox.Name] || strings.Contains(strings.ToLower(mailbox.Flags), `\noselect`) || strings.EqualFold(mailbox.Name, "INBOX") {
			return
		}
		seen[mailbox.Name] = true
		candidates = append(candidates, mailbox.Name)
	}
	for _, mailbox := range mailboxes {
		flags := strings.ToLower(mailbox.Flags)
		if strings.Contains(flags, `\junk`) || strings.Contains(flags, `\spam`) {
			add(mailbox)
		}
	}
	for _, mailbox := range mailboxes {
		if isLikelySpamMailbox(mailbox.Name) || isLikelySpamMailbox(mailbox.DecodedName) {
			add(mailbox)
		}
	}
	// A successful LIST lets us restrict legacy fallback names to real folders.
	for _, name := range []string{"Trash", "Junk", "Spam", "Bulk Mail", "Bulk", "Deleted Messages", "Deleted Items", "垃圾箱", "垃圾邮件", "垃圾信", "广告邮件"} {
		for _, mailbox := range mailboxes {
			if strings.EqualFold(mailbox.Name, name) || strings.EqualFold(mailbox.DecodedName, name) {
				add(mailbox)
			}
		}
	}
	return candidates
}

type quickMailDetailSession interface {
	readDetail(receivedMailMessage) (receiveMailDetailResponse, error)
	Close() error
}

type quickPOP3Session struct {
	ctx    context.Context
	conn   net.Conn
	reader *bufio.Reader
}

func openQuickPOP3Session(ctx context.Context, account mailAccountTestConfig) (*quickPOP3Session, error) {
	conn, err := openQuickMailConnection(ctx, account)
	if err != nil {
		return nil, err
	}
	session := &quickPOP3Session{ctx: ctx, conn: conn, reader: bufio.NewReader(conn)}
	err = setQuickMailDeadline(ctx, conn)
	if err == nil {
		err = expectPOP3OK(session.reader)
	}
	if err == nil {
		err = writeMailLine(conn, "USER %s", account.Email)
	}
	if err == nil {
		err = expectPOP3OK(session.reader)
	}
	if err == nil {
		err = writeMailLine(conn, "PASS %s", account.Password)
	}
	if err == nil {
		err = expectPOP3OK(session.reader)
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return session, nil
}

func (s *quickPOP3Session) Close() error { return s.conn.Close() }

func (s *quickPOP3Session) readDetail(header receivedMailMessage) (receiveMailDetailResponse, error) {
	if header.UID <= 0 {
		return receiveMailDetailResponse{}, fmt.Errorf("邮件编号无效")
	}
	if err := setQuickMailDeadline(s.ctx, s.conn); err != nil {
		return receiveMailDetailResponse{}, err
	}
	if err := writeMailLine(s.conn, "RETR %d", header.UID); err != nil {
		return receiveMailDetailResponse{}, err
	}
	if err := expectPOP3OK(s.reader); err != nil {
		return receiveMailDetailResponse{}, err
	}
	var body strings.Builder
	for {
		line, err := readMailLine(s.reader)
		if err != nil {
			return receiveMailDetailResponse{}, err
		}
		if line == "." {
			break
		}
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}
		body.WriteString(line)
		body.WriteString("\r\n")
	}
	raw := []byte(body.String())
	item := parseFullMailHeader(raw, "inbox", "INBOX")
	item.UID = header.UID
	plain, html := parseMailBody(raw)
	return receiveMailDetailResponse{receivedMailMessage: item, Body: plain, HTML: html}, nil
}

// Positions are assigned once, so parallel completion never changes list order.
func collectQuickMailDetails(ctx context.Context, items []receivedMailMessage, workers int, open func(context.Context) (quickMailDetailSession, error)) ([]receiveMailDetailResponse, error) {
	results := make([]receiveMailDetailResponse, len(items))
	if len(items) == 0 {
		return results, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var once sync.Once
	var firstError error
	fail := func(err error) { once.Do(func() { firstError = err; cancel() }) }
	for worker := 0; worker < min(max(1, workers), len(items)); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var session quickMailDetailSession
			defer func() {
				if session != nil {
					_ = session.Close()
				}
			}()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				var err error
				if session == nil {
					session, err = open(ctx)
				}
				if err == nil {
					results[index], err = session.readDetail(items[index])
				}
				if err != nil {
					fail(err)
					return
				}
			}
		}()
	}
dispatch:
	for index := range items {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
	if firstError != nil {
		return nil, firstError
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

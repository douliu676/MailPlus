package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	publicMailOperationTimeout = 90 * time.Second
	// The lease outlives both the bounded network request and its settlement.
	publicMailOperationLease = 110 * time.Second
)

type publicMailOperation struct {
	ID          string
	CardID      int
	CardKey     string
	RequestID   string
	Kind        string
	Status      string
	Fingerprint string
	Email       string
	Recipient   string
	Subject     string
	UserIP      string
	Error       string
	Reserved    int
}

type publicMailQuota struct {
	UsedCount     int    `json:"used_count"`
	ReservedCount int    `json:"reserved_count"`
	Remaining     int    `json:"remaining"`
	Busy          bool   `json:"busy"`
	Operation     string `json:"operation"`
}

type publicMailQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type publicMailOperationError struct {
	Status  int
	Message string
	Quota   publicMailQuota
}

func (e *publicMailOperationError) Error() string { return e.Message }

// These additive migrations deliberately leave existing usage and log rows intact.
func ensureCardKeyMailLogColumns(ctx context.Context, db *sql.DB) error {
	for _, statement := range []string{
		`ALTER TABLE card_key_use_logs ADD COLUMN IF NOT EXISTS operation_type TEXT NOT NULL DEFAULT 'receive'`,
		`ALTER TABLE card_key_use_logs ADD COLUMN IF NOT EXISTS operation_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE card_key_use_logs ADD COLUMN IF NOT EXISTS recipient TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE card_key_use_logs ADD COLUMN IF NOT EXISTS error_message TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE card_key_use_logs ADD COLUMN IF NOT EXISTS operation_id TEXT`,
		`CREATE UNIQUE INDEX IF NOT EXISTS card_key_use_logs_operation_idx ON card_key_use_logs(operation_id)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func ensurePublicMailOperationTables(ctx context.Context, db *sql.DB) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS card_key_mail_operations (
			id TEXT PRIMARY KEY,
			card_key_id INTEGER NOT NULL REFERENCES card_keys(id) ON DELETE CASCADE,
			card_key TEXT NOT NULL,
			request_id TEXT NOT NULL,
			operation_type TEXT NOT NULL CHECK (operation_type IN ('receive','send')),
			status TEXT NOT NULL CHECK (status IN ('running','success','failed','unknown')),
			fingerprint TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL,
			recipient TEXT NOT NULL DEFAULT '',
			mail_subject TEXT NOT NULL DEFAULT '',
			user_ip TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			reserved_count INTEGER NOT NULL DEFAULT 0 CHECK (reserved_count BETWEEN 0 AND 1),
			started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ NOT NULL,
			completed_at TIMESTAMPTZ,
			UNIQUE (card_key_id, request_id)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS card_key_mail_one_running_idx ON card_key_mail_operations(card_key_id) WHERE status='running'`,
		`CREATE INDEX IF NOT EXISTS card_key_mail_reserved_idx ON card_key_mail_operations(card_key_id) WHERE reserved_count > 0`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func readPublicMailQuota(ctx context.Context, db publicMailQueryer, card publicCardKey) (publicMailQuota, error) {
	var quota publicMailQuota
	err := db.QueryRowContext(ctx, `SELECT
		(SELECT COALESCE(SUM(charged_count),0) FROM card_key_mail_access_records WHERE card_key_id=$1),
		COALESCE(SUM(reserved_count),0),
		COALESCE(MAX(operation_type) FILTER (WHERE status='running'),'')
		FROM card_key_mail_operations WHERE card_key_id=$1`, card.ID).
		Scan(&quota.UsedCount, &quota.ReservedCount, &quota.Operation)
	quota.Remaining = max(0, card.UsageLimit-quota.UsedCount-quota.ReservedCount)
	quota.Busy = quota.Operation != ""
	return quota, err
}

const publicMailOperationColumns = `id,card_key_id,card_key,request_id,operation_type,status,fingerprint,email,recipient,mail_subject,user_ip,error_message,reserved_count`

func scanPublicMailOperation(row interface{ Scan(...any) error }) (publicMailOperation, error) {
	var op publicMailOperation
	err := row.Scan(&op.ID, &op.CardID, &op.CardKey, &op.RequestID, &op.Kind, &op.Status, &op.Fingerprint, &op.Email, &op.Recipient, &op.Subject, &op.UserIP, &op.Error, &op.Reserved)
	return op, err
}

func writePublicMailSendLog(ctx context.Context, tx *sql.Tx, op publicMailOperation) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO card_key_use_logs
		(card_key_id,card_key,bound_email,mail_subject,user_ip,operation_type,operation_status,recipient,error_message,operation_id)
		VALUES ($1,$2,$3,$4,$5,'send',$6,$7,$8,$9)
		ON CONFLICT (operation_id) DO UPDATE SET operation_status=EXCLUDED.operation_status,
		error_message=EXCLUDED.error_message`, op.CardID, op.CardKey, op.Email, op.Subject, op.UserIP, op.Status, op.Recipient, op.Error, op.ID)
	return err
}

// A crashed read is harmless to retry. A crashed send may already have been
// accepted by SMTP, so its reservation must survive restarts and log cleanup.
// Callers hold the card row lock; a live operation cannot exceed its lease.
func recoverExpiredPublicMailOperations(ctx context.Context, tx *sql.Tx, cardID int) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM card_key_mail_operations WHERE card_key_id=$1 AND operation_type='receive' AND expires_at<=clock_timestamp()`, cardID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `UPDATE card_key_mail_operations SET status='unknown',
		error_message='发送处理已中断，结果待确认；请勿重复发送', completed_at=NOW()
		WHERE card_key_id=$1 AND operation_type='send' AND status='running' AND expires_at<=clock_timestamp()
		RETURNING `+publicMailOperationColumns, cardID)
	if err != nil {
		return err
	}
	var recovered []publicMailOperation
	for rows.Next() {
		op, err := scanPublicMailOperation(rows)
		if err != nil {
			rows.Close()
			return err
		}
		recovered = append(recovered, op)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, op := range recovered {
		if err := writePublicMailSendLog(ctx, tx, op); err != nil {
			return err
		}
	}
	return nil
}

func loadPublicMailSnapshot(ctx context.Context, db *sql.DB, requested publicCardKey) (publicCardKey, publicMailQuota, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return requested, publicMailQuota{}, err
	}
	defer tx.Rollback()
	var card publicCardKey
	err = tx.QueryRowContext(ctx, `SELECT id,key,status,usage_limit,mail_days,mail_keyword,bound_email FROM card_keys WHERE id=$1 AND key=$2 FOR UPDATE`, requested.ID, requested.Key).
		Scan(&card.ID, &card.Key, &card.Status, &card.UsageLimit, &card.MailDays, &card.MailKeyword, &card.BoundEmail)
	if err != nil {
		return card, publicMailQuota{}, err
	}
	card.UsageLimit = max(1, card.UsageLimit)
	if err := recoverExpiredPublicMailOperations(ctx, tx, card.ID); err != nil {
		return card, publicMailQuota{}, err
	}
	quota, err := readPublicMailQuota(ctx, tx, card)
	if err != nil {
		return card, quota, err
	}
	return card, quota, tx.Commit()
}

func recoverPublicMailLogOperations(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT c.id,c.key FROM card_keys c WHERE EXISTS (
		SELECT 1 FROM card_key_mail_operations o WHERE o.card_key_id=c.id AND o.status='running' AND o.expires_at<=clock_timestamp()
	) ORDER BY c.id LIMIT 100`)
	if err != nil {
		return err
	}
	var cards []publicCardKey
	for rows.Next() {
		var card publicCardKey
		if err := rows.Scan(&card.ID, &card.Key); err != nil {
			rows.Close()
			return err
		}
		cards = append(cards, card)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, card := range cards {
		if _, _, err := loadPublicMailSnapshot(ctx, db, card); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

func beginPublicMailOperation(ctx context.Context, db *sql.DB, requested publicCardKey, op publicMailOperation) (publicMailOperation, publicCardKey, publicMailQuota, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return op, requested, publicMailQuota{}, false, err
	}
	defer tx.Rollback()
	card, err := lockPublicMailCard(ctx, tx, requested, op.Email)
	if err != nil {
		return op, card, publicMailQuota{}, false, &publicMailOperationError{Status: http.StatusForbidden, Message: err.Error()}
	}
	if err := recoverExpiredPublicMailOperations(ctx, tx, card.ID); err != nil {
		return op, card, publicMailQuota{}, false, err
	}
	quota, err := readPublicMailQuota(ctx, tx, card)
	if err != nil {
		return op, card, quota, false, err
	}
	previous, err := scanPublicMailOperation(tx.QueryRowContext(ctx, `SELECT `+publicMailOperationColumns+` FROM card_key_mail_operations WHERE card_key_id=$1 AND request_id=$2`, card.ID, op.RequestID))
	if err == nil {
		if previous.Kind != op.Kind || previous.Fingerprint != op.Fingerprint {
			return op, card, quota, false, &publicMailOperationError{http.StatusConflict, "请求标识已用于另一封邮件，请重新提交", quota}
		}
		return previous, card, quota, false, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return op, card, quota, false, err
	}
	if quota.Busy {
		action := "收取"
		if quota.Operation == "send" {
			action = "发送"
		}
		return op, card, quota, false, &publicMailOperationError{http.StatusConflict, "该卡密正在" + action + "邮件，请稍后重试", quota}
	}
	if quota.Remaining <= 0 {
		message := "卡密使用次数已用完"
		if quota.ReservedCount > 0 {
			message = "卡密次数已预留，存在发送结果待确认的邮件"
		}
		// Persist recovery even when an expired send consumes the last slot.
		if err := tx.Commit(); err != nil {
			return op, card, quota, false, err
		}
		return op, card, quota, false, &publicMailOperationError{http.StatusForbidden, message, quota}
	}
	op.ID, op.CardID, op.CardKey, op.Status = newToken(), card.ID, card.Key, "running"
	if op.Kind == "send" {
		op.Reserved = 1
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO card_key_mail_operations
		(id,card_key_id,card_key,request_id,operation_type,status,fingerprint,email,recipient,mail_subject,user_ip,reserved_count,expires_at)
		VALUES ($1,$2,$3,$4,$5,'running',$6,$7,$8,$9,$10,$11,clock_timestamp()+$12*INTERVAL '1 second')`,
		op.ID, card.ID, card.Key, op.RequestID, op.Kind, op.Fingerprint, op.Email, op.Recipient, op.Subject, op.UserIP, op.Reserved, int(publicMailOperationLease.Seconds()))
	if err != nil {
		return op, card, quota, false, err
	}
	if op.Kind == "send" {
		if err := writePublicMailSendLog(ctx, tx, op); err != nil {
			return op, card, quota, false, err
		}
	}
	quota, err = readPublicMailQuota(ctx, tx, card)
	if err != nil {
		return op, card, quota, false, err
	}
	return op, card, quota, true, tx.Commit()
}

func releasePublicMailRead(db *sql.DB, op publicMailOperation) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Expiry is the fallback if the database is unavailable during cleanup.
	_, _ = db.ExecContext(ctx, `DELETE FROM card_key_mail_operations WHERE id=$1 AND operation_type='receive'`, op.ID)
}

func finishPublicMailSend(ctx context.Context, db *sql.DB, operationID, status, detail string, resolve bool) (publicMailOperation, publicMailQuota, error) {
	if status != "success" && status != "failed" && status != "unknown" {
		return publicMailOperation{}, publicMailQuota{}, errors.New("无效的发送结果")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return publicMailOperation{}, publicMailQuota{}, err
	}
	defer tx.Rollback()
	var card publicCardKey
	err = tx.QueryRowContext(ctx, `SELECT c.id,c.usage_limit FROM card_keys c JOIN card_key_mail_operations o ON o.card_key_id=c.id WHERE o.id=$1 FOR UPDATE OF c`, operationID).
		Scan(&card.ID, &card.UsageLimit)
	if err != nil {
		return publicMailOperation{}, publicMailQuota{}, err
	}
	card.UsageLimit = max(1, card.UsageLimit)
	op, err := scanPublicMailOperation(tx.QueryRowContext(ctx, `SELECT `+publicMailOperationColumns+` FROM card_key_mail_operations WHERE id=$1 AND operation_type='send' FOR UPDATE`, operationID))
	if err != nil {
		return op, publicMailQuota{}, err
	}
	if resolve && (op.Status != "unknown" || status == "unknown") {
		return op, publicMailQuota{}, &publicMailOperationError{Status: http.StatusConflict, Message: "只能确认结果待确认的发件记录，请刷新日志"}
	}
	if op.Status == "success" || op.Status == "failed" {
		quota, err := readPublicMailQuota(ctx, tx, card)
		return op, quota, err
	}
	if status == "success" {
		// Successful sends join the existing counter ledger; historical counts
		// keep their meaning and clearing display logs can never refund quota.
		_, err = tx.ExecContext(ctx, `INSERT INTO card_key_mail_access_records
			(card_key_id,email,client_key,client_ip,charged_count)
			VALUES ($1,$2,$3,$4,1) ON CONFLICT (card_key_id,email,client_key) DO NOTHING`, op.CardID, op.Email, "send:"+op.ID, op.UserIP)
		if err != nil {
			return op, publicMailQuota{}, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE card_keys SET status=CASE WHEN status='disabled' THEN status ELSE 'used' END,
			used_by=$2,used_at=NOW(),updated_at=NOW() WHERE id=$1`, op.CardID, op.Email)
		if err != nil {
			return op, publicMailQuota{}, err
		}
	}
	op.Status, op.Error, op.Reserved = status, detail, 0
	if status == "unknown" {
		op.Reserved = 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE card_key_mail_operations SET status=$2,error_message=$3,reserved_count=$4,completed_at=NOW() WHERE id=$1`, op.ID, op.Status, op.Error, op.Reserved); err != nil {
		return op, publicMailQuota{}, err
	}
	if err := writePublicMailSendLog(ctx, tx, op); err != nil {
		return op, publicMailQuota{}, err
	}
	quota, err := readPublicMailQuota(ctx, tx, card)
	if err != nil {
		return op, quota, err
	}
	return op, quota, tx.Commit()
}

func publicMailOperationErrorResponse(err error) (int, string, gin.H) {
	var problem *publicMailOperationError
	if errors.As(err, &problem) {
		return problem.Status, problem.Message, gin.H{
			"used_count": problem.Quota.UsedCount, "reserved_count": problem.Quota.ReservedCount,
			"remaining": problem.Quota.Remaining, "busy": problem.Quota.Busy, "operation": problem.Quota.Operation,
		}
	}
	return http.StatusInternalServerError, "处理邮件失败，请稍后重试", nil
}

func resolvePublicMailEmail(card publicCardKey, requested string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(requested))
	if card.BoundEmail != "" {
		if email != "" && !strings.EqualFold(email, card.BoundEmail) {
			return "", errors.New("链接中的邮箱与卡密绑定邮箱不匹配")
		}
		email = strings.ToLower(strings.TrimSpace(card.BoundEmail))
	}
	if email == "" || !strings.Contains(email, "@") {
		return "", errors.New("请输入邮箱地址")
	}
	return email, nil
}

func publicMailSendMessage(op publicMailOperation, quota publicMailQuota) string {
	switch op.Status {
	case "success":
		return fmt.Sprintf("发送成功，扣除 1 次，剩余 %d 次", quota.Remaining)
	case "failed":
		return "发送失败，未扣次数：" + op.Error
	case "running":
		return "正在发送邮件，请勿重复提交"
	default:
		return "发送结果待确认，已预留 1 次，请勿重复发送"
	}
}

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Always acquire the card row before reading counters or updating an access
// record. A durable operation lease, rather than a long transaction, protects
// the card while contacting the remote mail server.
func lockPublicMailCard(ctx context.Context, tx *sql.Tx, requested publicCardKey, email string) (publicCardKey, error) {
	var card publicCardKey
	err := tx.QueryRowContext(ctx, `SELECT id, key, status, usage_limit, mail_days, mail_keyword, bound_email FROM card_keys WHERE id=$1 FOR UPDATE`, requested.ID).
		Scan(&card.ID, &card.Key, &card.Status, &card.UsageLimit, &card.MailDays, &card.MailKeyword, &card.BoundEmail)
	if err != nil {
		return card, errors.New("卡密不存在或暂时不可用")
	}
	if card.Key != requested.Key || card.Status == cardKeyStatusDisabled {
		return card, errors.New("卡密已变更或禁用")
	}
	card.BoundEmail = strings.ToLower(strings.TrimSpace(card.BoundEmail))
	if card.BoundEmail != "" && card.BoundEmail != strings.ToLower(email) {
		return card, errors.New("邮箱与卡密绑定邮箱不匹配")
	}
	if card.MailDays != requested.MailDays || strings.TrimSpace(card.MailKeyword) != requested.MailKeyword {
		return card, errors.New("卡密筛选条件已变更，请重试")
	}
	if card.UsageLimit <= 0 {
		card.UsageLimit = 1
	}
	return card, nil
}

func beginPublicMailRead(ctx context.Context, db *sql.DB, card publicCardKey, email, clientKey, clientIP string) (publicMailAccessRecord, int, int, error) {
	var record publicMailAccessRecord
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return record, 0, 0, err
	}
	defer tx.Rollback()
	card, err = lockPublicMailCard(ctx, tx, card, email)
	if err != nil {
		return record, 0, 0, err
	}
	err = tx.QueryRowContext(ctx, `SELECT last_request_at, last_message_key, charged_count FROM card_key_mail_access_records WHERE card_key_id=$1 AND email=$2 AND client_key=$3`, card.ID, email, clientKey).
		Scan(&record.LastRequestAt, &record.LastMessageKey, &record.ChargedCount)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return record, 0, 0, err
	}
	if wait := publicMailCooldownWait(record.LastRequestAt); wait > 0 {
		return record, 0, wait, nil
	}
	quota, err := readPublicMailQuota(ctx, tx, card)
	if err != nil {
		return record, 0, 0, err
	}
	used := quota.UsedCount
	if quota.Remaining <= 0 {
		return record, used, 0, errors.New("卡密无可用次数，次数已用完或已预留")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO card_key_mail_access_records (card_key_id, email, client_key, client_ip, last_request_at) VALUES ($1,$2,$3,$4,clock_timestamp()) ON CONFLICT (card_key_id,email,client_key) DO UPDATE SET client_ip=EXCLUDED.client_ip, last_request_at=clock_timestamp(), updated_at=NOW()`, card.ID, email, clientKey, clientIP); err != nil {
		return record, used, 0, err
	}
	return record, used, 0, tx.Commit()
}

func chargePublicMailUsage(ctx context.Context, db *sql.DB, card publicCardKey, email, clientKey, latestMessageKey string, usedCount int) (int, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return usedCount, false, errors.New("记录使用次数失败")
	}
	defer tx.Rollback()
	usedCount, charged, err := chargePublicMailUsageTx(ctx, tx, card, email, clientKey, latestMessageKey, usedCount)
	if err != nil {
		return usedCount, false, err
	}
	if err := tx.Commit(); err != nil {
		return usedCount, false, fmt.Errorf("记录使用次数失败: %w", err)
	}
	return usedCount, charged, nil
}

func chargePublicMailUsageTx(ctx context.Context, tx *sql.Tx, card publicCardKey, email, clientKey, latestMessageKey string, usedCount int) (int, bool, error) {
	var err error
	card, err = lockPublicMailCard(ctx, tx, card, email)
	if err != nil {
		return usedCount, false, err
	}
	quota, err := readPublicMailQuota(ctx, tx, card)
	if err != nil {
		return usedCount, false, errors.New("读取使用次数失败")
	}
	usedCount = quota.UsedCount
	var previous string
	if err := tx.QueryRowContext(ctx, `SELECT last_message_key FROM card_key_mail_access_records WHERE card_key_id=$1 AND email=$2 AND client_key=$3`, card.ID, email, clientKey).Scan(&previous); err != nil {
		return usedCount, false, errors.New("读取取件记录失败")
	}
	if latestMessageKey == "" || previous == latestMessageKey {
		return usedCount, false, nil
	}
	if quota.Remaining <= 0 {
		return usedCount, false, errors.New("卡密使用次数已用完")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE card_key_mail_access_records SET last_message_key=$4, charged_count=charged_count+1, updated_at=NOW() WHERE card_key_id=$1 AND email=$2 AND client_key=$3`, card.ID, email, clientKey, latestMessageKey); err != nil {
		return usedCount, false, errors.New("记录使用次数失败")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE card_keys SET status=$2, used_by=$3, used_at=COALESCE(used_at,NOW()), updated_at=NOW() WHERE id=$1`, card.ID, cardKeyStatusUsed, email); err != nil {
		return usedCount, false, errors.New("记录使用次数失败")
	}
	return usedCount + 1, true, nil
}

func finishPublicMailRead(ctx context.Context, db *sql.DB, card publicCardKey, op publicMailOperation, clientKey, messageKey, subject string) (publicMailQuota, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return publicMailQuota{}, false, err
	}
	defer tx.Rollback()
	card, err = lockPublicMailCard(ctx, tx, card, op.Email)
	if err != nil {
		return publicMailQuota{}, false, err
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM card_key_mail_operations WHERE id=$1 AND status='running' AND operation_type='receive' AND expires_at>clock_timestamp())`, op.ID).Scan(&active); err != nil || !active {
		return publicMailQuota{}, false, errors.New("收件操作已结束，请重试")
	}
	_, charged, err := chargePublicMailUsageTx(ctx, tx, card, op.Email, clientKey, messageKey, 0)
	if err != nil {
		return publicMailQuota{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE card_keys SET used_at=NOW(),updated_at=NOW() WHERE id=$1`, card.ID); err != nil {
		return publicMailQuota{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO card_key_use_logs(card_key_id,card_key,bound_email,mail_subject,user_ip,operation_type,operation_id)
		VALUES ($1,$2,$3,$4,$5,'receive',$6)`, card.ID, card.Key, op.Email, subject, op.UserIP, op.ID); err != nil {
		return publicMailQuota{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM card_key_mail_operations WHERE id=$1 AND operation_type='receive'`, op.ID); err != nil {
		return publicMailQuota{}, false, err
	}
	quota, err := readPublicMailQuota(ctx, tx, card)
	if err != nil {
		return quota, false, err
	}
	return quota, charged, tx.Commit()
}

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"mail-admin/houduan/ent"
)

// Both a connection and a transaction implement this interface. Batch imports
// use the same validation and insertion code as single-account creation.
type accountQuerier interface {
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

func canSetUserEnabled(current *ent.User, enabled bool) error {
	if !enabled && current.Role == "admin" {
		return errors.New("管理员账号不能禁用")
	}
	return nil
}

func saveManagedUser(ctx context.Context, db *sql.DB, id int, req saveUserRequest) (*ent.User, bool, error) {
	var hash []byte
	var err error
	if req.Password != "" {
		if err := validateNewPassword(req.Password); err != nil {
			return nil, false, err
		}
		hash, err = bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, false, err
		}
	}
	if req.Username != "" && strings.TrimSpace(req.Username) == "" {
		return nil, false, errors.New("用户名不能为空")
	}
	if req.Balance != nil && (*req.Balance < 0 || math.IsNaN(*req.Balance) || math.IsInf(*req.Balance, 0)) {
		return nil, false, errors.New("余额必须是有效的非负数")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	current := &ent.User{}
	err = tx.QueryRowContext(ctx, `SELECT id, username, email, avatar_url, balance, password_hash, role, enabled, created_at, updated_at FROM users WHERE id=$1 FOR UPDATE`, id).
		Scan(&current.ID, &current.Username, &current.Email, &current.AvatarURL, &current.Balance, &current.PasswordHash, &current.Role, &current.Enabled, &current.CreatedAt, &current.UpdatedAt)
	if err != nil {
		return nil, false, errors.New("用户不存在")
	}
	oldName, oldBalance := current.Username, current.Balance
	if req.Enabled != nil {
		if err := canSetUserEnabled(current, *req.Enabled); err != nil {
			return nil, false, err
		}
		current.Enabled = *req.Enabled
	}
	if req.Username != "" {
		current.Username = strings.TrimSpace(req.Username)
	}
	if req.Email != nil {
		current.Email = strings.TrimSpace(*req.Email)
	}
	if req.Balance != nil {
		current.Balance = *req.Balance
	}
	if hash != nil {
		current.PasswordHash = string(hash)
	}
	err = tx.QueryRowContext(ctx, `UPDATE users SET username=$2, email=$3, balance=$4, password_hash=$5, enabled=$6, must_change_password=CASE WHEN $7 THEN FALSE ELSE must_change_password END, updated_at=NOW() WHERE id=$1 RETURNING updated_at`,
		id, current.Username, current.Email, current.Balance, current.PasswordHash, current.Enabled, hash != nil).Scan(&current.UpdatedAt)
	if err != nil {
		return nil, false, errors.New("更新用户失败，请检查用户名是否重复")
	}
	if delta := current.Balance - oldBalance; delta != 0 {
		kind := "deposit"
		if delta < 0 {
			kind = "deduct"
			delta = -delta
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO balance_records (user_id,type,amount,balance_after,remark,created_at) VALUES ($1,$2,$3,$4,$5,NOW())`, id, kind, delta, current.Balance, "管理员调整余额"); err != nil {
			return nil, false, errors.New("余额记录保存失败，本次修改未保存")
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return current, oldName != current.Username || hash != nil || !current.Enabled, nil
}

func updateBalanceAtomically(ctx context.Context, db *sql.DB, id int, req updateBalanceRequest) (float64, error) {
	if req.Amount <= 0 || math.IsNaN(req.Amount) || math.IsInf(req.Amount, 0) {
		return 0, errors.New("金额必须为有效的正数")
	}
	if req.Type != "deposit" && req.Type != "deduct" {
		return 0, errors.New("余额操作类型不正确")
	}
	delta := req.Amount
	if req.Type == "deduct" {
		delta = -delta
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var balance float64
	err = tx.QueryRowContext(ctx, `UPDATE users SET balance = balance + $2, updated_at=NOW() WHERE id=$1 AND balance + $2 >= 0 RETURNING balance`, id, delta).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errors.New("用户不存在或余额不足")
	}
	if err != nil {
		return 0, fmt.Errorf("余额更新失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO balance_records (user_id, type, amount, balance_after, remark, created_at) VALUES ($1, $2, $3, $4, $5, NOW())`, id, req.Type, req.Amount, balance, strings.TrimSpace(req.Remark)); err != nil {
		return 0, fmt.Errorf("余额记录保存失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return balance, nil
}

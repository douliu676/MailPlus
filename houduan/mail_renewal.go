package main

import (
	"context"
	"database/sql"
	"log"
)

func ensureMailAccountRenewalColumn(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `ALTER TABLE mail_accounts ADD COLUMN IF NOT EXISTS last_renewed_at TIMESTAMP WITH TIME ZONE`)
	return err
}

// Record successful receive-protocol activity on the account actually used.
// Editing an account, failed reads and cancelled requests do not renew it.
func recordMailAccountRenewal(ctx context.Context, db *sql.DB, id int) {
	if id <= 0 || ctx.Err() != nil {
		return
	}
	_, err := db.ExecContext(ctx, `
UPDATE mail_accounts
SET last_renewed_at = GREATEST(last_renewed_at, NOW()), updated_at = NOW()
WHERE id = $1`, id)
	if err != nil {
		log.Printf("record mail account renewal: %v", err)
	}
}

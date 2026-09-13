package migrations

import (
	"context"
	"database/sql"
)

func upAITextAttempts(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		"ALTER TABLE stickers ADD COLUMN ai_text_attempts INTEGER NOT NULL DEFAULT 0",
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func downAITextAttempts(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "ALTER TABLE stickers DROP COLUMN ai_text_attempts")
	return err
}

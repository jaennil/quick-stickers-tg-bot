package migrations

import (
	"context"
	"database/sql"
)

func upAIText(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		"ALTER TABLE stickers ADD COLUMN ai_text TEXT",
		"ALTER TABLE stickers ADD COLUMN ai_text_model TEXT",
		"ALTER TABLE stickers ADD COLUMN ai_text_file_id TEXT",
		"ALTER TABLE stickers ADD COLUMN ai_text_attempted_at TIMESTAMP",
		"CREATE INDEX IF NOT EXISTS idx_stickers_ai_text_model ON stickers(ai_text_model)",
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func downAIText(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		"DROP INDEX IF EXISTS idx_stickers_ai_text_model",
		"ALTER TABLE stickers DROP COLUMN ai_text_attempted_at",
		"ALTER TABLE stickers DROP COLUMN ai_text_file_id",
		"ALTER TABLE stickers DROP COLUMN ai_text_model",
		"ALTER TABLE stickers DROP COLUMN ai_text",
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

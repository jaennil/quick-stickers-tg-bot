package migrations

import (
	"context"
	"database/sql"
)

func upMediaType(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "ALTER TABLE stickers ADD COLUMN media_type TEXT DEFAULT 'sticker'"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_stickers_media_type ON stickers(media_type)"); err != nil {
		return err
	}
	return nil
}

func downMediaType(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "DROP INDEX IF EXISTS idx_stickers_media_type"); err != nil {
		return err
	}
	if dialect == "sqlite3" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "ALTER TABLE stickers DROP COLUMN media_type"); err != nil {
		return err
	}
	return nil
}

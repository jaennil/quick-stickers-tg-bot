package migrations

import (
	"context"
	"database/sql"
)

func upOCRTracking(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "ALTER TABLE stickers ADD COLUMN ocr_engine TEXT DEFAULT ''"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "ALTER TABLE stickers ADD COLUMN manual_edit BOOLEAN DEFAULT FALSE"); err != nil {
		return err
	}
	return nil
}

func downOCRTracking(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "ALTER TABLE stickers DROP COLUMN manual_edit"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "ALTER TABLE stickers DROP COLUMN ocr_engine"); err != nil {
		return err
	}
	return nil
}

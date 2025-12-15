package migrations

import (
	"context"
	"database/sql"
)

func init() {
	// Will be registered in 001_init.go
}

func upDocumentID(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "ALTER TABLE stickers ADD COLUMN document_id BIGINT DEFAULT 0"); err != nil {
		return err
	}
	return nil
}

func downDocumentID(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "ALTER TABLE stickers DROP COLUMN document_id"); err != nil {
		return err
	}
	return nil
}

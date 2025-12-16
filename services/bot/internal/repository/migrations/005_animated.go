package migrations

import (
	"context"
	"database/sql"
)

func init() {
	// Will be registered in 001_init.go
}

func upAnimated(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "ALTER TABLE stickers ADD COLUMN is_animated BOOLEAN DEFAULT FALSE"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "ALTER TABLE stickers ADD COLUMN is_video BOOLEAN DEFAULT FALSE"); err != nil {
		return err
	}
	return nil
}

func downAnimated(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "ALTER TABLE stickers DROP COLUMN is_video"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "ALTER TABLE stickers DROP COLUMN is_animated"); err != nil {
		return err
	}
	return nil
}

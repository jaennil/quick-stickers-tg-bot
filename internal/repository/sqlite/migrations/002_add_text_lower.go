package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func Register() {
	goose.AddMigrationContext(upAddTextLower, downAddTextLower)
}

func upAddTextLower(ctx context.Context, tx *sql.Tx) error {
	var hasColumn bool
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info(stickers)")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "text_lower" {
			hasColumn = true
			break
		}
	}

	if !hasColumn {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE stickers ADD COLUMN text_lower TEXT"); err != nil {
			return err
		}
	}

	_, err = tx.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_stickers_text_lower ON stickers(text_lower)")
	return err
}

func downAddTextLower(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "DROP INDEX IF EXISTS idx_stickers_text_lower")
	return err
}

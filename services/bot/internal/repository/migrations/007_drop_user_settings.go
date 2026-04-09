package migrations

import (
	"context"
	"database/sql"
)

func upDropUserSettings(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS user_settings")
	return err
}

func downDropUserSettings(ctx context.Context, tx *sql.Tx) error {
	if dialect == "sqlite3" {
		_, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS user_settings (
				user_id INTEGER PRIMARY KEY,
				ocr_engine TEXT DEFAULT 'api'
			)`)
		return err
	}

	_, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS user_settings (
			user_id BIGINT PRIMARY KEY,
			ocr_engine TEXT DEFAULT 'api'
		)`)
	return err
}

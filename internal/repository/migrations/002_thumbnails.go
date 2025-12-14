package migrations

import (
	"context"
	"database/sql"
)

func upThumbnails(ctx context.Context, tx *sql.Tx) error {
	var thumbnailsSQL string
	if dialect == "sqlite3" {
		thumbnailsSQL = `
			CREATE TABLE IF NOT EXISTS sticker_thumbnails (
				file_id TEXT PRIMARY KEY,
				thumbnail BLOB NOT NULL,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			)`
	} else {
		thumbnailsSQL = `
			CREATE TABLE IF NOT EXISTS sticker_thumbnails (
				file_id TEXT PRIMARY KEY,
				thumbnail BYTEA NOT NULL,
				created_at TIMESTAMP DEFAULT NOW()
			)`
	}

	if _, err := tx.ExecContext(ctx, thumbnailsSQL); err != nil {
		return err
	}
	return nil
}

func downThumbnails(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS sticker_thumbnails"); err != nil {
		return err
	}
	return nil
}

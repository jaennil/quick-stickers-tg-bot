package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

var dialect string

func Register(d string) {
	dialect = d
	goose.AddNamedMigrationContext("001_init.go", upInit, downInit)
	goose.AddNamedMigrationContext("002_thumbnails.go", upThumbnails, downThumbnails)
	goose.AddNamedMigrationContext("003_ocr_tracking.go", upOCRTracking, downOCRTracking)
	goose.AddNamedMigrationContext("004_document_id.go", upDocumentID, downDocumentID)
	goose.AddNamedMigrationContext("005_animated.go", upAnimated, downAnimated)
	goose.AddNamedMigrationContext("006_media_type.go", upMediaType, downMediaType)
	goose.AddNamedMigrationContext("007_drop_user_settings.go", upDropUserSettings, downDropUserSettings)
	goose.AddNamedMigrationContext("008_media_jobs.go", upMediaJobs, downMediaJobs)
}

func upInit(ctx context.Context, tx *sql.Tx) error {
	var stickersSQL string
	if dialect == "sqlite3" {
		stickersSQL = `
			CREATE TABLE IF NOT EXISTS stickers (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				user_id INTEGER NOT NULL,
				sticker_id TEXT NOT NULL,
				set_name TEXT,
				file_id TEXT NOT NULL,
				text TEXT,
				text_lower TEXT,
				emoji TEXT,
				UNIQUE(user_id, sticker_id)
			)`
	} else {
		stickersSQL = `
			CREATE TABLE IF NOT EXISTS stickers (
				id SERIAL PRIMARY KEY,
				user_id BIGINT NOT NULL,
				sticker_id TEXT NOT NULL,
				set_name TEXT,
				file_id TEXT NOT NULL,
				text TEXT,
				text_lower TEXT,
				emoji TEXT,
				UNIQUE(user_id, sticker_id)
			)`
	}

	if _, err := tx.ExecContext(ctx, stickersSQL); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_stickers_user_id ON stickers(user_id)"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_stickers_text_lower ON stickers(text_lower)"); err != nil {
		return err
	}
	return nil
}

func downInit(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "DROP INDEX IF EXISTS idx_stickers_text_lower"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DROP INDEX IF EXISTS idx_stickers_user_id"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS stickers"); err != nil {
		return err
	}
	return nil
}

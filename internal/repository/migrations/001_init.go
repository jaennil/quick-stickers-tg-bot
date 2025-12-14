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
}

func upInit(ctx context.Context, tx *sql.Tx) error {
	var stickersSQL, settingsSQL string
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
		settingsSQL = `
			CREATE TABLE IF NOT EXISTS user_settings (
				user_id INTEGER PRIMARY KEY,
				ocr_engine TEXT DEFAULT 'paddle'
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
		settingsSQL = `
			CREATE TABLE IF NOT EXISTS user_settings (
				user_id BIGINT PRIMARY KEY,
				ocr_engine TEXT DEFAULT 'paddle'
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
	if _, err := tx.ExecContext(ctx, settingsSQL); err != nil {
		return err
	}
	return nil
}

func downInit(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS user_settings"); err != nil {
		return err
	}
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

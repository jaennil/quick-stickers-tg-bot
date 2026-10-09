package migrations

import (
	"context"
	"database/sql"
)

// Animated previews live apart from thumbnails: they are larger and only fetched
// for media that actually moves, while thumbnails are fetched for everything.
func upAnimations(ctx context.Context, tx *sql.Tx) error {
	table := `
		CREATE TABLE IF NOT EXISTS sticker_animations (
			file_id TEXT PRIMARY KEY,
			animation BYTEA NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)`
	if dialect == "sqlite3" {
		table = `
			CREATE TABLE IF NOT EXISTS sticker_animations (
				file_id TEXT PRIMARY KEY,
				animation BLOB NOT NULL,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			)`
	}
	statements := []string{
		table,
		"ALTER TABLE stickers ADD COLUMN animation_attempts INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE stickers ADD COLUMN animation_attempted_at TIMESTAMP",
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func downAnimations(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		"ALTER TABLE stickers DROP COLUMN animation_attempted_at",
		"ALTER TABLE stickers DROP COLUMN animation_attempts",
		"DROP TABLE IF EXISTS sticker_animations",
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

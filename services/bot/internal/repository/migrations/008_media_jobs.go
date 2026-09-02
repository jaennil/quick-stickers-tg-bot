package migrations

import (
	"context"
	"database/sql"
)

func upMediaJobs(ctx context.Context, tx *sql.Tx) error {
	var query string
	if dialect == "sqlite3" {
		query = `
			CREATE TABLE media_jobs (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				user_id INTEGER NOT NULL,
				chat_id INTEGER NOT NULL,
				progress_message_id INTEGER NOT NULL DEFAULT 0,
				sticker_id TEXT NOT NULL,
				file_id TEXT NOT NULL,
				media_type TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'pending',
				attempts INTEGER NOT NULL DEFAULT 0,
				last_error TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
				UNIQUE(user_id, sticker_id)
			)`
	} else {
		query = `
			CREATE TABLE media_jobs (
				id BIGSERIAL PRIMARY KEY,
				user_id BIGINT NOT NULL,
				chat_id BIGINT NOT NULL,
				progress_message_id INTEGER NOT NULL DEFAULT 0,
				sticker_id TEXT NOT NULL,
				file_id TEXT NOT NULL,
				media_type TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'pending',
				attempts INTEGER NOT NULL DEFAULT 0,
				last_error TEXT NOT NULL DEFAULT '',
				created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				UNIQUE(user_id, sticker_id)
			)`
	}
	if _, err := tx.ExecContext(ctx, query); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "CREATE INDEX idx_media_jobs_status_id ON media_jobs(status, id)")
	return err
}

func downMediaJobs(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS media_jobs")
	return err
}

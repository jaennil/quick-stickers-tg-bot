package migrations

import (
	"context"
	"database/sql"
)

func upAIEmbeddings(ctx context.Context, tx *sql.Tx) error {
	embeddingType := "BYTEA"
	if dialect == "sqlite3" {
		embeddingType = "BLOB"
	}

	statements := []string{
		"ALTER TABLE stickers ADD COLUMN ai_embedding " + embeddingType,
		"ALTER TABLE stickers ADD COLUMN ai_embedding_model TEXT",
		"ALTER TABLE stickers ADD COLUMN ai_embedding_text TEXT",
		"ALTER TABLE stickers ADD COLUMN ai_embedding_file_id TEXT",
		"ALTER TABLE stickers ADD COLUMN ai_embedding_attempted_at TIMESTAMP",
		"CREATE INDEX IF NOT EXISTS idx_stickers_ai_embedding_model ON stickers(user_id, ai_embedding_model)",
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func downAIEmbeddings(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		"DROP INDEX IF EXISTS idx_stickers_ai_embedding_model",
		"ALTER TABLE stickers DROP COLUMN ai_embedding_attempted_at",
		"ALTER TABLE stickers DROP COLUMN ai_embedding_file_id",
		"ALTER TABLE stickers DROP COLUMN ai_embedding_text",
		"ALTER TABLE stickers DROP COLUMN ai_embedding_model",
		"ALTER TABLE stickers DROP COLUMN ai_embedding",
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

package sqlite

import (
	"fmt"

	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/repository/migrations"
	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func New(dbPath string) (*repository.BaseRepository, error) {
	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	if err := goose.SetDialect("sqlite3"); err != nil {
		return nil, fmt.Errorf("failed to set dialect: %w", err)
	}
	migrations.Register("sqlite3")
	if err := goose.Up(db.DB, "."); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return repository.NewBase(db), nil
}

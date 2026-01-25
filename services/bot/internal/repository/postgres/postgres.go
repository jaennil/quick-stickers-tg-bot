package postgres

import (
	"fmt"

	"github.com/jaennil/sticker-search-bot/internal/config"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/repository/migrations"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
)

func New(cfg config.DatabaseConfig) (*repository.BaseRepository, error) {
	db, err := sqlx.Open("postgres", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool to prevent "too many clients" errors
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(0)  // Connections live forever (0 = unlimited)
	db.SetConnMaxIdleTime(0)  // Idle connections never expire (0 = unlimited)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	if err := goose.SetDialect("postgres"); err != nil {
		return nil, fmt.Errorf("failed to set dialect: %w", err)
	}
	migrations.Register("postgres")
	if err := goose.Up(db.DB, "."); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return repository.NewBase(db), nil
}

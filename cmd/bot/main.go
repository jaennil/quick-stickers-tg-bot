package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/jaennil/sticker-search-bot/internal/bot"
	"github.com/jaennil/sticker-search-bot/internal/config"
	"github.com/jaennil/sticker-search-bot/internal/ocr"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/repository/postgres"
	"github.com/jaennil/sticker-search-bot/internal/repository/sqlite"
)

func main() {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Telegram.Token == "" {
		log.Fatal("telegram.token is required in config.yaml")
	}

	repo, err := newRepository(cfg.Database)
	if err != nil {
		log.Fatalf("Failed to initialize repository: %v", err)
	}
	defer repo.Close()

	ocrService := ocr.New(cfg.OCR.SpaceAPIKeys)

	b, err := bot.New(cfg.Telegram.Token, repo, ocrService)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		cancel()
	}()

	b.Start(ctx)
}

func newRepository(cfg config.DatabaseConfig) (repository.Repository, error) {
	switch cfg.Driver {
	case "sqlite":
		return sqlite.New(cfg.DSN)
	case "postgres":
		return postgres.New(cfg.DSN)
	default:
		return nil, fmt.Errorf("unknown database driver: %s", cfg.Driver)
	}
}

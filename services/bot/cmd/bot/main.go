package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jaennil/sticker-search-bot/internal/api"
	"github.com/jaennil/sticker-search-bot/internal/bot"
	"github.com/jaennil/sticker-search-bot/internal/config"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/ocr"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/repository/postgres"
	"github.com/jaennil/sticker-search-bot/internal/repository/sqlite"
)

func main() {
	logger.Init()
	defer logger.Sync()

	cfg, err := config.Load("config.yaml")
	if err != nil {
		logger.Log.Fatalf("Failed to load config: %v", err)
	}

	// MODE env: "api" = API only, "" or "bot" = bot + API
	mode := os.Getenv("MODE")

	repo, err := newRepository(cfg.Database)
	if err != nil {
		logger.Log.Fatalf("Failed to initialize repository: %v", err)
	}
	defer repo.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Log.Info("Shutting down...")
		cancel()
	}()

	if mode == "api" {
		// API-only mode
		logger.Log.Info("Starting in API-only mode")
		apiServer := api.New(cfg.API, repo, cfg.Telegram.Token)
		if err := apiServer.Start(); err != nil {
			logger.Log.Fatalf("API server error: %v", err)
		}
	} else {
		// Full mode: bot + API
		if cfg.Telegram.Token == "" {
			logger.Log.Fatal("telegram.token is required in config.yaml")
		}

		ocrService := ocr.New(cfg.OCR.SpaceAPIKeys, cfg.OCR.ProxyURL)

		b, err := bot.New(cfg.Telegram.Token, repo, ocrService)
		if err != nil {
			logger.Log.Fatalf("Failed to create bot: %v", err)
		}

		// Start API server
		apiServer := api.New(cfg.API, repo, cfg.Telegram.Token)
		go func() {
			if err := apiServer.Start(); err != nil {
				logger.Log.Errorf("API server error: %v", err)
			}
		}()

		b.Start(ctx)
	}
}

func newRepository(cfg config.DatabaseConfig) (repository.Repository, error) {
	switch cfg.Driver {
	case "sqlite":
		return sqlite.New(cfg.DSN)
	case "postgres":
		return postgres.New(cfg)
	default:
		return nil, fmt.Errorf("unknown database driver: %s", cfg.Driver)
	}
}

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jaennil/sticker-search-bot/internal/ai"
	"github.com/jaennil/sticker-search-bot/internal/api"
	"github.com/jaennil/sticker-search-bot/internal/bot"
	"github.com/jaennil/sticker-search-bot/internal/config"
	"github.com/jaennil/sticker-search-bot/internal/logger"
	"github.com/jaennil/sticker-search-bot/internal/ocr"
	"github.com/jaennil/sticker-search-bot/internal/repository"
	"github.com/jaennil/sticker-search-bot/internal/repository/postgres"
	"github.com/jaennil/sticker-search-bot/internal/repository/sqlite"
	"github.com/jaennil/sticker-search-bot/internal/semantic"
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

	var embedder ai.Embedder
	var describer ai.Describer
	if cfg.AI.Token != "" {
		embedder = ai.NewClient(cfg.AI.BaseURL, cfg.AI.Token, cfg.AI.Model, cfg.AI.Dimensions)
		describer = ai.NewVisionClient(cfg.AI.BaseURL, cfg.AI.Token, cfg.AI.VisionModel)
	}
	semanticSearch := semantic.New(
		repo,
		embedder,
		describer,
		cfg.AI.MinScore,
		time.Duration(cfg.AI.IndexIntervalSeconds)*time.Second,
	)

	if mode == "api" {
		// API-only mode
		logger.Log.Info("Starting in API-only mode")
		apiServer := api.New(cfg.API, repo, semanticSearch, cfg.Telegram.Token, cfg.OCR.ProxyURL)
		if err := apiServer.Start(); err != nil {
			logger.Log.Fatalf("API server error: %v", err)
		}
	} else {
		// Full mode: bot + API
		if cfg.Telegram.Token == "" {
			logger.Log.Fatal("telegram.token is required in config.yaml")
		}

		ocrService := ocr.New(cfg.OCR.SpaceAPIKeys, cfg.OCR.ProxyURL)

		b, err := bot.New(cfg.Telegram.Token, repo, ocrService, semanticSearch)
		if err != nil {
			logger.Log.Fatalf("Failed to create bot: %v", err)
		}

		// Start API server
		apiServer := api.New(cfg.API, repo, semanticSearch, cfg.Telegram.Token, cfg.OCR.ProxyURL)
		go func() {
			if err := apiServer.Start(); err != nil {
				logger.Log.Errorf("API server error: %v", err)
			}
		}()

		if os.Getenv("AI_INDEX_ENABLED") == "false" {
			logger.Log.Info("[AI_INDEX] disabled by AI_INDEX_ENABLED=false")
		} else {
			go semanticSearch.RunIndexer(ctx)
		}
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

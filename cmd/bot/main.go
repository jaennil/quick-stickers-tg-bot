package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/jaennil/sticker-search-bot/internal/bot"
	"github.com/jaennil/sticker-search-bot/internal/config"
	"github.com/jaennil/sticker-search-bot/internal/ocr"
	"github.com/jaennil/sticker-search-bot/internal/storage"
)

func main() {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Telegram.Token == "" {
		log.Fatal("telegram.token is required in config.yaml")
	}

	// Инициализируем OCR
	ocrService := ocr.New(cfg.OCR.SpaceAPIKeys)

	// Инициализируем хранилище
	store, err := storage.New(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}
	defer store.Close()

	// Создаем бота
	b, err := bot.New(cfg.Telegram.Token, store, ocrService)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	// Запускаем бота
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		cancel()
	}()

	b.Start(ctx)
}

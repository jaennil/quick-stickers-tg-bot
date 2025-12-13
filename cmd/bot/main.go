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
	"github.com/joho/godotenv"
)

func main() {
	// Загружаем .env файл
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	cfg := config.Load()

	if cfg.TelegramToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN is required")
	}

	// Инициализируем OCR
	ocrService := ocr.New()
	if !ocrService.IsAvailable() {
		log.Println("Warning: Tesseract is not available, OCR will not work")
	}

	// Инициализируем хранилище
	store, err := storage.New(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}
	defer store.Close()

	// Создаем бота
	b, err := bot.New(cfg.TelegramToken, store, ocrService)
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

package config

import (
	"os"
)

type Config struct {
	TelegramToken string
	DatabasePath  string
}

func Load() *Config {
	return &Config{
		TelegramToken: getEnv("TELEGRAM_BOT_TOKEN", ""),
		DatabasePath:  getEnv("DATABASE_PATH", "stickers.db"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

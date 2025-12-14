package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Telegram TelegramConfig `yaml:"telegram"`
	Database DatabaseConfig `yaml:"database"`
	OCR      OCRConfig      `yaml:"ocr"`
}

type TelegramConfig struct {
	Token string `yaml:"token"`
}

type DatabaseConfig struct {
	Driver string `yaml:"driver"` // "sqlite" or "postgres"
	DSN    string `yaml:"dsn"`    // connection string or path for sqlite
}

type OCRConfig struct {
	Engine       string   `yaml:"engine"`
	SpaceAPIKeys []string `yaml:"space_api_keys"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Defaults
	if cfg.Database.Driver == "" {
		cfg.Database.Driver = "sqlite"
	}
	if cfg.Database.DSN == "" {
		cfg.Database.DSN = "stickers.db"
	}
	if cfg.OCR.Engine == "" {
		cfg.OCR.Engine = "paddle"
	}

	return &cfg, nil
}

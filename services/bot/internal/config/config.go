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
	ServerURL    string   `yaml:"server_url"`
	ProxyURL     string   `yaml:"proxy_url"`
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
	if cfg.OCR.ServerURL == "" {
		cfg.OCR.ServerURL = "http://127.0.0.1:8765"
	}

	return &cfg, nil
}

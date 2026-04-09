package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Telegram TelegramConfig `yaml:"telegram"`
	Database DatabaseConfig `yaml:"database"`
	OCR      OCRConfig      `yaml:"ocr"`
	API      APIConfig      `yaml:"api"`
}

type TelegramConfig struct {
	Token string `yaml:"token"`
}

type DatabaseConfig struct {
	Driver       string `yaml:"driver"`         // "sqlite" or "postgres"
	DSN          string `yaml:"dsn"`            // connection string or path for sqlite
	MaxOpenConns int    `yaml:"max_open_conns"` // max open connections
	MaxIdleConns int    `yaml:"max_idle_conns"` // max idle connections
}

type OCRConfig struct {
	SpaceAPIKeys []string `yaml:"space_api_keys"`
	ProxyURL     string   `yaml:"proxy_url"`
}

type APIConfig struct {
	Port   int    `yaml:"port"`
	APIKey string `yaml:"api_key"`
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
	if cfg.Database.MaxOpenConns == 0 {
		cfg.Database.MaxOpenConns = 25
	}
	if cfg.Database.MaxIdleConns == 0 {
		cfg.Database.MaxIdleConns = 10
	}
	if cfg.API.Port == 0 {
		cfg.API.Port = 8080
	}

	return &cfg, nil
}

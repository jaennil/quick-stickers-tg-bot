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
	Path string `yaml:"path"`
}

type OCRConfig struct {
	Engine       string   `yaml:"engine"`
	SpaceAPIKeys []string `yaml:"space_api_keys"`
	GoogleAPIKey string   `yaml:"google_api_key"`
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
	if cfg.Database.Path == "" {
		cfg.Database.Path = "stickers.db"
	}
	if cfg.OCR.Engine == "" {
		cfg.OCR.Engine = "paddle"
	}

	return &cfg, nil
}

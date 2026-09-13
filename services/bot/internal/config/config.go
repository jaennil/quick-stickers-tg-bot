package config

import (
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Telegram TelegramConfig `yaml:"telegram"`
	Database DatabaseConfig `yaml:"database"`
	OCR      OCRConfig      `yaml:"ocr"`
	API      APIConfig      `yaml:"api"`
	AI       AIConfig       `yaml:"ai"`
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
	// SkipWithoutText drops media the OCR could not read. It used to be the
	// only sensible choice; now the vision model can describe such media, so
	// keeping it is the default.
	SkipWithoutText bool     `yaml:"skip_without_text"`
	SpaceAPIKeys    []string `yaml:"space_api_keys"`
	ProxyURL        string   `yaml:"proxy_url"`
}

type APIConfig struct {
	Port   int    `yaml:"port"`
	APIKey string `yaml:"api_key"`
}

type AIConfig struct {
	Token                string  `yaml:"token"`
	BaseURL              string  `yaml:"base_url"`
	Model                string  `yaml:"model"`
	VisionModel          string  `yaml:"vision_model"`
	Dimensions           int     `yaml:"dimensions"`
	MinScore             float64 `yaml:"min_score"`
	IndexIntervalSeconds int     `yaml:"index_interval_seconds"`
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
	// Env overrides let the tunables be changed from the deployment manifest,
	// which is plain git YAML, instead of resealing the config secret.
	if v := os.Getenv("AI_EMBED_MODEL"); v != "" {
		cfg.AI.Model = v
	}
	if v := os.Getenv("AI_VISION_MODEL"); v != "" {
		cfg.AI.VisionModel = v
	}
	if v := os.Getenv("OCR_SKIP_WITHOUT_TEXT"); v != "" {
		cfg.OCR.SkipWithoutText = v == "true" || v == "1"
	}
	if v := os.Getenv("AI_MIN_SCORE"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.AI.MinScore = parsed
		}
	}

	if cfg.AI.BaseURL == "" {
		cfg.AI.BaseURL = "https://api.aitunnel.ru/v1"
	}
	if cfg.AI.Model == "" {
		cfg.AI.Model = "gemini-embedding-001"
	}
	if cfg.AI.VisionModel == "" {
		cfg.AI.VisionModel = "gemini-3.1-flash-lite"
	}
	if cfg.AI.Dimensions == 0 {
		cfg.AI.Dimensions = 768
	}
	if cfg.AI.MinScore == 0 {
		cfg.AI.MinScore = 0.60
	}
	if cfg.AI.IndexIntervalSeconds == 0 {
		cfg.AI.IndexIntervalSeconds = 2
	}

	return &cfg, nil
}

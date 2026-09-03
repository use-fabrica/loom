// Package config loads runtime configuration from the environment. Kept
// dependency-free (no viper/envconfig) so the core module surface stays
// small; swap in a real config loader once you need file-based config.
package config

import (
	"errors"
	"os"
	"strconv"

	"go.uber.org/fx"
)

type Config struct {
	HTTPAddr    string
	Environment string
	DatabaseURL string

	EmbedderBaseURL    string
	EmbedderAPIKey     string
	EmbedderModel      string
	EmbedderDimensions int
}

func Load() (*Config, error) {
	dbURL := os.Getenv("CE_DATABASE_URL")
	if dbURL == "" {
		return nil, errors.New("config: CE_DATABASE_URL is required")
	}
	return &Config{
		HTTPAddr:    getEnv("CE_HTTP_ADDR", ":8080"),
		Environment: getEnv("CE_ENV", "development"),
		DatabaseURL: dbURL,

		EmbedderBaseURL:    getEnv("CE_EMBEDDER_BASE_URL", "https://api.openai.com/v1"),
		EmbedderAPIKey:     getEnv("CE_EMBEDDER_API_KEY", ""),
		EmbedderModel:      getEnv("CE_EMBEDDER_MODEL", ""),
		EmbedderDimensions: getEnvInt("CE_EMBEDDER_DIMENSIONS", 0),
	}, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

var Module = fx.Module("config", fx.Provide(Load))

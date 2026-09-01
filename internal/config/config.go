// Package config loads runtime configuration from the environment. Kept
// dependency-free (no viper/envconfig) so the core module surface stays
// small; swap in a real config loader once you need file-based config.
package config

import (
	"os"

	"go.uber.org/fx"
)

type Config struct {
	HTTPAddr    string
	Environment string
}

func Load() (*Config, error) {
	return &Config{
		HTTPAddr:    getEnv("CE_HTTP_ADDR", ":8080"),
		Environment: getEnv("CE_ENV", "development"),
	}, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// func getEnvInt(key string, fallback int) int {
// 	if v := os.Getenv(key); v != "" {
// 		if i, err := strconv.Atoi(v); err == nil {
// 			return i
// 		}
// 	}
// 	return fallback
// }
//
// func getEnvFloat(key string, fallback float64) float64 {
// 	if v := os.Getenv(key); v != "" {
// 		if f, err := strconv.ParseFloat(v, 64); err == nil {
// 			return f
// 		}
// 	}
// 	return fallback
// }

var Module = fx.Module("config", fx.Provide(Load))

// Package config provides configuration handling for the API Gateway service
package config

import (
	"fmt"
	"strings"

	"github.com/knadh/koanf/parsers/dotenv"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// Config holds the API Gateway configuration
type Config struct {
	HTTPPort     string            `koanf:"http_port"`
	ServicesURLs map[string]string `koanf:"services_urls"`
}

// Load loads configuration from environment variables with defaults
func Load() (*Config, error) {
	k := koanf.New(".")

	// Set default values
	k.Set("http_port", ":8080")
	k.Set("services_urls", map[string]string{
		"balance":      "localhost:50053",
		"transactions": "localhost:50052",
		"cards":        "localhost:50051",
		"merchant":     "localhost:50054",
		"feed":         "localhost:50055",
		"disco":        "localhost:50057",
	})

	// Load from .env file if exists (optional)
	if err := k.Load(file.Provider(".env"), dotenv.Parser()); err != nil {
		// Ignore error if file doesn't exist
		if !strings.Contains(err.Error(), "no such file") {
			return nil, fmt.Errorf("error loading config from .env file: %w", err)
		}
	}

	// Load environment variables prefixed with API_
	// e.g. API_HTTP_PORT, API_SERVICES_URLS_BALANCE
	err := k.Load(env.Provider("API_", ".", func(s string) string {
		return strings.Replace(strings.ToLower(
			strings.TrimPrefix(s, "API_")), "_", ".", -1)
	}), nil)
	if err != nil {
		return nil, fmt.Errorf("error loading config from env: %w", err)
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("error unmarshalling config: %w", err)
	}

	return &cfg, nil
}

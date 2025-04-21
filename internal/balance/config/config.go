// Package config provides configuration handling for the balance service
package config

import (
	"fmt"
	"strings"

	"github.com/knadh/koanf/parsers/dotenv"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// Config holds the application configuration
type Config struct {
	DBDSN     string `koanf:"db_dsn"`
	RedisAddr string `koanf:"redis_addr"`
	HTTPPort  string `koanf:"http_port"`
	GRPCPort  string `koanf:"grpc_port"`
}

// Load loads configuration from environment variables with defaults
func Load() (*Config, error) {
	k := koanf.New(".")

	// Set default values
	k.Set("db_dsn", "user=user dbname=balance sslmode=disable")
	k.Set("redis_addr", "localhost:6379")
	k.Set("http_port", ":8082")
	k.Set("grpc_port", ":50053")

	// Load from .env file if exists (optional)
	if err := k.Load(file.Provider(".env"), dotenv.Parser()); err != nil {
		// Ignore error if file doesn't exist
		if !strings.Contains(err.Error(), "no such file") {
			return nil, fmt.Errorf("error loading config from .env file: %w", err)
		}
	}

	// Load environment variables prefixed with BALANCE_
	// e.g. BALANCE_DB_DSN, BALANCE_REDIS_ADDR
	err := k.Load(env.Provider("BALANCE_", ".", func(s string) string {
		return strings.Replace(strings.ToLower(
			strings.TrimPrefix(s, "BALANCE_")), "_", ".", -1)
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

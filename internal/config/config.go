package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	DatabaseURL string
	Port        string
	LogLevel    string
}

func Load() (Config, error) {
	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required (see .env.example)")
	}

	level := strings.ToLower(strings.TrimSpace(getenv("LOG_LEVEL", "info")))
	switch level {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, fmt.Errorf("invalid LOG_LEVEL %q: want debug|info|warn|error", level)
	}

	return Config{
		DatabaseURL: dbURL,
		Port:        getenv("PORT", "8080"),
		LogLevel:    level,
	}, nil
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

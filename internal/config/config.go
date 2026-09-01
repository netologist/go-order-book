// Package config provides environment-driven configuration for the server.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration. Populated from env vars with
// sensible defaults.
type Config struct {
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
	LogLevel        string
	LogFormat       string // "json" or "text"
}

// Load reads config from environment variables, falling back to defaults.
func Load() Config {
	return Config{
		Port:            envInt("PORT", 8080),
		ReadTimeout:     envDuration("READ_TIMEOUT", 10*time.Second),
		WriteTimeout:    envDuration("WRITE_TIMEOUT", 10*time.Second),
		ShutdownTimeout: envDuration("SHUTDOWN_TIMEOUT", 15*time.Second),
		LogLevel:        envStr("LOG_LEVEL", "info"),
		LogFormat:       envStr("LOG_FORMAT", "json"),
	}
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

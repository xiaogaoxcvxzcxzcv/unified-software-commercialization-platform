package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type LookupEnv func(string) (string, bool)

type Config struct {
	Environment        string
	HTTPAddress        string
	LogLevel           slog.Level
	HealthCheckTimeout time.Duration
	ReadHeaderTimeout  time.Duration
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	IdleTimeout        time.Duration
	ShutdownTimeout    time.Duration
	Database           Database
}

type Database struct {
	URL            string
	MaxConnections int32
	MinConnections int32
	ConnectTimeout time.Duration
}

func Load(lookup LookupEnv) (Config, error) {
	cfg := Config{
		Environment:        value(lookup, "PLATFORM_ENVIRONMENT", "local"),
		HTTPAddress:        value(lookup, "PLATFORM_HTTP_ADDRESS", ":8080"),
		HealthCheckTimeout: duration(lookup, "PLATFORM_HEALTH_CHECK_TIMEOUT", 2*time.Second),
		ReadHeaderTimeout:  duration(lookup, "PLATFORM_HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:        duration(lookup, "PLATFORM_HTTP_READ_TIMEOUT", 15*time.Second),
		WriteTimeout:       duration(lookup, "PLATFORM_HTTP_WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:        duration(lookup, "PLATFORM_HTTP_IDLE_TIMEOUT", 60*time.Second),
		ShutdownTimeout:    duration(lookup, "PLATFORM_SHUTDOWN_TIMEOUT", 15*time.Second),
		Database: Database{
			URL:            value(lookup, "PLATFORM_DATABASE_URL", ""),
			MaxConnections: int32Value(lookup, "PLATFORM_DATABASE_MAX_CONNECTIONS", 20),
			MinConnections: int32Value(lookup, "PLATFORM_DATABASE_MIN_CONNECTIONS", 2),
			ConnectTimeout: duration(lookup, "PLATFORM_DATABASE_CONNECT_TIMEOUT", 5*time.Second),
		},
	}

	level, err := parseLogLevel(value(lookup, "PLATFORM_LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}
	cfg.LogLevel = level
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	switch c.Environment {
	case "local", "test", "production":
	default:
		return fmt.Errorf("PLATFORM_ENVIRONMENT must be local, test, or production")
	}
	if strings.TrimSpace(c.HTTPAddress) == "" {
		return errors.New("PLATFORM_HTTP_ADDRESS must not be empty")
	}
	if c.Database.URL == "" {
		return errors.New("PLATFORM_DATABASE_URL is required")
	}
	u, err := url.Parse(c.Database.URL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return errors.New("PLATFORM_DATABASE_URL must be a valid PostgreSQL URL")
	}
	if c.Environment == "production" && strings.EqualFold(u.Query().Get("sslmode"), "disable") {
		return errors.New("PLATFORM_DATABASE_URL cannot disable TLS in production")
	}
	if c.Database.MaxConnections < 1 || c.Database.MaxConnections > 500 {
		return errors.New("PLATFORM_DATABASE_MAX_CONNECTIONS must be between 1 and 500")
	}
	if c.Database.MinConnections < 0 || c.Database.MinConnections > c.Database.MaxConnections {
		return errors.New("PLATFORM_DATABASE_MIN_CONNECTIONS must be between 0 and max connections")
	}
	for name, timeout := range map[string]time.Duration{
		"PLATFORM_HEALTH_CHECK_TIMEOUT":     c.HealthCheckTimeout,
		"PLATFORM_HTTP_READ_HEADER_TIMEOUT": c.ReadHeaderTimeout,
		"PLATFORM_HTTP_READ_TIMEOUT":        c.ReadTimeout,
		"PLATFORM_HTTP_WRITE_TIMEOUT":       c.WriteTimeout,
		"PLATFORM_HTTP_IDLE_TIMEOUT":        c.IdleTimeout,
		"PLATFORM_SHUTDOWN_TIMEOUT":         c.ShutdownTimeout,
		"PLATFORM_DATABASE_CONNECT_TIMEOUT": c.Database.ConnectTimeout,
	} {
		if timeout <= 0 || timeout > 10*time.Minute {
			return fmt.Errorf("%s must be greater than zero and at most 10m", name)
		}
	}
	return nil
}

func parseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, errors.New("PLATFORM_LOG_LEVEL must be debug, info, warn, or error")
	}
}

func value(lookup LookupEnv, key, fallback string) string {
	if v, ok := lookup(key); ok {
		return strings.TrimSpace(v)
	}
	return fallback
}

func duration(lookup LookupEnv, key string, fallback time.Duration) time.Duration {
	raw, ok := lookup(key)
	if !ok {
		return fallback
	}
	v, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return -1
	}
	return v
}

func int32Value(lookup LookupEnv, key string, fallback int32) int32 {
	raw, ok := lookup(key)
	if !ok {
		return fallback
	}
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 32)
	if err != nil {
		return -1
	}
	return int32(v)
}

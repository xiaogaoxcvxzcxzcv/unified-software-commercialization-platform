package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"path/filepath"
	"regexp"
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
	AdminAuth          AdminAuth
	Assembly           Assembly
}

type Database struct {
	URL            string
	MaxConnections int32
	MinConnections int32
	ConnectTimeout time.Duration
}

type AdminAuth struct {
	TokenPepper          string
	AllowedOrigins       []string
	AccessTTL            time.Duration
	RefreshTTL           time.Duration
	LoginWindow          time.Duration
	LoginBlockDuration   time.Duration
	LoginMaximumAttempts int
	BcryptCost           int
	BearerEnabled        bool
}

type Assembly struct {
	SchemaDirectory         string
	CapabilityPackageRoot   string
	TemplateRoot            string
	GeneratorToolRoot       string
	SDKToolRoot             string
	FeatureBlockCatalogPath string
	OutputTargets           []AssemblyOutputTarget
}

type AssemblyOutputTarget struct {
	Reference    string `json:"ref"`
	TargetRoot   string `json:"target_root"`
	ArtifactRoot string `json:"artifact_root"`
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
		AdminAuth: AdminAuth{
			TokenPepper:          value(lookup, "PLATFORM_ADMIN_TOKEN_PEPPER", ""),
			AllowedOrigins:       commaSeparated(value(lookup, "PLATFORM_ADMIN_ALLOWED_ORIGINS", "https://127.0.0.1:5174")),
			AccessTTL:            duration(lookup, "PLATFORM_ADMIN_ACCESS_TTL", 15*time.Minute),
			RefreshTTL:           duration(lookup, "PLATFORM_ADMIN_REFRESH_TTL", 7*24*time.Hour),
			LoginWindow:          duration(lookup, "PLATFORM_ADMIN_LOGIN_WINDOW", 15*time.Minute),
			LoginBlockDuration:   duration(lookup, "PLATFORM_ADMIN_LOGIN_BLOCK_DURATION", 15*time.Minute),
			LoginMaximumAttempts: intValue(lookup, "PLATFORM_ADMIN_LOGIN_MAXIMUM_ATTEMPTS", 5),
			BcryptCost:           intValue(lookup, "PLATFORM_ADMIN_BCRYPT_COST", 12),
			BearerEnabled:        false,
		},
		Assembly: Assembly{
			SchemaDirectory:         value(lookup, "PLATFORM_ASSEMBLY_SCHEMA_DIRECTORY", "../contracts/schemas/v1"),
			CapabilityPackageRoot:   value(lookup, "PLATFORM_ASSEMBLY_CAPABILITY_PACKAGE_ROOT", "../capability-packages"),
			TemplateRoot:            value(lookup, "PLATFORM_ASSEMBLY_TEMPLATE_ROOT", "../templates"),
			GeneratorToolRoot:       value(lookup, "PLATFORM_ASSEMBLY_GENERATOR_TOOL_ROOT", "../tools/generators"),
			SDKToolRoot:             value(lookup, "PLATFORM_ASSEMBLY_SDK_TOOL_ROOT", "../tools/sdks"),
			FeatureBlockCatalogPath: value(lookup, "PLATFORM_ASSEMBLY_FEATURE_BLOCK_CATALOG", "../contracts/catalogs/v1/feature-blocks.json"),
			OutputTargets:           outputTargets(value(lookup, "PLATFORM_ASSEMBLY_OUTPUT_TARGETS", "")),
		},
	}
	bearerEnabled, err := strictBoolValue(lookup, "PLATFORM_ADMIN_BEARER_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	cfg.AdminAuth.BearerEnabled = bearerEnabled

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
	if len(c.AdminAuth.TokenPepper) < 32 {
		return errors.New("PLATFORM_ADMIN_TOKEN_PEPPER must be at least 32 bytes")
	}
	if len(c.AdminAuth.AllowedOrigins) == 0 {
		return errors.New("PLATFORM_ADMIN_ALLOWED_ORIGINS must contain at least one exact origin")
	}
	for _, raw := range c.AdminAuth.AllowedOrigins {
		origin, err := url.Parse(raw)
		if err != nil || origin.Scheme == "" || origin.Host == "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
			return fmt.Errorf("PLATFORM_ADMIN_ALLOWED_ORIGINS contains invalid exact origin")
		}
		if c.Environment == "production" && origin.Scheme != "https" {
			return errors.New("PLATFORM_ADMIN_ALLOWED_ORIGINS must use HTTPS in production")
		}
	}
	if c.AdminAuth.AccessTTL <= 0 || c.AdminAuth.AccessTTL > 15*time.Minute {
		return errors.New("PLATFORM_ADMIN_ACCESS_TTL must be greater than zero and at most 15m")
	}
	if c.AdminAuth.RefreshTTL < time.Hour || c.AdminAuth.RefreshTTL > 90*24*time.Hour {
		return errors.New("PLATFORM_ADMIN_REFRESH_TTL must be between 1h and 2160h")
	}
	if c.AdminAuth.LoginWindow <= 0 || c.AdminAuth.LoginWindow > 24*time.Hour || c.AdminAuth.LoginBlockDuration <= 0 || c.AdminAuth.LoginBlockDuration > 24*time.Hour {
		return errors.New("administrator login window and block duration must be between zero and 24h")
	}
	if c.AdminAuth.LoginMaximumAttempts < 3 || c.AdminAuth.LoginMaximumAttempts > 20 {
		return errors.New("PLATFORM_ADMIN_LOGIN_MAXIMUM_ATTEMPTS must be between 3 and 20")
	}
	if c.AdminAuth.BcryptCost < 10 || c.AdminAuth.BcryptCost > 14 {
		return errors.New("PLATFORM_ADMIN_BCRYPT_COST must be between 10 and 14")
	}
	for name, path := range map[string]string{
		"PLATFORM_ASSEMBLY_SCHEMA_DIRECTORY":        c.Assembly.SchemaDirectory,
		"PLATFORM_ASSEMBLY_CAPABILITY_PACKAGE_ROOT": c.Assembly.CapabilityPackageRoot,
		"PLATFORM_ASSEMBLY_TEMPLATE_ROOT":           c.Assembly.TemplateRoot,
		"PLATFORM_ASSEMBLY_GENERATOR_TOOL_ROOT":     c.Assembly.GeneratorToolRoot,
		"PLATFORM_ASSEMBLY_SDK_TOOL_ROOT":           c.Assembly.SDKToolRoot,
		"PLATFORM_ASSEMBLY_FEATURE_BLOCK_CATALOG":   c.Assembly.FeatureBlockCatalogPath,
	} {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("%s must not be empty", name)
		}
	}
	if len(c.Assembly.OutputTargets) == 0 {
		return errors.New("PLATFORM_ASSEMBLY_OUTPUT_TARGETS must contain at least one server-controlled target")
	}
	seenTargets := make(map[string]struct{}, len(c.Assembly.OutputTargets))
	for _, target := range c.Assembly.OutputTargets {
		if len(target.Reference) < 3 || len(target.Reference) > 128 || !assemblyReferencePattern.MatchString(target.Reference) {
			return errors.New("PLATFORM_ASSEMBLY_OUTPUT_TARGETS contains an invalid reference")
		}
		if strings.TrimSpace(target.TargetRoot) == "" || strings.TrimSpace(target.ArtifactRoot) == "" {
			return errors.New("PLATFORM_ASSEMBLY_OUTPUT_TARGETS roots must not be empty")
		}
		if !filepath.IsAbs(target.TargetRoot) || !filepath.IsAbs(target.ArtifactRoot) {
			return errors.New("PLATFORM_ASSEMBLY_OUTPUT_TARGETS roots must be absolute paths")
		}
		if _, duplicate := seenTargets[target.Reference]; duplicate {
			return errors.New("PLATFORM_ASSEMBLY_OUTPUT_TARGETS contains a duplicate reference")
		}
		seenTargets[target.Reference] = struct{}{}
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

var assemblyReferencePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

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

func intValue(lookup LookupEnv, key string, fallback int) int {
	raw, ok := lookup(key)
	if !ok {
		return fallback
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return -1
	}
	return v
}

func strictBoolValue(lookup LookupEnv, key string, fallback bool) (bool, error) {
	raw, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	v, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return v, nil
}

func commaSeparated(raw string) []string {
	var result []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func outputTargets(raw string) []AssemblyOutputTarget {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var targets []AssemblyOutputTarget
	if err := decoder.Decode(&targets); err != nil {
		return nil
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil
	}
	return targets
}

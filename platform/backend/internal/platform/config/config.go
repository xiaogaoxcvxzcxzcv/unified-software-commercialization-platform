package config

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	"unicode/utf8"
)

type LookupEnv func(string) (string, bool)

type Config struct {
	Environment          string
	HTTPAddress          string
	LogLevel             slog.Level
	HealthCheckTimeout   time.Duration
	ReadHeaderTimeout    time.Duration
	ReadTimeout          time.Duration
	WriteTimeout         time.Duration
	IdleTimeout          time.Duration
	ShutdownTimeout      time.Duration
	Database             Database
	AdminAuth            AdminAuth
	UserAuth             UserAuth
	HostedInteraction    HostedInteraction
	SecurityNotification SecurityNotification
	Assembly             Assembly
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

type UserAuth struct {
	TokenPepper             string
	AccessTTL               time.Duration
	RefreshTTL              time.Duration
	AbsoluteTTL             time.Duration
	RefreshRecoveryWindow   time.Duration
	LoginWindow             time.Duration
	LoginBlockDuration      time.Duration
	LoginMaximumAttempts    int
	BcryptCost              int
	RecoveryTTL             time.Duration
	RecoveryMaximumAttempts int
	RecentAuthTTL           time.Duration
}

type SecurityNotification struct {
	Enabled            bool
	ProviderRef        string
	ProviderURL        string
	ProviderSecret     string
	ProviderIdempotent bool
	PayloadKey         string
	DigestKey          string
}

type HostedInteraction struct {
	BaseURL        string
	AllowedOrigin  string
	StateKeyRef    string
	StateKey       string
	DigestKey      string
	InteractionTTL time.Duration
	BrowserTTL     time.Duration
	AuthLeaseTTL   time.Duration
	GrantTTL       time.Duration
	GrantLeaseTTL  time.Duration
	AuthProofTTL   time.Duration
}

type Assembly struct {
	SchemaDirectory                   string
	CapabilityPackageRoot             string
	TemplateRoot                      string
	GeneratorToolRoot                 string
	SDKToolRoot                       string
	ExtensionRoot                     string
	ExperimentalCapabilityPackageRoot string
	ExperimentalTemplateRoot          string
	ExperimentalGeneratorToolRoot     string
	ExperimentalSDKToolRoot           string
	ExperimentalExtensionRoot         string
	FeatureBlockCatalogPath           string
	OutputTargets                     []AssemblyOutputTarget
}

type AssemblyOutputTarget struct {
	Reference    string `json:"ref"`
	Environment  string `json:"environment"`
	DisplayName  string `json:"display_name"`
	Summary      string `json:"summary"`
	IsDefault    bool   `json:"is_default"`
	TargetRoot   string `json:"target_root"`
	ArtifactRoot string `json:"artifact_root"`
}

func Load(lookup LookupEnv) (Config, error) {
	environment := value(lookup, "PLATFORM_ENVIRONMENT", "local")
	adminTokenPepper := value(lookup, "PLATFORM_ADMIN_TOKEN_PEPPER", "")
	userTokenPepper, userTokenPepperProvided := lookup("PLATFORM_USER_TOKEN_PEPPER")
	userTokenPepper = strings.TrimSpace(userTokenPepper)
	if !userTokenPepperProvided && environment == "production" {
		return Config{}, errors.New("PLATFORM_USER_TOKEN_PEPPER is required in production")
	}
	if !userTokenPepperProvided {
		userTokenPepper = deriveSecret(adminTokenPepper, "platform-user-auth-v1")
	}
	if userTokenPepper != "" && hmac.Equal([]byte(userTokenPepper), []byte(adminTokenPepper)) {
		return Config{}, errors.New("PLATFORM_USER_TOKEN_PEPPER must be independent from PLATFORM_ADMIN_TOKEN_PEPPER")
	}
	hostedStateKey, hostedStateKeyProvided := lookup("PLATFORM_HOSTED_STATE_KEY")
	hostedDigestKey, hostedDigestKeyProvided := lookup("PLATFORM_HOSTED_DIGEST_KEY")
	hostedStateKey, hostedDigestKey = strings.TrimSpace(hostedStateKey), strings.TrimSpace(hostedDigestKey)
	if environment == "production" && (!hostedStateKeyProvided || !hostedDigestKeyProvided) {
		return Config{}, errors.New("PLATFORM_HOSTED_STATE_KEY and PLATFORM_HOSTED_DIGEST_KEY are required in production")
	}
	if !hostedStateKeyProvided {
		hostedStateKey = deriveSecret(userTokenPepper, "platform-hosted-state-v1")
	}
	if !hostedDigestKeyProvided {
		hostedDigestKey = deriveSecret(userTokenPepper, "platform-hosted-digest-v1")
	}
	cfg := Config{
		Environment:        environment,
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
			TokenPepper:          adminTokenPepper,
			AllowedOrigins:       commaSeparated(value(lookup, "PLATFORM_ADMIN_ALLOWED_ORIGINS", "https://127.0.0.1:5174")),
			AccessTTL:            duration(lookup, "PLATFORM_ADMIN_ACCESS_TTL", 15*time.Minute),
			RefreshTTL:           duration(lookup, "PLATFORM_ADMIN_REFRESH_TTL", 7*24*time.Hour),
			LoginWindow:          duration(lookup, "PLATFORM_ADMIN_LOGIN_WINDOW", 15*time.Minute),
			LoginBlockDuration:   duration(lookup, "PLATFORM_ADMIN_LOGIN_BLOCK_DURATION", 15*time.Minute),
			LoginMaximumAttempts: intValue(lookup, "PLATFORM_ADMIN_LOGIN_MAXIMUM_ATTEMPTS", 5),
			BcryptCost:           intValue(lookup, "PLATFORM_ADMIN_BCRYPT_COST", 12),
			BearerEnabled:        false,
		},
		UserAuth: UserAuth{
			TokenPepper:             userTokenPepper,
			AccessTTL:               duration(lookup, "PLATFORM_USER_ACCESS_TTL", 15*time.Minute),
			RefreshTTL:              duration(lookup, "PLATFORM_USER_REFRESH_TTL", 30*24*time.Hour),
			AbsoluteTTL:             duration(lookup, "PLATFORM_USER_ABSOLUTE_TTL", 90*24*time.Hour),
			RefreshRecoveryWindow:   duration(lookup, "PLATFORM_USER_REFRESH_RECOVERY_WINDOW", 30*time.Second),
			LoginWindow:             duration(lookup, "PLATFORM_USER_LOGIN_WINDOW", 15*time.Minute),
			LoginBlockDuration:      duration(lookup, "PLATFORM_USER_LOGIN_BLOCK_DURATION", 15*time.Minute),
			LoginMaximumAttempts:    intValue(lookup, "PLATFORM_USER_LOGIN_MAXIMUM_ATTEMPTS", 5),
			BcryptCost:              intValue(lookup, "PLATFORM_USER_BCRYPT_COST", 12),
			RecoveryTTL:             duration(lookup, "PLATFORM_USER_RECOVERY_TTL", 15*time.Minute),
			RecoveryMaximumAttempts: intValue(lookup, "PLATFORM_USER_RECOVERY_MAXIMUM_ATTEMPTS", 5),
			RecentAuthTTL:           duration(lookup, "PLATFORM_USER_RECENT_AUTH_TTL", 10*time.Minute),
		},
		HostedInteraction: HostedInteraction{
			BaseURL:        value(lookup, "PLATFORM_HOSTED_BASE_URL", "https://127.0.0.1:5175"),
			AllowedOrigin:  value(lookup, "PLATFORM_HOSTED_ALLOWED_ORIGIN", "https://127.0.0.1:5175"),
			StateKeyRef:    value(lookup, "PLATFORM_HOSTED_STATE_KEY_REF", "hosted.state.v1"),
			StateKey:       hostedStateKey,
			DigestKey:      hostedDigestKey,
			InteractionTTL: duration(lookup, "PLATFORM_HOSTED_INTERACTION_TTL", 10*time.Minute),
			BrowserTTL:     duration(lookup, "PLATFORM_HOSTED_BROWSER_TTL", 10*time.Minute),
			AuthLeaseTTL:   duration(lookup, "PLATFORM_HOSTED_AUTH_LEASE_TTL", 30*time.Second),
			GrantTTL:       duration(lookup, "PLATFORM_HOSTED_GRANT_TTL", 2*time.Minute),
			GrantLeaseTTL:  duration(lookup, "PLATFORM_HOSTED_GRANT_LEASE_TTL", 30*time.Second),
			AuthProofTTL:   duration(lookup, "PLATFORM_HOSTED_AUTH_PROOF_TTL", 5*time.Minute),
		},
		SecurityNotification: SecurityNotification{
			ProviderRef:    value(lookup, "PLATFORM_SECURITY_NOTIFICATION_PROVIDER_REF", ""),
			ProviderURL:    value(lookup, "PLATFORM_SECURITY_NOTIFICATION_PROVIDER_URL", ""),
			ProviderSecret: value(lookup, "PLATFORM_SECURITY_NOTIFICATION_PROVIDER_SECRET", ""),
			PayloadKey:     value(lookup, "PLATFORM_SECURITY_NOTIFICATION_PAYLOAD_KEY", ""),
			DigestKey:      value(lookup, "PLATFORM_SECURITY_NOTIFICATION_DIGEST_KEY", ""),
		},
		Assembly: Assembly{
			SchemaDirectory:                   value(lookup, "PLATFORM_ASSEMBLY_SCHEMA_DIRECTORY", "../contracts/schemas/v1"),
			CapabilityPackageRoot:             value(lookup, "PLATFORM_ASSEMBLY_CAPABILITY_PACKAGE_ROOT", "../capability-packages"),
			TemplateRoot:                      value(lookup, "PLATFORM_ASSEMBLY_TEMPLATE_ROOT", "../templates"),
			GeneratorToolRoot:                 value(lookup, "PLATFORM_ASSEMBLY_GENERATOR_TOOL_ROOT", "../tools/generators"),
			SDKToolRoot:                       value(lookup, "PLATFORM_ASSEMBLY_SDK_TOOL_ROOT", "../tools/sdks"),
			ExtensionRoot:                     value(lookup, "PLATFORM_ASSEMBLY_EXTENSION_ROOT", "../extensions"),
			ExperimentalCapabilityPackageRoot: value(lookup, "PLATFORM_ASSEMBLY_EXPERIMENTAL_CAPABILITY_PACKAGE_ROOT", "../experimental/capability-packages"),
			ExperimentalTemplateRoot:          value(lookup, "PLATFORM_ASSEMBLY_EXPERIMENTAL_TEMPLATE_ROOT", "../experimental/templates"),
			ExperimentalGeneratorToolRoot:     value(lookup, "PLATFORM_ASSEMBLY_EXPERIMENTAL_GENERATOR_TOOL_ROOT", "../experimental/tools/generators"),
			ExperimentalSDKToolRoot:           value(lookup, "PLATFORM_ASSEMBLY_EXPERIMENTAL_SDK_TOOL_ROOT", "../experimental/tools/sdks"),
			ExperimentalExtensionRoot:         value(lookup, "PLATFORM_ASSEMBLY_EXPERIMENTAL_EXTENSION_ROOT", "../experimental/extensions"),
			FeatureBlockCatalogPath:           value(lookup, "PLATFORM_ASSEMBLY_FEATURE_BLOCK_CATALOG", "../contracts/catalogs/v1/feature-blocks.json"),
			OutputTargets:                     outputTargets(value(lookup, "PLATFORM_ASSEMBLY_OUTPUT_TARGETS", "")),
		},
	}
	bearerEnabled, err := strictBoolValue(lookup, "PLATFORM_ADMIN_BEARER_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	cfg.AdminAuth.BearerEnabled = bearerEnabled
	securityNotificationEnabled, err := strictBoolValue(lookup, "PLATFORM_SECURITY_NOTIFICATION_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	securityNotificationIdempotent, err := strictBoolValue(lookup, "PLATFORM_SECURITY_NOTIFICATION_PROVIDER_IDEMPOTENT", false)
	if err != nil {
		return Config{}, err
	}
	cfg.SecurityNotification.Enabled = securityNotificationEnabled
	cfg.SecurityNotification.ProviderIdempotent = securityNotificationIdempotent

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

func deriveSecret(root, label string) string {
	mac := hmac.New(sha256.New, []byte(root))
	_, _ = mac.Write([]byte(label))
	return hex.EncodeToString(mac.Sum(nil))
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
	if len(c.UserAuth.TokenPepper) < 32 {
		return errors.New("PLATFORM_USER_TOKEN_PEPPER must be at least 32 bytes")
	}
	if c.UserAuth.AccessTTL <= 0 || c.UserAuth.AccessTTL > 15*time.Minute {
		return errors.New("PLATFORM_USER_ACCESS_TTL must be greater than zero and at most 15m")
	}
	if c.UserAuth.RefreshTTL < time.Hour || c.UserAuth.RefreshTTL > 90*24*time.Hour || c.UserAuth.AbsoluteTTL < c.UserAuth.RefreshTTL || c.UserAuth.AbsoluteTTL > 365*24*time.Hour {
		return errors.New("user refresh TTL must be 1h..2160h and absolute TTL must contain it within 8760h")
	}
	if c.UserAuth.RefreshRecoveryWindow <= 0 || c.UserAuth.RefreshRecoveryWindow > 5*time.Minute {
		return errors.New("PLATFORM_USER_REFRESH_RECOVERY_WINDOW must be greater than zero and at most 5m")
	}
	if c.UserAuth.LoginWindow <= 0 || c.UserAuth.LoginWindow > 24*time.Hour || c.UserAuth.LoginBlockDuration <= 0 || c.UserAuth.LoginBlockDuration > 24*time.Hour {
		return errors.New("user login window and block duration must be between zero and 24h")
	}
	if c.UserAuth.LoginMaximumAttempts < 3 || c.UserAuth.LoginMaximumAttempts > 20 {
		return errors.New("PLATFORM_USER_LOGIN_MAXIMUM_ATTEMPTS must be between 3 and 20")
	}
	if c.UserAuth.BcryptCost < 10 || c.UserAuth.BcryptCost > 14 {
		return errors.New("PLATFORM_USER_BCRYPT_COST must be between 10 and 14")
	}
	if c.UserAuth.RecoveryTTL <= 0 || c.UserAuth.RecoveryTTL > 24*time.Hour || c.UserAuth.RecoveryMaximumAttempts < 1 || c.UserAuth.RecoveryMaximumAttempts > 20 {
		return errors.New("user recovery TTL must be 0..24h and maximum attempts must be 1..20")
	}
	if c.UserAuth.RecentAuthTTL <= 0 || c.UserAuth.RecentAuthTTL > 24*time.Hour {
		return errors.New("PLATFORM_USER_RECENT_AUTH_TTL must be greater than zero and at most 24h")
	}
	baseURL, err := url.Parse(c.HostedInteraction.BaseURL)
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil || baseURL.Path != "" || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return errors.New("PLATFORM_HOSTED_BASE_URL must be an exact HTTPS base URL")
	}
	origin, err := url.Parse(c.HostedInteraction.AllowedOrigin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return errors.New("PLATFORM_HOSTED_ALLOWED_ORIGIN must be an exact HTTPS origin")
	}
	if baseURL.Scheme != origin.Scheme || baseURL.Host != origin.Host {
		return errors.New("PLATFORM_HOSTED_BASE_URL and PLATFORM_HOSTED_ALLOWED_ORIGIN must be the same origin")
	}
	if len(c.HostedInteraction.StateKey) < 32 || len(c.HostedInteraction.DigestKey) < 32 {
		return errors.New("hosted state and digest keys must each be at least 32 bytes")
	}
	if !assemblyReferencePattern.MatchString(c.HostedInteraction.StateKeyRef) {
		return errors.New("PLATFORM_HOSTED_STATE_KEY_REF must be a stable secret reference")
	}
	if hmac.Equal([]byte(c.HostedInteraction.StateKey), []byte(c.HostedInteraction.DigestKey)) || hmac.Equal([]byte(c.HostedInteraction.StateKey), []byte(c.UserAuth.TokenPepper)) || hmac.Equal([]byte(c.HostedInteraction.DigestKey), []byte(c.UserAuth.TokenPepper)) {
		return errors.New("hosted state, digest, and user token secrets must be independent")
	}
	if c.HostedInteraction.InteractionTTL <= 0 || c.HostedInteraction.InteractionTTL > 30*time.Minute || c.HostedInteraction.BrowserTTL <= 0 || c.HostedInteraction.BrowserTTL > c.HostedInteraction.InteractionTTL || c.HostedInteraction.AuthLeaseTTL <= 0 || c.HostedInteraction.AuthLeaseTTL > time.Minute || c.HostedInteraction.GrantTTL <= 0 || c.HostedInteraction.GrantTTL > 10*time.Minute || c.HostedInteraction.GrantLeaseTTL <= 0 || c.HostedInteraction.GrantLeaseTTL > time.Minute || c.HostedInteraction.AuthProofTTL <= 0 || c.HostedInteraction.AuthProofTTL > c.HostedInteraction.InteractionTTL {
		return errors.New("hosted interaction TTL policy is outside the allowed bounds")
	}
	if c.SecurityNotification.Enabled {
		if !assemblyReferencePattern.MatchString(c.SecurityNotification.ProviderRef) {
			return errors.New("PLATFORM_SECURITY_NOTIFICATION_PROVIDER_REF must be a stable provider reference")
		}
		providerURL, err := url.Parse(c.SecurityNotification.ProviderURL)
		if err != nil || providerURL.Scheme != "https" || providerURL.Host == "" || providerURL.User != nil || providerURL.RawQuery != "" || providerURL.Fragment != "" {
			return errors.New("PLATFORM_SECURITY_NOTIFICATION_PROVIDER_URL must be an exact HTTPS endpoint")
		}
		if len(c.SecurityNotification.ProviderSecret) < 32 || len(c.SecurityNotification.PayloadKey) < 32 || len(c.SecurityNotification.DigestKey) < 32 {
			return errors.New("security notification provider, payload, and digest secrets must each be at least 32 bytes")
		}
		if hmac.Equal([]byte(c.SecurityNotification.ProviderSecret), []byte(c.SecurityNotification.PayloadKey)) || hmac.Equal([]byte(c.SecurityNotification.ProviderSecret), []byte(c.SecurityNotification.DigestKey)) || hmac.Equal([]byte(c.SecurityNotification.PayloadKey), []byte(c.SecurityNotification.DigestKey)) {
			return errors.New("security notification secrets must be independent")
		}
		if !c.SecurityNotification.ProviderIdempotent {
			return errors.New("PLATFORM_SECURITY_NOTIFICATION_PROVIDER_IDEMPOTENT must be true when security notification is enabled")
		}
	}
	for name, path := range map[string]string{
		"PLATFORM_ASSEMBLY_SCHEMA_DIRECTORY":                     c.Assembly.SchemaDirectory,
		"PLATFORM_ASSEMBLY_CAPABILITY_PACKAGE_ROOT":              c.Assembly.CapabilityPackageRoot,
		"PLATFORM_ASSEMBLY_TEMPLATE_ROOT":                        c.Assembly.TemplateRoot,
		"PLATFORM_ASSEMBLY_GENERATOR_TOOL_ROOT":                  c.Assembly.GeneratorToolRoot,
		"PLATFORM_ASSEMBLY_SDK_TOOL_ROOT":                        c.Assembly.SDKToolRoot,
		"PLATFORM_ASSEMBLY_EXTENSION_ROOT":                       c.Assembly.ExtensionRoot,
		"PLATFORM_ASSEMBLY_EXPERIMENTAL_CAPABILITY_PACKAGE_ROOT": c.Assembly.ExperimentalCapabilityPackageRoot,
		"PLATFORM_ASSEMBLY_EXPERIMENTAL_TEMPLATE_ROOT":           c.Assembly.ExperimentalTemplateRoot,
		"PLATFORM_ASSEMBLY_EXPERIMENTAL_GENERATOR_TOOL_ROOT":     c.Assembly.ExperimentalGeneratorToolRoot,
		"PLATFORM_ASSEMBLY_EXPERIMENTAL_SDK_TOOL_ROOT":           c.Assembly.ExperimentalSDKToolRoot,
		"PLATFORM_ASSEMBLY_EXPERIMENTAL_EXTENSION_ROOT":          c.Assembly.ExperimentalExtensionRoot,
		"PLATFORM_ASSEMBLY_FEATURE_BLOCK_CATALOG":                c.Assembly.FeatureBlockCatalogPath,
	} {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("%s must not be empty", name)
		}
	}
	if len(c.Assembly.OutputTargets) == 0 {
		return errors.New("PLATFORM_ASSEMBLY_OUTPUT_TARGETS must contain at least one server-controlled target")
	}
	seenTargets := make(map[string]struct{}, len(c.Assembly.OutputTargets))
	defaultByEnvironment := make(map[string]string, 4)
	for _, target := range c.Assembly.OutputTargets {
		if len(target.Reference) < 3 || len(target.Reference) > 128 || !assemblyReferencePattern.MatchString(target.Reference) {
			return errors.New("PLATFORM_ASSEMBLY_OUTPUT_TARGETS contains an invalid reference")
		}
		if !assemblyEnvironment(target.Environment) {
			return errors.New("PLATFORM_ASSEMBLY_OUTPUT_TARGETS contains an invalid environment")
		}
		if !validOutputTargetDisplay(target.DisplayName, 120) || !validOutputTargetDisplay(target.Summary, 240) {
			return errors.New("PLATFORM_ASSEMBLY_OUTPUT_TARGETS contains invalid display metadata")
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
		if target.IsDefault {
			if previous := defaultByEnvironment[target.Environment]; previous != "" {
				return errors.New("PLATFORM_ASSEMBLY_OUTPUT_TARGETS contains multiple defaults for one environment")
			}
			defaultByEnvironment[target.Environment] = target.Reference
		}
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

func assemblyEnvironment(value string) bool {
	switch value {
	case "development", "test", "staging", "production":
		return true
	default:
		return false
	}
}

func validOutputTargetDisplay(value string, maximum int) bool {
	if value == "" || value != strings.TrimSpace(value) || utf8.RuneCountInString(value) > maximum || strings.ContainsAny(value, "/\\") {
		return false
	}
	for _, character := range value {
		if character <= 0x1f || character == 0x7f {
			return false
		}
	}
	return true
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

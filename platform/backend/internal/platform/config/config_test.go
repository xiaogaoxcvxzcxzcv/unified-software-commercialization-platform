package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	_, err := Load(func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("expected missing database URL to fail")
	}
}

func TestLoadRejectsDisabledTLSInProduction(t *testing.T) {
	values := map[string]string{
		"PLATFORM_ENVIRONMENT":        "production",
		"PLATFORM_DATABASE_URL":       "postgres://user@db.example/platform?sslmode=disable",
		"PLATFORM_ADMIN_TOKEN_PEPPER": validTestPepper(),
	}
	_, err := Load(func(key string) (string, bool) { v, ok := values[key]; return v, ok })
	if err == nil {
		t.Fatal("expected production sslmode=disable to fail")
	}
}

func TestLoadAcceptsValidConfiguration(t *testing.T) {
	values := map[string]string{
		"PLATFORM_DATABASE_URL": "postgres://user@localhost/platform?sslmode=disable", "PLATFORM_ADMIN_TOKEN_PEPPER": validTestPepper(),
		"PLATFORM_ASSEMBLY_OUTPUT_TARGETS":              validAssemblyOutputTargets(t, "workspace.default"),
		"PLATFORM_ASSEMBLY_EXTENSION_ROOT":              "../trusted-extensions",
		"PLATFORM_ASSEMBLY_EXPERIMENTAL_EXTENSION_ROOT": "../candidate-extensions",
	}
	cfg, err := Load(func(key string) (string, bool) { v, ok := values[key]; return v, ok })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Database.MaxConnections != 20 {
		t.Fatalf("MaxConnections = %d", cfg.Database.MaxConnections)
	}
	if cfg.Assembly.SchemaDirectory == "" || cfg.Assembly.ExperimentalCapabilityPackageRoot == "" || cfg.Assembly.ExperimentalTemplateRoot == "" ||
		cfg.Assembly.ExperimentalGeneratorToolRoot == "" || cfg.Assembly.ExperimentalSDKToolRoot == "" ||
		cfg.Assembly.ExtensionRoot != "../trusted-extensions" || cfg.Assembly.ExperimentalExtensionRoot != "../candidate-extensions" || len(cfg.Assembly.OutputTargets) != 1 ||
		cfg.Assembly.OutputTargets[0].Reference != "workspace.default" || cfg.Assembly.OutputTargets[0].Environment != "test" || !cfg.Assembly.OutputTargets[0].IsDefault {
		t.Fatalf("Assembly = %#v", cfg.Assembly)
	}
}

func TestLoadRejectsEmptyExtensionCatalogRoots(t *testing.T) {
	for _, key := range []string{"PLATFORM_ASSEMBLY_EXTENSION_ROOT", "PLATFORM_ASSEMBLY_EXPERIMENTAL_EXTENSION_ROOT"} {
		t.Run(key, func(t *testing.T) {
			values := map[string]string{
				"PLATFORM_DATABASE_URL":            "postgres://user@localhost/platform?sslmode=disable",
				"PLATFORM_ADMIN_TOKEN_PEPPER":      validTestPepper(),
				"PLATFORM_ASSEMBLY_OUTPUT_TARGETS": validAssemblyOutputTargets(t, "workspace.default"),
				key:                                " ",
			}
			_, err := Load(func(name string) (string, bool) { value, ok := values[name]; return value, ok })
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("Load() error = %v, want %s", err, key)
			}
		})
	}
}

func TestLoadAllowsNoDefaultAndRejectsMultipleDefaultsPerEnvironment(t *testing.T) {
	root := t.TempDir()
	targets := []AssemblyOutputTarget{
		{Reference: "workspace.first", Environment: "production", DisplayName: "First", Summary: "First managed target", IsDefault: true, TargetRoot: filepath.Join(root, "first-target"), ArtifactRoot: filepath.Join(root, "first-artifacts")},
		{Reference: "workspace.second", Environment: "production", DisplayName: "Second", Summary: "Second managed target", IsDefault: true, TargetRoot: filepath.Join(root, "second-target"), ArtifactRoot: filepath.Join(root, "second-artifacts")},
	}
	encoded, err := json.Marshal(targets)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"PLATFORM_DATABASE_URL": "postgres://user@localhost/platform?sslmode=disable", "PLATFORM_ADMIN_TOKEN_PEPPER": validTestPepper(), "PLATFORM_ASSEMBLY_OUTPUT_TARGETS": string(encoded)}
	if _, err := Load(func(key string) (string, bool) { v, ok := values[key]; return v, ok }); err == nil || !strings.Contains(err.Error(), "multiple defaults") {
		t.Fatalf("Load() error = %v", err)
	}
	targets[0].IsDefault, targets[1].IsDefault = false, false
	encoded, _ = json.Marshal(targets)
	values["PLATFORM_ASSEMBLY_OUTPUT_TARGETS"] = string(encoded)
	if _, err := Load(func(key string) (string, bool) { v, ok := values[key]; return v, ok }); err != nil {
		t.Fatalf("no explicit default must be valid: %v", err)
	}
}

func TestLoadCountsOutputTargetDisplayLimitsByUnicodeCodePoint(t *testing.T) {
	root := t.TempDir()
	target := AssemblyOutputTarget{
		Reference: "workspace.unicode", Environment: "test", DisplayName: strings.Repeat("中", 120), Summary: strings.Repeat("文", 240),
		TargetRoot: filepath.Join(root, "target"), ArtifactRoot: filepath.Join(root, "artifacts"),
	}
	encode := func() string {
		raw, err := json.Marshal([]AssemblyOutputTarget{target})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	values := map[string]string{"PLATFORM_DATABASE_URL": "postgres://user@localhost/platform?sslmode=disable", "PLATFORM_ADMIN_TOKEN_PEPPER": validTestPepper(), "PLATFORM_ASSEMBLY_OUTPUT_TARGETS": encode()}
	if _, err := Load(func(key string) (string, bool) { v, ok := values[key]; return v, ok }); err != nil {
		t.Fatalf("Unicode limits should accept 120/240 code points: %v", err)
	}
	target.DisplayName += "中"
	values["PLATFORM_ASSEMBLY_OUTPUT_TARGETS"] = encode()
	if _, err := Load(func(key string) (string, bool) { v, ok := values[key]; return v, ok }); err == nil {
		t.Fatal("expected 121-code-point display name to fail")
	}
}

func TestLoadRejectsPathLikeOutputTargetDisplayMetadata(t *testing.T) {
	root := t.TempDir()
	target := AssemblyOutputTarget{
		Reference: "workspace.redacted", Environment: "test", DisplayName: "D:/private/source", Summary: "Managed output",
		TargetRoot: filepath.Join(root, "target"), ArtifactRoot: filepath.Join(root, "artifacts"),
	}
	encoded, err := json.Marshal([]AssemblyOutputTarget{target})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"PLATFORM_DATABASE_URL": "postgres://user@localhost/platform?sslmode=disable", "PLATFORM_ADMIN_TOKEN_PEPPER": validTestPepper(), "PLATFORM_ASSEMBLY_OUTPUT_TARGETS": string(encoded)}
	if _, err := Load(func(key string) (string, bool) { value, ok := values[key]; return value, ok }); err == nil || !strings.Contains(err.Error(), "display metadata") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadRejectsControlCharacterInOutputTargetDisplayMetadata(t *testing.T) {
	root := t.TempDir()
	target := AssemblyOutputTarget{
		Reference: "workspace.redacted", Environment: "test", DisplayName: "Local\x00workspace", Summary: "Managed output",
		TargetRoot: filepath.Join(root, "target"), ArtifactRoot: filepath.Join(root, "artifacts"),
	}
	raw, err := json.Marshal([]AssemblyOutputTarget{target})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"PLATFORM_DATABASE_URL":            "postgres://user@localhost/platform?sslmode=disable",
		"PLATFORM_ADMIN_TOKEN_PEPPER":      validTestPepper(),
		"PLATFORM_ASSEMBLY_OUTPUT_TARGETS": string(raw),
	}
	if _, err := Load(func(key string) (string, bool) { value, ok := values[key]; return value, ok }); err == nil || !strings.Contains(err.Error(), "display metadata") {
		t.Fatalf("expected control character display metadata rejection, got %v", err)
	}
}

func TestLoadRejectsInvalidAssemblyOutputReference(t *testing.T) {
	values := map[string]string{
		"PLATFORM_DATABASE_URL":            "postgres://user@localhost/platform?sslmode=disable",
		"PLATFORM_ADMIN_TOKEN_PEPPER":      validTestPepper(),
		"PLATFORM_ASSEMBLY_OUTPUT_TARGETS": validAssemblyOutputTargets(t, "../outside"),
	}
	if _, err := Load(func(key string) (string, bool) { v, ok := values[key]; return v, ok }); err == nil {
		t.Fatal("expected invalid assembly output reference to fail")
	}
}

func validTestPepper() string { return strings.Repeat("test-only-", 4) }

func validAssemblyOutputTargets(t *testing.T, reference string) string {
	t.Helper()
	root := t.TempDir()
	targets := []AssemblyOutputTarget{{
		Reference:    reference,
		Environment:  "test",
		DisplayName:  "Test workspace",
		Summary:      "Server-managed test output",
		IsDefault:    true,
		TargetRoot:   filepath.Join(root, "assembly-target"),
		ArtifactRoot: filepath.Join(root, "assembly-artifacts"),
	}}
	encoded, err := json.Marshal(targets)
	if err != nil {
		t.Fatalf("marshal assembly output targets: %v", err)
	}
	return string(encoded)
}

func TestLoadRejectsWeakAdminPepper(t *testing.T) {
	values := map[string]string{"PLATFORM_DATABASE_URL": "postgres://user@localhost/platform?sslmode=disable", "PLATFORM_ADMIN_TOKEN_PEPPER": "short"}
	if _, err := Load(func(key string) (string, bool) { v, ok := values[key]; return v, ok }); err == nil {
		t.Fatal("expected weak pepper to fail")
	}
}

func TestLoadRejectsInvalidAdminBearerBoolean(t *testing.T) {
	values := map[string]string{
		"PLATFORM_DATABASE_URL":         "postgres://user@localhost/platform?sslmode=disable",
		"PLATFORM_ADMIN_TOKEN_PEPPER":   validTestPepper(),
		"PLATFORM_ADMIN_BEARER_ENABLED": "sometimes",
	}
	_, err := Load(func(key string) (string, bool) { v, ok := values[key]; return v, ok })
	if err == nil || !strings.Contains(err.Error(), "PLATFORM_ADMIN_BEARER_ENABLED") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadUserAuthDefaultsUseDomainSeparatedPlatformSecret(t *testing.T) {
	values := map[string]string{
		"PLATFORM_DATABASE_URL":            "postgres://user@localhost/platform?sslmode=disable",
		"PLATFORM_ADMIN_TOKEN_PEPPER":      validTestPepper(),
		"PLATFORM_ASSEMBLY_OUTPUT_TARGETS": validAssemblyOutputTargets(t, "test-target"),
	}
	cfg, err := Load(func(key string) (string, bool) { v, ok := values[key]; return v, ok })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.UserAuth.TokenPepper == cfg.AdminAuth.TokenPepper || len(cfg.UserAuth.TokenPepper) < 32 || cfg.UserAuth.RefreshRecoveryWindow <= 0 || cfg.UserAuth.AbsoluteTTL < cfg.UserAuth.RefreshTTL {
		t.Fatalf("unexpected user auth defaults: %#v", cfg.UserAuth)
	}
}

func TestLoadRejectsUnsafeUserAuthPolicy(t *testing.T) {
	values := map[string]string{
		"PLATFORM_DATABASE_URL":                 "postgres://user@localhost/platform?sslmode=disable",
		"PLATFORM_ADMIN_TOKEN_PEPPER":           validTestPepper(),
		"PLATFORM_USER_TOKEN_PEPPER":            "short",
		"PLATFORM_USER_REFRESH_RECOVERY_WINDOW": "10m",
		"PLATFORM_ASSEMBLY_OUTPUT_TARGETS":      validAssemblyOutputTargets(t, "test-target"),
	}
	_, err := Load(func(key string) (string, bool) { v, ok := values[key]; return v, ok })
	if err == nil || !strings.Contains(err.Error(), "PLATFORM_USER_TOKEN_PEPPER") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadRequiresIndependentUserPepperInProduction(t *testing.T) {
	base := map[string]string{
		"PLATFORM_ENVIRONMENT":             "production",
		"PLATFORM_DATABASE_URL":            "postgres://user@localhost/platform?sslmode=require",
		"PLATFORM_ADMIN_TOKEN_PEPPER":      validTestPepper(),
		"PLATFORM_ADMIN_ALLOWED_ORIGINS":   "https://admin.example.test",
		"PLATFORM_ASSEMBLY_OUTPUT_TARGETS": validAssemblyOutputTargets(t, "production-target"),
	}
	if _, err := Load(func(key string) (string, bool) { v, ok := base[key]; return v, ok }); err == nil || !strings.Contains(err.Error(), "required in production") {
		t.Fatalf("missing production user pepper error = %v", err)
	}
	base["PLATFORM_USER_TOKEN_PEPPER"] = validTestPepper()
	if _, err := Load(func(key string) (string, bool) { v, ok := base[key]; return v, ok }); err == nil || !strings.Contains(err.Error(), "independent") {
		t.Fatalf("shared user pepper error = %v", err)
	}
}

func TestLoadSecurityNotificationFailsClosedAndAcceptsIndependentSecrets(t *testing.T) {
	base := map[string]string{
		"PLATFORM_DATABASE_URL":                  "postgres://user@localhost/platform?sslmode=disable",
		"PLATFORM_ADMIN_TOKEN_PEPPER":            validTestPepper(),
		"PLATFORM_ASSEMBLY_OUTPUT_TARGETS":       validAssemblyOutputTargets(t, "notification-target"),
		"PLATFORM_SECURITY_NOTIFICATION_ENABLED": "true",
	}
	if _, err := Load(func(key string) (string, bool) { value, ok := base[key]; return value, ok }); err == nil {
		t.Fatal("enabled security notification without provider configuration must fail")
	}
	base["PLATFORM_SECURITY_NOTIFICATION_PROVIDER_REF"] = "notification.security.primary"
	base["PLATFORM_SECURITY_NOTIFICATION_PROVIDER_URL"] = "https://notification.example.test/security-deliveries"
	base["PLATFORM_SECURITY_NOTIFICATION_PROVIDER_SECRET"] = strings.Repeat("provider-secret-", 3)
	base["PLATFORM_SECURITY_NOTIFICATION_PAYLOAD_KEY"] = strings.Repeat("payload-secret-", 3)
	base["PLATFORM_SECURITY_NOTIFICATION_DIGEST_KEY"] = strings.Repeat("digest-secret-", 3)
	base["PLATFORM_SECURITY_NOTIFICATION_PROVIDER_IDEMPOTENT"] = "true"
	cfg, err := Load(func(key string) (string, bool) { value, ok := base[key]; return value, ok })
	if err != nil {
		t.Fatalf("Load() security notification error = %v", err)
	}
	if !cfg.SecurityNotification.Enabled || !cfg.SecurityNotification.ProviderIdempotent || cfg.SecurityNotification.ProviderRef != "notification.security.primary" {
		t.Fatalf("SecurityNotification = %#v", cfg.SecurityNotification)
	}
	base["PLATFORM_SECURITY_NOTIFICATION_DIGEST_KEY"] = base["PLATFORM_SECURITY_NOTIFICATION_PAYLOAD_KEY"]
	if _, err := Load(func(key string) (string, bool) { value, ok := base[key]; return value, ok }); err == nil || !strings.Contains(err.Error(), "independent") {
		t.Fatalf("shared notification key error = %v", err)
	}
}

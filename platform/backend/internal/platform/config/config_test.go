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
		"PLATFORM_ASSEMBLY_OUTPUT_TARGETS": validAssemblyOutputTargets(t, "workspace.default"),
	}
	cfg, err := Load(func(key string) (string, bool) { v, ok := values[key]; return v, ok })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Database.MaxConnections != 20 {
		t.Fatalf("MaxConnections = %d", cfg.Database.MaxConnections)
	}
	if cfg.Assembly.SchemaDirectory == "" || len(cfg.Assembly.OutputTargets) != 1 || cfg.Assembly.OutputTargets[0].Reference != "workspace.default" {
		t.Fatalf("Assembly = %#v", cfg.Assembly)
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

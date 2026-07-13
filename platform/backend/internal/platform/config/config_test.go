package config

import "testing"

func TestLoadRequiresDatabaseURL(t *testing.T) {
	_, err := Load(func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("expected missing database URL to fail")
	}
}

func TestLoadRejectsDisabledTLSInProduction(t *testing.T) {
	values := map[string]string{
		"PLATFORM_ENVIRONMENT":  "production",
		"PLATFORM_DATABASE_URL": "postgres://user@db.example/platform?sslmode=disable",
	}
	_, err := Load(func(key string) (string, bool) { v, ok := values[key]; return v, ok })
	if err == nil {
		t.Fatal("expected production sslmode=disable to fail")
	}
}

func TestLoadAcceptsValidConfiguration(t *testing.T) {
	values := map[string]string{"PLATFORM_DATABASE_URL": "postgres://user@localhost/platform?sslmode=disable"}
	cfg, err := Load(func(key string) (string, bool) { v, ok := values[key]; return v, ok })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Database.MaxConnections != 20 {
		t.Fatalf("MaxConnections = %d", cfg.Database.MaxConnections)
	}
}

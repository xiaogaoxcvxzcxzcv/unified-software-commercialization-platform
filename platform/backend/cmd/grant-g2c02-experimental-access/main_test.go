package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/platform/config"
)

func TestParseArgsRequiresExplicitAcceptanceAndSafeAdmin(t *testing.T) {
	if _, err := parseArgs([]string{"--admin-user-id", "admin_123"}); !errors.Is(err, errUsage) {
		t.Fatalf("missing acceptance error = %v", err)
	}
	if _, err := parseArgs([]string{"--acceptance-g2c02", "--admin-user-id", "../admin"}); !errors.Is(err, errUsage) {
		t.Fatalf("unsafe admin error = %v", err)
	}
	got, err := parseArgs([]string{"--acceptance-g2c02", "--admin-user-id", "user_admin-123"})
	if err != nil || got != "user_admin-123" {
		t.Fatalf("parse args = %q, %v", got, err)
	}
}

func TestValidateLocalPlatformDatabaseURLFailsClosed(t *testing.T) {
	for _, raw := range []string{
		"",
		"postgres://platform@database.example/platform_local?sslmode=require",
		"postgres://platform@127.0.0.1/platform_prod?sslmode=disable",
		"mysql://platform@127.0.0.1/platform_local",
	} {
		if err := validateLocalPlatformDatabaseURL(raw); err == nil {
			t.Fatalf("validateLocalPlatformDatabaseURL(%q) succeeded", raw)
		}
	}
	if err := validateLocalPlatformDatabaseURL("postgres://platform@127.0.0.1:15432/platform_local?sslmode=disable"); err != nil {
		t.Fatalf("loopback platform_local rejected: %v", err)
	}
	if err := validateLocalPlatformDatabaseURL("postgres://platform@127.0.0.1:15432/platform_g2c02_acceptance?sslmode=disable"); err != nil {
		t.Fatalf("loopback platform_g2c02_acceptance rejected: %v", err)
	}
}

func TestRunUsesConfigAndGrantWithoutLeakingDatabaseURL(t *testing.T) {
	var output bytes.Buffer
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	lookup := func(key string) (string, bool) {
		switch key {
		case "PLATFORM_ENVIRONMENT":
			return "local", true
		case "PLATFORM_DATABASE_URL":
			return "postgres://platform@127.0.0.1:15432/platform_local?sslmode=disable", true
		case "PLATFORM_ADMIN_TOKEN_PEPPER":
			return strings.Repeat("p", 32), true
		}
		return "", false
	}
	deps := dependencies{
		loadConfig: func(config.LookupEnv) (config.Config, error) {
			return config.Config{
				Environment: "local",
				Database: config.Database{
					URL: "postgres://platform@127.0.0.1:15432/platform_local?sslmode=disable",
				},
			}, nil
		},
		openDB: func(context.Context, config.Database) (databaseHandle, error) {
			return fakeDB{}, nil
		},
		grant: func(_ context.Context, _ *pgxpool.Pool, options grantOptions) (grantResult, error) {
			if options.AdminUserID != "user_admin-123" || !options.Now.Equal(now) {
				t.Fatalf("grant options = %#v", options)
			}
			return grantResult{AdminUserID: options.AdminUserID, RoleCode: experimentalRoleCode, PermissionCode: permissionExperimentalUse, AuthorizationVersion: 3}, nil
		},
		now: func() time.Time { return now },
	}
	err := run(context.Background(), []string{"--acceptance-g2c02", "--admin-user-id", "user_admin-123"}, lookup, &output, deps)
	if err != nil {
		t.Fatalf("run error = %v", err)
	}
	got := output.String()
	if !strings.Contains(got, "authorization_version=3") {
		t.Fatalf("output = %q", got)
	}
	if strings.Contains(got, "secret-password") || strings.Contains(got, "postgres://") {
		t.Fatalf("output leaked database URL: %q", got)
	}
}

type fakeDB struct{}

func (fakeDB) Close()              {}
func (fakeDB) Pool() *pgxpool.Pool { return nil }

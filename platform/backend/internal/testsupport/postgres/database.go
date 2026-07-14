package postgres

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/platform/migrations"
)

var databaseSequence atomic.Uint64

type Database struct {
	Pool          *pgxpool.Pool
	MigrationPath string
}

func Open(t *testing.T) Database {
	t.Helper()
	controlURL := os.Getenv("TEST_DATABASE_URL")
	if controlURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; PostgreSQL integration test skipped")
	}
	config, err := pgxpool.ParseConfig(controlURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	validateControlConfig(t, config)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	control, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("connect PostgreSQL control database: %v", err)
	}
	if err := control.Ping(ctx); err != nil {
		control.Close()
		t.Fatalf("ping PostgreSQL control database: %v", err)
	}

	databaseName := fmt.Sprintf("platform_test_%d_%d", os.Getpid(), databaseSequence.Add(1))
	identifier := pgx.Identifier{databaseName}.Sanitize()
	if _, err := control.Exec(ctx, "CREATE DATABASE "+identifier+" TEMPLATE template0 ENCODING 'UTF8'"); err != nil {
		control.Close()
		t.Fatalf("create isolated PostgreSQL test database: %v", err)
	}

	testConfig := config.Copy()
	testConfig.ConnConfig.Database = databaseName
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		dropDatabase(t, control, databaseName)
		control.Close()
		t.Fatalf("connect isolated PostgreSQL test database: %v", err)
	}
	migrationPath := migrationDirectory(t)
	if err := migrations.ApplyUp(ctx, pool, migrationPath); err != nil {
		pool.Close()
		dropDatabase(t, control, databaseName)
		control.Close()
		t.Fatalf("apply migrations to isolated PostgreSQL test database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropDatabase(t, control, databaseName)
		control.Close()
	})
	return Database{Pool: pool, MigrationPath: migrationPath}
}

func validateControlConfig(t *testing.T, config *pgxpool.Config) {
	t.Helper()
	if config.ConnConfig.Database != "platform_test_control" {
		t.Fatalf("TEST_DATABASE_URL must target platform_test_control, got %q", config.ConnConfig.Database)
	}
	host := strings.TrimSpace(strings.ToLower(config.ConnConfig.Host))
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatalf("TEST_DATABASE_URL must use a loopback host, got %q", config.ConnConfig.Host)
		}
	}
}

func dropDatabase(t *testing.T, control *pgxpool.Pool, databaseName string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	identifier := pgx.Identifier{databaseName}.Sanitize()
	if _, err := control.Exec(ctx, "DROP DATABASE IF EXISTS "+identifier+" WITH (FORCE)"); err != nil {
		t.Errorf("drop isolated PostgreSQL test database %q: %v", databaseName, err)
	}
}

func migrationDirectory(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate PostgreSQL test helper source")
	}
	directory := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "migrations"))
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("locate migration directory: %v", err)
	}
	return directory
}

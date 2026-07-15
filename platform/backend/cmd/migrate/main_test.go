package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/platform/config"
	"platform.local/capability-platform/backend/internal/platform/migrations"
)

type fakeDatabase struct {
	closed bool
	pool   *pgxpool.Pool
}

func (f *fakeDatabase) Close()              { f.closed = true }
func (f *fakeDatabase) Pool() *pgxpool.Pool { return f.pool }

func TestRunDefaultsToUpAndClosesDatabase(t *testing.T) {
	t.Parallel()

	db := &fakeDatabase{}
	workingDirectory := t.TempDir()
	wantDirectory := filepath.Join(workingDirectory, "platform", "backend", "migrations")
	var appliedDirectory string
	deps := dependencies{
		loadConfig: func(config.LookupEnv) (config.Config, error) {
			return config.Config{Database: config.Database{URL: "redacted"}}, nil
		},
		openDB: func(context.Context, config.Database) (databaseConnection, error) {
			return db, nil
		},
		applyUp: func(_ context.Context, pool *pgxpool.Pool, directory string) error {
			if pool != db.pool {
				t.Fatal("run passed an unexpected database pool")
			}
			appliedDirectory = directory
			return nil
		},
		getwd: func() (string, error) { return workingDirectory, nil },
		loadFiles: func(directory string) ([]migrations.Migration, error) {
			if directory == wantDirectory {
				return []migrations.Migration{{Version: 1}}, nil
			}
			return nil, os.ErrNotExist
		},
	}

	if err := run(context.Background(), nil, nil, deps); err != nil {
		t.Fatalf("run returned an error: %v", err)
	}
	if appliedDirectory != wantDirectory {
		t.Fatalf("apply directory = %q, want %q", appliedDirectory, wantDirectory)
	}
	if !db.closed {
		t.Fatal("database was not closed")
	}
}

func TestRunRejectsEveryActionExceptUp(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"down"}, {"status"}, {"up", "extra"}, {"--help"}} {
		args := args
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Parallel()
			called := false
			deps := dependencies{
				loadConfig: func(config.LookupEnv) (config.Config, error) {
					called = true
					return config.Config{}, nil
				},
			}
			err := run(context.Background(), args, nil, deps)
			if !errors.Is(err, errInvalidAction) {
				t.Fatalf("run error = %v, want %v", err, errInvalidAction)
			}
			if called {
				t.Fatal("configuration was loaded for a rejected action")
			}
		})
	}
}

func TestLocateMigrationDirectoryFromRepositoryAndBackend(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	migrationDirectory := filepath.Join(root, "platform", "backend", "migrations")
	if err := os.MkdirAll(migrationDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMigrationPair(t, migrationDirectory)

	for _, start := range []string{root, filepath.Join(root, "platform", "backend", "cmd", "migrate")} {
		got, err := locateMigrationDirectory(start, migrations.Load)
		if err != nil {
			t.Fatalf("locate from %q: %v", start, err)
		}
		if got != migrationDirectory {
			t.Fatalf("locate from %q = %q, want %q", start, got, migrationDirectory)
		}
	}
}

func TestRunDoesNotExposeDatabaseURL(t *testing.T) {
	t.Parallel()

	const secretURL = "postgres://secret-user:secret-password@database.example/platform"
	workingDirectory := t.TempDir()
	deps := dependencies{
		loadConfig: func(config.LookupEnv) (config.Config, error) {
			return config.Config{Database: config.Database{URL: secretURL}}, nil
		},
		openDB: func(context.Context, config.Database) (databaseConnection, error) {
			return nil, errors.New("dial " + secretURL)
		},
		getwd: func() (string, error) { return workingDirectory, nil },
		loadFiles: func(string) ([]migrations.Migration, error) {
			return []migrations.Migration{{Version: 1}}, nil
		},
	}

	err := run(context.Background(), []string{"up"}, nil, deps)
	if err == nil {
		t.Fatal("run unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), secretURL) || strings.Contains(err.Error(), "secret-password") {
		t.Fatalf("error leaked database URL: %v", err)
	}
}

func writeMigrationPair(t *testing.T, directory string) {
	t.Helper()
	for _, file := range []struct {
		name string
		body string
	}{
		{name: "000001_test.up.sql", body: "BEGIN;\nSELECT 1;\nCOMMIT;\n"},
		{name: "000001_test.down.sql", body: "BEGIN;\nSELECT 1;\nCOMMIT;\n"},
	} {
		if err := os.WriteFile(filepath.Join(directory, file.name), []byte(file.body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/platform/config"
	"platform.local/capability-platform/backend/internal/platform/database"
	"platform.local/capability-platform/backend/internal/platform/migrations"
)

var errInvalidAction = errors.New("migration action must be up")

type databaseConnection interface {
	Close()
	Pool() *pgxpool.Pool
}

type dependencies struct {
	loadConfig func(config.LookupEnv) (config.Config, error)
	openDB     func(context.Context, config.Database) (databaseConnection, error)
	applyUp    func(context.Context, *pgxpool.Pool, string) error
	getwd      func() (string, error)
	loadFiles  func(string) ([]migrations.Migration, error)
}

func defaultDependencies() dependencies {
	return dependencies{
		loadConfig: config.Load,
		openDB: func(ctx context.Context, cfg config.Database) (databaseConnection, error) {
			return database.Open(ctx, cfg)
		},
		applyUp:   migrations.ApplyUp,
		getwd:     os.Getwd,
		loadFiles: migrations.Load,
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.LookupEnv, defaultDependencies()); err != nil {
		logger.Error("database migration failed", "error", err.Error())
		os.Exit(1)
	}
	logger.Info("database migrations are current")
}

func run(ctx context.Context, args []string, lookup config.LookupEnv, deps dependencies) error {
	if err := validateAction(args); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("migration canceled: %w", err)
	}

	cfg, err := deps.loadConfig(lookup)
	if err != nil {
		return fmt.Errorf("load platform configuration: %w", err)
	}
	workingDirectory, err := deps.getwd()
	if err != nil {
		return errors.New("locate migration directory: read working directory")
	}
	migrationDirectory, err := locateMigrationDirectory(workingDirectory, deps.loadFiles)
	if err != nil {
		return err
	}

	db, err := deps.openDB(ctx, cfg.Database)
	if err != nil {
		// Database driver errors can include the connection URL. Keep the public
		// error at the operation boundary so credentials never reach logs.
		return errors.New("connect to migration database")
	}
	defer db.Close()

	if err := deps.applyUp(ctx, db.Pool(), migrationDirectory); err != nil {
		// Migration SQL and driver failures may also contain connection details.
		return errors.New("apply database migrations")
	}
	return nil
}

func validateAction(args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) == 1 && args[0] == "up" {
		return nil
	}
	return errInvalidAction
}

func locateMigrationDirectory(start string, load func(string) ([]migrations.Migration, error)) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", errors.New("locate migration directory: resolve working directory")
	}

	for {
		candidates := []string{
			filepath.Join(current, "migrations"),
			filepath.Join(current, "platform", "backend", "migrations"),
		}
		for _, candidate := range candidates {
			loaded, loadErr := load(candidate)
			if loadErr == nil && len(loaded) > 0 {
				return candidate, nil
			}
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", errors.New("locate migration directory: no valid migration set found")
}

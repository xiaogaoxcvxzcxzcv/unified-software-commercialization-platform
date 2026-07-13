package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"platform.local/commercialization/backend/internal/platform/config"
	"platform.local/commercialization/backend/internal/platform/database"
	"platform.local/commercialization/backend/internal/platform/logging"
	platformserver "platform.local/commercialization/backend/internal/platform/server"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	bootstrapLogger := logging.New(os.Stderr, slog.LevelInfo)
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		bootstrapLogger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	logger := logging.New(os.Stdout, cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		logger.Error("database initialization failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	app := platformserver.New(cfg, logger, db, platformserver.BuildInfo{
		Version: version, Commit: commit, BuildTime: buildTime,
	})
	if err := app.Run(ctx); err != nil {
		logger.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
}

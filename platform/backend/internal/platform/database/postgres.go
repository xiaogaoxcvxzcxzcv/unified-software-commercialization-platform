package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/commercialization/backend/internal/platform/config"
)

type Postgres struct {
	pool         *pgxpool.Pool
	probeTimeout time.Duration
}

func Open(ctx context.Context, cfg config.Database) (*Postgres, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	poolConfig.MaxConns = cfg.MaxConnections
	poolConfig.MinConns = cfg.MinConnections
	poolConfig.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return &Postgres{pool: pool, probeTimeout: cfg.ConnectTimeout}, nil
}

func (p *Postgres) Name() string { return "postgres" }

func (p *Postgres) Check(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, p.probeTimeout)
	defer cancel()
	return p.pool.Ping(probeCtx)
}

func (p *Postgres) Close() { p.pool.Close() }

// Pool is intentionally exposed only to adapters inside the owning module's
// composition root. Modules must never share repositories or query each other's tables.
func (p *Postgres) Pool() *pgxpool.Pool { return p.pool }

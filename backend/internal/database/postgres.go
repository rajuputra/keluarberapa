// Package database owns the PostgreSQL connection pool and the migration
// runner. Repositories in the domain packages take a *DB and are expected to
// write parameterised SQL only: string-concatenated queries are never
// acceptable (architecture.md section 2).
package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rajuputra/keluarberapa/backend/internal/config"
)

// DB is the application handle on PostgreSQL. The embedded pool exposes the
// pgx query API (Query, QueryRow, Exec, Begin), all of which bind arguments as
// parameters rather than interpolating them into SQL text.
type DB struct {
	*pgxpool.Pool

	healthTimeout time.Duration
}

// Connect opens the pool and verifies it with a round trip, so that a bad DSN
// or an unreachable server fails at startup rather than on the first request.
func Connect(ctx context.Context, cfg config.Database, logger *slog.Logger) (*DB, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL.Reveal())
	if err != nil {
		// The error from pgx can echo back the DSN, which contains the
		// password. Report only that parsing failed.
		return nil, errors.New("parse DATABASE_URL: invalid connection string")
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	// Sessions run in UTC so that stored timestamps never depend on the
	// server's locale. Conversion to Asia/Jakarta happens in queries that need
	// calendar semantics, using AT TIME ZONE.
	poolCfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	poolCfg.ConnConfig.RuntimeParams["application_name"] = "keluarberapa-api"

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	db := &DB{Pool: pool, healthTimeout: cfg.HealthTimeout}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	logger.Info("database connected",
		slog.String("host", poolCfg.ConnConfig.Host),
		slog.Uint64("port", uint64(poolCfg.ConnConfig.Port)),
		slog.String("database", poolCfg.ConnConfig.Database),
		slog.Int64("max_conns", int64(poolCfg.MaxConns)),
	)
	return db, nil
}

// Ping checks that a connection can be acquired and answers, bounded by
// DATABASE_HEALTH_TIMEOUT. It satisfies the health-check interface consumed by
// the /ready endpoint.
func (db *DB) Ping(ctx context.Context) error {
	timeout := db.healthTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return db.Pool.Ping(ctx)
}

// Close releases every pooled connection. Safe to call on a nil *DB so callers
// can defer it unconditionally.
func (db *DB) Close() {
	if db == nil || db.Pool == nil {
		return
	}
	db.Pool.Close()
}

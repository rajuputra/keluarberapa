// Command api runs the KeluarBerapa REST API.
//
// Configuration comes from the environment; see backend/.env.example.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rajuputra/keluarberapa/backend/internal/config"
	"github.com/rajuputra/keluarberapa/backend/internal/database"
	httpapi "github.com/rajuputra/keluarberapa/backend/internal/http"
	"github.com/rajuputra/keluarberapa/backend/internal/logging"
)

// migrateTimeout bounds an automatic migration run at startup.
const migrateTimeout = 2 * time.Minute

func main() {
	if err := run(); err != nil {
		// The logger may not exist yet when configuration fails, so this last
		// resort goes straight to stderr.
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// A .env file is a local-development convenience. Real environment
	// variables always take precedence, and a missing file is not an error.
	envFile := os.Getenv("ENV_FILE")
	if envFile == "" {
		envFile = config.DefaultDotEnvPath
	}
	if err := config.LoadDotEnv(envFile); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := logging.New(cfg.App, os.Stdout)

	// Cancelled on SIGINT or SIGTERM, which starts the shutdown sequence.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Connect(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	// Closed only after the server has fully stopped, so no handler can be
	// using a connection at the time.
	defer db.Close()

	if cfg.Database.AutoMigrate {
		if err := migrate(ctx, db, logger); err != nil {
			return err
		}
	}

	// Bind before announcing readiness: a taken port must fail startup rather
	// than surface later as a confusing shutdown.
	listener, err := net.Listen("tcp", cfg.HTTP.Addr())
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.HTTP.Addr(), err)
	}

	server := &http.Server{
		Handler: httpapi.NewRouter(httpapi.Deps{
			Config: cfg,
			Logger: logger,
			DB:     db,
		}),
		ReadHeaderTimeout: cfg.HTTP.ReadTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	// env and version are already attached to every line by the logger.
	logger.Info("api starting",
		slog.String("timezone", cfg.App.Timezone),
		slog.Bool("auto_migrate", cfg.Database.AutoMigrate),
	)

	return httpapi.Serve(ctx, server, listener, cfg.HTTP.ShutdownTimeout, logger)
}

// migrate applies pending migrations at startup when DATABASE_AUTO_MIGRATE is
// enabled. Deployments that migrate as a separate step leave it off.
func migrate(ctx context.Context, db *database.DB, logger *slog.Logger) error {
	pending, err := database.Embedded()
	if err != nil {
		return err
	}

	migrateCtx, cancel := context.WithTimeout(ctx, migrateTimeout)
	defer cancel()

	applied, err := database.Migrate(migrateCtx, db.Pool, pending, logger)
	if err != nil {
		return err
	}
	logger.Info("migrations checked",
		slog.Int("applied", len(applied)),
		slog.Int("available", len(pending)),
	)
	return nil
}

// Command migrate applies the SQL migrations embedded in the binary.
//
//	go run ./cmd/migrate            # apply everything pending
//	go run ./cmd/migrate -action status
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"text/tabwriter"
	"time"

	"github.com/rajuputra/keluarberapa/backend/internal/config"
	"github.com/rajuputra/keluarberapa/backend/internal/database"
	"github.com/rajuputra/keluarberapa/backend/internal/logging"
)

// migrateTimeout bounds a full migration run.
const migrateTimeout = 5 * time.Minute

func main() {
	action := flag.String("action", "up", "up (apply pending migrations) or status (list applied migrations)")
	flag.Parse()

	if err := run(*action); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run(action string) error {
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

	ctx, cancel := context.WithTimeout(context.Background(), migrateTimeout)
	defer cancel()

	db, err := database.Connect(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	switch action {
	case "up":
		return up(ctx, db, logger)
	case "status":
		return status(ctx, db)
	default:
		return fmt.Errorf("unknown -action %q: expected up or status", action)
	}
}

func up(ctx context.Context, db *database.DB, logger *slog.Logger) error {
	pending, err := database.Embedded()
	if err != nil {
		return err
	}

	applied, err := database.Migrate(ctx, db.Pool, pending, logger)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		fmt.Println("no pending migrations")
		return nil
	}
	for _, m := range applied {
		fmt.Printf("applied %03d_%s\n", m.Version, m.Name)
	}
	return nil
}

func status(ctx context.Context, db *database.DB) error {
	available, err := database.Embedded()
	if err != nil {
		return err
	}
	applied, err := database.Status(ctx, db.Pool)
	if err != nil {
		return err
	}

	appliedByVersion := make(map[int]database.AppliedMigration, len(applied))
	for _, m := range applied {
		appliedByVersion[m.Version] = m
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "VERSION\tNAME\tSTATUS\tAPPLIED AT")
	for _, m := range available {
		if record, ok := appliedByVersion[m.Version]; ok {
			state := "applied"
			if record.Checksum != m.Checksum() {
				state = "applied (file modified!)"
			}
			fmt.Fprintf(w, "%03d\t%s\t%s\t%s\n",
				m.Version, m.Name, state, record.AppliedAt.UTC().Format(time.RFC3339))
			continue
		}
		fmt.Fprintf(w, "%03d\t%s\t%s\t%s\n", m.Version, m.Name, "pending", "-")
	}
	return w.Flush()
}

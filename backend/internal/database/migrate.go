package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rajuputra/keluarberapa/backend/migrations"
)

// advisoryLockKey serialises migration runs across processes, so two API
// instances starting at the same time cannot apply the same file twice.
const advisoryLockKey int64 = 8021977

// migrationFilePattern matches "001_initial_schema.sql".
var migrationFilePattern = regexp.MustCompile(`^(\d+)_([a-z0-9_]+)\.sql$`)

// Migration is a single versioned schema change.
type Migration struct {
	Version  int
	Name     string
	Filename string
	SQL      string
}

// Checksum is the SHA-256 of the migration body. It is recorded on apply so a
// later edit to an already-applied file is detected instead of silently ignored.
func (m Migration) Checksum() string {
	sum := sha256.Sum256([]byte(m.SQL))
	return hex.EncodeToString(sum[:])
}

// AppliedMigration is a row of the schema_migrations bookkeeping table.
type AppliedMigration struct {
	Version   int
	Name      string
	Checksum  string
	AppliedAt time.Time
}

// LoadMigrations reads every "<version>_<name>.sql" file from fsys, ordered by
// version. Duplicate versions are rejected: the apply order would be ambiguous.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}

	var loaded []Migration
	seen := make(map[int]string)

	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".sql" {
			continue
		}
		match := migrationFilePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("migration %q must be named <version>_<snake_case_name>.sql", entry.Name())
		}
		version, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, fmt.Errorf("migration %q: invalid version: %w", entry.Name(), err)
		}
		if version <= 0 {
			return nil, fmt.Errorf("migration %q: version must be positive", entry.Name())
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations %q and %q share version %d", other, entry.Name(), version)
		}
		seen[version] = entry.Name()

		body, err := fs.ReadFile(fsys, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		loaded = append(loaded, Migration{
			Version:  version,
			Name:     match[2],
			Filename: entry.Name(),
			SQL:      string(body),
		})
	}

	if len(loaded) == 0 {
		return nil, errors.New("no migrations found")
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].Version < loaded[j].Version })
	return loaded, nil
}

// Embedded returns the migrations compiled into the binary.
func Embedded() ([]Migration, error) { return LoadMigrations(migrations.FS) }

// Migrate applies every migration that has not been applied yet and returns
// those it applied. It is safe to call on every startup.
//
// Each migration runs inside its own transaction together with the
// schema_migrations insert, so a failure leaves the database on the previous
// version rather than half-migrated.
func Migrate(ctx context.Context, pool *pgxpool.Pool, pending []Migration, logger *slog.Logger) ([]Migration, error) {
	if err := ensureMigrationsTable(ctx, pool); err != nil {
		return nil, err
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	// Session-scoped lock: held for the whole run, released on unlock or when
	// the connection goes back to the pool.
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return nil, fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		// Best effort: the lock is also released when the session ends.
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", advisoryLockKey)
	}()

	applied, err := appliedMigrations(ctx, conn)
	if err != nil {
		return nil, err
	}

	var justApplied []Migration
	for _, m := range pending {
		if existing, ok := applied[m.Version]; ok {
			if existing.Checksum != m.Checksum() {
				return justApplied, fmt.Errorf(
					"migration %s was modified after it was applied (recorded checksum %s, file checksum %s): "+
						"add a new migration instead of editing an applied one",
					m.Filename, existing.Checksum, m.Checksum())
			}
			continue
		}

		start := time.Now()
		if err := applyMigration(ctx, conn, m); err != nil {
			return justApplied, err
		}
		justApplied = append(justApplied, m)
		logger.Info("migration applied",
			slog.Int("schema_version", m.Version),
			slog.String("name", m.Name),
			slog.Duration("duration", time.Since(start)),
		)
	}

	if len(justApplied) == 0 {
		// schema_version, not version: the logger already carries the app version.
		logger.Info("database schema up to date", slog.Int("schema_version", maxVersion(pending)))
	}
	return justApplied, nil
}

// Status reports the migrations recorded in the database, ordered by version.
func Status(ctx context.Context, pool *pgxpool.Pool) ([]AppliedMigration, error) {
	if err := ensureMigrationsTable(ctx, pool); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT version, name, checksum, applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	var out []AppliedMigration
	for rows.Next() {
		var m AppliedMigration
		if err := rows.Scan(&m.Version, &m.Name, &m.Checksum, &m.AppliedAt); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func ensureMigrationsTable(ctx context.Context, pool *pgxpool.Pool) error {
	const stmt = `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER     PRIMARY KEY,
			name       TEXT        NOT NULL,
			checksum   TEXT        NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`
	if _, err := pool.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func appliedMigrations(ctx context.Context, conn *pgxpool.Conn) (map[int]AppliedMigration, error) {
	rows, err := conn.Query(ctx, `SELECT version, name, checksum, applied_at FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	out := make(map[int]AppliedMigration)
	for rows.Next() {
		var m AppliedMigration
		if err := rows.Scan(&m.Version, &m.Name, &m.Checksum, &m.AppliedAt); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		out[m.Version] = m
	}
	return out, rows.Err()
}

func applyMigration(ctx context.Context, conn *pgxpool.Conn, m Migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", m.Filename, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The migration body is trusted, version-controlled SQL, not user input.
	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.Filename, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
		m.Version, m.Name, m.Checksum(),
	); err != nil {
		return fmt.Errorf("record migration %s: %w", m.Filename, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.Filename, err)
	}
	return nil
}

func maxVersion(ms []Migration) int {
	highest := 0
	for _, m := range ms {
		if m.Version > highest {
			highest = m.Version
		}
	}
	return highest
}

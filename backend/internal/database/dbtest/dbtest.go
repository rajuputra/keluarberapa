// Package dbtest provides the PostgreSQL harness used by the repository
// integration tests in the domain packages.
//
// Like net/http/httptest it imports "testing" from a non-test file, because a
// helper in a _test.go file is not importable from another package. Nothing
// under cmd/ imports this package, so it is never linked into a binary.
//
// internal/database keeps its own copy of this logic in testsupport_test.go:
// an in-package test file there cannot import dbtest, because dbtest imports
// internal/database and Go rejects the resulting cycle.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rajuputra/keluarberapa/backend/internal/config"
	"github.com/rajuputra/keluarberapa/backend/internal/database"
)

// DatabaseURLEnv names the connection string used by the integration tests.
// They skip themselves when it is unset, so `go test ./...` stays runnable on a
// machine with no PostgreSQL (see backend/README.md, "Running the tests").
const DatabaseURLEnv = "TEST_DATABASE_URL"

// New migrates a throwaway schema and returns a pool bound to it.
//
// Each caller gets its own PostgreSQL schema rather than its own database, so no
// CREATE DATABASE privilege is needed, tests are isolated from one another, and
// cleanup is a single DROP registered with t.Cleanup.
func New(t *testing.T) *database.DB {
	t.Helper()

	dsn := os.Getenv(DatabaseURLEnv)
	if dsn == "" {
		t.Skipf("%s is not set; skipping database integration test", DatabaseURLEnv)
	}

	schema := "test_" + randomSuffix(t)

	// search_path makes every unqualified CREATE in the migrations land in the
	// throwaway schema. Setting it before the schema exists is harmless:
	// PostgreSQL resolves search_path per statement.
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	db, err := database.Connect(ctx, config.Database{
		URL:            config.Secret(dsn + separator + "search_path=" + schema),
		MaxConns:       4,
		MinConns:       1,
		ConnectTimeout: 10 * time.Second,
		HealthTimeout:  2 * time.Second,
	}, discardLogger())
	if err != nil {
		t.Fatalf("connect to the test database: %v", err)
	}

	if _, err := db.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		db.Close()
		t.Fatalf("create schema %s: %v", schema, err)
	}

	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dropCancel()
		if _, err := db.Exec(dropCtx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Errorf("drop schema %s: %v", schema, err)
		}
		db.Close()
	})

	pending, err := database.Embedded()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if _, err := database.Migrate(ctx, db.Pool, pending, discardLogger()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	return db
}

// randomSuffix produces a schema-name-safe unique suffix. A schema name cannot
// be bound as a parameter, so the value must be generated here rather than
// taken from anything resembling input.
func randomSuffix(t *testing.T) string {
	t.Helper()

	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatalf("generate schema suffix: %v", err)
	}
	return hex.EncodeToString(buf[:])
}

// discardLogger keeps harness output off the test log. internal/logging is not
// used here so that dbtest depends on nothing beyond config and database.
func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

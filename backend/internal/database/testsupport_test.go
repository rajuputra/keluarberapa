package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rajuputra/keluarberapa/backend/internal/config"
	"github.com/rajuputra/keluarberapa/backend/internal/logging"
)

// TestDatabaseURLEnv names the connection string used by the integration tests.
// They are skipped when it is unset, so `go test ./...` stays runnable without
// a database (see backend/README.md "Running the tests").
const TestDatabaseURLEnv = "TEST_DATABASE_URL"

// newTestDB migrates a throwaway schema and returns a pool bound to it.
//
// Each test gets its own PostgreSQL schema rather than its own database, so no
// CREATE DATABASE privilege is needed and cleanup is a single DROP.
func newTestDB(t *testing.T) *DB {
	t.Helper()

	dsn := os.Getenv(TestDatabaseURLEnv)
	if dsn == "" {
		t.Skipf("%s is not set; skipping database integration test", TestDatabaseURLEnv)
	}

	schema := "test_" + randomSuffix(t)

	// search_path makes every unqualified CREATE in the migration land in the
	// throwaway schema. Setting it before the schema exists is harmless:
	// PostgreSQL resolves search_path per statement.
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	scopedDSN := dsn + separator + "search_path=" + schema

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	db, err := Connect(ctx, config.Database{
		URL:            config.Secret(scopedDSN),
		MaxConns:       4,
		MinConns:       1,
		ConnectTimeout: 10 * time.Second,
		HealthTimeout:  2 * time.Second,
	}, logging.Discard())
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

	pending, err := Embedded()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if _, err := Migrate(ctx, db.Pool, pending, logging.Discard()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	return db
}

// randomSuffix produces a schema-name-safe unique suffix. Schema names cannot be
// parameterised, so the value must be generated rather than taken from input.
func randomSuffix(t *testing.T) string {
	t.Helper()
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatalf("generate schema suffix: %v", err)
	}
	return hex.EncodeToString(buf[:])
}

// insertUser creates a user and returns its id. Every value is bound as a
// parameter, never interpolated.
func insertUser(t *testing.T, db *DB, email string) string {
	t.Helper()

	var id string
	err := db.QueryRow(context.Background(),
		`INSERT INTO users (name, email, password_hash)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
		"Test "+email, email, "argon2id$placeholder",
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert user %s: %v", email, err)
	}
	return id
}

// categoryID returns the id of a seeded system category.
func categoryID(t *testing.T, db *DB, name string) string {
	t.Helper()

	var id string
	err := db.QueryRow(context.Background(),
		`SELECT id FROM categories WHERE user_id IS NULL AND name = $1`, name).Scan(&id)
	if err != nil {
		t.Fatalf("look up system category %s: %v", name, err)
	}
	return id
}

// insertTransaction records a web-sourced expense for userID.
func insertTransaction(t *testing.T, db *DB, userID, catID, description string, amount int64) string {
	t.Helper()

	var id string
	err := db.QueryRow(context.Background(),
		`INSERT INTO transactions (user_id, category_id, amount, description, source)
		 VALUES ($1, $2, $3, $4, 'web')
		 RETURNING id`,
		userID, catID, amount, description,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert transaction for %s: %v", userID, err)
	}
	return id
}

// insertWhatsAppMessage stores an inbound message and returns its id.
func insertWhatsAppMessage(t *testing.T, db *DB, userID any, phone, wamid, body string) (string, error) {
	t.Helper()

	var id string
	err := db.QueryRow(context.Background(),
		`INSERT INTO whatsapp_messages (user_id, provider_message_id, from_phone_number, body)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		userID, wamid, phone, body,
	).Scan(&id)
	return id, err
}

// expectConstraintViolation asserts that err is a rejection from the database
// mentioning the named constraint or column.
func expectConstraintViolation(t *testing.T, err error, mustMention string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected the database to reject the statement (%s)", mustMention)
	}
	if !strings.Contains(err.Error(), mustMention) {
		t.Errorf("error = %v, want it to mention %q", err, mustMention)
	}
}

func newPhone(index int) string { return fmt.Sprintf("62812%09d", index) }

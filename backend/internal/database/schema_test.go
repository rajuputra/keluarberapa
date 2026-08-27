package database

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/rajuputra/keluarberapa/backend/internal/category"
	"github.com/rajuputra/keluarberapa/backend/internal/logging"
)

// TestMigrationsApplyCleanly is the "migrations execute successfully" gate from
// ai_instructions.md section 2.
func TestMigrationsApplyCleanly(t *testing.T) {
	db := newTestDB(t) // applies every migration
	ctx := context.Background()

	applied, err := Status(ctx, db.Pool)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	available, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	if len(applied) != len(available) {
		t.Fatalf("%d migrations applied, want %d", len(applied), len(available))
	}
	for i, record := range applied {
		if record.Checksum != available[i].Checksum() {
			t.Errorf("migration %d checksum mismatch", record.Version)
		}
		if record.AppliedAt.IsZero() {
			t.Errorf("migration %d has no applied_at", record.Version)
		}
	}
}

// Running the migrator twice must be a no-op, because the API can be configured
// to migrate on every startup.
func TestMigrateIsIdempotent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	pending, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	applied, err := Migrate(ctx, db.Pool, pending, logging.Discard())
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("second run applied %d migrations, want 0", len(applied))
	}
}

// Editing a migration that has already run would leave environments silently
// out of step, so the migrator must refuse instead of skipping it.
func TestMigrateRejectsAnEditedAppliedMigration(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	pending, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	tampered := make([]Migration, len(pending))
	copy(tampered, pending)
	tampered[0].SQL += "\n-- an innocent-looking edit\n"

	if _, err := Migrate(ctx, db.Pool, tampered, logging.Discard()); err == nil {
		t.Fatal("expected Migrate to reject a modified applied migration")
	}
}

func TestSchemaHasEverySpecifiedTable(t *testing.T) {
	db := newTestDB(t)

	// current_schema() keeps the assertion inside this test's throwaway schema.
	rows, err := db.Query(context.Background(),
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()

	found := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		found[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}

	// architecture.md section 4, plus the migrator's own bookkeeping table.
	want := []string{
		"users", "whatsapp_accounts", "categories",
		"transactions", "whatsapp_messages", "refresh_tokens",
		"schema_migrations",
	}
	for _, table := range want {
		if !found[table] {
			t.Errorf("table %q is missing", table)
		}
	}
}

// The columns named in architecture.md section 4 must exist under those exact
// names: the API contract and the dashboard depend on them.
func TestSchemaHasEverySpecifiedColumn(t *testing.T) {
	db := newTestDB(t)

	specified := map[string][]string{
		"users":             {"id", "name", "email", "password_hash", "status", "timezone"},
		"whatsapp_accounts": {"id", "user_id", "phone_number", "provider", "verification_status"},
		"categories":        {"id", "user_id", "name", "is_system"},
		"transactions": {
			"id", "user_id", "category_id", "amount", "currency",
			"description", "transaction_date", "source", "whatsapp_message_id",
		},
		"whatsapp_messages": {"id", "user_id", "provider", "provider_message_id", "from_phone_number", "body", "status"},
		"refresh_tokens":    {"id", "user_id", "token_hash", "expires_at"},
	}

	for table, columns := range specified {
		for _, column := range columns {
			var exists bool
			err := db.QueryRow(context.Background(),
				`SELECT EXISTS (
					SELECT 1 FROM information_schema.columns
					WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2
				)`, table, column).Scan(&exists)
			if err != nil {
				t.Fatalf("check %s.%s: %v", table, column, err)
			}
			if !exists {
				t.Errorf("column %s.%s is missing", table, column)
			}
		}
	}
}

// architecture.md section 1: money is BIGINT, ids are UUID.
func TestMoneyAndIDColumnTypes(t *testing.T) {
	db := newTestDB(t)

	tests := []struct{ table, column, want string }{
		{"transactions", "amount", "bigint"},
		{"transactions", "id", "uuid"},
		{"transactions", "user_id", "uuid"},
		{"users", "id", "uuid"},
		{"whatsapp_accounts", "user_id", "uuid"},
		{"refresh_tokens", "user_id", "uuid"},
	}

	for _, tt := range tests {
		var dataType string
		err := db.QueryRow(context.Background(),
			`SELECT data_type FROM information_schema.columns
			 WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`,
			tt.table, tt.column).Scan(&dataType)
		if err != nil {
			t.Fatalf("type of %s.%s: %v", tt.table, tt.column, err)
		}
		if dataType != tt.want {
			t.Errorf("%s.%s is %s, want %s", tt.table, tt.column, dataType, tt.want)
		}
	}
}

func TestSystemCategoriesAreSeeded(t *testing.T) {
	db := newTestDB(t)

	rows, err := db.Query(context.Background(),
		`SELECT name FROM categories WHERE is_system AND user_id IS NULL ORDER BY name`)
	if err != nil {
		t.Fatalf("query categories: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan category: %v", err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate categories: %v", err)
	}

	want := category.SystemNames()
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("seeded categories = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("category %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Multi-tenant isolation (architecture.md section 2, ai_instructions.md section 3)
// ---------------------------------------------------------------------------

// A query scoped by user_id must return that user's rows and nothing else. This
// is the invariant every repository added in a later stage relies on.
func TestTransactionsAreIsolatedByUserID(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	alice := insertUser(t, db, "alice@example.com")
	bob := insertUser(t, db, "bob@example.com")
	makan := categoryID(t, db, category.NameMakan)

	insertTransaction(t, db, alice, makan, "Makan siang", 25_000)
	insertTransaction(t, db, alice, makan, "Kopi", 18_000)
	bobTx := insertTransaction(t, db, bob, makan, "Belanja bulanan", 350_000)

	var count int
	var total int64
	err := db.QueryRow(ctx,
		`SELECT count(*), coalesce(sum(amount), 0)
		 FROM transactions
		 WHERE user_id = $1 AND deleted_at IS NULL`, alice).Scan(&count, &total)
	if err != nil {
		t.Fatalf("aggregate alice's transactions: %v", err)
	}
	if count != 2 {
		t.Errorf("alice has %d transactions, want 2", count)
	}
	if total != 43_000 {
		t.Errorf("alice's total = %d, want 43000", total)
	}

	// Reading a foreign row by id must come back empty, which is what lets the
	// API answer 404 instead of leaking that the row exists.
	var found string
	err = db.QueryRow(ctx,
		`SELECT id FROM transactions WHERE id = $1 AND user_id = $2`, bobTx, alice).Scan(&found)
	if err == nil {
		t.Fatalf("alice was able to read bob's transaction %s", bobTx)
	}

	// The same applies to writes: a scoped UPDATE must affect zero rows.
	tag, err := db.Exec(ctx,
		`UPDATE transactions SET description = $1 WHERE id = $2 AND user_id = $3`,
		"hijacked", bobTx, alice)
	if err != nil {
		t.Fatalf("scoped update: %v", err)
	}
	if tag.RowsAffected() != 0 {
		t.Errorf("a scoped update touched %d of bob's rows, want 0", tag.RowsAffected())
	}

	// And to a scoped soft delete.
	tag, err = db.Exec(ctx,
		`UPDATE transactions SET deleted_at = now() WHERE id = $1 AND user_id = $2`, bobTx, alice)
	if err != nil {
		t.Fatalf("scoped delete: %v", err)
	}
	if tag.RowsAffected() != 0 {
		t.Errorf("a scoped delete touched %d of bob's rows, want 0", tag.RowsAffected())
	}
}

// A transaction without an owner would be invisible to every scoped query and
// belong to nobody, so the column is NOT NULL.
func TestTransactionRequiresAUser(t *testing.T) {
	db := newTestDB(t)

	_, err := db.Exec(context.Background(),
		`INSERT INTO transactions (amount, description, source) VALUES ($1, $2, 'web')`,
		25_000, "Makan")
	expectConstraintViolation(t, err, "user_id")
}

func TestDeletingAUserRemovesOnlyTheirData(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	alice := insertUser(t, db, "alice@example.com")
	bob := insertUser(t, db, "bob@example.com")
	makan := categoryID(t, db, category.NameMakan)

	insertTransaction(t, db, alice, makan, "Makan", 25_000)
	insertTransaction(t, db, bob, makan, "Kopi", 18_000)

	if _, err := db.Exec(ctx, `DELETE FROM users WHERE id = $1`, alice); err != nil {
		t.Fatalf("delete alice: %v", err)
	}

	var remaining int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM transactions`).Scan(&remaining); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	if remaining != 1 {
		t.Errorf("%d transactions remain, want only bob's 1", remaining)
	}

	var owner string
	if err := db.QueryRow(ctx, `SELECT user_id FROM transactions`).Scan(&owner); err != nil {
		t.Fatalf("read remaining owner: %v", err)
	}
	if owner != bob {
		t.Errorf("remaining transaction belongs to %s, want bob (%s)", owner, bob)
	}
}

// A user category must never be usable by another user; a system category is
// shared on purpose.
func TestCategoryOwnershipIsEnforced(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	alice := insertUser(t, db, "alice@example.com")

	var aliceCategory string
	if err := db.QueryRow(ctx,
		`INSERT INTO categories (user_id, name, is_system) VALUES ($1, $2, FALSE) RETURNING id`,
		alice, "Kopi Spesial").Scan(&aliceCategory); err != nil {
		t.Fatalf("insert alice's category: %v", err)
	}

	bob := insertUser(t, db, "bob@example.com")

	// The query a repository would run: own categories plus shared ones.
	var visible int
	if err := db.QueryRow(ctx,
		`SELECT count(*) FROM categories WHERE user_id = $1 OR user_id IS NULL`, bob).Scan(&visible); err != nil {
		t.Fatalf("count bob's visible categories: %v", err)
	}
	if want := len(category.SystemNames()); visible != want {
		t.Errorf("bob sees %d categories, want the %d system ones only", visible, want)
	}

	// A system category may not be owned, and a user category must be.
	_, err := db.Exec(ctx,
		`INSERT INTO categories (user_id, name, is_system) VALUES ($1, $2, TRUE)`, alice, "Bogus System")
	expectConstraintViolation(t, err, "categories_ownership_valid")

	_, err = db.Exec(ctx, `INSERT INTO categories (name, is_system) VALUES ($1, FALSE)`, "Ownerless")
	expectConstraintViolation(t, err, "categories_ownership_valid")
}

// ---------------------------------------------------------------------------
// WhatsApp identity and idempotency
// ---------------------------------------------------------------------------

// PRD section 1.3: exactly one WhatsApp number per user.
func TestOneWhatsAppAccountPerUser(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	alice := insertUser(t, db, "alice@example.com")

	if _, err := db.Exec(ctx,
		`INSERT INTO whatsapp_accounts (user_id, phone_number) VALUES ($1, $2)`,
		alice, newPhone(1)); err != nil {
		t.Fatalf("link alice's first number: %v", err)
	}

	_, err := db.Exec(ctx,
		`INSERT INTO whatsapp_accounts (user_id, phone_number) VALUES ($1, $2)`,
		alice, newPhone(2))
	expectConstraintViolation(t, err, "whatsapp_accounts_one_per_user")
}

// If two users could claim the same number, inbound identity resolution would be
// ambiguous, which ai_instructions.md section 3 treats as an absolute failure.
func TestWhatsAppNumberResolvesToExactlyOneUser(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	alice := insertUser(t, db, "alice@example.com")
	bob := insertUser(t, db, "bob@example.com")
	phone := newPhone(3)

	if _, err := db.Exec(ctx,
		`INSERT INTO whatsapp_accounts (user_id, phone_number) VALUES ($1, $2)`,
		alice, phone); err != nil {
		t.Fatalf("link alice's number: %v", err)
	}

	_, err := db.Exec(ctx,
		`INSERT INTO whatsapp_accounts (user_id, phone_number) VALUES ($1, $2)`, bob, phone)
	expectConstraintViolation(t, err, "whatsapp_accounts_phone_unique")

	// The resolution query returns a single owner.
	var resolved string
	if err := db.QueryRow(ctx,
		`SELECT user_id FROM whatsapp_accounts WHERE phone_number = $1`, phone).Scan(&resolved); err != nil {
		t.Fatalf("resolve %s: %v", phone, err)
	}
	if resolved != alice {
		t.Errorf("%s resolved to %s, want alice (%s)", phone, resolved, alice)
	}
}

func TestWhatsAppVerificationStateIsConsistent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	alice := insertUser(t, db, "alice@example.com")

	// verified without a timestamp
	_, err := db.Exec(ctx,
		`INSERT INTO whatsapp_accounts (user_id, phone_number, verification_status)
		 VALUES ($1, $2, 'verified')`, alice, newPhone(4))
	expectConstraintViolation(t, err, "whatsapp_accounts_verified_at_consistent")

	// pending with a timestamp
	_, err = db.Exec(ctx,
		`INSERT INTO whatsapp_accounts (user_id, phone_number, verification_status, verified_at)
		 VALUES ($1, $2, 'pending', now())`, alice, newPhone(5))
	expectConstraintViolation(t, err, "whatsapp_accounts_verified_at_consistent")

	// the consistent combination is accepted
	if _, err := db.Exec(ctx,
		`INSERT INTO whatsapp_accounts (user_id, phone_number, verification_status, verified_at)
		 VALUES ($1, $2, 'verified', now())`, alice, newPhone(6)); err != nil {
		t.Fatalf("verified account with a timestamp was rejected: %v", err)
	}
}

// user_stories.md Epic 2: a duplicate webhook delivery must not create a second
// transaction.
func TestDuplicateWhatsAppMessageIsRejected(t *testing.T) {
	db := newTestDB(t)

	alice := insertUser(t, db, "alice@example.com")
	phone := newPhone(7)
	const wamid = "wamid.HBgNNjI4MTI="

	if _, err := insertWhatsAppMessage(t, db, alice, phone, wamid, "Makan 25000"); err != nil {
		t.Fatalf("store the first delivery: %v", err)
	}

	_, err := insertWhatsAppMessage(t, db, alice, phone, wamid, "Makan 25000")
	expectConstraintViolation(t, err, "whatsapp_messages_provider_message_unique")
}

// Messages from unregistered numbers still have to be recorded, or a retry from
// an unknown sender would look new every time.
func TestWhatsAppMessageFromUnknownSenderIsStored(t *testing.T) {
	db := newTestDB(t)

	if _, err := insertWhatsAppMessage(t, db, nil, newPhone(8), "wamid.unknown", "Makan 25000"); err != nil {
		t.Fatalf("store a message from an unregistered number: %v", err)
	}
}

// Even if application logic were wrong, one inbound message can only ever
// produce one transaction.
func TestOneTransactionPerWhatsAppMessage(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	alice := insertUser(t, db, "alice@example.com")
	makan := categoryID(t, db, category.NameMakan)
	messageID, err := insertWhatsAppMessage(t, db, alice, newPhone(9), "wamid.once", "Makan 25000")
	if err != nil {
		t.Fatalf("store the message: %v", err)
	}

	const insert = `INSERT INTO transactions
		(user_id, category_id, amount, description, source, whatsapp_message_id)
		VALUES ($1, $2, $3, $4, 'whatsapp', $5)`

	if _, err := db.Exec(ctx, insert, alice, makan, 25_000, "Makan", messageID); err != nil {
		t.Fatalf("first transaction: %v", err)
	}

	_, err = db.Exec(ctx, insert, alice, makan, 25_000, "Makan", messageID)
	expectConstraintViolation(t, err, "transactions_whatsapp_message_unique")
}

// ---------------------------------------------------------------------------
// Column-level invariants
// ---------------------------------------------------------------------------

func TestTransactionCheckConstraints(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	alice := insertUser(t, db, "alice@example.com")
	makan := categoryID(t, db, category.NameMakan)

	tests := []struct {
		name       string
		sql        string
		args       []any
		constraint string
	}{
		{
			name:       "zero amount",
			sql:        `INSERT INTO transactions (user_id, category_id, amount, description, source) VALUES ($1, $2, 0, 'Makan', 'web')`,
			args:       []any{alice, makan},
			constraint: "transactions_amount_positive",
		},
		{
			name:       "negative amount",
			sql:        `INSERT INTO transactions (user_id, category_id, amount, description, source) VALUES ($1, $2, -1, 'Makan', 'web')`,
			args:       []any{alice, makan},
			constraint: "transactions_amount_positive",
		},
		{
			name:       "blank description",
			sql:        `INSERT INTO transactions (user_id, category_id, amount, description, source) VALUES ($1, $2, 25000, '   ', 'web')`,
			args:       []any{alice, makan},
			constraint: "transactions_description_not_blank",
		},
		{
			name:       "unknown source",
			sql:        `INSERT INTO transactions (user_id, category_id, amount, description, source) VALUES ($1, $2, 25000, 'Makan', 'telegram')`,
			args:       []any{alice, makan},
			constraint: "transactions_source_valid",
		},
		{
			name:       "unsupported currency",
			sql:        `INSERT INTO transactions (user_id, category_id, amount, currency, description, source) VALUES ($1, $2, 25000, 'USD', 'Makan', 'web')`,
			args:       []any{alice, makan},
			constraint: "transactions_currency_valid",
		},
		{
			name:       "whatsapp source without a message",
			sql:        `INSERT INTO transactions (user_id, category_id, amount, description, source) VALUES ($1, $2, 25000, 'Makan', 'whatsapp')`,
			args:       []any{alice, makan},
			constraint: "transactions_source_message_consistent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := db.Exec(ctx, tt.sql, tt.args...)
			expectConstraintViolation(t, err, tt.constraint)
		})
	}
}

func TestTransactionDefaults(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	alice := insertUser(t, db, "alice@example.com")
	makan := categoryID(t, db, category.NameMakan)
	id := insertTransaction(t, db, alice, makan, "Makan siang", 25_000)

	var currency string
	var date, createdAt time.Time
	var deletedAt *time.Time
	err := db.QueryRow(ctx,
		`SELECT currency, transaction_date, created_at, deleted_at FROM transactions WHERE id = $1`, id).
		Scan(&currency, &date, &createdAt, &deletedAt)
	if err != nil {
		t.Fatalf("read the transaction: %v", err)
	}

	if currency != "IDR" {
		t.Errorf("currency = %q, want IDR", currency)
	}
	if date.IsZero() {
		t.Error("transaction_date should default to now()")
	}
	if deletedAt != nil {
		t.Error("deleted_at should start NULL")
	}
}

// The trigger keeps updated_at honest even for SQL that forgets to set it.
func TestUpdatedAtTriggerFires(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	alice := insertUser(t, db, "alice@example.com")

	var before time.Time
	if err := db.QueryRow(ctx, `SELECT updated_at FROM users WHERE id = $1`, alice).Scan(&before); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}

	if _, err := db.Exec(ctx, `UPDATE users SET name = $1 WHERE id = $2`, "Alice Updated", alice); err != nil {
		t.Fatalf("update the user: %v", err)
	}

	var after time.Time
	if err := db.QueryRow(ctx, `SELECT updated_at FROM users WHERE id = $1`, alice).Scan(&after); err != nil {
		t.Fatalf("re-read updated_at: %v", err)
	}
	if !after.After(before) {
		t.Errorf("updated_at did not advance: before %s, after %s", before, after)
	}
}

// user_stories.md Epic 1: email uniqueness. The index is on lower(email), so a
// different casing must not create a second account.
func TestEmailUniquenessIsCaseInsensitive(t *testing.T) {
	db := newTestDB(t)

	insertUser(t, db, "alice@example.com")

	_, err := db.Exec(context.Background(),
		`INSERT INTO users (name, email, password_hash) VALUES ($1, $2, $3)`,
		"Impostor", "ALICE@example.com", "hash")
	expectConstraintViolation(t, err, "users_email_unique")
}

func TestUserStatusIsConstrained(t *testing.T) {
	db := newTestDB(t)

	_, err := db.Exec(context.Background(),
		`INSERT INTO users (name, email, password_hash, status) VALUES ($1, $2, $3, 'zombie')`,
		"Alice", "alice@example.com", "hash")
	expectConstraintViolation(t, err, "users_status_valid")
}

func TestUserDefaultsToJakartaTimezone(t *testing.T) {
	db := newTestDB(t)

	alice := insertUser(t, db, "alice@example.com")

	var timezone, status string
	err := db.QueryRow(context.Background(),
		`SELECT timezone, status FROM users WHERE id = $1`, alice).Scan(&timezone, &status)
	if err != nil {
		t.Fatalf("read the user: %v", err)
	}
	if timezone != "Asia/Jakarta" {
		t.Errorf("timezone = %q, want Asia/Jakarta", timezone)
	}
	if status != "active" {
		t.Errorf("status = %q, want active", status)
	}
}

// Refresh tokens are looked up by hash, so the hash must be unique, and a token
// must disappear with its user.
func TestRefreshTokenConstraints(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	alice := insertUser(t, db, "alice@example.com")
	const hash = "6c1f8a2e0d3b4c5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f"

	if _, err := db.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, now() + interval '30 days')`, alice, hash); err != nil {
		t.Fatalf("store the refresh token: %v", err)
	}

	_, err := db.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, now() + interval '30 days')`, alice, hash)
	expectConstraintViolation(t, err, "refresh_tokens_token_hash_unique")

	if _, err := db.Exec(ctx, `DELETE FROM users WHERE id = $1`, alice); err != nil {
		t.Fatalf("delete alice: %v", err)
	}
	var remaining int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens`).Scan(&remaining); err != nil {
		t.Fatalf("count refresh tokens: %v", err)
	}
	if remaining != 0 {
		t.Errorf("%d refresh tokens survived their user, want 0", remaining)
	}
}

// The pool must answer a ping, which is what /ready reports on.
func TestPing(t *testing.T) {
	db := newTestDB(t)

	if err := db.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

// Close must be safe to defer even when Connect failed.
func TestCloseOnNilDBIsSafe(t *testing.T) {
	var db *DB
	db.Close()
}

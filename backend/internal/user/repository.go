package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rajuputra/keluarberapa/backend/internal/database"
)

// Repository errors.
var (
	// ErrNotFound means no user matched. Callers must not distinguish "no such
	// user" from "not yours" in anything they return to a client
	// (ai_instructions.md section 3).
	ErrNotFound = errors.New("user not found")
	// ErrEmailTaken means the users_email_unique index rejected the write.
	ErrEmailTaken = errors.New("email is already registered")
	// ErrNoChanges means an update was asked for with nothing to change.
	ErrNoChanges = errors.New("no fields to update")
)

// userColumns is the select list shared by every read, so a new column cannot be
// added to one query and forgotten in another.
const userColumns = `id, name, email, password_hash, status, timezone, created_at, updated_at`

// Repository reads and writes the users table.
//
// Every statement binds its arguments as parameters; no query is assembled by
// string concatenation (architecture.md section 2).
type Repository struct {
	db *database.DB
}

// NewRepository returns a repository backed by db.
func NewRepository(db *database.DB) *Repository { return &Repository{db: db} }

// NewUser is the input to Create. PasswordHash is already derived: the
// repository never sees a plaintext password.
type NewUser struct {
	Name         string
	Email        string // must already be normalised with NormalizeEmail
	PasswordHash string
	Timezone     string
}

// Changes describes a partial update. A nil field is left alone, which is what
// lets a client PATCH one field without having to resend the rest.
type Changes struct {
	Name     *string
	Timezone *string
}

// IsEmpty reports whether the update would change nothing.
func (c Changes) IsEmpty() bool { return c.Name == nil && c.Timezone == nil }

// Create inserts a user and returns the stored row.
//
// A duplicate email comes back as ErrEmailTaken. The check is the unique index
// rather than a preceding SELECT, so two simultaneous registrations of the same
// address cannot both succeed.
func (r *Repository) Create(ctx context.Context, in NewUser) (*User, error) {
	const stmt = `
		INSERT INTO users (name, email, password_hash, timezone)
		VALUES ($1, $2, $3, $4)
		RETURNING ` + userColumns

	created, err := scanUser(r.db.QueryRow(ctx, stmt, in.Name, in.Email, in.PasswordHash, in.Timezone))
	if err != nil {
		if database.IsUniqueViolation(err, "users_email_unique") {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}
	return created, nil
}

// GetByEmail looks a user up by address, for login.
//
// email is matched against lower(email), the expression the users_email_unique
// index is built on, so the lookup uses that index and cannot disagree with the
// uniqueness rule about what counts as the same address.
func (r *Repository) GetByEmail(ctx context.Context, email string) (*User, error) {
	const stmt = `SELECT ` + userColumns + ` FROM users WHERE lower(email) = $1`

	found, err := scanUser(r.db.QueryRow(ctx, stmt, NormalizeEmail(email)))
	if err != nil {
		return nil, wrapRead(err, "select user by email")
	}
	return found, nil
}

// GetByID loads the user identified by id, which in an authenticated request
// always comes from the JWT context and never from the request body
// (ai_instructions.md section 1.7).
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*User, error) {
	const stmt = `SELECT ` + userColumns + ` FROM users WHERE id = $1`

	found, err := scanUser(r.db.QueryRow(ctx, stmt, id))
	if err != nil {
		return nil, wrapRead(err, "select user by id")
	}
	return found, nil
}

// Update applies ch to the user identified by id and returns the stored row.
//
// COALESCE keeps an unset field at its current value, so the whole partial
// update is a single statement and cannot interleave with a concurrent write the
// way a read-modify-write pair could. updated_at is maintained by the
// users_set_updated_at trigger.
func (r *Repository) Update(ctx context.Context, id uuid.UUID, ch Changes) (*User, error) {
	if ch.IsEmpty() {
		return nil, ErrNoChanges
	}

	const stmt = `
		UPDATE users
		SET name     = COALESCE($2, name),
		    timezone = COALESCE($3, timezone)
		WHERE id = $1
		RETURNING ` + userColumns

	updated, err := scanUser(r.db.QueryRow(ctx, stmt, id, ch.Name, ch.Timezone))
	if err != nil {
		return nil, wrapRead(err, "update user")
	}
	return updated, nil
}

// scanUser materialises one row. Status is read into a string and converted
// rather than scanned into the named type directly, so the mapping stays
// explicit and independent of the driver's reflection rules.
func scanUser(row pgx.Row) (*User, error) {
	var (
		u      User
		status string
	)
	err := row.Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &status,
		&u.Timezone, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	u.Status = Status(status)
	return &u, nil
}

// wrapRead turns "no rows" into ErrNotFound and annotates anything else.
func wrapRead(err error, operation string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

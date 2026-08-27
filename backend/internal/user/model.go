// Package user holds the account aggregate. Stage 1 provides the model and the
// invariants that mirror the database constraints; the repository, service and
// handler arrive with the authentication stage.
package user

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Status is the users.status column. The values match the
// users_status_valid CHECK constraint in 001_initial_schema.sql.
type Status string

const (
	StatusActive    Status = "active"
	StatusInactive  Status = "inactive"
	StatusSuspended Status = "suspended"
)

// Statuses lists every accepted status, in the order used by the schema.
func Statuses() []Status {
	return []Status{StatusActive, StatusInactive, StatusSuspended}
}

// Valid reports whether s is a status the database will accept.
func (s Status) Valid() bool {
	for _, candidate := range Statuses() {
		if s == candidate {
			return true
		}
	}
	return false
}

func (s Status) String() string { return string(s) }

// User is a row of the users table.
//
// PasswordHash is unexported-by-convention data: it carries no JSON tag with a
// name because the type is never serialised directly. API responses are built
// from dedicated view structs, so a hash can never leak by accident
// (ai_instructions.md section 1.8).
type User struct {
	ID           uuid.UUID
	Name         string
	Email        string
	PasswordHash string
	Status       Status
	// Timezone is an IANA name, defaulting to Asia/Jakarta.
	Timezone  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsActive reports whether the account may authenticate.
func (u User) IsActive() bool { return u.Status == StatusActive }

// NormalizeEmail produces the canonical form used for storage and lookup.
//
// The users_email_unique index is built on lower(email), so every read and write
// must normalise the same way or uniqueness would be bypassable with "A@b.com".
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

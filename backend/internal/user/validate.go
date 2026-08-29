package user

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/rajuputra/keluarberapa/backend/internal/config"
)

// DefaultTimezone mirrors config.DefaultTimezone and the DEFAULT on
// users.timezone, so the Go default, the environment default and the schema
// default cannot drift apart.
const DefaultTimezone = config.DefaultTimezone

// Field length limits. The schema only forbids blank values, so the upper bounds
// live here: they exist to keep a hostile request from storing a megabyte of
// display name, not to express a product rule.
const (
	MaxNameLength  = 120
	MaxEmailLength = 254 // RFC 5321 maximum forward-path length
)

// Validation errors. Each is matchable with errors.Is, and Validate joins them
// so one response can report every problem at once.
var (
	ErrNameRequired = errors.New("name is required")
	ErrNameTooLong  = fmt.Errorf("name must be at most %d characters", MaxNameLength)

	ErrEmailRequired = errors.New("email is required")
	ErrEmailTooLong  = fmt.Errorf("email must be at most %d characters", MaxEmailLength)
	ErrEmailInvalid  = errors.New("email is not a valid address")

	ErrTimezoneInvalid = errors.New("timezone is not a valid IANA name")
)

// ValidateName checks the display name. The returned name is the trimmed form
// that should be stored, matching the users_name_not_blank CHECK.
func ValidateName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	switch {
	case trimmed == "":
		return "", ErrNameRequired
	case utf8.RuneCountInString(trimmed) > MaxNameLength:
		return "", ErrNameTooLong
	}
	return trimmed, nil
}

// ValidateEmail checks the address and returns its canonical stored form.
//
// Normalisation happens first so that validation sees exactly what will be
// written, and so the result matches the lower(email) expression behind the
// users_email_unique index.
func ValidateEmail(email string) (string, error) {
	normalized := NormalizeEmail(email)
	switch {
	case normalized == "":
		return "", ErrEmailRequired
	case len(normalized) > MaxEmailLength:
		return "", ErrEmailTooLong
	}

	// net/mail also accepts `Display Name <a@b>`; requiring the parse to round
	// trip rejects those, because only the bare address is a login identifier.
	parsed, err := mail.ParseAddress(normalized)
	if err != nil || parsed.Address != normalized || parsed.Name != "" {
		return "", ErrEmailInvalid
	}
	// mail.ParseAddress accepts a bare hostname such as "alice@localhost". A
	// deliverable address needs a dot-separated domain.
	at := strings.LastIndex(normalized, "@")
	domain := normalized[at+1:]
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", ErrEmailInvalid
	}

	return normalized, nil
}

// ValidateTimezone checks an IANA timezone name, substituting DefaultTimezone
// for the empty string so a client that does not care can omit it.
func ValidateTimezone(timezone string) (string, error) {
	trimmed := strings.TrimSpace(timezone)
	if trimmed == "" {
		return DefaultTimezone, nil
	}
	if _, err := time.LoadLocation(trimmed); err != nil {
		return "", fmt.Errorf("%w: %q", ErrTimezoneInvalid, trimmed)
	}
	return trimmed, nil
}

// Profile is the safe projection of a User.
//
// It exists so that a password hash cannot reach a response by accident
// (ai_instructions.md section 1.8): the field is absent from the type, so no
// handler, log call or JSON encoder can reach it. Wire-format tags belong on the
// response types in the HTTP layer, not here.
type Profile struct {
	ID        uuid.UUID
	Name      string
	Email     string
	Status    Status
	Timezone  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ProfileOf projects u, dropping PasswordHash.
func ProfileOf(u User) Profile {
	return Profile{
		ID:        u.ID,
		Name:      u.Name,
		Email:     u.Email,
		Status:    u.Status,
		Timezone:  u.Timezone,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

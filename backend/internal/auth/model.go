// Package auth holds the authentication aggregate.
//
// Stage 1 provides the refresh-token model only. Password hashing, token
// issuing and the login/register handlers arrive with the authentication stage.
package auth

import (
	"time"

	"github.com/google/uuid"
)

// RefreshToken is a row of the refresh_tokens table.
//
// Only the hash of the token is ever persisted (architecture.md section 2), so
// a database disclosure does not hand out usable sessions. The plaintext token
// exists solely in the response to the client.
type RefreshToken struct {
	ID     uuid.UUID
	UserID uuid.UUID
	// TokenHash is the hex-encoded SHA-256 of the refresh token. Lookups hash
	// the presented token and compare hashes; the plaintext is never stored.
	TokenHash string
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

// IsRevoked reports whether the token was explicitly invalidated.
func (t RefreshToken) IsRevoked() bool { return t.RevokedAt != nil }

// IsExpired reports whether the token is past its lifetime at time now.
func (t RefreshToken) IsExpired(now time.Time) bool { return !now.Before(t.ExpiresAt) }

// IsUsable reports whether the token may still be exchanged for a new access
// token. Callers must check this rather than testing the fields separately, so
// that a revoked-or-expired token is never accepted by omission.
func (t RefreshToken) IsUsable(now time.Time) bool {
	return !t.IsRevoked() && !t.IsExpired(now)
}

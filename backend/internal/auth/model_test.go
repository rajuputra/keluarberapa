package auth

import (
	"testing"
	"time"
)

func TestIsRevoked(t *testing.T) {
	if (RefreshToken{}).IsRevoked() {
		t.Error("a token with no revoked_at is not revoked")
	}
	now := time.Now()
	if !(RefreshToken{RevokedAt: &now}).IsRevoked() {
		t.Error("a token with revoked_at is revoked")
	}
}

func TestIsExpired(t *testing.T) {
	now := time.Now()

	if (RefreshToken{ExpiresAt: now.Add(time.Hour)}).IsExpired(now) {
		t.Error("a token expiring in the future is not expired")
	}
	if !(RefreshToken{ExpiresAt: now.Add(-time.Hour)}).IsExpired(now) {
		t.Error("a token that expired an hour ago is expired")
	}
	// The boundary counts as expired: a token is valid strictly before its
	// expiry instant.
	if !(RefreshToken{ExpiresAt: now}).IsExpired(now) {
		t.Error("a token expiring exactly now should be expired")
	}
}

// A single call must cover both failure modes, so that neither can be forgotten
// at a call site.
func TestIsUsable(t *testing.T) {
	now := time.Now()
	revoked := now.Add(-time.Minute)

	tests := []struct {
		name  string
		token RefreshToken
		want  bool
	}{
		{"fresh", RefreshToken{ExpiresAt: now.Add(30 * 24 * time.Hour)}, true},
		{"expired", RefreshToken{ExpiresAt: now.Add(-time.Second)}, false},
		{"revoked", RefreshToken{ExpiresAt: now.Add(time.Hour), RevokedAt: &revoked}, false},
		{"revoked and expired", RefreshToken{ExpiresAt: now.Add(-time.Hour), RevokedAt: &revoked}, false},
		{"zero value", RefreshToken{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.token.IsUsable(now); got != tt.want {
				t.Errorf("IsUsable() = %v, want %v", got, tt.want)
			}
		})
	}
}

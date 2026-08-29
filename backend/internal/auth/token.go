package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// refreshTokenBytes is the amount of entropy in a refresh token: 256 bits.
const refreshTokenBytes = 32

// RefreshTokenHashLength is the length of a hex-encoded SHA-256 digest, the
// format stored in refresh_tokens.token_hash.
const RefreshTokenHashLength = sha256.Size * 2

// NewRefreshTokenValue returns a fresh refresh token in plaintext.
//
// The value is URL-safe so it survives being placed in a header, a cookie or a
// JSON body without further encoding. Only its hash is ever persisted; the
// plaintext exists in the login response and nowhere else.
func NewRefreshTokenValue() (string, error) {
	buf := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashRefreshToken returns the hex-encoded SHA-256 of token: the value stored in
// refresh_tokens.token_hash (architecture.md section 2).
//
// A plain digest is the right primitive here, unlike for passwords. A refresh
// token is 256 uniformly random bits, so there is no dictionary to run and
// nothing for a slow, memory-hard function to defend against; a fixed-length
// digest also keeps lookup a single indexed equality on
// refresh_tokens_token_hash_unique. Passwords are different because users choose
// them, which is why they get Argon2id instead.
func HashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// EqualTokenHash compares two token hashes in constant time. Used where a
// comparison happens in Go rather than in a WHERE clause.
func EqualTokenHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

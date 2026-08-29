package auth

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// A refresh token is a bearer credential with no structure to fall back on, so
// its only defence is entropy. 32 random bytes is 256 bits.
func TestNewRefreshTokenValueHasFullEntropy(t *testing.T) {
	token, err := NewRefreshTokenValue()
	if err != nil {
		t.Fatalf("NewRefreshTokenValue: %v", err)
	}

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("token %q is not raw base64url: %v", token, err)
	}
	if len(raw) != refreshTokenBytes {
		t.Errorf("token carries %d bytes of entropy, want %d", len(raw), refreshTokenBytes)
	}

	// URL-safe so the value survives a header, a cookie or a JSON body untouched.
	for _, unsafe := range []string{"+", "/", "=", " "} {
		if strings.Contains(token, unsafe) {
			t.Errorf("token %q contains %q, which is not URL-safe", token, unsafe)
		}
	}
}

func TestNewRefreshTokenValueIsUnique(t *testing.T) {
	const iterations = 1000

	seen := make(map[string]struct{}, iterations)
	for i := 0; i < iterations; i++ {
		token, err := NewRefreshTokenValue()
		if err != nil {
			t.Fatalf("NewRefreshTokenValue: %v", err)
		}
		if _, duplicate := seen[token]; duplicate {
			t.Fatalf("token %q was generated twice in %d draws", token, iterations)
		}
		seen[token] = struct{}{}
	}
}

// The hash is what goes into refresh_tokens.token_hash, and the column has a
// unique index on it, so the format must be exactly one fixed-length digest.
func TestHashRefreshTokenFormat(t *testing.T) {
	token, err := NewRefreshTokenValue()
	if err != nil {
		t.Fatalf("NewRefreshTokenValue: %v", err)
	}

	hash := HashRefreshToken(token)
	if len(hash) != RefreshTokenHashLength {
		t.Errorf("hash length = %d, want %d", len(hash), RefreshTokenHashLength)
	}
	if _, err := hex.DecodeString(hash); err != nil {
		t.Errorf("hash %q is not hexadecimal: %v", hash, err)
	}
	if hash != strings.ToLower(hash) {
		t.Errorf("hash %q is not lower case; checkHash requires it", hash)
	}
	if strings.Contains(hash, token) {
		t.Error("the hash contains the token itself")
	}
}

func TestHashRefreshTokenIsDeterministicAndDistinct(t *testing.T) {
	// Deterministic: the lookup is a WHERE token_hash = $1, so the same token has
	// to produce the same digest on every call and in every process.
	if HashRefreshToken("token-a") != HashRefreshToken("token-a") {
		t.Error("hashing the same token twice produced different digests")
	}
	if HashRefreshToken("token-a") == HashRefreshToken("token-b") {
		t.Error("two different tokens produced the same digest")
	}
	// Known-answer check, so the stored format cannot change unnoticed.
	const want = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	if got := HashRefreshToken("test"); got != want {
		t.Errorf("HashRefreshToken(%q) = %q, want the SHA-256 %q", "test", got, want)
	}
}

// checkHash is the guard that stops a plaintext token being written into the
// token_hash column by a caller that forgot to hash it.
func TestCheckHash(t *testing.T) {
	valid := HashRefreshToken("anything")
	if err := checkHash(valid); err != nil {
		t.Errorf("checkHash(%q) = %v, want nil", valid, err)
	}

	token, err := NewRefreshTokenValue()
	if err != nil {
		t.Fatalf("NewRefreshTokenValue: %v", err)
	}

	invalid := map[string]string{
		"empty":            "",
		"raw token":        token,
		"too short":        valid[:len(valid)-1],
		"too long":         valid + "a",
		"upper case":       strings.ToUpper(valid),
		"non-hex":          strings.Repeat("z", RefreshTokenHashLength),
		"hex with spacing": strings.Repeat("ab ", RefreshTokenHashLength/3),
	}
	for name, hash := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := checkHash(hash); err == nil {
				t.Errorf("checkHash accepted %q", hash)
			}
		})
	}
}

func TestEqualTokenHash(t *testing.T) {
	a := HashRefreshToken("token-a")
	b := HashRefreshToken("token-b")

	if !EqualTokenHash(a, a) {
		t.Error("a hash should equal itself")
	}
	if EqualTokenHash(a, b) {
		t.Error("different hashes should not compare equal")
	}
	if EqualTokenHash(a, a[:10]) {
		t.Error("a prefix should not compare equal to the whole hash")
	}
	if !EqualTokenHash("", "") {
		t.Error("two empty strings should compare equal")
	}
}

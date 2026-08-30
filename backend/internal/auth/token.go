package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/rajuputra/keluarberapa/backend/internal/user"
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

// JWT errors.
var (
	ErrInvalidAccessToken = errors.New("access token is invalid or expired")
	ErrMissingAccessToken = errors.New("access token is required")
)

// AccessTokenClaims are the claims embedded in the access token JWT.
//
// The subject is the user id. The issuer and audience are fixed per
// configuration so that a token minted for one deployment cannot be used in
// another. Only the subject is required by the middleware; the rest are
// defensive.
type AccessTokenClaims struct {
	jwt.RegisteredClaims
}

// JWTAccessTokenIssuer issues and verifies short-lived access tokens using JWT.
//
// The concrete implementation uses HS256 with the secret from config. Both
// operations live here so the claim set can never drift between signing and
// verification.
type JWTAccessTokenIssuer struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

// NewAccessTokenIssuer returns an issuer configured with the given secret,
// issuer string and TTL. secret must be at least 32 bytes.
func NewAccessTokenIssuer(secret string, issuer string, ttl time.Duration) (*JWTAccessTokenIssuer, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("access token secret must be at least 32 bytes, got %d", len(secret))
	}
	if issuer == "" {
		return nil, errors.New("issuer must not be empty")
	}
	if ttl <= 0 {
		return nil, errors.New("ttl must be positive")
	}
	return &JWTAccessTokenIssuer{
		secret: []byte(secret),
		issuer: issuer,
		ttl:    ttl,
	}, nil
}

// IssueAccessToken mints a new access token for the given user.
func (i *JWTAccessTokenIssuer) IssueAccessToken(u user.User, now time.Time) (AccessToken, error) {
	claims := AccessTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.ID.String(),
			Issuer:    i.issuer,
			Audience:  jwt.ClaimStrings{i.issuer},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(i.secret)
	if err != nil {
		return AccessToken{}, fmt.Errorf("sign access token: %w", err)
	}
	return AccessToken{
		Token:     signed,
		ExpiresAt: now.Add(i.ttl),
	}, nil
}

// ParseAccessToken verifies the token and returns its claims.
//
// The token must be signed with the same secret, have the expected issuer
// and audience, and not be expired. The subject is returned as a UUID.
func (i *JWTAccessTokenIssuer) ParseAccessToken(tokenString string) (*AccessTokenClaims, error) {
	if tokenString == "" {
		return nil, ErrMissingAccessToken
	}
	token, err := jwt.ParseWithClaims(tokenString, &AccessTokenClaims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return i.secret, nil
	}, jwt.WithIssuer(i.issuer), jwt.WithAudience(i.issuer), jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAccessToken, err)
	}
	claims, ok := token.Claims.(*AccessTokenClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidAccessToken
	}
	return claims, nil
}

// UserIDFromClaims extracts the user ID from parsed claims.
func UserIDFromClaims(claims *AccessTokenClaims) (uuid.UUID, error) {
	return uuid.Parse(claims.Subject)
}

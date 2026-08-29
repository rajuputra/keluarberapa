package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rajuputra/keluarberapa/backend/internal/database"
)

// RefreshTokenRepository errors.
var (
	// ErrRefreshTokenNotFound means no live refresh_tokens row matched the
	// presented hash.
	ErrRefreshTokenNotFound = errors.New("refresh token not found")
	// ErrRefreshTokenHashInvalid means the caller passed something that is not a
	// hex SHA-256 digest, so it cannot be a hash this code produced.
	ErrRefreshTokenHashInvalid = errors.New("refresh token hash is malformed")
)

const refreshTokenColumns = `id, user_id, token_hash, expires_at, revoked_at, created_at`

// RefreshTokenRepository reads and writes the refresh_tokens table.
//
// Only hashes are stored (architecture.md section 2): every method takes or
// returns a token hash, never a token. A caller that has a plaintext token runs
// it through HashRefreshToken first.
type RefreshTokenRepository struct {
	db *database.DB
}

// NewRefreshTokenRepository returns a repository backed by db.
func NewRefreshTokenRepository(db *database.DB) *RefreshTokenRepository {
	return &RefreshTokenRepository{db: db}
}

// Create stores a new refresh token for userID.
func (r *RefreshTokenRepository) Create(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) (*RefreshToken, error) {
	if err := checkHash(tokenHash); err != nil {
		return nil, err
	}

	const stmt = `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
		RETURNING ` + refreshTokenColumns

	created, err := scanRefreshToken(r.db.QueryRow(ctx, stmt, userID, tokenHash, expiresAt))
	if err != nil {
		return nil, fmt.Errorf("insert refresh token: %w", err)
	}
	return created, nil
}

// GetByTokenHash returns the token with this hash, whether or not it is still
// usable.
//
// Expired and revoked rows come back rather than being filtered out, because the
// service needs to tell them apart: a revoked token being presented again is a
// replay worth reacting to, while an expired one is just an old session. Callers
// decide with RefreshToken.IsUsable.
func (r *RefreshTokenRepository) GetByTokenHash(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	if err := checkHash(tokenHash); err != nil {
		return nil, err
	}

	const stmt = `SELECT ` + refreshTokenColumns + ` FROM refresh_tokens WHERE token_hash = $1`

	found, err := scanRefreshToken(r.db.QueryRow(ctx, stmt, tokenHash))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRefreshTokenNotFound
		}
		return nil, fmt.Errorf("select refresh token: %w", err)
	}
	return found, nil
}

// Revoke invalidates one token and reports whether it had been live.
//
// The `revoked_at IS NULL` guard makes the call idempotent and makes the return
// value meaningful: false means the token was already revoked or never existed.
// Logout uses that to stay silent either way rather than confirming to a caller
// that a token was real.
func (r *RefreshTokenRepository) Revoke(ctx context.Context, tokenHash string) (bool, error) {
	if err := checkHash(tokenHash); err != nil {
		return false, err
	}

	// now() is the server clock, which keeps revoked_at >= created_at true even
	// if an application host's clock is behind
	// (refresh_tokens_revoked_after_creation).
	const stmt = `UPDATE refresh_tokens SET revoked_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL`

	tag, err := r.db.Exec(ctx, stmt, tokenHash)
	if err != nil {
		return false, fmt.Errorf("revoke refresh token: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// RevokeAllForUser invalidates every live token of one user and returns how many
// it revoked. Used to end all sessions at once, for example after a revoked
// token is replayed.
func (r *RefreshTokenRepository) RevokeAllForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	const stmt = `UPDATE refresh_tokens SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL`

	tag, err := r.db.Exec(ctx, stmt, userID)
	if err != nil {
		return 0, fmt.Errorf("revoke user refresh tokens: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Rotate revokes oldTokenHash and stores newTokenHash in a single transaction.
//
// Doing both in one transaction is what makes refresh-token rotation safe: there
// is no window in which the old token is already spent and the new one does not
// exist yet, and no window in which both are usable. If oldTokenHash is not live
// — because a concurrent refresh already spent it — nothing is written and the
// call returns ErrRefreshTokenNotFound.
func (r *RefreshTokenRepository) Rotate(ctx context.Context, oldTokenHash string, userID uuid.UUID, newTokenHash string, expiresAt time.Time) (*RefreshToken, error) {
	if err := checkHash(oldTokenHash); err != nil {
		return nil, err
	}
	if err := checkHash(newTokenHash); err != nil {
		return nil, err
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin refresh token rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Scoped by user_id as well as by hash: a hash from one user must never be
	// able to spend a row belonging to another (architecture.md section 2).
	const revoke = `UPDATE refresh_tokens SET revoked_at = now()
		WHERE token_hash = $1 AND user_id = $2 AND revoked_at IS NULL`

	tag, err := tx.Exec(ctx, revoke, oldTokenHash, userID)
	if err != nil {
		return nil, fmt.Errorf("revoke rotated refresh token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrRefreshTokenNotFound
	}

	const insert = `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
		RETURNING ` + refreshTokenColumns

	created, err := scanRefreshToken(tx.QueryRow(ctx, insert, userID, newTokenHash, expiresAt))
	if err != nil {
		return nil, fmt.Errorf("insert rotated refresh token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit refresh token rotation: %w", err)
	}
	return created, nil
}

// DeleteExpiredBefore removes tokens that expired before cutoff and returns how
// many rows went. Nothing calls it on a schedule yet; it exists so that the
// table can be pruned without hand-written SQL.
func (r *RefreshTokenRepository) DeleteExpiredBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	const stmt = `DELETE FROM refresh_tokens WHERE expires_at < $1`

	tag, err := r.db.Exec(ctx, stmt, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete expired refresh tokens: %w", err)
	}
	return tag.RowsAffected(), nil
}

func scanRefreshToken(row pgx.Row) (*RefreshToken, error) {
	var t RefreshToken
	if err := row.Scan(&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

// checkHash rejects anything that is not a hex SHA-256 digest before it reaches
// the database, so a caller that passes a raw token by mistake gets an error
// instead of storing the token itself in the token_hash column.
func checkHash(tokenHash string) error {
	if len(tokenHash) != RefreshTokenHashLength {
		return fmt.Errorf("%w: expected %d hex characters, got %d",
			ErrRefreshTokenHashInvalid, RefreshTokenHashLength, len(tokenHash))
	}
	for i := 0; i < len(tokenHash); i++ {
		c := tokenHash[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("%w: it is not lower-case hexadecimal", ErrRefreshTokenHashInvalid)
		}
	}
	return nil
}

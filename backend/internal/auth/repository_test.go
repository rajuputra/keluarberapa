package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/rajuputra/keluarberapa/backend/internal/database/dbtest"
)

func TestRefreshTokenRepository(t *testing.T) {
	db := dbtest.New(t)
	repo := NewRefreshTokenRepository(db)
	ctx := context.Background()

	t.Run("Create and GetByTokenHash", func(t *testing.T) {
		userID := uuid.New()
		tokenHash := HashRefreshToken("test-token-value")
		expiresAt := time.Now().Add(30 * 24 * time.Hour)

		created, err := repo.Create(ctx, userID, tokenHash, expiresAt)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if created.ID == uuid.Nil {
			t.Error("created token has no id")
		}
		if created.UserID != userID {
			t.Errorf("created token user_id = %s, want %s", created.UserID, userID)
		}
		if created.TokenHash != tokenHash {
			t.Error("created token hash does not match")
		}
		if created.RevokedAt != nil {
			t.Error("new token should not be revoked")
		}

		found, err := repo.GetByTokenHash(ctx, tokenHash)
		if err != nil {
			t.Fatalf("GetByTokenHash: %v", err)
		}
		if found.ID != created.ID {
			t.Errorf("found token id = %s, want %s", found.ID, created.ID)
		}
	})

	t.Run("GetByTokenHash returns ErrRefreshTokenNotFound for unknown hash", func(t *testing.T) {
		_, err := repo.GetByTokenHash(ctx, HashRefreshToken("never-issued"))
		if !errors.Is(err, ErrRefreshTokenNotFound) {
			t.Errorf("GetByTokenHash = %v, want ErrRefreshTokenNotFound", err)
		}
	})

	t.Run("Revoke returns true and revokes the token", func(t *testing.T) {
		userID := uuid.New()
		tokenHash := HashRefreshToken("token-to-revoke")
		_, err := repo.Create(ctx, userID, tokenHash, time.Now().Add(24*time.Hour))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		revoked, err := repo.Revoke(ctx, tokenHash)
		if err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		if !revoked {
			t.Error("Revoke returned false for a live token")
		}

		found, err := repo.GetByTokenHash(ctx, tokenHash)
		if err != nil {
			t.Fatalf("GetByTokenHash after revoke: %v", err)
		}
		if found.RevokedAt == nil {
			t.Error("token should be revoked")
		}
		if !found.IsRevoked() {
			t.Error("IsRevoked should be true")
		}
	})

	t.Run("Revoke is idempotent and returns false for already revoked", func(t *testing.T) {
		userID := uuid.New()
		tokenHash := HashRefreshToken("token-to-revoke-twice")
		_, err := repo.Create(ctx, userID, tokenHash, time.Now().Add(24*time.Hour))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		_, _ = repo.Revoke(ctx, tokenHash)
		revoked, err := repo.Revoke(ctx, tokenHash)
		if err != nil {
			t.Fatalf("second Revoke: %v", err)
		}
		if revoked {
			t.Error("second Revoke returned true for already revoked token")
		}
	})

	t.Run("Revoke returns false for unknown hash", func(t *testing.T) {
		revoked, err := repo.Revoke(ctx, HashRefreshToken("unknown"))
		if err != nil {
			t.Fatalf("Revoke unknown: %v", err)
		}
		if revoked {
			t.Error("Revoke returned true for unknown token")
		}
	})

	t.Run("RevokeAllForUser revokes all live tokens for that user", func(t *testing.T) {
		userID := uuid.New()
		for i := 0; i < 3; i++ {
			_, err := repo.Create(ctx, userID, HashRefreshToken("token-"+string(rune('a'+i))), time.Now().Add(24*time.Hour))
			if err != nil {
				t.Fatalf("Create %d: %v", i, err)
			}
		}

		revoked, err := repo.RevokeAllForUser(ctx, userID)
		if err != nil {
			t.Fatalf("RevokeAllForUser: %v", err)
		}
		if revoked != 3 {
			t.Errorf("RevokeAllForUser revoked %d tokens, want 3", revoked)
		}

		found, err := repo.GetByTokenHash(ctx, HashRefreshToken("token-a"))
		if err != nil {
			t.Fatalf("GetByTokenHash: %v", err)
		}
		if !found.IsRevoked() {
			t.Error("token-a should be revoked")
		}
	})

	t.Run("RevokeAllForUser returns 0 for user with no tokens", func(t *testing.T) {
		revoked, err := repo.RevokeAllForUser(ctx, uuid.New())
		if err != nil {
			t.Fatalf("RevokeAllForUser: %v", err)
		}
		if revoked != 0 {
			t.Errorf("RevokeAllForUser returned %d, want 0", revoked)
		}
	})

	t.Run("Rotate revokes old and creates new in one transaction", func(t *testing.T) {
		userID := uuid.New()
		oldHash := HashRefreshToken("old-token")
		newHash := HashRefreshToken("new-token")

		_, err := repo.Create(ctx, userID, oldHash, time.Now().Add(24*time.Hour))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		rotated, err := repo.Rotate(ctx, oldHash, userID, newHash, time.Now().Add(48*time.Hour))
		if err != nil {
			t.Fatalf("Rotate: %v", err)
		}
		if rotated.TokenHash != newHash {
			t.Errorf("rotated token hash = %s, want %s", rotated.TokenHash, newHash)
		}

		oldFound, err := repo.GetByTokenHash(ctx, oldHash)
		if err != nil {
			t.Fatalf("GetByTokenHash old: %v", err)
		}
		if !oldFound.IsRevoked() {
			t.Error("old token should be revoked after rotation")
		}

		newFound, err := repo.GetByTokenHash(ctx, newHash)
		if err != nil {
			t.Fatalf("GetByTokenHash new: %v", err)
		}
		if newFound.IsRevoked() {
			t.Error("new token should not be revoked")
		}
		if newFound.ID != rotated.ID {
			t.Error("rotated token ID does not match lookup")
		}
	})

	t.Run("Rotate fails if old token is not live (wrong user)", func(t *testing.T) {
		userA := uuid.New()
		userB := uuid.New()
		oldHash := HashRefreshToken("token-a")
		newHash := HashRefreshToken("token-b")

		_, err := repo.Create(ctx, userA, oldHash, time.Now().Add(24*time.Hour))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		_, err = repo.Rotate(ctx, oldHash, userB, newHash, time.Now().Add(48*time.Hour))
		if !errors.Is(err, ErrRefreshTokenNotFound) {
			t.Errorf("Rotate with wrong user = %v, want ErrRefreshTokenNotFound", err)
		}
	})

	t.Run("Rotate fails if old token already revoked", func(t *testing.T) {
		userID := uuid.New()
		oldHash := HashRefreshToken("old-token")
		newHash := HashRefreshToken("new-token")

		_, err := repo.Create(ctx, userID, oldHash, time.Now().Add(24*time.Hour))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		_, _ = repo.Revoke(ctx, oldHash)

		_, err = repo.Rotate(ctx, oldHash, userID, newHash, time.Now().Add(48*time.Hour))
		if !errors.Is(err, ErrRefreshTokenNotFound) {
			t.Errorf("Rotate with revoked token = %v, want ErrRefreshTokenNotFound", err)
		}
	})

	t.Run("Rotate fails if new token hash already exists (unique index)", func(t *testing.T) {
		userID := uuid.New()
		tokenHash := HashRefreshToken("shared-hash")

		_, err := repo.Create(ctx, userID, tokenHash, time.Now().Add(24*time.Hour))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		_, err = repo.Rotate(ctx, HashRefreshToken("old-token"), userID, tokenHash, time.Now().Add(48*time.Hour))
		if err == nil {
			t.Fatal("Rotate with duplicate hash succeeded, expected unique violation")
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
			t.Errorf("Rotate = %v, want unique violation", err)
		}
	})

	t.Run("DeleteExpiredBefore removes only expired tokens", func(t *testing.T) {
		userID := uuid.New()
		expiredHash := HashRefreshToken("expired-token")
		liveHash := HashRefreshToken("live-token")

		_, err := repo.Create(ctx, userID, expiredHash, time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("Create expired: %v", err)
		}
		_, err = repo.Create(ctx, userID, liveHash, time.Now().Add(24*time.Hour))
		if err != nil {
			t.Fatalf("Create live: %v", err)
		}

		deleted, err := repo.DeleteExpiredBefore(ctx, time.Now())
		if err != nil {
			t.Fatalf("DeleteExpiredBefore: %v", err)
		}
		if deleted != 1 {
			t.Errorf("DeleteExpiredBefore deleted %d rows, want 1", deleted)
		}

		_, err = repo.GetByTokenHash(ctx, expiredHash)
		if !errors.Is(err, ErrRefreshTokenNotFound) {
			t.Errorf("expired token still exists: %v", err)
		}

		_, err = repo.GetByTokenHash(ctx, liveHash)
		if err != nil {
			t.Errorf("live token was deleted: %v", err)
		}
	})

	t.Run("CheckHash rejects non-hex, wrong length, upper case", func(t *testing.T) {
		validHash := HashRefreshToken("anything")

		tests := map[string]string{
			"empty":         "",
			"raw token":     "not-a-hash",
			"too short":     validHash[:len(validHash)-1],
			"too long":      validHash + "a",
			"upper case":    "ABCDEF",
			"non-hex chars": "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
			"spaced hex":    "ab cd ef 00 11 22 33 44 55 66 77 88 99 aa bb cc dd ee ff 00 11 22 33 44 55 66 77 88 99 aa bb cc",
		}
		for name, hash := range tests {
			t.Run(name, func(t *testing.T) {
				err := checkHash(hash)
				if err == nil {
					t.Errorf("checkHash accepted %q", hash)
				}
			})
		}
	})
}

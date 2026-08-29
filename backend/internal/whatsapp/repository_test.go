package whatsapp

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/rajuputra/keluarberapa/backend/internal/database/dbtest"
)

func TestAccountRepository(t *testing.T) {
	db := dbtest.New(t)
	repo := NewAccountRepository(db)
	ctx := context.Background()

	t.Run("Create links a number to a user", func(t *testing.T) {
		userID := uuid.New()
		account, err := repo.Create(ctx, userID, "6281234567890")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if account.ID == uuid.Nil {
			t.Error("account has no id")
		}
		if account.UserID != userID {
			t.Errorf("user_id = %s, want %s", account.UserID, userID)
		}
		if account.PhoneNumber != "6281234567890" {
			t.Errorf("phone_number = %q, want %q", account.PhoneNumber, "6281234567890")
		}
		if account.Provider != ProviderMetaCloudAPI {
			t.Errorf("provider = %q, want %q", account.Provider, ProviderMetaCloudAPI)
		}
		if account.VerificationStatus != VerificationPending {
			t.Errorf("verification_status = %q, want %q", account.VerificationStatus, VerificationPending)
		}
		if account.VerifiedAt != nil {
			t.Error("verified_at should be nil for pending status")
		}
	})

	t.Run("Create returns ErrUserAlreadyLinked for second number for same user", func(t *testing.T) {
		userID := uuid.New()
		_, err := repo.Create(ctx, userID, "6281234567890")
		if err != nil {
			t.Fatalf("first Create: %v", err)
		}

		_, err = repo.Create(ctx, userID, "6281355555555")
		if !errors.Is(err, ErrUserAlreadyLinked) {
			t.Errorf("second Create = %v, want ErrUserAlreadyLinked", err)
		}
	})

	t.Run("Create returns ErrPhoneNumberTaken for same number on different user", func(t *testing.T) {
		userA := uuid.New()
		userB := uuid.New()

		_, err := repo.Create(ctx, userA, "6281234567890")
		if err != nil {
			t.Fatalf("Create for user A: %v", err)
		}

		_, err = repo.Create(ctx, userB, "6281234567890")
		if !errors.Is(err, ErrPhoneNumberTaken) {
			t.Errorf("Create for user B = %v, want ErrPhoneNumberTaken", err)
		}
	})

	t.Run("Create rejects unnormalized phone number (CHECK constraint)", func(t *testing.T) {
		userID := uuid.New()
		_, err := repo.Create(ctx, userID, "+62 812-3456-7890")
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Errorf("Create with unnormalized number = %v, want check violation", err)
		}
	})

	t.Run("GetByUserID returns the account for that user", func(t *testing.T) {
		userID := uuid.New()
		created, err := repo.Create(ctx, userID, "6281234567890")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		found, err := repo.GetByUserID(ctx, userID)
		if err != nil {
			t.Fatalf("GetByUserID: %v", err)
		}
		if found.ID != created.ID {
			t.Errorf("found ID = %s, want %s", found.ID, created.ID)
		}
	})

	t.Run("GetByUserID returns ErrAccountNotFound for user with no account", func(t *testing.T) {
		_, err := repo.GetByUserID(ctx, uuid.New())
		if !errors.Is(err, ErrAccountNotFound) {
			t.Errorf("GetByUserID = %v, want ErrAccountNotFound", err)
		}
	})

	t.Run("GetByPhoneNumber resolves number to account", func(t *testing.T) {
		userID := uuid.New()
		created, err := repo.Create(ctx, userID, "6281234567890")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		found, err := repo.GetByPhoneNumber(ctx, "6281234567890")
		if err != nil {
			t.Fatalf("GetByPhoneNumber: %v", err)
		}
		if found.ID != created.ID {
			t.Errorf("found ID = %s, want %s", found.ID, created.ID)
		}
		if found.UserID != userID {
			t.Errorf("found user_id = %s, want %s", found.UserID, userID)
		}
	})

	t.Run("GetByPhoneNumber returns ErrAccountNotFound for unknown number", func(t *testing.T) {
		_, err := repo.GetByPhoneNumber(ctx, "6289999999999")
		if !errors.Is(err, ErrAccountNotFound) {
			t.Errorf("GetByPhoneNumber = %v, want ErrAccountNotFound", err)
		}
	})
}

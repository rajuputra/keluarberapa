package user

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/rajuputra/keluarberapa/backend/internal/database/dbtest"
	"github.com/rajuputra/keluarberapa/backend/internal/whatsapp"
)

func TestUserService(t *testing.T) {
	db := dbtest.New(t)
	userRepo := NewRepository(db)
	waRepo := whatsapp.NewAccountRepository(db)

	svc, err := NewService(ServiceConfig{
		Users:            userRepo,
		WhatsAppAccounts: waRepo,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()

	seedUser := func(t *testing.T) uuid.UUID {
		t.Helper()
		created, err := userRepo.Create(ctx, NewUser{
			Name:         "Test User",
			Email:        "test@example.com",
			PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$key",
			Timezone:     "Asia/Jakarta",
		})
		if err != nil {
			t.Fatalf("seed user: %v", err)
		}
		return created.ID
	}

	t.Run("Profile returns user without password hash", func(t *testing.T) {
		userID := seedUser(t)

		profile, err := svc.Profile(ctx, userID)
		if err != nil {
			t.Fatalf("Profile: %v", err)
		}
		if profile.ID != userID {
			t.Errorf("profile ID = %s, want %s", profile.ID, userID)
		}
		if profile.Name != "Test User" {
			t.Errorf("name = %q, want %q", profile.Name, "Test User")
		}
		if profile.Email != "test@example.com" {
			t.Errorf("email = %q, want %q", profile.Email, "test@example.com")
		}
	})

	t.Run("Profile returns ErrNotFound for unknown user", func(t *testing.T) {
		_, err := svc.Profile(ctx, uuid.New())
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Profile = %v, want ErrNotFound", err)
		}
	})

	t.Run("UpdateProfile changes name", func(t *testing.T) {
		userID := seedUser(t)

		newName := "Updated Name"
		profile, err := svc.UpdateProfile(ctx, userID, UpdateProfileInput{Name: &newName})
		if err != nil {
			t.Fatalf("UpdateProfile: %v", err)
		}
		if profile.Name != "Updated Name" {
			t.Errorf("name = %q, want %q", profile.Name, "Updated Name")
		}
	})

	t.Run("UpdateProfile changes timezone", func(t *testing.T) {
		userID := seedUser(t)

		newTZ := "Asia/Makassar"
		profile, err := svc.UpdateProfile(ctx, userID, UpdateProfileInput{Timezone: &newTZ})
		if err != nil {
			t.Fatalf("UpdateProfile: %v", err)
		}
		if profile.Timezone != "Asia/Makassar" {
			t.Errorf("timezone = %q, want %q", profile.Timezone, "Asia/Makassar")
		}
	})

	t.Run("UpdateProfile with empty input returns ErrNoChanges", func(t *testing.T) {
		userID := seedUser(t)

		_, err := svc.UpdateProfile(ctx, userID, UpdateProfileInput{})
		if !errors.Is(err, ErrNoChanges) {
			t.Errorf("UpdateProfile = %v, want ErrNoChanges", err)
		}
	})

	t.Run("UpdateProfile validates name", func(t *testing.T) {
		userID := seedUser(t)

		badName := "   "
		_, err := svc.UpdateProfile(ctx, userID, UpdateProfileInput{Name: &badName})
		if !errors.Is(err, ErrNameRequired) {
			t.Errorf("UpdateProfile with blank name = %v, want ErrNameRequired", err)
		}

		longName := "a"
		for i := 0; i < 199; i++ {
			longName += "a"
		}
		_, err = svc.UpdateProfile(ctx, userID, UpdateProfileInput{Name: &longName})
		if !errors.Is(err, ErrNameTooLong) {
			t.Errorf("UpdateProfile with long name = %v, want ErrNameTooLong", err)
		}
	})

	t.Run("UpdateProfile validates timezone", func(t *testing.T) {
		userID := seedUser(t)

		badTZ := "Mars/Olympus"
		_, err := svc.UpdateProfile(ctx, userID, UpdateProfileInput{Timezone: &badTZ})
		if !errors.Is(err, ErrTimezoneInvalid) {
			t.Errorf("UpdateProfile with bad timezone = %v, want ErrTimezoneInvalid", err)
		}
	})

	t.Run("UpdateProfile on unknown user returns ErrNotFound", func(t *testing.T) {
		name := "Ghost"
		_, err := svc.UpdateProfile(ctx, uuid.New(), UpdateProfileInput{Name: &name})
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("UpdateProfile = %v, want ErrNotFound", err)
		}
	})

	t.Run("LinkWhatsAppNumber links a normalized number", func(t *testing.T) {
		userID := seedUser(t)

		account, err := svc.LinkWhatsAppNumber(ctx, userID, "+62 812-3456-7890")
		if err != nil {
			t.Fatalf("LinkWhatsAppNumber: %v", err)
		}
		if account.UserID != userID {
			t.Errorf("account user_id = %s, want %s", account.UserID, userID)
		}
		if account.PhoneNumber != "6281234567890" {
			t.Errorf("phone_number = %q, want normalized %q", account.PhoneNumber, "6281234567890")
		}
		if account.VerificationStatus != whatsapp.VerificationPending {
			t.Errorf("verification_status = %q, want %q", account.VerificationStatus, whatsapp.VerificationPending)
		}
	})

	t.Run("LinkWhatsAppNumber returns ErrUserAlreadyLinked for second number", func(t *testing.T) {
		userID := seedUser(t)

		_, err := svc.LinkWhatsAppNumber(ctx, userID, "+62 812-3456-7890")
		if err != nil {
			t.Fatalf("first LinkWhatsAppNumber: %v", err)
		}

		_, err = svc.LinkWhatsAppNumber(ctx, userID, "+62 813-5555-5555")
		if !errors.Is(err, whatsapp.ErrUserAlreadyLinked) {
			t.Errorf("second LinkWhatsAppNumber = %v, want whatsapp.ErrUserAlreadyLinked", err)
		}
	})

	t.Run("LinkWhatsAppNumber returns ErrPhoneNumberTaken if number belongs to another user", func(t *testing.T) {
		userA := seedUser(t)
		userB := uuid.New()

		_, err := userRepo.Create(ctx, NewUser{
			Name:         "User B",
			Email:        "userb@example.com",
			PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$key",
			Timezone:     "Asia/Jakarta",
		})
		if err != nil {
			t.Fatalf("create user B: %v", err)
		}

		_, err = svc.LinkWhatsAppNumber(ctx, userA, "+62 812-3456-7890")
		if err != nil {
			t.Fatalf("user A LinkWhatsAppNumber: %v", err)
		}

		_, err = svc.LinkWhatsAppNumber(ctx, userB, "081234567890")
		if !errors.Is(err, whatsapp.ErrPhoneNumberTaken) {
			t.Errorf("user B LinkWhatsAppNumber = %v, want whatsapp.ErrPhoneNumberTaken", err)
		}
	})

	t.Run("LinkWhatsAppNumber returns ErrNotFound for unknown user", func(t *testing.T) {
		_, err := svc.LinkWhatsAppNumber(ctx, uuid.New(), "+62 812-3456-7890")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("LinkWhatsAppNumber = %v, want ErrNotFound", err)
		}
	})

	t.Run("LinkWhatsAppNumber rejects invalid phone number", func(t *testing.T) {
		userID := seedUser(t)

		_, err := svc.LinkWhatsAppNumber(ctx, userID, "not a number")
		if !errors.Is(err, whatsapp.ErrInvalidPhoneNumber) {
			t.Errorf("LinkWhatsAppNumber = %v, want whatsapp.ErrInvalidPhoneNumber", err)
		}
	})

	t.Run("WhatsAppAccount returns linked account", func(t *testing.T) {
		userID := seedUser(t)

		linked, err := svc.LinkWhatsAppNumber(ctx, userID, "+62 812-3456-7890")
		if err != nil {
			t.Fatalf("LinkWhatsAppNumber: %v", err)
		}

		account, err := svc.WhatsAppAccount(ctx, userID)
		if err != nil {
			t.Fatalf("WhatsAppAccount: %v", err)
		}
		if account.ID != linked.ID {
			t.Errorf("account ID = %s, want %s", account.ID, linked.ID)
		}
	})

	t.Run("WhatsAppAccount returns ErrAccountNotFound when no number linked", func(t *testing.T) {
		userID := seedUser(t)

		_, err := svc.WhatsAppAccount(ctx, userID)
		if !errors.Is(err, whatsapp.ErrAccountNotFound) {
			t.Errorf("WhatsAppAccount = %v, want whatsapp.ErrAccountNotFound", err)
		}
	})

	t.Run("WhatsAppAccount returns ErrNotFound for unknown user", func(t *testing.T) {
		_, err := svc.WhatsAppAccount(ctx, uuid.New())
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("WhatsAppAccount = %v, want ErrNotFound", err)
		}
	})

	t.Run("service ensures user exists before linking (foreign key)", func(t *testing.T) {
		_, err := svc.LinkWhatsAppNumber(ctx, uuid.New(), "+62 812-3456-7890")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("LinkWhatsAppNumber for unknown user = %v, want ErrNotFound", err)
		}
	})
}

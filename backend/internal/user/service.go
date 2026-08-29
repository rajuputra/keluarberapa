package user

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/rajuputra/keluarberapa/backend/internal/whatsapp"
)

// Store is the slice of the users table the service needs. *Repository
// implements it; tests substitute an in-memory fake.
type Store interface {
	GetByID(ctx context.Context, id uuid.UUID) (*User, error)
	Update(ctx context.Context, id uuid.UUID, ch Changes) (*User, error)
}

// WhatsAppAccountStore is the slice of the whatsapp_accounts table the service
// needs. *whatsapp.AccountRepository implements it.
//
// Only the scoped operations appear here. Resolving a number to its owner
// (GetByPhoneNumber) belongs to the inbound webhook path, not to a user managing
// their own account, so it is deliberately out of reach from this service.
type WhatsAppAccountStore interface {
	Create(ctx context.Context, userID uuid.UUID, phoneNumber string) (*whatsapp.Account, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) (*whatsapp.Account, error)
}

// ServiceConfig wires the user service. Users and WhatsAppAccounts are required.
type ServiceConfig struct {
	Users            Store
	WhatsAppAccounts WhatsAppAccountStore
	// Logger defaults to a discarding logger.
	Logger *slog.Logger
}

// Service reads and updates the authenticated user's own account.
//
// Every method takes the user id as its first argument, and that id must come
// from the JWT context rather than from a request body or query string
// (ai_instructions.md section 1.7). There is no method that takes an id from
// anywhere else, so a handler has nothing else to pass.
type Service struct {
	users    Store
	accounts WhatsAppAccountStore
	logger   *slog.Logger
}

// NewService validates cfg and returns the service.
func NewService(cfg ServiceConfig) (*Service, error) {
	var missing []error
	if cfg.Users == nil {
		missing = append(missing, errors.New("user: Users store is required"))
	}
	if cfg.WhatsAppAccounts == nil {
		missing = append(missing, errors.New("user: WhatsAppAccounts store is required"))
	}
	if err := errors.Join(missing...); err != nil {
		return nil, err
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Service{users: cfg.Users, accounts: cfg.WhatsAppAccounts, logger: logger}, nil
}

// Profile returns the account of userID, without its password hash.
func (s *Service) Profile(ctx context.Context, userID uuid.UUID) (*Profile, error) {
	found, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	profile := ProfileOf(*found)
	return &profile, nil
}

// UpdateProfileInput is a partial profile update: a nil field is left unchanged.
//
// Email is not updatable. Changing a login identifier needs a
// confirm-at-the-new-address flow to stop an account being moved to an address
// its owner does not control, and no such flow is specified — see
// backend/README.md for the open questions this stage did not invent an answer
// to. Password changes are likewise not part of Epic 1.
type UpdateProfileInput struct {
	Name     *string
	Timezone *string
}

// Validate normalises the input and reports every problem at once.
func (in UpdateProfileInput) Validate() (Changes, error) {
	var (
		out      Changes
		problems []error
	)

	if in.Name != nil {
		name, err := ValidateName(*in.Name)
		if err != nil {
			problems = append(problems, err)
		} else {
			out.Name = &name
		}
	}
	if in.Timezone != nil {
		// An explicitly empty timezone means "reset to the default" rather than
		// "leave alone"; leaving alone is expressed by omitting the field.
		timezone, err := ValidateTimezone(*in.Timezone)
		if err != nil {
			problems = append(problems, err)
		} else {
			out.Timezone = &timezone
		}
	}
	if err := errors.Join(problems...); err != nil {
		return Changes{}, err
	}
	if out.IsEmpty() {
		return Changes{}, ErrNoChanges
	}
	return out, nil
}

// UpdateProfile applies in to the account of userID.
func (s *Service) UpdateProfile(ctx context.Context, userID uuid.UUID, in UpdateProfileInput) (*Profile, error) {
	changes, err := in.Validate()
	if err != nil {
		return nil, err
	}

	// Scoped by userID inside the UPDATE itself, so a mismatch changes nothing
	// and reports ErrNotFound rather than touching another account's row.
	updated, err := s.users.Update(ctx, userID, changes)
	if err != nil {
		return nil, err
	}

	s.logger.Info("profile updated", slog.String("user_id", userID.String()))
	profile := ProfileOf(*updated)
	return &profile, nil
}

// LinkWhatsAppNumber links a WhatsApp number to the account of userID
// (user_stories.md Epic 1).
//
// The number is normalised first, so what is stored is the same canonical form
// the webhook will present later. Exactly one number per user is enforced by the
// database; this method surfaces that as whatsapp.ErrUserAlreadyLinked rather
// than replacing the existing link, because whether a user may move their number
// — and how a new one gets verified — is an unsettled product question recorded
// in backend/README.md.
func (s *Service) LinkWhatsAppNumber(ctx context.Context, userID uuid.UUID, rawPhoneNumber string) (*whatsapp.Account, error) {
	phoneNumber, err := whatsapp.NormalizePhoneNumber(rawPhoneNumber)
	if err != nil {
		return nil, err
	}

	// The user must exist before a row is written for them: a foreign-key error
	// is a worse answer than ErrNotFound, and the id is trusted only as far as
	// "the JWT said so".
	if _, err := s.users.GetByID(ctx, userID); err != nil {
		return nil, err
	}

	account, err := s.accounts.Create(ctx, userID, phoneNumber)
	if err != nil {
		return nil, err
	}

	// The number is not logged: it identifies a person, and log lines outlive the
	// request they came from.
	s.logger.Info("whatsapp number linked",
		slog.String("user_id", userID.String()),
		slog.String("account_id", account.ID.String()),
		slog.String("verification_status", account.VerificationStatus.String()),
	)
	return account, nil
}

// WhatsAppAccount returns the number linked to userID, or
// whatsapp.ErrAccountNotFound when there is none.
func (s *Service) WhatsAppAccount(ctx context.Context, userID uuid.UUID) (*whatsapp.Account, error) {
	account, err := s.accounts.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if account.UserID != userID {
		// Unreachable: the query is scoped by user_id. Kept as a tripwire, since
		// a scoping mistake here is exactly the failure ai_instructions.md
		// section 3 calls absolute.
		return nil, fmt.Errorf("whatsapp account %s does not belong to user %s", account.ID, userID)
	}
	return account, nil
}

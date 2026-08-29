package whatsapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rajuputra/keluarberapa/backend/internal/database"
)

// AccountRepository errors.
var (
	// ErrAccountNotFound means no whatsapp_accounts row matched.
	ErrAccountNotFound = errors.New("whatsapp account not found")
	// ErrUserAlreadyLinked means the user already has a number. Exactly one
	// number per user is the rule from architecture.md section 1, enforced by the
	// whatsapp_accounts_one_per_user constraint.
	ErrUserAlreadyLinked = errors.New("user already has a linked whatsapp number")
	// ErrPhoneNumberTaken means the number belongs to a different user. Allowing
	// two owners would make inbound identity resolution ambiguous, which
	// ai_instructions.md section 3 treats as an absolute failure condition.
	ErrPhoneNumberTaken = errors.New("whatsapp number is already linked to another account")
)

const accountColumns = `id, user_id, phone_number, provider, verification_status,
	verified_at, created_at, updated_at`

// AccountRepository reads and writes the whatsapp_accounts table: the mapping
// between a WhatsApp number and its owning user.
type AccountRepository struct {
	db *database.DB
}

// NewAccountRepository returns a repository backed by db.
func NewAccountRepository(db *database.DB) *AccountRepository {
	return &AccountRepository{db: db}
}

// Create links phoneNumber to userID.
//
// phoneNumber must already be canonical (NormalizePhoneNumber). New accounts
// start out unverified: verification_status defaults to 'pending' and
// verified_at stays NULL, which is the only combination the
// whatsapp_accounts_verified_at_consistent CHECK allows for a new row.
//
// Both uniqueness rules are enforced by the database rather than by a preceding
// SELECT, so two concurrent requests cannot both win the race.
func (r *AccountRepository) Create(ctx context.Context, userID uuid.UUID, phoneNumber string) (*Account, error) {
	const stmt = `
		INSERT INTO whatsapp_accounts (user_id, phone_number, provider)
		VALUES ($1, $2, $3)
		RETURNING ` + accountColumns

	created, err := scanAccount(r.db.QueryRow(ctx, stmt, userID, phoneNumber, string(ProviderMetaCloudAPI)))
	if err != nil {
		switch {
		case database.IsUniqueViolation(err, "whatsapp_accounts_one_per_user"):
			return nil, ErrUserAlreadyLinked
		case database.IsUniqueViolation(err, "whatsapp_accounts_phone_unique"):
			return nil, ErrPhoneNumberTaken
		case database.IsCheckViolation(err, "whatsapp_accounts_phone_format"):
			// NormalizePhoneNumber and the CHECK share one pattern, so reaching
			// here means an unnormalised value was passed in: a bug, not input.
			return nil, fmt.Errorf("insert whatsapp account: phone number was not normalised: %w", err)
		}
		return nil, fmt.Errorf("insert whatsapp account: %w", err)
	}
	return created, nil
}

// GetByUserID returns the account belonging to userID.
//
// This is the scoped read: the id comes from the JWT context, so a user can only
// ever see their own linked number.
func (r *AccountRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*Account, error) {
	const stmt = `SELECT ` + accountColumns + ` FROM whatsapp_accounts WHERE user_id = $1`

	found, err := scanAccount(r.db.QueryRow(ctx, stmt, userID))
	if err != nil {
		return nil, wrapAccountRead(err, "select whatsapp account by user")
	}
	return found, nil
}

// GetByPhoneNumber resolves an inbound number to its account, and through it to
// the owning user (architecture.md section 2, ai_instructions.md section 3).
//
// This is deliberately the one read that is not scoped by user id: it is the
// query that *establishes* which user is speaking, so there is no id to scope by
// yet. whatsapp_accounts_phone_unique guarantees the answer is a single user, and
// every query made afterwards on that user's behalf must be scoped to the
// user_id this returns.
func (r *AccountRepository) GetByPhoneNumber(ctx context.Context, phoneNumber string) (*Account, error) {
	const stmt = `SELECT ` + accountColumns + ` FROM whatsapp_accounts WHERE phone_number = $1`

	found, err := scanAccount(r.db.QueryRow(ctx, stmt, phoneNumber))
	if err != nil {
		return nil, wrapAccountRead(err, "select whatsapp account by phone number")
	}
	return found, nil
}

// scanAccount materialises one row. The enum-like columns are read as strings and
// converted explicitly, keeping the mapping visible rather than relying on the
// driver's handling of named string types.
func scanAccount(row pgx.Row) (*Account, error) {
	var (
		a                  Account
		provider           string
		verificationStatus string
	)
	err := row.Scan(&a.ID, &a.UserID, &a.PhoneNumber, &provider, &verificationStatus,
		&a.VerifiedAt, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	a.Provider = Provider(provider)
	a.VerificationStatus = VerificationStatus(verificationStatus)
	return &a, nil
}

func wrapAccountRead(err error, operation string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAccountNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

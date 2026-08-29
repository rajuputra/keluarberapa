package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/rajuputra/keluarberapa/backend/internal/user"
)

// Service errors. Each is matchable with errors.Is so the HTTP layer can map it
// to a status code without inspecting error text.
var (
	// ErrInvalidCredentials is returned for a wrong password *and* for an unknown
	// email. The two must be indistinguishable, or the login endpoint becomes a
	// way to test which addresses have accounts.
	ErrInvalidCredentials = errors.New("email or password is incorrect")
	// ErrAccountNotActive means the account exists but may not authenticate
	// (users.status is inactive or suspended).
	ErrAccountNotActive = errors.New("account is not active")
	// ErrInvalidRefreshToken covers unknown, expired, revoked and replayed
	// tokens. As with credentials, the caller learns nothing beyond "log in
	// again".
	ErrInvalidRefreshToken = errors.New("refresh token is invalid or expired")
	// ErrMissingRefreshToken means the request carried no token at all.
	ErrMissingRefreshToken = errors.New("refresh token is required")
)

// UserStore is the slice of the users table this service needs. *user.Repository
// implements it; tests substitute an in-memory fake.
type UserStore interface {
	Create(ctx context.Context, in user.NewUser) (*user.User, error)
	GetByEmail(ctx context.Context, email string) (*user.User, error)
	GetByID(ctx context.Context, id uuid.UUID) (*user.User, error)
}

// RefreshTokenStore is the slice of the refresh_tokens table this service needs.
// *RefreshTokenRepository implements it.
type RefreshTokenStore interface {
	Create(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) (*RefreshToken, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	Revoke(ctx context.Context, tokenHash string) (bool, error)
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	Rotate(ctx context.Context, oldTokenHash string, userID uuid.UUID, newTokenHash string, expiresAt time.Time) (*RefreshToken, error)
}

// PasswordHasher derives and checks password hashes. Hasher implements it.
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(encoded, password string) (bool, error)
}

// AccessToken is a signed short-lived credential and its expiry.
type AccessToken struct {
	Token     string
	ExpiresAt time.Time
}

// AccessTokenIssuer mints the access token returned by Login and Refresh.
//
// The concrete JWT implementation is deliberately not here. Signing and
// verification have to agree on the claim set exactly, so they belong in one
// change together with the middleware that verifies them; until then this service
// is complete and testable against a fake issuer.
type AccessTokenIssuer interface {
	IssueAccessToken(u user.User, now time.Time) (AccessToken, error)
}

// Session is what a successful login or refresh produces.
//
// User is a user.Profile rather than a user.User, so a password hash cannot
// travel out of this package even if a caller serialises the whole struct
// (ai_instructions.md section 1.8).
type Session struct {
	User                  user.Profile
	AccessToken           string
	AccessTokenExpiresAt  time.Time
	RefreshToken          string
	RefreshTokenExpiresAt time.Time
}

// ServiceConfig wires the auth service. Users, Tokens, Hasher, AccessTokens and
// RefreshTTL are required; the rest have defaults.
type ServiceConfig struct {
	Users        UserStore
	Tokens       RefreshTokenStore
	Hasher       PasswordHasher
	AccessTokens AccessTokenIssuer
	// RefreshTTL is JWT_REFRESH_TTL, 30 days by default (architecture.md
	// section 2).
	RefreshTTL time.Duration
	// Now defaults to time.Now. Injected so token lifetimes are testable.
	Now func() time.Time
	// Logger defaults to a discarding logger.
	Logger *slog.Logger
}

// Service implements registration, login, refresh and logout.
type Service struct {
	users        UserStore
	tokens       RefreshTokenStore
	hasher       PasswordHasher
	accessTokens AccessTokenIssuer
	refreshTTL   time.Duration
	now          func() time.Time
	logger       *slog.Logger
}

// NewService validates cfg and returns the service.
//
// Missing dependencies are an error rather than a nil panic later, so a wiring
// mistake in main shows up at startup.
func NewService(cfg ServiceConfig) (*Service, error) {
	var missing []error
	if cfg.Users == nil {
		missing = append(missing, errors.New("auth: Users store is required"))
	}
	if cfg.Tokens == nil {
		missing = append(missing, errors.New("auth: Tokens store is required"))
	}
	if cfg.Hasher == nil {
		missing = append(missing, errors.New("auth: Hasher is required"))
	}
	if cfg.AccessTokens == nil {
		missing = append(missing, errors.New("auth: AccessTokens issuer is required"))
	}
	if cfg.RefreshTTL <= 0 {
		missing = append(missing, errors.New("auth: RefreshTTL must be positive"))
	}
	if err := errors.Join(missing...); err != nil {
		return nil, err
	}

	s := &Service{
		users:        cfg.Users,
		tokens:       cfg.Tokens,
		hasher:       cfg.Hasher,
		accessTokens: cfg.AccessTokens,
		refreshTTL:   cfg.RefreshTTL,
		now:          cfg.Now,
		logger:       cfg.Logger,
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	return s, nil
}

// RegisterInput is the registration request.
type RegisterInput struct {
	Name     string
	Email    string
	Password string
	// Timezone is optional; empty means user.DefaultTimezone.
	Timezone string
}

// Validate normalises the input and reports every problem at once.
//
// The returned value is what should be stored: the email is already lower-cased
// and trimmed, so it matches the lower(email) expression behind
// users_email_unique.
func (in RegisterInput) Validate() (RegisterInput, error) {
	var (
		out      RegisterInput
		problems []error
	)

	name, err := user.ValidateName(in.Name)
	if err != nil {
		problems = append(problems, err)
	}
	email, err := user.ValidateEmail(in.Email)
	if err != nil {
		problems = append(problems, err)
	}
	if err := ValidatePassword(in.Password); err != nil {
		problems = append(problems, err)
	}
	timezone, err := user.ValidateTimezone(in.Timezone)
	if err != nil {
		problems = append(problems, err)
	}
	if err := errors.Join(problems...); err != nil {
		return RegisterInput{}, err
	}

	out.Name = name
	out.Email = email
	out.Password = in.Password
	out.Timezone = timezone
	return out, nil
}

// Register creates an account (user_stories.md Epic 1).
//
// No session is returned: registering and logging in are separate stories, so a
// new account authenticates through Login like any other. Uniqueness is decided
// by the users_email_unique index inside Create, not by a SELECT here, so two
// simultaneous registrations of one address cannot both succeed.
func (s *Service) Register(ctx context.Context, in RegisterInput) (*user.Profile, error) {
	valid, err := in.Validate()
	if err != nil {
		return nil, err
	}

	hash, err := s.hasher.Hash(valid.Password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	created, err := s.users.Create(ctx, user.NewUser{
		Name:         valid.Name,
		Email:        valid.Email,
		PasswordHash: hash,
		Timezone:     valid.Timezone,
	})
	if err != nil {
		// Includes user.ErrEmailTaken, which the HTTP layer maps to 409.
		return nil, err
	}

	// The email is logged because it is the account identifier an operator needs;
	// the password and its hash never are (ai_instructions.md section 1.8).
	s.logger.Info("user registered",
		slog.String("user_id", created.ID.String()),
		slog.String("email", created.Email),
	)

	profile := user.ProfileOf(*created)
	return &profile, nil
}

// LoginInput is the login request.
type LoginInput struct {
	Email    string
	Password string
}

// Login checks the credentials and starts a session (user_stories.md Epic 1).
//
// Input is not validated the way registration is: a malformed address is simply
// wrong credentials. Reporting "that is not a valid email" here would answer a
// question the caller has not earned an answer to.
func (s *Service) Login(ctx context.Context, in LoginInput) (*Session, error) {
	found, err := s.users.GetByEmail(ctx, user.NormalizeEmail(in.Email))
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			// Derive a hash anyway and throw it away. Without this, a login for
			// an unknown address returns in microseconds while a known one takes
			// as long as Argon2id does, and that difference is a usable oracle
			// for which addresses have accounts. The error is ignored on purpose:
			// the only point of the call is the time it takes.
			_, _ = s.hasher.Hash(in.Password)
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	match, err := s.hasher.Verify(found.PasswordHash, in.Password)
	if err != nil {
		// A stored hash that cannot be parsed is an operational fault, not a
		// failed login, so it is reported rather than shown as bad credentials.
		return nil, fmt.Errorf("verify password for user %s: %w", found.ID, err)
	}
	if !match {
		s.logger.Info("login rejected", slog.String("user_id", found.ID.String()), slog.String("reason", "password"))
		return nil, ErrInvalidCredentials
	}
	// Checked after the password, so a suspended account is not revealed to
	// someone who does not know its password.
	if !found.IsActive() {
		s.logger.Warn("login rejected",
			slog.String("user_id", found.ID.String()),
			slog.String("reason", "status"),
			slog.String("status", found.Status.String()),
		)
		return nil, ErrAccountNotActive
	}

	session, err := s.startSession(ctx, *found)
	if err != nil {
		return nil, err
	}
	s.logger.Info("login succeeded", slog.String("user_id", found.ID.String()))
	return session, nil
}

// Refresh exchanges a refresh token for a new session.
//
// The presented token is rotated, not reused: it is revoked and replaced in one
// transaction. That bounds the damage from a leaked token to a single use and
// makes replay detectable, which is the point of the check below.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (*Session, error) {
	if refreshToken == "" {
		return nil, ErrMissingRefreshToken
	}
	hash := HashRefreshToken(refreshToken)

	stored, err := s.tokens.GetByTokenHash(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrRefreshTokenNotFound) || errors.Is(err, ErrRefreshTokenHashInvalid) {
			return nil, ErrInvalidRefreshToken
		}
		return nil, err
	}

	now := s.now()
	if stored.IsRevoked() {
		// A revoked token was presented. Either it leaked and someone is
		// replaying it, or the legitimate client is retrying with a spent token
		// after a rotation it did not see. Both are handled the same way, by
		// ending every session for that user: the safe response to "a token that
		// should be dead is in use" is to make all of them dead.
		revoked, revokeErr := s.tokens.RevokeAllForUser(ctx, stored.UserID)
		if revokeErr != nil {
			return nil, fmt.Errorf("revoke sessions after refresh token replay: %w", revokeErr)
		}
		s.logger.Warn("revoked refresh token replayed; all sessions ended",
			slog.String("user_id", stored.UserID.String()),
			slog.Int64("sessions_revoked", revoked),
		)
		return nil, ErrInvalidRefreshToken
	}
	if stored.IsExpired(now) {
		return nil, ErrInvalidRefreshToken
	}

	owner, err := s.users.GetByID(ctx, stored.UserID)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			// The row is gone but the token is not; ON DELETE CASCADE makes this
			// unreachable in practice. Treated as an invalid token rather than a
			// 404, because the caller is not asking about a user.
			return nil, ErrInvalidRefreshToken
		}
		return nil, err
	}
	if !owner.IsActive() {
		return nil, ErrAccountNotActive
	}

	newToken, err := NewRefreshTokenValue()
	if err != nil {
		return nil, err
	}
	rotated, err := s.tokens.Rotate(ctx, hash, owner.ID, HashRefreshToken(newToken), now.Add(s.refreshTTL))
	if err != nil {
		if errors.Is(err, ErrRefreshTokenNotFound) {
			// A concurrent refresh spent the token between the read and the
			// rotation. The client retries by logging in.
			return nil, ErrInvalidRefreshToken
		}
		return nil, err
	}

	access, err := s.accessTokens.IssueAccessToken(*owner, now)
	if err != nil {
		return nil, fmt.Errorf("issue access token: %w", err)
	}

	s.logger.Info("session refreshed", slog.String("user_id", owner.ID.String()))
	return &Session{
		User:                  user.ProfileOf(*owner),
		AccessToken:           access.Token,
		AccessTokenExpiresAt:  access.ExpiresAt,
		RefreshToken:          newToken,
		RefreshTokenExpiresAt: rotated.ExpiresAt,
	}, nil
}

// Logout revokes one refresh token.
//
// It succeeds whether or not the token was live. An unknown or already-revoked
// token is not an error, both because logging out twice should not fail and
// because a distinct error would confirm to a caller that a token they guessed
// exists. The access token is not revoked: it is short-lived by design
// (15 minutes) and is not tracked server-side.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return ErrMissingRefreshToken
	}

	revoked, err := s.tokens.Revoke(ctx, HashRefreshToken(refreshToken))
	if err != nil {
		if errors.Is(err, ErrRefreshTokenHashInvalid) {
			// Cannot happen: HashRefreshToken always produces a valid digest.
			return ErrInvalidRefreshToken
		}
		return err
	}
	s.logger.Info("logout", slog.Bool("session_ended", revoked))
	return nil
}

// LogoutAll ends every session of one user. userID comes from the JWT context.
func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID) (int64, error) {
	revoked, err := s.tokens.RevokeAllForUser(ctx, userID)
	if err != nil {
		return 0, err
	}
	s.logger.Info("all sessions ended",
		slog.String("user_id", userID.String()),
		slog.Int64("sessions_revoked", revoked),
	)
	return revoked, nil
}

// startSession issues an access token and stores a fresh refresh token.
func (s *Service) startSession(ctx context.Context, u user.User) (*Session, error) {
	now := s.now()

	access, err := s.accessTokens.IssueAccessToken(u, now)
	if err != nil {
		return nil, fmt.Errorf("issue access token: %w", err)
	}

	refreshToken, err := NewRefreshTokenValue()
	if err != nil {
		return nil, err
	}
	stored, err := s.tokens.Create(ctx, u.ID, HashRefreshToken(refreshToken), now.Add(s.refreshTTL))
	if err != nil {
		return nil, err
	}

	return &Session{
		User:                  user.ProfileOf(u),
		AccessToken:           access.Token,
		AccessTokenExpiresAt:  access.ExpiresAt,
		RefreshToken:          refreshToken,
		RefreshTokenExpiresAt: stored.ExpiresAt,
	}, nil
}

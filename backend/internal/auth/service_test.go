package auth_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rajuputra/keluarberapa/backend/internal/auth"
	"github.com/rajuputra/keluarberapa/backend/internal/user"
)

// The service is exercised through in-memory fakes rather than PostgreSQL, so
// these tests state what the service decides — which errors it maps, what it
// stores, what it refuses — independently of SQL. The repositories have their own
// integration tests against a real schema.

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeUsers is an in-memory users table. It reproduces the two behaviours the
// service depends on: case-insensitive email uniqueness, and ErrNotFound.
type fakeUsers struct {
	mu        sync.Mutex
	byID      map[uuid.UUID]user.User
	createErr error
	getErr    error
	calls     int
}

func newFakeUsers() *fakeUsers {
	return &fakeUsers{byID: make(map[uuid.UUID]user.User)}
}

func (f *fakeUsers) Create(_ context.Context, in user.NewUser) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.createErr != nil {
		return nil, f.createErr
	}
	for _, existing := range f.byID {
		if user.NormalizeEmail(existing.Email) == user.NormalizeEmail(in.Email) {
			return nil, user.ErrEmailTaken
		}
	}

	now := time.Now().UTC()
	created := user.User{
		ID:           uuid.New(),
		Name:         in.Name,
		Email:        in.Email,
		PasswordHash: in.PasswordHash,
		Status:       user.StatusActive,
		Timezone:     in.Timezone,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	f.byID[created.ID] = created
	return &created, nil
}

func (f *fakeUsers) GetByEmail(_ context.Context, email string) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	for _, existing := range f.byID {
		if user.NormalizeEmail(existing.Email) == user.NormalizeEmail(email) {
			found := existing
			return &found, nil
		}
	}
	return nil, user.ErrNotFound
}

func (f *fakeUsers) GetByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.getErr != nil {
		return nil, f.getErr
	}
	found, ok := f.byID[id]
	if !ok {
		return nil, user.ErrNotFound
	}
	return &found, nil
}

// put inserts a user directly, for arranging a test.
func (f *fakeUsers) put(u user.User) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[u.ID] = u
}

// fakeTokens is an in-memory refresh_tokens table. Rotate is atomic here for the
// same reason it is a transaction in the repository.
type fakeTokens struct {
	mu        sync.Mutex
	byHash    map[string]auth.RefreshToken
	rotations int
}

func newFakeTokens() *fakeTokens {
	return &fakeTokens{byHash: make(map[string]auth.RefreshToken)}
}

func (f *fakeTokens) Create(_ context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) (*auth.RefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.insert(userID, tokenHash, expiresAt)
}

func (f *fakeTokens) insert(userID uuid.UUID, tokenHash string, expiresAt time.Time) (*auth.RefreshToken, error) {
	if _, exists := f.byHash[tokenHash]; exists {
		return nil, errors.New("refresh_tokens_token_hash_unique")
	}
	stored := auth.RefreshToken{
		ID:        uuid.New(),
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now().UTC(),
	}
	f.byHash[tokenHash] = stored
	return &stored, nil
}

func (f *fakeTokens) GetByTokenHash(_ context.Context, tokenHash string) (*auth.RefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	stored, ok := f.byHash[tokenHash]
	if !ok {
		return nil, auth.ErrRefreshTokenNotFound
	}
	return &stored, nil
}

func (f *fakeTokens) Revoke(_ context.Context, tokenHash string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	stored, ok := f.byHash[tokenHash]
	if !ok || stored.RevokedAt != nil {
		return false, nil
	}
	now := time.Now().UTC()
	stored.RevokedAt = &now
	f.byHash[tokenHash] = stored
	return true, nil
}

func (f *fakeTokens) RevokeAllForUser(_ context.Context, userID uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Now().UTC()
	var revoked int64
	for hash, stored := range f.byHash {
		if stored.UserID == userID && stored.RevokedAt == nil {
			stored.RevokedAt = &now
			f.byHash[hash] = stored
			revoked++
		}
	}
	return revoked, nil
}

func (f *fakeTokens) Rotate(_ context.Context, oldTokenHash string, userID uuid.UUID, newTokenHash string, expiresAt time.Time) (*auth.RefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	stored, ok := f.byHash[oldTokenHash]
	if !ok || stored.RevokedAt != nil || stored.UserID != userID {
		return nil, auth.ErrRefreshTokenNotFound
	}
	now := time.Now().UTC()
	stored.RevokedAt = &now
	f.byHash[oldTokenHash] = stored
	f.rotations++

	return f.insert(userID, newTokenHash, expiresAt)
}

func (f *fakeTokens) live() []auth.RefreshToken {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []auth.RefreshToken
	for _, stored := range f.byHash {
		if stored.RevokedAt == nil {
			out = append(out, stored)
		}
	}
	return out
}

func (f *fakeTokens) get(tokenHash string) (auth.RefreshToken, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored, ok := f.byHash[tokenHash]
	return stored, ok
}

// fakeIssuer stands in for the JWT signer, which arrives with the HTTP stage.
type fakeIssuer struct {
	ttl    time.Duration
	err    error
	issued []uuid.UUID
}

func (f *fakeIssuer) IssueAccessToken(u user.User, now time.Time) (auth.AccessToken, error) {
	if f.err != nil {
		return auth.AccessToken{}, f.err
	}
	f.issued = append(f.issued, u.ID)
	ttl := f.ttl
	if ttl == 0 {
		ttl = 15 * time.Minute
	}
	return auth.AccessToken{
		Token:     fmt.Sprintf("access-token-for-%s", u.ID),
		ExpiresAt: now.Add(ttl),
	}, nil
}

// cheapHasher keeps Argon2id honest but affordable: the algorithm and the
// encoding are the real ones, only the cost is lowered.
func cheapHasher() auth.Hasher {
	h := auth.DefaultHasher()
	h.MemoryKiB = 8 * 1024
	h.Time = 1
	return h
}

type harness struct {
	service *auth.Service
	users   *fakeUsers
	tokens  *fakeTokens
	issuer  *fakeIssuer
	hasher  auth.Hasher
	now     time.Time
}

const testRefreshTTL = 30 * 24 * time.Hour

func newHarness(t *testing.T) *harness {
	t.Helper()

	h := &harness{
		users:  newFakeUsers(),
		tokens: newFakeTokens(),
		issuer: &fakeIssuer{},
		hasher: cheapHasher(),
		now:    time.Date(2026, time.August, 27, 10, 0, 0, 0, time.UTC),
	}

	service, err := auth.NewService(auth.ServiceConfig{
		Users:        h.users,
		Tokens:       h.tokens,
		Hasher:       h.hasher,
		AccessTokens: h.issuer,
		RefreshTTL:   testRefreshTTL,
		Now:          func() time.Time { return h.now },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	h.service = service
	return h
}

// seedUser registers a user through the service, so the stored hash is a real one.
func (h *harness) seedUser(t *testing.T, email, password string) user.Profile {
	t.Helper()

	profile, err := h.service.Register(context.Background(), auth.RegisterInput{
		Name:     "Raju Putra",
		Email:    email,
		Password: password,
	})
	if err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return *profile
}

// ---------------------------------------------------------------------------
// Wiring
// ---------------------------------------------------------------------------

// A missing dependency has to fail at wiring time, not as a nil dereference on
// the first login.
func TestNewServiceRequiresEveryDependency(t *testing.T) {
	complete := auth.ServiceConfig{
		Users:        newFakeUsers(),
		Tokens:       newFakeTokens(),
		Hasher:       cheapHasher(),
		AccessTokens: &fakeIssuer{},
		RefreshTTL:   testRefreshTTL,
	}
	if _, err := auth.NewService(complete); err != nil {
		t.Fatalf("a complete config was rejected: %v", err)
	}

	tests := map[string]func(cfg *auth.ServiceConfig){
		"no users":       func(cfg *auth.ServiceConfig) { cfg.Users = nil },
		"no tokens":      func(cfg *auth.ServiceConfig) { cfg.Tokens = nil },
		"no hasher":      func(cfg *auth.ServiceConfig) { cfg.Hasher = nil },
		"no issuer":      func(cfg *auth.ServiceConfig) { cfg.AccessTokens = nil },
		"zero ttl":       func(cfg *auth.ServiceConfig) { cfg.RefreshTTL = 0 },
		"negative ttl":   func(cfg *auth.ServiceConfig) { cfg.RefreshTTL = -time.Hour },
		"nothing at all": func(cfg *auth.ServiceConfig) { *cfg = auth.ServiceConfig{} },
	}
	for name, break_ := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := complete
			break_(&cfg)
			if _, err := auth.NewService(cfg); err == nil {
				t.Error("NewService accepted an incomplete config")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Register (user_stories.md Epic 1)
// ---------------------------------------------------------------------------

func TestRegisterCreatesAnAccount(t *testing.T) {
	h := newHarness(t)

	profile, err := h.service.Register(context.Background(), auth.RegisterInput{
		Name:     "  Raju Putra  ",
		Email:    "  Raju@Example.COM ",
		Password: "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if profile.ID == uuid.Nil {
		t.Error("the new user has no id")
	}
	if profile.Name != "Raju Putra" {
		t.Errorf("name = %q, want the trimmed %q", profile.Name, "Raju Putra")
	}
	// Stored in the form the users_email_unique index is built on.
	if profile.Email != "raju@example.com" {
		t.Errorf("email = %q, want the normalised %q", profile.Email, "raju@example.com")
	}
	if profile.Status != user.StatusActive {
		t.Errorf("status = %q, want active", profile.Status)
	}
	if profile.Timezone != user.DefaultTimezone {
		t.Errorf("timezone = %q, want the default %q", profile.Timezone, user.DefaultTimezone)
	}
}

// The password must be stored only as an Argon2id hash that verifies.
func TestRegisterStoresOnlyAHash(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"

	profile := h.seedUser(t, "raju@example.com", password)

	stored, err := h.users.GetByID(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.PasswordHash == "" {
		t.Fatal("no password hash was stored")
	}
	if strings.Contains(stored.PasswordHash, password) {
		t.Fatal("the stored hash contains the plaintext password")
	}
	if !strings.HasPrefix(stored.PasswordHash, "$argon2id$") {
		t.Errorf("stored hash = %q, want an Argon2id hash", stored.PasswordHash)
	}

	match, err := h.hasher.Verify(stored.PasswordHash, password)
	if err != nil {
		t.Fatalf("Verify the stored hash: %v", err)
	}
	if !match {
		t.Error("the stored hash does not verify against the registered password")
	}
}

// Register does not log the user in: registering and logging in are separate
// stories, so no session is created here.
func TestRegisterDoesNotStartASession(t *testing.T) {
	h := newHarness(t)

	h.seedUser(t, "raju@example.com", "correct horse battery staple")

	if live := h.tokens.live(); len(live) != 0 {
		t.Errorf("Register created %d refresh tokens, want 0", len(live))
	}
	if len(h.issuer.issued) != 0 {
		t.Errorf("Register issued %d access tokens, want 0", len(h.issuer.issued))
	}
}

// Uniqueness is case-insensitive, matching the index on lower(email).
func TestRegisterRejectsADuplicateEmail(t *testing.T) {
	h := newHarness(t)
	h.seedUser(t, "raju@example.com", "correct horse battery staple")

	for _, email := range []string{"raju@example.com", "RAJU@example.com", "  Raju@Example.Com  "} {
		_, err := h.service.Register(context.Background(), auth.RegisterInput{
			Name:     "Impostor",
			Email:    email,
			Password: "another perfectly fine password",
		})
		if !errors.Is(err, user.ErrEmailTaken) {
			t.Errorf("Register(%q) = %v, want user.ErrEmailTaken", email, err)
		}
	}
}

func TestRegisterValidatesItsInput(t *testing.T) {
	h := newHarness(t)

	valid := auth.RegisterInput{
		Name:     "Raju Putra",
		Email:    "raju@example.com",
		Password: "correct horse battery staple",
	}

	tests := map[string]struct {
		mutate func(in *auth.RegisterInput)
		want   error
	}{
		"no name":        {func(in *auth.RegisterInput) { in.Name = "" }, user.ErrNameRequired},
		"blank name":     {func(in *auth.RegisterInput) { in.Name = "   " }, user.ErrNameRequired},
		"long name":      {func(in *auth.RegisterInput) { in.Name = strings.Repeat("a", 200) }, user.ErrNameTooLong},
		"no email":       {func(in *auth.RegisterInput) { in.Email = "" }, user.ErrEmailRequired},
		"bad email":      {func(in *auth.RegisterInput) { in.Email = "not-an-email" }, user.ErrEmailInvalid},
		"short password": {func(in *auth.RegisterInput) { in.Password = "short" }, auth.ErrPasswordTooShort},
		"no password":    {func(in *auth.RegisterInput) { in.Password = "" }, auth.ErrPasswordTooShort},
		"blank password": {func(in *auth.RegisterInput) { in.Password = "         " }, auth.ErrPasswordBlank},
		"bad timezone":   {func(in *auth.RegisterInput) { in.Timezone = "Mars/Olympus" }, user.ErrTimezoneInvalid},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			in := valid
			tt.mutate(&in)

			profile, err := h.service.Register(context.Background(), in)
			if !errors.Is(err, tt.want) {
				t.Errorf("Register = %v, want %v", err, tt.want)
			}
			if profile != nil {
				t.Error("a rejected registration returned a profile")
			}
		})
	}

	// Nothing was written by any of the rejected attempts.
	if len(h.users.byID) != 0 {
		t.Errorf("%d users were created by invalid registrations, want 0", len(h.users.byID))
	}
}

// Every problem is reported at once, the way config validation does, so a client
// does not have to fix one field per round trip.
func TestRegisterReportsEveryValidationProblem(t *testing.T) {
	h := newHarness(t)

	_, err := h.service.Register(context.Background(), auth.RegisterInput{
		Name:     "",
		Email:    "nope",
		Password: "x",
		Timezone: "Mars/Olympus",
	})
	if err == nil {
		t.Fatal("Register accepted input with four problems")
	}

	for _, want := range []error{
		user.ErrNameRequired, user.ErrEmailInvalid,
		auth.ErrPasswordTooShort, user.ErrTimezoneInvalid,
	} {
		if !errors.Is(err, want) {
			t.Errorf("error %v does not report %v", err, want)
		}
	}
}

func TestRegisterAcceptsAnExplicitTimezone(t *testing.T) {
	h := newHarness(t)

	profile, err := h.service.Register(context.Background(), auth.RegisterInput{
		Name:     "Ari",
		Email:    "ari@example.com",
		Password: "correct horse battery staple",
		Timezone: "Asia/Makassar",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if profile.Timezone != "Asia/Makassar" {
		t.Errorf("timezone = %q, want Asia/Makassar", profile.Timezone)
	}
}

// ---------------------------------------------------------------------------
// Login (user_stories.md Epic 1)
// ---------------------------------------------------------------------------

func TestLoginReturnsBothTokens(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	profile := h.seedUser(t, "raju@example.com", password)

	session, err := h.service.Login(context.Background(), auth.LoginInput{
		Email:    "RAJU@Example.com", // casing must not matter
		Password: password,
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if session.User.ID != profile.ID {
		t.Errorf("session user = %s, want %s", session.User.ID, profile.ID)
	}
	if session.AccessToken == "" {
		t.Error("no access token was issued")
	}
	if session.RefreshToken == "" {
		t.Error("no refresh token was issued")
	}
	if session.AccessToken == session.RefreshToken {
		t.Error("the access and refresh tokens are the same value")
	}

	// architecture.md section 2: 15 minute access token, 30 day refresh token.
	if want := h.now.Add(15 * time.Minute); !session.AccessTokenExpiresAt.Equal(want) {
		t.Errorf("access token expires %s, want %s", session.AccessTokenExpiresAt, want)
	}
	if want := h.now.Add(testRefreshTTL); !session.RefreshTokenExpiresAt.Equal(want) {
		t.Errorf("refresh token expires %s, want %s", session.RefreshTokenExpiresAt, want)
	}
}

// architecture.md section 2: refresh tokens are stored hashed. The plaintext must
// exist only in the response.
func TestLoginStoresTheRefreshTokenHashed(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	profile := h.seedUser(t, "raju@example.com", password)

	session, err := h.service.Login(context.Background(),
		auth.LoginInput{Email: "raju@example.com", Password: password})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if _, stored := h.tokens.get(session.RefreshToken); stored {
		t.Fatal("the plaintext refresh token is a key in the token store")
	}

	stored, ok := h.tokens.get(auth.HashRefreshToken(session.RefreshToken))
	if !ok {
		t.Fatal("no row was stored for the issued refresh token's hash")
	}
	if stored.TokenHash == session.RefreshToken {
		t.Error("token_hash holds the token itself")
	}
	if len(stored.TokenHash) != auth.RefreshTokenHashLength {
		t.Errorf("token_hash length = %d, want %d", len(stored.TokenHash), auth.RefreshTokenHashLength)
	}
	if stored.UserID != profile.ID {
		t.Errorf("the token belongs to %s, want %s", stored.UserID, profile.ID)
	}
	if stored.RevokedAt != nil {
		t.Error("a freshly issued token is already revoked")
	}
}

// A wrong password and an unknown address must be indistinguishable, or login
// becomes a way to enumerate which addresses have accounts.
func TestLoginRejectsBadCredentialsIdentically(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	h.seedUser(t, "raju@example.com", password)

	wrongPassword := h.mustFailLogin(t, "raju@example.com", "not the password")
	unknownEmail := h.mustFailLogin(t, "nobody@example.com", password)

	if wrongPassword.Error() != unknownEmail.Error() {
		t.Errorf("a wrong password says %q but an unknown email says %q; the two must match",
			wrongPassword, unknownEmail)
	}
	for _, err := range []error{wrongPassword, unknownEmail} {
		if !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Errorf("error = %v, want ErrInvalidCredentials", err)
		}
	}
}

// The unknown-email path must still do the hashing work, or its response time
// alone reveals that the address has no account.
func TestLoginHashesEvenForAnUnknownEmail(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	h.seedUser(t, "raju@example.com", password)

	known := timeCall(func() {
		_, _ = h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: "wrong password"})
	})
	unknown := timeCall(func() {
		_, _ = h.service.Login(context.Background(), auth.LoginInput{Email: "nobody@example.com", Password: password})
	})

	// A generous bound: the point is that the unknown-email path is not orders of
	// magnitude faster, not that the two are equal to the microsecond.
	if unknown < known/4 {
		t.Errorf("an unknown email answered in %s while a known one took %s; the timing difference is an enumeration oracle",
			unknown, known)
	}
}

// A rejected login must leave no session behind.
func TestFailedLoginIssuesNothing(t *testing.T) {
	h := newHarness(t)
	h.seedUser(t, "raju@example.com", "correct horse battery staple")

	h.mustFailLogin(t, "raju@example.com", "not the password")
	h.mustFailLogin(t, "nobody@example.com", "not the password")

	if live := h.tokens.live(); len(live) != 0 {
		t.Errorf("a failed login left %d live refresh tokens, want 0", len(live))
	}
	if len(h.issuer.issued) != 0 {
		t.Errorf("a failed login issued %d access tokens, want 0", len(h.issuer.issued))
	}
}

// users.status gates authentication, and it is checked only after the password,
// so a stranger cannot learn that an address is suspended.
func TestLoginRejectsAnInactiveAccount(t *testing.T) {
	for _, status := range []user.Status{user.StatusInactive, user.StatusSuspended} {
		t.Run(string(status), func(t *testing.T) {
			h := newHarness(t)
			const password = "correct horse battery staple"
			profile := h.seedUser(t, "raju@example.com", password)

			stored, err := h.users.GetByID(context.Background(), profile.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			stored.Status = status
			h.users.put(*stored)

			_, err = h.service.Login(context.Background(),
				auth.LoginInput{Email: "raju@example.com", Password: password})
			if !errors.Is(err, auth.ErrAccountNotActive) {
				t.Fatalf("Login = %v, want ErrAccountNotActive", err)
			}
			if live := h.tokens.live(); len(live) != 0 {
				t.Errorf("an inactive account got %d refresh tokens, want 0", len(live))
			}

			// With the wrong password the answer is the credentials error, so the
			// status is not disclosed to someone who does not know the password.
			_, err = h.service.Login(context.Background(),
				auth.LoginInput{Email: "raju@example.com", Password: "wrong"})
			if !errors.Is(err, auth.ErrInvalidCredentials) {
				t.Errorf("wrong password on a %s account = %v, want ErrInvalidCredentials", status, err)
			}
		})
	}
}

// A hash that cannot be parsed is an operational fault. Reporting it as bad
// credentials would hide a corrupted column behind a plausible-looking 401.
func TestLoginReportsACorruptStoredHash(t *testing.T) {
	h := newHarness(t)
	profile := h.seedUser(t, "raju@example.com", "correct horse battery staple")

	stored, err := h.users.GetByID(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	stored.PasswordHash = "not-a-hash"
	h.users.put(*stored)

	_, err = h.service.Login(context.Background(),
		auth.LoginInput{Email: "raju@example.com", Password: "correct horse battery staple"})
	if err == nil {
		t.Fatal("Login succeeded against an unparsable hash")
	}
	if errors.Is(err, auth.ErrInvalidCredentials) {
		t.Error("a corrupt hash was reported as bad credentials, hiding the fault")
	}
	if !errors.Is(err, auth.ErrInvalidHash) {
		t.Errorf("error = %v, want it to wrap ErrInvalidHash", err)
	}
}

// Each login is its own session, so two logins must not share a refresh token.
func TestLoginIssuesADistinctTokenEachTime(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	h.seedUser(t, "raju@example.com", password)

	first, err := h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: password})
	if err != nil {
		t.Fatalf("first Login: %v", err)
	}
	second, err := h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: password})
	if err != nil {
		t.Fatalf("second Login: %v", err)
	}

	if first.RefreshToken == second.RefreshToken {
		t.Error("two logins produced the same refresh token")
	}
	if live := h.tokens.live(); len(live) != 2 {
		t.Errorf("%d live refresh tokens after two logins, want 2: logging in on a second device must not end the first session", len(live))
	}
}

// ---------------------------------------------------------------------------
// Refresh
// ---------------------------------------------------------------------------

func TestRefreshRotatesTheToken(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	profile := h.seedUser(t, "raju@example.com", password)

	session, err := h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: password})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	h.now = h.now.Add(20 * time.Minute) // the access token has expired
	refreshed, err := h.service.Refresh(context.Background(), session.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if refreshed.User.ID != profile.ID {
		t.Errorf("refreshed session belongs to %s, want %s", refreshed.User.ID, profile.ID)
	}
	if refreshed.RefreshToken == session.RefreshToken {
		t.Error("Refresh returned the same refresh token: it was not rotated")
	}
	if refreshed.AccessToken == "" {
		t.Error("Refresh issued no access token")
	}
	if want := h.now.Add(15 * time.Minute); !refreshed.AccessTokenExpiresAt.Equal(want) {
		t.Errorf("new access token expires %s, want %s", refreshed.AccessTokenExpiresAt, want)
	}
	// The refresh window restarts, so an active user is not logged out on day 30.
	if want := h.now.Add(testRefreshTTL); !refreshed.RefreshTokenExpiresAt.Equal(want) {
		t.Errorf("new refresh token expires %s, want %s", refreshed.RefreshTokenExpiresAt, want)
	}

	// Exactly one live token: the old one is spent, the new one replaced it.
	live := h.tokens.live()
	if len(live) != 1 {
		t.Fatalf("%d live refresh tokens after a rotation, want 1", len(live))
	}
	if live[0].TokenHash != auth.HashRefreshToken(refreshed.RefreshToken) {
		t.Error("the surviving token is not the newly issued one")
	}
}

// The spent token must not work a second time. This is the whole point of
// rotation: a leaked token is good for one use, not for thirty days.
func TestRefreshRejectsTheSpentToken(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	h.seedUser(t, "raju@example.com", password)

	session, err := h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: password})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := h.service.Refresh(context.Background(), session.RefreshToken); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	_, err = h.service.Refresh(context.Background(), session.RefreshToken)
	if !errors.Is(err, auth.ErrInvalidRefreshToken) {
		t.Errorf("replaying a spent token = %v, want ErrInvalidRefreshToken", err)
	}
}

// A revoked token being presented means it either leaked or the client is out of
// step. Both are answered by ending every session for that user.
func TestRefreshReplayEndsEverySession(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	h.seedUser(t, "raju@example.com", password)

	// Two devices.
	phone, err := h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: password})
	if err != nil {
		t.Fatalf("first Login: %v", err)
	}
	laptop, err := h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: password})
	if err != nil {
		t.Fatalf("second Login: %v", err)
	}

	// The phone refreshes normally, spending its token.
	if _, err := h.service.Refresh(context.Background(), phone.RefreshToken); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	// Someone replays the spent token.
	if _, err := h.service.Refresh(context.Background(), phone.RefreshToken); !errors.Is(err, auth.ErrInvalidRefreshToken) {
		t.Fatalf("replay = %v, want ErrInvalidRefreshToken", err)
	}

	if live := h.tokens.live(); len(live) != 0 {
		t.Errorf("%d live refresh tokens survived a replay, want 0", len(live))
	}
	// Including the laptop's, which is the deliberate cost of reacting to a replay.
	if _, err := h.service.Refresh(context.Background(), laptop.RefreshToken); !errors.Is(err, auth.ErrInvalidRefreshToken) {
		t.Errorf("the other session still works after a replay: %v", err)
	}
}

func TestRefreshRejectsAnExpiredToken(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	h.seedUser(t, "raju@example.com", password)

	session, err := h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: password})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// One second past the 30 day window.
	h.now = h.now.Add(testRefreshTTL + time.Second)

	if _, err := h.service.Refresh(context.Background(), session.RefreshToken); !errors.Is(err, auth.ErrInvalidRefreshToken) {
		t.Errorf("Refresh with an expired token = %v, want ErrInvalidRefreshToken", err)
	}
	// Natural expiry is not a replay, so the other sessions are left alone.
	if h.tokens.rotations != 0 {
		t.Error("an expired token was rotated")
	}
}

// Expiry is exclusive at the boundary: the token is usable up to, but not at, its
// expires_at.
func TestRefreshAtTheExpiryBoundary(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	h.seedUser(t, "raju@example.com", password)

	session, err := h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: password})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	h.now = session.RefreshTokenExpiresAt.Add(-time.Nanosecond)
	if _, err := h.service.Refresh(context.Background(), session.RefreshToken); err != nil {
		t.Errorf("a token one nanosecond before expiry was rejected: %v", err)
	}
}

func TestRefreshRejectsUnusableInput(t *testing.T) {
	h := newHarness(t)

	tests := map[string]struct {
		token string
		want  error
	}{
		"empty":            {token: "", want: auth.ErrMissingRefreshToken},
		"never issued":     {token: "Zm9yZ2VkLXRva2VuLXZhbHVl", want: auth.ErrInvalidRefreshToken},
		"an access token":  {token: "access-token-for-someone", want: auth.ErrInvalidRefreshToken},
		"a hash not token": {token: auth.HashRefreshToken("anything"), want: auth.ErrInvalidRefreshToken},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := h.service.Refresh(context.Background(), tt.token); !errors.Is(err, tt.want) {
				t.Errorf("Refresh(%q) = %v, want %v", tt.token, err, tt.want)
			}
		})
	}
}

// Suspending an account must end its ability to refresh, not just to log in;
// otherwise a suspended user keeps working for up to 30 days.
func TestRefreshRejectsAnInactiveAccount(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	profile := h.seedUser(t, "raju@example.com", password)

	session, err := h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: password})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	stored, err := h.users.GetByID(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	stored.Status = user.StatusSuspended
	h.users.put(*stored)

	if _, err := h.service.Refresh(context.Background(), session.RefreshToken); !errors.Is(err, auth.ErrAccountNotActive) {
		t.Errorf("Refresh on a suspended account = %v, want ErrAccountNotActive", err)
	}
}

// ---------------------------------------------------------------------------
// Logout
// ---------------------------------------------------------------------------

func TestLogoutRevokesTheToken(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	h.seedUser(t, "raju@example.com", password)

	session, err := h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: password})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if err := h.service.Logout(context.Background(), session.RefreshToken); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if live := h.tokens.live(); len(live) != 0 {
		t.Errorf("%d live refresh tokens after logout, want 0", len(live))
	}
	if _, err := h.service.Refresh(context.Background(), session.RefreshToken); !errors.Is(err, auth.ErrInvalidRefreshToken) {
		t.Errorf("a logged-out token still refreshes: %v", err)
	}
}

// Logging out twice, or with a token that never existed, must not fail and must
// not report anything different: a distinct error would confirm that a guessed
// token is real.
func TestLogoutIsIdempotentAndSilent(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	h.seedUser(t, "raju@example.com", password)

	session, err := h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: password})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	for attempt := 1; attempt <= 3; attempt++ {
		if err := h.service.Logout(context.Background(), session.RefreshToken); err != nil {
			t.Errorf("logout attempt %d: %v", attempt, err)
		}
	}
	if err := h.service.Logout(context.Background(), "a-token-that-was-never-issued"); err != nil {
		t.Errorf("logging out an unknown token = %v, want nil", err)
	}
	if err := h.service.Logout(context.Background(), ""); !errors.Is(err, auth.ErrMissingRefreshToken) {
		t.Errorf("Logout(\"\") = %v, want ErrMissingRefreshToken", err)
	}
}

// One device logging out must not end the other's session.
func TestLogoutIsScopedToOneSession(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	h.seedUser(t, "raju@example.com", password)

	phone, err := h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: password})
	if err != nil {
		t.Fatalf("first Login: %v", err)
	}
	laptop, err := h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: password})
	if err != nil {
		t.Fatalf("second Login: %v", err)
	}

	if err := h.service.Logout(context.Background(), phone.RefreshToken); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := h.service.Refresh(context.Background(), laptop.RefreshToken); err != nil {
		t.Errorf("logging out one device ended the other's session: %v", err)
	}
}

func TestLogoutAllEndsEverySession(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	profile := h.seedUser(t, "raju@example.com", password)

	var sessions []*auth.Session
	for i := 0; i < 3; i++ {
		session, err := h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: password})
		if err != nil {
			t.Fatalf("Login %d: %v", i, err)
		}
		sessions = append(sessions, session)
	}

	revoked, err := h.service.LogoutAll(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("LogoutAll: %v", err)
	}
	if revoked != 3 {
		t.Errorf("LogoutAll revoked %d sessions, want 3", revoked)
	}
	for i, session := range sessions {
		if _, err := h.service.Refresh(context.Background(), session.RefreshToken); !errors.Is(err, auth.ErrInvalidRefreshToken) {
			t.Errorf("session %d still refreshes after LogoutAll: %v", i, err)
		}
	}
}

// LogoutAll must only touch the given user's tokens.
func TestLogoutAllIsScopedToOneUser(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"

	alice := h.seedUser(t, "alice@example.com", password)
	h.seedUser(t, "bob@example.com", password)

	aliceSession, err := h.service.Login(context.Background(), auth.LoginInput{Email: "alice@example.com", Password: password})
	if err != nil {
		t.Fatalf("alice Login: %v", err)
	}
	bobSession, err := h.service.Login(context.Background(), auth.LoginInput{Email: "bob@example.com", Password: password})
	if err != nil {
		t.Fatalf("bob Login: %v", err)
	}

	if _, err := h.service.LogoutAll(context.Background(), alice.ID); err != nil {
		t.Fatalf("LogoutAll: %v", err)
	}

	if _, err := h.service.Refresh(context.Background(), aliceSession.RefreshToken); !errors.Is(err, auth.ErrInvalidRefreshToken) {
		t.Errorf("alice's session survived her own LogoutAll: %v", err)
	}
	if _, err := h.service.Refresh(context.Background(), bobSession.RefreshToken); err != nil {
		t.Errorf("alice's LogoutAll ended bob's session: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Session shape
// ---------------------------------------------------------------------------

// Session carries a user.Profile, not a user.User, so a handler that serialises
// the whole struct cannot leak a password hash (ai_instructions.md section 1.8).
func TestSessionCannotCarryAPasswordHash(t *testing.T) {
	h := newHarness(t)
	const password = "correct horse battery staple"
	profile := h.seedUser(t, "raju@example.com", password)

	stored, err := h.users.GetByID(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	session, err := h.service.Login(context.Background(), auth.LoginInput{Email: "raju@example.com", Password: password})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	rendered := fmt.Sprintf("%+v", *session)
	if strings.Contains(rendered, stored.PasswordHash) {
		t.Errorf("a rendered Session contains the password hash: %s", rendered)
	}
	if strings.Contains(rendered, password) {
		t.Error("a rendered Session contains the plaintext password")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (h *harness) mustFailLogin(t *testing.T, email, password string) error {
	t.Helper()

	session, err := h.service.Login(context.Background(), auth.LoginInput{Email: email, Password: password})
	if err == nil {
		t.Fatalf("Login(%q) succeeded, want a failure", email)
	}
	if session != nil {
		t.Errorf("Login(%q) failed but returned a session", email)
	}
	return err
}

func timeCall(fn func()) time.Duration {
	start := time.Now()
	fn()
	return time.Since(start)
}

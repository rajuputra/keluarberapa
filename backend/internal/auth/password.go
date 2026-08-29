package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Password hashing uses Argon2id, the first option offered by architecture.md
// section 2 and the algorithm OWASP recommends for new applications. It is
// memory-hard, so a stolen hash is expensive to attack with GPUs, and unlike
// bcrypt it does not silently ignore input past 72 bytes.
//
// Hashes are stored in the PHC string format:
//
//	$argon2id$v=19$m=65536,t=3,p=2$<base64 salt>$<base64 key>
//
// The parameters travel inside the hash, so they can be raised later without
// invalidating the passwords already in the database: Verify always derives with
// the parameters recorded in the stored hash, never with the current defaults.
const (
	// defaultMemoryKiB is 64 MiB, the OWASP baseline for Argon2id.
	defaultMemoryKiB uint32 = 64 * 1024
	defaultTime      uint32 = 3
	defaultThreads   uint8  = 2
	defaultSaltBytes uint32 = 16
	defaultKeyBytes  uint32 = 32
)

// Password length bounds.
//
// The minimum follows NIST SP 800-63B, which prefers length over composition
// rules, so no upper/lower/digit mix is demanded. The maximum exists because
// Argon2 hashes the whole input: without a cap, a multi-megabyte "password"
// would be a cheap way to make the server do expensive work.
const (
	MinPasswordLength = 8
	MaxPasswordLength = 128
)

// Errors returned when a password fails the policy. They are joined rather than
// returned one at a time so a caller can report every problem in one response.
var (
	ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	ErrPasswordTooLong  = fmt.Errorf("password must be at most %d characters", MaxPasswordLength)
	ErrPasswordBlank    = errors.New("password must not be only whitespace")
)

// Errors returned when a stored hash cannot be used.
var (
	// ErrInvalidHash means the stored string is not a hash this code wrote.
	ErrInvalidHash = errors.New("password hash is malformed")
	// ErrUnsupportedHash means the algorithm, version or parameters are outside
	// what this build accepts.
	ErrUnsupportedHash = errors.New("password hash uses unsupported parameters")
)

// Bounds on the parameters accepted from a stored hash. A tampered or corrupted
// row must not be able to talk the server into a multi-gigabyte allocation.
const (
	maxMemoryKiB uint32 = 1024 * 1024 // 1 GiB
	maxTime      uint32 = 32
	maxThreads   uint8  = 16
	maxSaltBytes        = 64
	maxKeyBytes         = 64
	minSaltBytes        = 8
	minKeyBytes         = 16
)

// Hasher derives and verifies Argon2id password hashes.
//
// The zero value is not usable; construct one with DefaultHasher. The fields are
// exported so a deployment on constrained hardware, or a test that cannot afford
// 64 MiB per call, can lower the cost deliberately.
type Hasher struct {
	// MemoryKiB is the memory cost in kibibytes.
	MemoryKiB uint32
	// Time is the number of passes over memory.
	Time uint32
	// Threads is the degree of parallelism.
	Threads uint8
	// SaltBytes is the length of the random salt generated per password.
	SaltBytes uint32
	// KeyBytes is the length of the derived key.
	KeyBytes uint32
}

// DefaultHasher returns the parameters used in production.
func DefaultHasher() Hasher {
	return Hasher{
		MemoryKiB: defaultMemoryKiB,
		Time:      defaultTime,
		Threads:   defaultThreads,
		SaltBytes: defaultSaltBytes,
		KeyBytes:  defaultKeyBytes,
	}
}

// Validate reports whether the parameters are inside the accepted bounds.
//
// Argon2 itself panics on a zero time cost and silently raises too-small memory
// values, so the check happens here where the caller still gets an error.
func (h Hasher) Validate() error {
	switch {
	case h.Time < 1 || h.Time > maxTime:
		return fmt.Errorf("%w: time cost %d is outside 1..%d", ErrUnsupportedHash, h.Time, maxTime)
	case h.Threads < 1 || h.Threads > maxThreads:
		return fmt.Errorf("%w: parallelism %d is outside 1..%d", ErrUnsupportedHash, h.Threads, maxThreads)
	case h.MemoryKiB < 8*uint32(h.Threads) || h.MemoryKiB > maxMemoryKiB:
		return fmt.Errorf("%w: memory %d KiB is outside %d..%d",
			ErrUnsupportedHash, h.MemoryKiB, 8*uint32(h.Threads), maxMemoryKiB)
	case h.SaltBytes < minSaltBytes || h.SaltBytes > maxSaltBytes:
		return fmt.Errorf("%w: salt length %d is outside %d..%d",
			ErrUnsupportedHash, h.SaltBytes, minSaltBytes, maxSaltBytes)
	case h.KeyBytes < minKeyBytes || h.KeyBytes > maxKeyBytes:
		return fmt.Errorf("%w: key length %d is outside %d..%d",
			ErrUnsupportedHash, h.KeyBytes, minKeyBytes, maxKeyBytes)
	}
	return nil
}

// Hash derives a new hash for password, with a fresh random salt.
//
// The returned string is what goes into users.password_hash. The plaintext is
// never stored and never logged (ai_instructions.md section 1.8).
func (h Hasher) Hash(password string) (string, error) {
	if err := h.Validate(); err != nil {
		return "", err
	}
	// Independent of ValidatePassword: Hash is public, so it enforces the cost
	// ceiling itself rather than trusting every caller to have validated.
	if len(password) > MaxPasswordLength*4 {
		return "", ErrPasswordTooLong
	}

	salt := make([]byte, h.SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, h.Time, h.MemoryKiB, h.Threads, h.KeyBytes)

	return encodeHash(h, salt, key), nil
}

// Verify reports whether password matches encoded.
//
// Cost parameters come from encoded, not from h, so hashes written with older
// parameters keep working after the defaults are raised. A false return means
// the password is wrong; an error means the stored hash is unusable, which is an
// operational problem rather than a failed login.
func (h Hasher) Verify(encoded, password string) (bool, error) {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	if len(password) > MaxPasswordLength*4 {
		// Refuse before deriving: see the note on MaxPasswordLength.
		return false, nil
	}

	got := argon2.IDKey([]byte(password), salt, params.Time, params.MemoryKiB, params.Threads, uint32(len(want)))

	// Constant time: a length-independent early exit would leak how much of the
	// key matched.
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// ValidatePassword applies the password policy. Every failure is returned at
// once via errors.Join, and each one is matchable with errors.Is.
func ValidatePassword(password string) error {
	var problems []error

	if strings.TrimSpace(password) == "" {
		problems = append(problems, ErrPasswordBlank)
	}
	// Counted in runes: a user typing eight accented characters has typed eight
	// characters, whatever that costs in bytes.
	switch length := len([]rune(password)); {
	case length < MinPasswordLength:
		problems = append(problems, ErrPasswordTooShort)
	case length > MaxPasswordLength:
		problems = append(problems, ErrPasswordTooLong)
	}

	return errors.Join(problems...)
}

// b64 is the unpadded base64 alphabet used by the PHC string format.
var b64 = base64.RawStdEncoding

func encodeHash(h Hasher, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.MemoryKiB, h.Time, h.Threads,
		b64.EncodeToString(salt), b64.EncodeToString(key))
}

// decodeHash parses a PHC-format Argon2id hash into its parameters, salt and key.
func decodeHash(encoded string) (Hasher, []byte, []byte, error) {
	// "" / "$argon2id" / "v=19" / "m=..,t=..,p=.." / salt / key
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" {
		return Hasher{}, nil, nil, fmt.Errorf("%w: expected 5 fields", ErrInvalidHash)
	}
	if parts[1] != "argon2id" {
		return Hasher{}, nil, nil, fmt.Errorf("%w: algorithm %q is not argon2id", ErrUnsupportedHash, parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return Hasher{}, nil, nil, fmt.Errorf("%w: unreadable version", ErrInvalidHash)
	}
	if version != argon2.Version {
		return Hasher{}, nil, nil, fmt.Errorf("%w: argon2 version %d, this build supports %d",
			ErrUnsupportedHash, version, argon2.Version)
	}

	var params Hasher
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.MemoryKiB, &params.Time, &params.Threads); err != nil {
		return Hasher{}, nil, nil, fmt.Errorf("%w: unreadable cost parameters", ErrInvalidHash)
	}

	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return Hasher{}, nil, nil, fmt.Errorf("%w: unreadable salt", ErrInvalidHash)
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil {
		return Hasher{}, nil, nil, fmt.Errorf("%w: unreadable key", ErrInvalidHash)
	}

	params.SaltBytes = uint32(len(salt))
	params.KeyBytes = uint32(len(key))
	if err := params.Validate(); err != nil {
		return Hasher{}, nil, nil, err
	}

	return params, salt, key, nil
}

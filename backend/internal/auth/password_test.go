package auth

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

// testHasher keeps the suite fast. Argon2id at production settings costs 64 MiB
// and a few milliseconds per call, which is the point of it; a test only needs
// the encoding and comparison logic to be exercised.
func testHasher() Hasher {
	h := DefaultHasher()
	h.MemoryKiB = 8 * 1024 // 8 MiB
	h.Time = 1
	return h
}

func TestDefaultHasherParametersAreAccepted(t *testing.T) {
	h := DefaultHasher()
	if err := h.Validate(); err != nil {
		t.Fatalf("DefaultHasher().Validate() = %v, want nil", err)
	}
	// The OWASP baseline for Argon2id. A drop below it should be a deliberate,
	// visible change rather than a silent edit.
	if h.MemoryKiB < 64*1024 {
		t.Errorf("default memory = %d KiB, want at least 65536", h.MemoryKiB)
	}
	if h.Time < 2 {
		t.Errorf("default time cost = %d, want at least 2", h.Time)
	}
	if h.SaltBytes < 16 || h.KeyBytes < 32 {
		t.Errorf("default salt/key = %d/%d bytes, want at least 16/32", h.SaltBytes, h.KeyBytes)
	}
}

func TestHashAndVerifyRoundTrip(t *testing.T) {
	h := testHasher()
	const password = "correct horse battery staple"

	encoded, err := h.Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	match, err := h.Verify(encoded, password)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !match {
		t.Error("the correct password did not verify")
	}
}

func TestVerifyRejectsWrongPasswords(t *testing.T) {
	h := testHasher()
	const password = "correct horse battery staple"

	encoded, err := h.Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	wrong := []string{
		"",
		"Correct horse battery staple", // case matters
		"correct horse battery stapl",  // one character short
		"correct horse battery staple ",
		" correct horse battery staple",
		"correct horse battery staple\x00", // trailing NUL is not a truncator
	}
	for _, candidate := range wrong {
		match, err := h.Verify(encoded, candidate)
		if err != nil {
			t.Fatalf("Verify(%q): %v", candidate, err)
		}
		if match {
			t.Errorf("%q verified against a hash of a different password", candidate)
		}
	}
}

// Unlike bcrypt, Argon2 hashes the whole input rather than the first 72 bytes, so
// two long passwords that share a prefix must not be interchangeable.
func TestVerifyUsesTheWholePassword(t *testing.T) {
	h := testHasher()
	prefix := strings.Repeat("a", 72)

	encoded, err := h.Hash(prefix + "1")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	match, err := h.Verify(encoded, prefix+"2")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if match {
		t.Error("two passwords differing only after 72 bytes were treated as equal")
	}
}

// A per-password salt is what stops one precomputed table from covering every
// account, so two users with the same password must not share a hash.
func TestHashIsSaltedPerCall(t *testing.T) {
	h := testHasher()
	const password = "shared-password"

	first, err := h.Hash(password)
	if err != nil {
		t.Fatalf("first Hash: %v", err)
	}
	second, err := h.Hash(password)
	if err != nil {
		t.Fatalf("second Hash: %v", err)
	}
	if first == second {
		t.Fatal("hashing the same password twice produced identical hashes: the salt is not random")
	}

	// Both must still verify.
	for i, encoded := range []string{first, second} {
		match, err := h.Verify(encoded, password)
		if err != nil {
			t.Fatalf("Verify hash %d: %v", i, err)
		}
		if !match {
			t.Errorf("hash %d did not verify", i)
		}
	}
}

// The stored string must never contain the password, and must be recognisable as
// an Argon2id PHC hash so an operator can tell what produced it.
func TestHashEncoding(t *testing.T) {
	h := testHasher()
	const password = "recognisable-plaintext"

	encoded, err := h.Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	if strings.Contains(encoded, password) {
		t.Fatal("the encoded hash contains the plaintext password")
	}

	wantPrefix := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$",
		argon2.Version, h.MemoryKiB, h.Time, h.Threads)
	if !strings.HasPrefix(encoded, wantPrefix) {
		t.Errorf("encoded hash = %q, want prefix %q", encoded, wantPrefix)
	}
	if fields := strings.Split(encoded, "$"); len(fields) != 6 {
		t.Errorf("encoded hash has %d $-separated fields, want 6", len(fields))
	}
}

// Parameters live in the hash so they can be raised later. A hash written with
// the old settings must keep verifying under a hasher configured differently.
func TestVerifyUsesTheParametersInTheStoredHash(t *testing.T) {
	const password = "unchanged-password"

	weak := testHasher()
	encoded, err := weak.Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	stronger := weak
	stronger.MemoryKiB *= 2
	stronger.Time += 2

	match, err := stronger.Verify(encoded, password)
	if err != nil {
		t.Fatalf("Verify with raised parameters: %v", err)
	}
	if !match {
		t.Error("raising the hasher's cost invalidated an existing hash")
	}
}

// A single flipped character must not verify, whichever field it lands in.
func TestVerifyRejectsATamperedHash(t *testing.T) {
	h := testHasher()
	const password = "tamper-target"

	encoded, err := h.Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	fields := strings.Split(encoded, "$")

	for _, index := range []int{4, 5} { // salt, key
		tampered := append([]string(nil), fields...)
		tampered[index] = flipFirstBase64Char(tampered[index])

		match, err := h.Verify(strings.Join(tampered, "$"), password)
		if err != nil {
			t.Fatalf("Verify with field %d tampered: %v", index, err)
		}
		if match {
			t.Errorf("a hash with field %d altered still verified", index)
		}
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	h := testHasher()

	valid, err := h.Hash("some-password")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	fields := strings.Split(valid, "$")

	tests := map[string]struct {
		encoded string
		want    error
	}{
		"empty":                {encoded: "", want: ErrInvalidHash},
		"plaintext":            {encoded: "some-password", want: ErrInvalidHash},
		"bcrypt hash":          {encoded: "$2y$10$abcdefghijklmnopqrstuv", want: ErrInvalidHash},
		"too few fields":       {encoded: "$argon2id$v=19$m=8192,t=1,p=2$c2FsdA", want: ErrInvalidHash},
		"unreadable version":   {encoded: "$argon2id$v=x$m=8192,t=1,p=2$c2FsdA$a2V5", want: ErrInvalidHash},
		"unreadable cost":      {encoded: "$argon2id$v=19$m=lots$c2FsdA$a2V5", want: ErrInvalidHash},
		"salt not base64":      {encoded: "$argon2id$v=19$m=8192,t=1,p=2$!!!!$" + fields[5], want: ErrInvalidHash},
		"wrong algorithm":      {encoded: "$argon2i$v=19$m=8192,t=1,p=2$" + fields[4] + "$" + fields[5], want: ErrUnsupportedHash},
		"wrong argon2 version": {encoded: "$argon2id$v=16$m=8192,t=1,p=2$" + fields[4] + "$" + fields[5], want: ErrUnsupportedHash},
		// A tampered row must not be able to demand a huge allocation.
		"absurd memory": {encoded: "$argon2id$v=19$m=4294967295,t=1,p=2$" + fields[4] + "$" + fields[5], want: ErrUnsupportedHash},
		"zero time":     {encoded: "$argon2id$v=19$m=8192,t=0,p=2$" + fields[4] + "$" + fields[5], want: ErrUnsupportedHash},
		"short salt":    {encoded: "$argon2id$v=19$m=8192,t=1,p=2$c2FsdA$" + fields[5], want: ErrUnsupportedHash},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			match, err := h.Verify(tt.encoded, "some-password")
			if match {
				t.Error("a malformed hash must never report a match")
			}
			if !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestHashRejectsInvalidParameters(t *testing.T) {
	tests := map[string]func(h *Hasher){
		"zero time":     func(h *Hasher) { h.Time = 0 },
		"zero threads":  func(h *Hasher) { h.Threads = 0 },
		"tiny memory":   func(h *Hasher) { h.MemoryKiB = 1 },
		"huge memory":   func(h *Hasher) { h.MemoryKiB = maxMemoryKiB + 1 },
		"short salt":    func(h *Hasher) { h.SaltBytes = 4 },
		"short key":     func(h *Hasher) { h.KeyBytes = 8 },
		"zero value":    func(h *Hasher) { *h = Hasher{} },
		"too many runs": func(h *Hasher) { h.Time = maxTime + 1 },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			h := testHasher()
			mutate(&h)

			if err := h.Validate(); err == nil {
				t.Error("Validate accepted parameters outside the allowed range")
			}
			if _, err := h.Hash("some-password"); err == nil {
				t.Error("Hash accepted parameters outside the allowed range")
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	tests := map[string]struct {
		password string
		want     error
	}{
		"typical":               {password: "correct horse", want: nil},
		"exactly the minimum":   {password: strings.Repeat("a", MinPasswordLength), want: nil},
		"exactly the maximum":   {password: strings.Repeat("a", MaxPasswordLength), want: nil},
		"non-ascii counts once": {password: strings.Repeat("é", MinPasswordLength), want: nil},
		"empty":                 {password: "", want: ErrPasswordTooShort},
		"one short":             {password: strings.Repeat("a", MinPasswordLength-1), want: ErrPasswordTooShort},
		"one long":              {password: strings.Repeat("a", MaxPasswordLength+1), want: ErrPasswordTooLong},
		"only whitespace":       {password: strings.Repeat(" ", MinPasswordLength), want: ErrPasswordBlank},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := ValidatePassword(tt.password)
			if tt.want == nil {
				if err != nil {
					t.Errorf("ValidatePassword = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Errorf("ValidatePassword = %v, want %v", err, tt.want)
			}
		})
	}
}

// The policy allows no composition rules, so a long passphrase is accepted while
// a short complex string is not. Asserted explicitly because the opposite is a
// common and worse default.
func TestPasswordPolicyPrefersLengthOverComposition(t *testing.T) {
	if err := ValidatePassword("all lower case words no digits"); err != nil {
		t.Errorf("a long passphrase was rejected: %v", err)
	}
	if err := ValidatePassword("A1!x"); err == nil {
		t.Error("a four-character password was accepted")
	}
}

// Hashing is the expensive step, so an oversized input must be rejected before
// it happens, whatever the caller did about validation.
func TestHashRefusesAnOversizedPassword(t *testing.T) {
	h := testHasher()

	if _, err := h.Hash(strings.Repeat("a", MaxPasswordLength*4+1)); !errors.Is(err, ErrPasswordTooLong) {
		t.Errorf("Hash of an oversized password = %v, want ErrPasswordTooLong", err)
	}
}

func TestVerifyRefusesAnOversizedPassword(t *testing.T) {
	h := testHasher()

	encoded, err := h.Hash("a-normal-password")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	match, err := h.Verify(encoded, strings.Repeat("a", MaxPasswordLength*4+1))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if match {
		t.Error("an oversized password reported a match")
	}
}

// Hasher must satisfy the interface the service depends on.
var _ PasswordHasher = Hasher{}

// flipFirstBase64Char changes one character to a different one from the same
// alphabet, so the field still decodes and only its value differs.
func flipFirstBase64Char(field string) string {
	if field == "" {
		return "A"
	}
	replacement := byte('A')
	if field[0] == 'A' {
		replacement = 'B'
	}
	return string(replacement) + field[1:]
}

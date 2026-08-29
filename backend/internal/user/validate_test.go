package user

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateName(t *testing.T) {
	accepted := map[string]string{
		"Raju Putra":      "Raju Putra",
		"  Raju Putra  ":  "Raju Putra",
		"Ana":             "Ana",
		"A":               "A",
		"Siti Nurhaliza ": "Siti Nurhaliza",
		// Non-ASCII names are counted in characters, not bytes.
		"Ngô Bảo Châu": "Ngô Bảo Châu",
	}
	for in, want := range accepted {
		got, err := ValidateName(in)
		if err != nil {
			t.Errorf("ValidateName(%q) returned %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ValidateName(%q) = %q, want %q", in, got, want)
		}
	}

	rejected := map[string]struct {
		in   string
		want error
	}{
		"empty":      {in: "", want: ErrNameRequired},
		"whitespace": {in: " \t\n ", want: ErrNameRequired},
		"too long":   {in: strings.Repeat("a", MaxNameLength+1), want: ErrNameTooLong},
	}
	for name, tt := range rejected {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateName(tt.in); !errors.Is(err, tt.want) {
				t.Errorf("ValidateName = %v, want %v", err, tt.want)
			}
		})
	}

	// The limit counts characters, so a name of accented letters is not penalised
	// for the bytes they cost.
	if _, err := ValidateName(strings.Repeat("é", MaxNameLength)); err != nil {
		t.Errorf("a %d-character non-ASCII name was rejected: %v", MaxNameLength, err)
	}
}

// The returned address must equal what the users_email_unique index normalises
// to, or a duplicate account could slip through with different casing.
func TestValidateEmailNormalises(t *testing.T) {
	tests := map[string]string{
		"alice@example.com":         "alice@example.com",
		"ALICE@EXAMPLE.COM":         "alice@example.com",
		"  Alice@Example.Com  ":     "alice@example.com",
		"raju.putra+tag@gmail.com":  "raju.putra+tag@gmail.com",
		"user_name@sub.example.co":  "user_name@sub.example.co",
		"a@b.co":                    "a@b.co",
		"budi@keluarberapa.co.id\n": "budi@keluarberapa.co.id",
	}
	for in, want := range tests {
		got, err := ValidateEmail(in)
		if err != nil {
			t.Errorf("ValidateEmail(%q) returned %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ValidateEmail(%q) = %q, want %q", in, got, want)
		}
		if got != NormalizeEmail(got) {
			t.Errorf("ValidateEmail(%q) = %q, which is not its own normal form", in, got)
		}
	}
}

func TestValidateEmailRejectsInvalidAddresses(t *testing.T) {
	rejected := map[string]struct {
		in   string
		want error
	}{
		"empty":              {in: "", want: ErrEmailRequired},
		"whitespace":         {in: "   ", want: ErrEmailRequired},
		"no at sign":         {in: "alice.example.com", want: ErrEmailInvalid},
		"no local part":      {in: "@example.com", want: ErrEmailInvalid},
		"no domain":          {in: "alice@", want: ErrEmailInvalid},
		"bare hostname":      {in: "alice@localhost", want: ErrEmailInvalid},
		"trailing dot":       {in: "alice@example.", want: ErrEmailInvalid},
		"leading dot":        {in: "alice@.com", want: ErrEmailInvalid},
		"two at signs":       {in: "alice@@example.com", want: ErrEmailInvalid},
		"internal space":     {in: "alice smith@example.com", want: ErrEmailInvalid},
		"display name form":  {in: "alice <alice@example.com>", want: ErrEmailInvalid},
		"angle bracket form": {in: "<alice@example.com>", want: ErrEmailInvalid},
		"comma separated":    {in: "alice@example.com, bob@example.com", want: ErrEmailInvalid},
		"newline injection":  {in: "alice@example.com\nbcc: bob@example.com", want: ErrEmailInvalid},
		"too long":           {in: strings.Repeat("a", MaxEmailLength) + "@example.com", want: ErrEmailTooLong},
	}
	for name, tt := range rejected {
		t.Run(name, func(t *testing.T) {
			got, err := ValidateEmail(tt.in)
			if !errors.Is(err, tt.want) {
				t.Errorf("ValidateEmail(%q) = %v, want %v", tt.in, err, tt.want)
			}
			if got != "" {
				t.Errorf("a rejected address returned %q, want an empty string", got)
			}
		})
	}
}

func TestValidateTimezone(t *testing.T) {
	// Empty means "I do not care", which resolves to the project default rather
	// than to an error.
	got, err := ValidateTimezone("")
	if err != nil {
		t.Fatalf("ValidateTimezone(\"\") returned %v", err)
	}
	if got != DefaultTimezone {
		t.Errorf("ValidateTimezone(\"\") = %q, want %q", got, DefaultTimezone)
	}

	for _, in := range []string{"Asia/Jakarta", "Asia/Makassar", "UTC", "Europe/Amsterdam", "  Asia/Jakarta  "} {
		got, err := ValidateTimezone(in)
		if err != nil {
			t.Errorf("ValidateTimezone(%q) returned %v", in, err)
			continue
		}
		if _, err := time.LoadLocation(got); err != nil {
			t.Errorf("ValidateTimezone(%q) returned %q, which does not load: %v", in, got, err)
		}
	}

	for _, in := range []string{"Mars/Olympus", "GMT+7", "asia/jakarta", "Asia/Jakarta; DROP TABLE users"} {
		if _, err := ValidateTimezone(in); !errors.Is(err, ErrTimezoneInvalid) {
			t.Errorf("ValidateTimezone(%q) = %v, want ErrTimezoneInvalid", in, err)
		}
	}
}

// The Go default, the config default and the schema default all have to agree, or
// a user's monthly summary would be bucketed in a timezone nobody chose.
func TestDefaultTimezoneMatchesTheSpecification(t *testing.T) {
	if DefaultTimezone != "Asia/Jakarta" {
		t.Errorf("DefaultTimezone = %q, want Asia/Jakarta (architecture.md section 1)", DefaultTimezone)
	}
	if _, err := time.LoadLocation(DefaultTimezone); err != nil {
		t.Errorf("DefaultTimezone does not load: %v", err)
	}
}

// Profile is the type that makes a leaked password hash structurally impossible,
// so it must carry every field the API needs and none it must not.
func TestProfileOfDropsThePasswordHash(t *testing.T) {
	now := time.Now()
	u := User{
		ID:           uuid.New(),
		Name:         "Raju Putra",
		Email:        "raju@example.com",
		PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHQ$a2V5",
		Status:       StatusActive,
		Timezone:     "Asia/Jakarta",
		CreatedAt:    now.Add(-time.Hour),
		UpdatedAt:    now,
	}

	got := ProfileOf(u)

	if got.ID != u.ID || got.Name != u.Name || got.Email != u.Email {
		t.Errorf("ProfileOf lost identity fields: %+v", got)
	}
	if got.Status != u.Status || got.Timezone != u.Timezone {
		t.Errorf("ProfileOf lost status or timezone: %+v", got)
	}
	if !got.CreatedAt.Equal(u.CreatedAt) || !got.UpdatedAt.Equal(u.UpdatedAt) {
		t.Errorf("ProfileOf lost timestamps: %+v", got)
	}

	// The hash cannot appear in any rendering of a Profile, because the type has
	// nowhere to put it (ai_instructions.md section 1.8). %+v reflects over every
	// field, so this stays true if a field is added later.
	if rendered := fmt.Sprintf("%+v", got); strings.Contains(rendered, u.PasswordHash) {
		t.Errorf("a rendered Profile contains the password hash: %s", rendered)
	}
	if rendered, err := json.Marshal(got); err != nil {
		t.Errorf("marshal Profile: %v", err)
	} else if strings.Contains(string(rendered), u.PasswordHash) {
		t.Errorf("a marshalled Profile contains the password hash: %s", rendered)
	}
}

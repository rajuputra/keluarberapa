package whatsapp

import (
	"errors"
	"strings"
	"testing"
)

// Normalisation is what makes inbound identity resolution possible: the webhook
// only ever presents the canonical wa_id form, so anything stored in a different
// shape can never be matched (ai_instructions.md section 3).
func TestNormalizePhoneNumberAcceptedForms(t *testing.T) {
	// Every input below is the same Indonesian number written differently.
	const want = "6281234567890"

	inputs := []string{
		"6281234567890",
		"+6281234567890",
		"+62 812 3456 7890",
		"+62-812-3456-7890",
		"+62 (812) 3456-7890",
		"  +62 812 3456 7890  ",
		"006281234567890", // international access code
		"00 62 812 3456 7890",
		"081234567890", // Indonesian national format
		"0812-3456-7890",
		"0812 3456 7890",
		"62.812.3456.7890",
	}

	for _, in := range inputs {
		got, err := NormalizePhoneNumber(in)
		if err != nil {
			t.Errorf("NormalizePhoneNumber(%q) returned %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizePhoneNumber(%q) = %q, want %q", in, got, want)
		}
	}
}

// A number with another country code must survive untouched, so the Indonesian
// default cannot corrupt it.
func TestNormalizePhoneNumberKeepsOtherCountryCodes(t *testing.T) {
	tests := map[string]string{
		"+1 415 555 0123":  "14155550123",
		"+44 20 7946 0958": "442079460958",
		"+65 6123 4567":    "6561234567",
		"001 415 555 0123": "14155550123",
	}
	for in, want := range tests {
		got, err := NormalizePhoneNumber(in)
		if err != nil {
			t.Errorf("NormalizePhoneNumber(%q) returned %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizePhoneNumber(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizePhoneNumberIsIdempotent(t *testing.T) {
	// The stored form goes back through normalisation on every write; running it
	// twice must not change the answer.
	first, err := NormalizePhoneNumber("+62 812-3456-7890")
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	second, err := NormalizePhoneNumber(first)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if first != second {
		t.Errorf("normalising twice changed %q into %q", first, second)
	}
}

func TestNormalizePhoneNumberRejectsInvalidInput(t *testing.T) {
	invalid := map[string]string{
		"empty":                  "",
		"blank":                  "   ",
		"letters":                "62812ABCD890",
		"a word":                 "not a number",
		"too short":              "628123",
		"too long":               strings.Repeat("6", 16),
		"leading zero after cc":  "0",
		"only a plus":            "+",
		"plus in the middle":     "62812+3456",
		"internal country code0": "+0812345678",
		"slash separator":        "62/812/3456/7890",
		"asterisk":               "*6281234567890",
		"unicode digit":          "6281234567890٣",
	}

	for name, in := range invalid {
		t.Run(name, func(t *testing.T) {
			got, err := NormalizePhoneNumber(in)
			if err == nil {
				t.Fatalf("NormalizePhoneNumber(%q) = %q, want an error", in, got)
			}
			if !errors.Is(err, ErrInvalidPhoneNumber) {
				t.Errorf("error = %v, want ErrInvalidPhoneNumber", err)
			}
			if got != "" {
				t.Errorf("a rejected number returned %q, want an empty string", got)
			}
		})
	}
}

// Whatever this function accepts, the database must accept too: the pattern here
// and the whatsapp_accounts_phone_format CHECK are the same expression, and a
// disagreement would turn a validation problem into a 500.
func TestNormalizedNumberMatchesTheSchemaConstraint(t *testing.T) {
	const constraint = `^[1-9][0-9]{6,14}$`
	if phoneNumberPattern.String() != constraint {
		t.Errorf("phoneNumberPattern = %q, want the whatsapp_accounts_phone_format expression %q",
			phoneNumberPattern.String(), constraint)
	}

	// Boundaries of the accepted length: 7 digits minimum, 15 maximum.
	for _, in := range []string{"6212345", strings.Repeat("6", 15)} {
		if _, err := NormalizePhoneNumber(in); err != nil {
			t.Errorf("NormalizePhoneNumber(%q) rejected a length the schema allows: %v", in, err)
		}
	}
	for _, in := range []string{"621234", strings.Repeat("6", 16)} {
		if _, err := NormalizePhoneNumber(in); err == nil {
			t.Errorf("NormalizePhoneNumber(%q) accepted a length the schema rejects", in)
		}
	}
}

func TestDefaultCountryCodeIsIndonesia(t *testing.T) {
	if DefaultCountryCode != "62" {
		t.Errorf("DefaultCountryCode = %q, want 62 (Indonesia)", DefaultCountryCode)
	}
}

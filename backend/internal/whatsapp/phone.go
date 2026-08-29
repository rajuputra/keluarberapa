package whatsapp

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// DefaultCountryCode is prepended when a number is given in Indonesian national
// format ("0812…" becomes "62812…").
//
// KeluarBerapa is an Indonesian product throughout — IDR-only amounts, an
// Asia/Jakarta default timezone and Indonesian category names — so a leading
// zero is read as the national trunk prefix rather than as a digit. A number for
// another country must be given with its country code, in either "+<cc>…" or
// "00<cc>…" form.
const DefaultCountryCode = "62"

// phoneNumberPattern mirrors the whatsapp_accounts_phone_format CHECK
// constraint. Keeping the two identical means a value this function accepts is a
// value the database accepts.
var phoneNumberPattern = regexp.MustCompile(`^[1-9][0-9]{6,14}$`)

// ErrInvalidPhoneNumber means the input is not a WhatsApp-addressable number.
var ErrInvalidPhoneNumber = errors.New("phone number is not valid")

// separators are the punctuation people put in written phone numbers. They carry
// no information, so they are removed. Anything else that is not a digit is
// rejected instead, so a typo is reported rather than silently swallowed.
const separators = " \t-.() –—"

// NormalizePhoneNumber converts a number as typed into the canonical stored
// form: E.164 digits with no leading "+", which is the wa_id format Meta sends
// on an inbound message (user_stories.md Epic 1: "Number is normalized").
//
// Normalising on the way in is what makes identity resolution work at all: the
// webhook only ever presents the canonical form, so a number stored as
// "+62 812-3456-7890" would never match the "6281234567890" that arrives.
func NormalizePhoneNumber(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%w: it is empty", ErrInvalidPhoneNumber)
	}

	// A "+" is only meaningful as the very first character.
	international := strings.HasPrefix(trimmed, "+")
	if international {
		trimmed = trimmed[1:]
	}

	var digits strings.Builder
	digits.Grow(len(trimmed))
	for _, r := range trimmed {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case strings.ContainsRune(separators, r):
			// Formatting only.
		default:
			return "", fmt.Errorf("%w: %q is not a digit", ErrInvalidPhoneNumber, r)
		}
	}

	number := digits.String()
	switch {
	case international:
		// Already country-coded.
	case strings.HasPrefix(number, "00"):
		// International access code, the dialled equivalent of "+".
		number = strings.TrimPrefix(number, "00")
	case strings.HasPrefix(number, "0"):
		number = DefaultCountryCode + strings.TrimPrefix(number, "0")
	}

	if !phoneNumberPattern.MatchString(number) {
		return "", fmt.Errorf("%w: %d digits is not an E.164 number", ErrInvalidPhoneNumber, len(number))
	}
	return number, nil
}

package user

import "testing"

func TestStatusValid(t *testing.T) {
	for _, status := range Statuses() {
		if !status.Valid() {
			t.Errorf("%q should be valid", status)
		}
	}
	for _, status := range []Status{"", "zombie", "Active", "ACTIVE"} {
		if status.Valid() {
			t.Errorf("%q should not be valid", status)
		}
	}
}

func TestIsActive(t *testing.T) {
	if !(User{Status: StatusActive}).IsActive() {
		t.Error("an active user should report IsActive")
	}
	for _, status := range []Status{StatusInactive, StatusSuspended, ""} {
		if (User{Status: status}).IsActive() {
			t.Errorf("status %q should not report IsActive", status)
		}
	}
}

// The users_email_unique index is on lower(email), so normalisation has to match
// it exactly or a duplicate account could slip through with different casing.
func TestNormalizeEmail(t *testing.T) {
	tests := map[string]string{
		"alice@example.com":     "alice@example.com",
		"ALICE@EXAMPLE.COM":     "alice@example.com",
		"  Alice@Example.Com  ": "alice@example.com",
		"\tbob@example.com\n":   "bob@example.com",
		"":                      "",
	}
	for in, want := range tests {
		if got := NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

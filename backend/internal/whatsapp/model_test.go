package whatsapp

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestProviderValid(t *testing.T) {
	if !ProviderMetaCloudAPI.Valid() {
		t.Error("meta_cloud_api should be valid")
	}
	for _, p := range []Provider{"", "twilio", "Meta_Cloud_API"} {
		if p.Valid() {
			t.Errorf("%q should not be valid", p)
		}
	}
}

func TestVerificationStatusValid(t *testing.T) {
	for _, s := range VerificationStatuses() {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []VerificationStatus{"", "Verified", "unknown"} {
		if s.Valid() {
			t.Errorf("%q should not be valid", s)
		}
	}
}

func TestMessageStatusValid(t *testing.T) {
	for _, s := range MessageStatuses() {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []MessageStatus{"", "Processed", "pending"} {
		if s.Valid() {
			t.Errorf("%q should not be valid", s)
		}
	}
}

func TestAccountIsVerified(t *testing.T) {
	now := time.Now()

	verified := Account{VerificationStatus: VerificationVerified, VerifiedAt: &now}
	if !verified.IsVerified() {
		t.Error("a verified account should report IsVerified")
	}
	for _, s := range []VerificationStatus{VerificationPending, VerificationFailed, ""} {
		if (Account{VerificationStatus: s}).IsVerified() {
			t.Errorf("status %q should not report IsVerified", s)
		}
	}
}

// Message history must stay scoped to its owner, and a message from an
// unregistered number belongs to nobody.
func TestMessageBelongsTo(t *testing.T) {
	alice := uuid.New()
	bob := uuid.New()

	owned := Message{UserID: &alice}
	if !owned.BelongsTo(alice) {
		t.Error("alice's message should belong to alice")
	}
	if owned.BelongsTo(bob) {
		t.Error("alice's message must not belong to bob")
	}

	unregistered := Message{}
	if unregistered.BelongsTo(alice) || unregistered.BelongsTo(uuid.Nil) {
		t.Error("a message with no user must not belong to anyone")
	}
}

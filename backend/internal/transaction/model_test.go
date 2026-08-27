package transaction

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSourceValid(t *testing.T) {
	for _, s := range Sources() {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []Source{"", "telegram", "WhatsApp", "WEB"} {
		if s.Valid() {
			t.Errorf("%q should not be valid", s)
		}
	}
}

// A transaction must never read as belonging to a user other than its owner.
func TestBelongsTo(t *testing.T) {
	alice := uuid.New()
	bob := uuid.New()

	tx := Transaction{UserID: alice}
	if !tx.BelongsTo(alice) {
		t.Error("alice's transaction should belong to alice")
	}
	if tx.BelongsTo(bob) {
		t.Error("alice's transaction must not belong to bob")
	}
	if tx.BelongsTo(uuid.Nil) {
		t.Error("a zero user id must not match a real owner")
	}
}

func TestIsDeleted(t *testing.T) {
	if (Transaction{}).IsDeleted() {
		t.Error("a transaction with no deleted_at is not deleted")
	}
	now := time.Now()
	if !(Transaction{DeletedAt: &now}).IsDeleted() {
		t.Error("a transaction with deleted_at is deleted")
	}
}

func TestFromWhatsApp(t *testing.T) {
	if !(Transaction{Source: SourceWhatsApp}).FromWhatsApp() {
		t.Error("a whatsapp-sourced transaction should report FromWhatsApp")
	}
	if (Transaction{Source: SourceWeb}).FromWhatsApp() {
		t.Error("a web-sourced transaction should not report FromWhatsApp")
	}
}

// architecture.md section 1: IDR only, money as an integer.
func TestCurrencyIsIDR(t *testing.T) {
	if CurrencyIDR != "IDR" {
		t.Errorf("CurrencyIDR = %q, want IDR", CurrencyIDR)
	}
}

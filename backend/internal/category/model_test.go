package category

import (
	"testing"

	"github.com/google/uuid"
)

// prd.md section 5 fixes the default set; the migration seeds exactly these.
func TestSystemNamesMatchThePRD(t *testing.T) {
	want := []string{"Makan", "Transportasi", "Hiburan", "Tagihan", "Belanja", "Lainnya"}

	got := SystemNames()
	if len(got) != len(want) {
		t.Fatalf("SystemNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SystemNames()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// OwnedBy must never return true for another user's category.
func TestOwnedBy(t *testing.T) {
	alice := uuid.New()
	bob := uuid.New()

	system := Category{Name: NameLainnya, IsSystem: true}
	if !system.OwnedBy(alice) || !system.OwnedBy(bob) {
		t.Error("a system category should be usable by every user")
	}

	owned := Category{Name: "Kopi Spesial", UserID: &alice}
	if !owned.OwnedBy(alice) {
		t.Error("alice should own her own category")
	}
	if owned.OwnedBy(bob) {
		t.Error("bob must not be able to use alice's category")
	}

	// A user category with no owner is malformed; nobody may use it.
	orphan := Category{Name: "Orphan"}
	if orphan.OwnedBy(alice) {
		t.Error("an unowned non-system category must not be usable")
	}
}

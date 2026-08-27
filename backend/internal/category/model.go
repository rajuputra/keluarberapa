// Package category holds the expense-category aggregate.
//
// The six system categories from prd.md section 5 are seeded by
// 001_initial_schema.sql as shared rows with a NULL user_id. User-created
// categories (post-MVP P1) always carry a user_id.
package category

import (
	"time"

	"github.com/google/uuid"
)

// The seeded system categories, in the order given by prd.md section 5.
const (
	NameMakan        = "Makan"
	NameTransportasi = "Transportasi"
	NameHiburan      = "Hiburan"
	NameTagihan      = "Tagihan"
	NameBelanja      = "Belanja"
	// NameLainnya is the fallback when no keyword rule matches.
	NameLainnya = "Lainnya"
)

// SystemNames lists the seeded categories.
func SystemNames() []string {
	return []string{
		NameMakan, NameTransportasi, NameHiburan,
		NameTagihan, NameBelanja, NameLainnya,
	}
}

// Category is a row of the categories table.
type Category struct {
	ID uuid.UUID
	// UserID is nil for a system category, which every user can read.
	UserID    *uuid.UUID
	Name      string
	IsSystem  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// OwnedBy reports whether userID may use this category: either it is a shared
// system category, or it belongs to that user. It must never return true for
// another user's category.
func (c Category) OwnedBy(userID uuid.UUID) bool {
	if c.IsSystem {
		return true
	}
	return c.UserID != nil && *c.UserID == userID
}

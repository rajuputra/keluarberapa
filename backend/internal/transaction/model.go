// Package transaction holds the expense aggregate.
//
// Stage 1 provides the model only. Every query added in later stages must be
// scoped with "user_id = $1" (ai_instructions.md section 3).
package transaction

import (
	"time"

	"github.com/google/uuid"
)

// CurrencyIDR is the only currency the MVP stores (architecture.md section 1).
const CurrencyIDR = "IDR"

// Source is the transactions.source column: how the expense was recorded.
type Source string

const (
	// SourceWhatsApp means the expense came from an inbound WhatsApp message.
	SourceWhatsApp Source = "whatsapp"
	// SourceWeb means the expense was entered through the dashboard.
	SourceWeb Source = "web"
)

// Sources lists every accepted value.
func Sources() []Source { return []Source{SourceWhatsApp, SourceWeb} }

// Valid reports whether s is a source the database will accept.
func (s Source) Valid() bool {
	for _, candidate := range Sources() {
		if s == candidate {
			return true
		}
	}
	return false
}

func (s Source) String() string { return string(s) }

// Transaction is a row of the transactions table.
//
// Amount is a positive integer in the smallest unit of Currency. IDR has no
// minor unit, so 25000 means Rp25.000; money is never a float.
type Transaction struct {
	ID     uuid.UUID
	UserID uuid.UUID
	// CategoryID is nil only if the referenced category was deleted.
	CategoryID  *uuid.UUID
	Amount      int64
	Currency    string
	Description string
	// Date is when the expense happened, stored in UTC. Monthly summaries bucket
	// it in the user's timezone, which defaults to Asia/Jakarta.
	Date   time.Time
	Source Source
	// WhatsAppMessageID links back to the inbound message for WhatsApp-sourced
	// transactions and is nil for web-sourced ones.
	WhatsAppMessageID *uuid.UUID
	CreatedAt         time.Time
	UpdatedAt         time.Time
	// DeletedAt marks a soft-deleted row, which listings must exclude.
	DeletedAt *time.Time
}

// BelongsTo reports whether the transaction is owned by userID.
//
// This is a defence in depth check, not a substitute for filtering in SQL: a
// query for another user's row must not return it in the first place.
func (t Transaction) BelongsTo(userID uuid.UUID) bool { return t.UserID == userID }

// IsDeleted reports whether the transaction has been soft-deleted.
func (t Transaction) IsDeleted() bool { return t.DeletedAt != nil }

// FromWhatsApp reports whether the expense originated from a WhatsApp message.
func (t Transaction) FromWhatsApp() bool { return t.Source == SourceWhatsApp }

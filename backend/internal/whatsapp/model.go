// Package whatsapp holds the WhatsApp identity and inbound-message models.
//
// Stage 1 provides the models only. The webhook handler, the Meta client and
// the expense parser arrive with the WhatsApp stage.
package whatsapp

import (
	"time"

	"github.com/google/uuid"
)

// Provider is the whatsapp_accounts.provider / whatsapp_messages.provider
// column. The MVP integrates exactly one provider (architecture.md section 1).
type Provider string

// ProviderMetaCloudAPI is the Meta WhatsApp Cloud API.
const ProviderMetaCloudAPI Provider = "meta_cloud_api"

// Valid reports whether p is a provider the database will accept.
func (p Provider) Valid() bool { return p == ProviderMetaCloudAPI }

func (p Provider) String() string { return string(p) }

// VerificationStatus is the whatsapp_accounts.verification_status column.
type VerificationStatus string

const (
	VerificationPending  VerificationStatus = "pending"
	VerificationVerified VerificationStatus = "verified"
	VerificationFailed   VerificationStatus = "failed"
)

// VerificationStatuses lists every accepted value.
func VerificationStatuses() []VerificationStatus {
	return []VerificationStatus{VerificationPending, VerificationVerified, VerificationFailed}
}

// Valid reports whether s is a status the database will accept.
func (s VerificationStatus) Valid() bool {
	for _, candidate := range VerificationStatuses() {
		if s == candidate {
			return true
		}
	}
	return false
}

func (s VerificationStatus) String() string { return string(s) }

// Account is a row of the whatsapp_accounts table: the link between a WhatsApp
// number and its owning user.
//
// Exactly one account exists per user, enforced by the
// whatsapp_accounts_one_per_user constraint. PhoneNumber is unique across all
// users, which is what makes inbound identity resolution unambiguous.
type Account struct {
	ID     uuid.UUID
	UserID uuid.UUID
	// PhoneNumber is E.164 digits without the leading "+", matching Meta's
	// wa_id format, for example 6281234567890.
	PhoneNumber        string
	Provider           Provider
	VerificationStatus VerificationStatus
	VerifiedAt         *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// IsVerified reports whether the number may be used to record expenses.
func (a Account) IsVerified() bool { return a.VerificationStatus == VerificationVerified }

// MessageStatus is the whatsapp_messages.status column: the outcome of
// processing an inbound message.
type MessageStatus string

const (
	// MessageReceived is the state on arrival, before processing.
	MessageReceived MessageStatus = "received"
	// MessageProcessed means a transaction was saved from this message.
	MessageProcessed MessageStatus = "processed"
	// MessageRejected means the sender is known but the text was unparsable.
	MessageRejected MessageStatus = "rejected"
	// MessageIgnored means the sender is unknown, or the payload was not text.
	MessageIgnored MessageStatus = "ignored"
)

// MessageStatuses lists every accepted value.
func MessageStatuses() []MessageStatus {
	return []MessageStatus{MessageReceived, MessageProcessed, MessageRejected, MessageIgnored}
}

// Valid reports whether s is a status the database will accept.
func (s MessageStatus) Valid() bool {
	for _, candidate := range MessageStatuses() {
		if s == candidate {
			return true
		}
	}
	return false
}

func (s MessageStatus) String() string { return string(s) }

// Message is a row of the whatsapp_messages table. It exists so that a webhook
// redelivery can be recognised and answered without creating a second
// transaction (user_stories.md Epic 2).
type Message struct {
	ID uuid.UUID
	// UserID is nil when the sender's number is not registered. Such messages
	// are still stored, so a retry is still recognised as a duplicate.
	UserID   *uuid.UUID
	Provider Provider
	// ProviderMessageID is Meta's wamid: the idempotency key.
	ProviderMessageID string
	FromPhoneNumber   string
	Body              string
	Status            MessageStatus
	ErrorReason       *string
	ReceivedAt        time.Time
	ProcessedAt       *time.Time
	CreatedAt         time.Time
}

// BelongsTo reports whether the message was sent by userID. Used to keep
// message history scoped to its owner.
func (m Message) BelongsTo(userID uuid.UUID) bool {
	return m.UserID != nil && *m.UserID == userID
}

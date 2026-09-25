package billing

import (
	"time"
)

// PaymentChannel is a payment channel registry row (feature-14 AD1).
// v1 ships a mock channel that is always available and auto-pays on a
// :pay callback.
type PaymentChannel struct {
	// ID is the channel id (e.g. "mock").
	ID string `gorm:"primaryKey;size:64"`
	// Type is the channel type ("mock"; real PSP types follow).
	Type string `gorm:"size:32;not null"`
	// DisplayName is the human-readable name.
	DisplayName string `gorm:"size:128;not null"`
	// Config is a JSON config string (empty for mock).
	Config string `gorm:"type:text"`
	// Enabled reports whether the channel accepts new intents.
	Enabled bool `gorm:"not null;default:true"`
	// CreatedAt is the row write time (UTC).
	CreatedAt time.Time
}

// TableName overrides the default GORM table name.
func (PaymentChannel) TableName() string { return "payment_channels" }

// PaymentIntent is one payment attempt (feature-14 AD2). The channel
// callback marks it paid and credits the account exactly once.
type PaymentIntent struct {
	// ID is the server-generated UUID v4, exposed as intent_id.
	ID string `gorm:"primaryKey;type:uuid"`
	// AccountID is the funded account.
	AccountID string `gorm:"type:uuid;not null;index"`
	// AmountCents is the payment amount in minor units (> 0).
	AmountCents int64 `gorm:"not null"`
	// Channel is the channel id.
	Channel string `gorm:"size:64;not null"`
	// Status is pending | paid | failed | expired.
	Status string `gorm:"size:16;not null;default:pending"`
	// IdempotencyKey is the replay guard (unique).
	IdempotencyKey string `gorm:"size:128;not null;uniqueIndex"`
	// Reference is a free-text note.
	Reference string `gorm:"size:255"`
	// CreatedAt is the intent creation time.
	CreatedAt time.Time
	// PaidAt is when the intent was paid (zero until paid).
	PaidAt *time.Time
	// UpdatedAt is bumped on every write.
	UpdatedAt time.Time
}

// TableName overrides the default GORM table name.
func (PaymentIntent) TableName() string { return "payment_intents" }

// Invoice is a per-period billing document derived from a settled bill
// (feature-14 AD3). One invoice per bill.
type Invoice struct {
	// ID is the server-generated UUID v4, exposed as invoice_id.
	ID string `gorm:"primaryKey;type:uuid"`
	// BillID is the source bill (unique: one invoice per bill).
	BillID string `gorm:"type:uuid;not null;uniqueIndex"`
	// OrganizationID is the billed org.
	OrganizationID string `gorm:"size:64;not null;index"`
	// PeriodStart/PeriodEnd are the bill period bounds (unix seconds).
	PeriodStart int64 `gorm:"not null"`
	PeriodEnd   int64 `gorm:"not null"`
	// TotalCents is the bill total in minor units.
	TotalCents int64 `gorm:"not null"`
	// Currency is the platform billing currency.
	Currency string `gorm:"size:8;not null"`
	// Status is issued | paid.
	Status string `gorm:"size:16;not null;default:issued"`
	// IssuedAt is the issue time.
	IssuedAt time.Time
	// PaidAt is when the invoice was paid (zero until paid).
	PaidAt *time.Time
}

// TableName overrides the default GORM table name.
func (Invoice) TableName() string { return "invoices" }
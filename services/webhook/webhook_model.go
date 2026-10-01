package webhook

import (
	"time"
)

// Surface constants: a webhook's surface is derived from the request
// path (admin prefix → "admin", user prefix → "user") and constrains
// which event types are valid at creation (feature #23, §3.4).
const (
	SurfaceAdmin = "admin"
	SurfaceUser  = "user"
)

// Event catalog split by surface (feature #23, §3.4). The admin surface
// subscribes to platform orchestration events; the end-user surface to
// tenant account events.
var (
	// AdminEventTypes is the admin-surface event catalog.
	AdminEventTypes = []string{
		"deployment.status_changed",
		"autoscaling.scaled",
		"autoscaling.scale_to_zero",
	}
	// UserEventTypes is the end-user-surface event catalog.
	UserEventTypes = []string{
		"billing.invoice_created",
		"billing.invoice_paid",
		"billing.spend_limit_breached",
		"billing.balance_low",
	}
	// PingEventType is the synthetic test/ping event (AD7).
	PingEventType = "webhook.ping"
)

// Delivery statuses (AD8).
const (
	DeliveryStatusDelivered = "delivered"
	DeliveryStatusFailed    = "failed"
	DeliveryStatusPending   = "pending"
)

// Webhook is one outbound webhook endpoint + event subscription
// (feature #23, §4.1).
type Webhook struct {
	// WebhookID is the server-generated UUID v4.
	WebhookID string `gorm:"primaryKey;type:uuid"`
	// OrganizationID is the owning organization.
	OrganizationID string `gorm:"size:64;not null;index:idx_webhooks_org_created,priority:1"`
	// Surface is "admin" / "user" — which surface's catalog this webhook
	// subscribes to (§3.4).
	Surface string `gorm:"size:16;not null;index"`
	// Name is the display name (≤ 64 chars).
	Name string `gorm:"size:64;not null"`
	// URL is the endpoint URL (valid https:// or http://, ≤ 2048 chars).
	URL string `gorm:"size:2048;not null"`
	// Enabled is whether the webhook is active or paused (AD6).
	Enabled bool `gorm:"not null"`
	// EnabledEventTypes is the JSON array of enabled event type strings.
	EnabledEventTypes string `gorm:"type:jsonb;not null"`
	// SecretCiphertext is the signing secret encrypted at rest
	// (AES-256-GCM, AD3).
	SecretCiphertext []byte `gorm:"not null"`
	// MaxAttempts is the retry policy: max attempts (1-10).
	MaxAttempts int `gorm:"not null;default:5"`
	// BackoffSeconds is the retry policy: seconds between attempts
	// (1-3600).
	BackoffSeconds int `gorm:"not null;default:60"`
	// CreatedAt is the creation time (UTC).
	CreatedAt time.Time `gorm:"not null;index:idx_webhooks_org_created,priority:2"`
	// UpdatedAt is the last update time (UTC).
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName overrides the default GORM table name.
func (Webhook) TableName() string { return "webhooks" }

// WebhookDelivery is one delivery attempt record (feature #23, §4.2).
type WebhookDelivery struct { //nolint:revive // webhook.WebhookDelivery is the domain name
	// DeliveryID is the server-generated UUID v4.
	DeliveryID string `gorm:"primaryKey;type:uuid"`
	// WebhookID is the owning webhook (cascade delete).
	WebhookID string `gorm:"type:uuid;not null;index:idx_webhook_deliveries_webhook_created,priority:1"`
	// OrganizationID is the owning organization.
	OrganizationID string `gorm:"size:64;not null"`
	// EventID is the source event id (or the synthetic webhook.ping id).
	EventID string `gorm:"size:128;not null"`
	// EventType is the event type delivered.
	EventType string `gorm:"size:64;not null;index"`
	// Status is delivered / failed / pending (AD8).
	Status string `gorm:"size:16;not null;index"`
	// HTTPStatusCode is the last HTTP response status; NULL before the
	// first attempt.
	HTTPStatusCode *int `gorm:"index"`
	// AttemptCount is the number of attempts made.
	AttemptCount int `gorm:"not null;default:0"`
	// FailureReason is the last failure reason.
	FailureReason string `gorm:"size:512;not null;default:''"`
	// Payload is the signed payload sent ({"id","type","created_at","data"}).
	Payload string `gorm:"type:jsonb;not null"`
	// NextAttemptAt is when the next retry is due (pending); NULL when
	// delivered/failed.
	NextAttemptAt *time.Time `gorm:"index"`
	// CreatedAt is the first attempt time (UTC).
	CreatedAt time.Time `gorm:"not null;index:idx_webhook_deliveries_webhook_created,priority:2"`
	// LastAttemptAt is the last attempt time (UTC).
	LastAttemptAt *time.Time `gorm:"index"`
}

// TableName overrides the default GORM table name.
func (WebhookDelivery) TableName() string { return "webhook_deliveries" }

// WebhookFilter scopes a webhook list query (FR1.2).
type WebhookFilter struct { //nolint:revive // webhook.WebhookFilter is the domain name
	OrganizationID string
	Name           string
	EnabledFilter  bool
	Enabled        bool
	Offset         int
	Limit          int
}

// DeliveryFilter scopes a delivery list query (FR3.2).
type DeliveryFilter struct {
	OrganizationID string
	WebhookID      string
	Status         string
	EventType      string
	Offset         int
	Limit          int
}

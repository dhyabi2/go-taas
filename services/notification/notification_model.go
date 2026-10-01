// Package notification implements the in-console notification center:
// notification persistence with read/unread state, per-user event-type
// preferences, configurable threshold alerts, and the event consumer
// that fans out the platform event catalog to enabled users (feature
// #26).
package notification

import (
	"time"
)

// Surface constants: a notification's surface is derived from the
// request path (admin prefix → "admin", user prefix → "user") and
// constrains which event types are valid in preferences and which
// metrics are valid in thresholds (feature #26, §3.4).
const (
	SurfaceAdmin = "admin"
	SurfaceUser  = "user"
)

// Event catalog split by surface (feature #26, §3.4), exactly matching
// the feature-23 webhook catalog. The admin surface subscribes to
// platform orchestration events; the end-user surface to tenant account
// events.
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
)

// Metric sets split by surface (feature #26, AD5). The end-user metrics
// are balance_low and spend_limit; the admin metrics are
// autoscaling_replicas and deployment_failure.
var (
	// AdminMetrics is the admin-surface threshold metric set.
	AdminMetrics = []string{
		"autoscaling_replicas",
		"deployment_failure",
	}
	// UserMetrics is the end-user-surface threshold metric set.
	UserMetrics = []string{
		"balance_low",
		"spend_limit",
	}
)

// Severity constants (feature #26, AD3).
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Threshold operators (feature #26, AD5).
const (
	OperatorLT = "lt"
	OperatorGT = "gt"
)

// Notification is one persisted in-console event with read/unread state
// (feature #26, §4.1).
type Notification struct {
	// NotificationID is the server-generated UUID v4.
	NotificationID string `gorm:"primaryKey;type:uuid"`
	// OrganizationID is the owning organization.
	OrganizationID string `gorm:"size:64;not null;index:idx_notifications_user_created,priority:1"`
	// UserID is the owning user (per-user read state).
	UserID string `gorm:"size:64;not null;index:idx_notifications_user_created,priority:2"`
	// Surface is "admin" / "user" — which surface's catalog this
	// notification belongs to (§3.4).
	Surface string `gorm:"size:16;not null;index"`
	// EventType is the event type that produced this notification.
	EventType string `gorm:"size:64;not null;index"`
	// Title is the display title (≤ 256 chars).
	Title string `gorm:"size:256;not null"`
	// Body is the display body (≤ 1024 chars).
	Body string `gorm:"size:1024;not null"`
	// Severity is info / warning / critical (closed enum).
	Severity string `gorm:"size:16;not null;index"`
	// Read is the read/unread state (per-user, per-notification).
	Read bool `gorm:"not null;default:false;index"`
	// Data is the event payload (or the threshold's metric/value) as JSON.
	Data string `gorm:"type:jsonb;not null"`
	// Link is an optional deep link to the page where the user can act.
	Link string `gorm:"size:2048;not null;default:''"`
	// CreatedAt is the creation time (UTC).
	CreatedAt time.Time `gorm:"not null;index:idx_notifications_user_created,priority:3"`
}

// TableName overrides the default GORM table name.
func (Notification) TableName() string { return "notifications" }

// NotificationPreference is one user's per-surface event-type preference
// set (feature #26, §4.2). One row per user per surface.
type NotificationPreference struct { //nolint:revive // notification.NotificationPreference is the domain name
	// UserID is the owning user (one row per user per surface).
	UserID string `gorm:"primaryKey;size:64"`
	// OrganizationID is the owning organization.
	OrganizationID string `gorm:"size:64;not null"`
	// Surface is "admin" / "user" — which surface's catalog this
	// preference set covers.
	Surface string `gorm:"size:16;not null"`
	// EnabledEventTypes is the JSON array of enabled event type strings
	// (subset of the surface's catalog; default all enabled).
	EnabledEventTypes string `gorm:"type:jsonb;not null"`
	// CreatedAt is the creation time (UTC).
	CreatedAt time.Time `gorm:"not null"`
	// UpdatedAt is the last update time (UTC).
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName overrides the default GORM table name.
func (NotificationPreference) TableName() string { return "notification_preferences" }

// NotificationThreshold is one configurable threshold alert (feature
// #26, §4.3).
type NotificationThreshold struct { //nolint:revive // notification.NotificationThreshold is the domain name
	// ThresholdID is the server-generated UUID v4.
	ThresholdID string `gorm:"primaryKey;type:uuid"`
	// OrganizationID is the owning organization.
	OrganizationID string `gorm:"size:64;not null;index:idx_notification_thresholds_user_created,priority:1"`
	// UserID is the owning user (per-user thresholds).
	UserID string `gorm:"size:64;not null;index:idx_notification_thresholds_user_created,priority:2"`
	// Surface is "admin" / "user" — which surface's metric set this
	// threshold uses.
	Surface string `gorm:"size:16;not null;index"`
	// Name is the display name (≤ 64 chars).
	Name string `gorm:"size:64;not null"`
	// Metric is balance_low / spend_limit (user) or
	// autoscaling_replicas / deployment_failure (admin).
	Metric string `gorm:"size:32;not null;index"`
	// Operator is lt / gt (closed enum).
	Operator string `gorm:"size:4;not null"`
	// Value is the threshold value.
	Value float64 `gorm:"not null"`
	// Enabled is whether the threshold is active.
	Enabled bool `gorm:"not null;index"`
	// CreatedAt is the creation time (UTC).
	CreatedAt time.Time `gorm:"not null;index:idx_notification_thresholds_user_created,priority:3"`
	// UpdatedAt is the last update time (UTC).
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName overrides the default GORM table name.
func (NotificationThreshold) TableName() string { return "notification_thresholds" }

// NotificationFilter scopes a notification list query (FR1.1).
type NotificationFilter struct { //nolint:revive // notification.NotificationFilter is the domain name
	OrganizationID string
	UserID         string
	ReadFilter     bool
	Read           bool
	EventType      string
	Offset         int
	Limit          int
}

// ThresholdFilter scopes a threshold list query (FR3.2).
type ThresholdFilter struct {
	OrganizationID string
	UserID         string
	EnabledFilter  bool
	Enabled        bool
	Offset         int
	Limit          int
}

// eventTypesForSurface returns the event catalog of a surface.
func eventTypesForSurface(surface string) []string {
	if surface == SurfaceAdmin {
		return AdminEventTypes
	}
	return UserEventTypes
}

// metricsForSurface returns the threshold metric set of a surface.
func metricsForSurface(surface string) []string {
	if surface == SurfaceAdmin {
		return AdminMetrics
	}
	return UserMetrics
}

// containsString reports whether s is in the slice.
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

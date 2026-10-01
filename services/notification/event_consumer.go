package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/logger"
	"github.com/go-taas/go-taas/pkg/mq"
	"github.com/go-taas/go-taas/pkg/server"
)

// notificationEvent is the canonical event envelope published on
// notification.events (AD9). It mirrors the feature-23 webhook event
// envelope so the two consumers stay consistent.
type notificationEvent struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	CreatedAt int64           `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

// EventConsumer subscribes to notification.events and, for each event,
// creates a notification for every user on that surface whose
// preferences enable the event type, and evaluates threshold alerts on
// the same stream (AD9, AD10). It implements server.Runner.
type EventConsumer struct {
	client  mq.Client
	repo    *Repository
	workers int
}

// NewEventConsumer constructs an EventConsumer. workers <= 0 falls back
// to 1 (sequential handling).
func NewEventConsumer(client mq.Client, repo *Repository, workers int) *EventConsumer {
	if workers <= 0 {
		workers = 1
	}
	return &EventConsumer{client: client, repo: repo, workers: workers}
}

// NewEventConsumerRunner builds the EventConsumer from the shared server
// components. It returns nil when the required components are
// unavailable, so callers can pass the result straight to AddRunner.
func NewEventConsumerRunner(components server.Components) *EventConsumer {
	if components == nil {
		return nil
	}
	if components.MQ() == nil || components.DB() == nil {
		logger.S().Warn("notification: event consumer disabled, mq or db component unavailable")
		return nil
	}
	client, ok := components.MQ().Client().(mq.Client)
	if !ok {
		logger.S().Fatalw("notification: unexpected mq handle type",
			"type", fmt.Sprintf("%T", components.MQ().Client()))
		return nil
	}
	raw := components.DB().GormDB()
	db, ok := raw.(*gorm.DB)
	if !ok {
		logger.S().Fatalw("notification: unexpected db handle type",
			"type", fmt.Sprintf("%T", raw))
		return nil
	}
	return NewEventConsumer(client, NewRepository(db), 4)
}

// Run implements server.Runner: it subscribes to notification.events and
// blocks until ctx is cancelled or the subscription fails.
func (c *EventConsumer) Run(ctx context.Context) error {
	return c.client.Subscribe(ctx, mq.DefaultSubjects().NotificationEvents, func(msg mq.Message) error {
		return c.handle(ctx, msg)
	})
}

// HandleForTest applies one notification event synchronously. It exists
// so full-verification tests can drive the consumer without a broker.
func (c *EventConsumer) HandleForTest(ctx context.Context, msg mq.Message) error {
	return c.handle(ctx, msg)
}

// handle applies one notification event: for each enabled user it
// inserts a notification row, and evaluates enabled thresholds against
// the event data. Parse errors are logged and skipped.
func (c *EventConsumer) handle(ctx context.Context, msg mq.Message) error {
	var ev notificationEvent
	if err := json.Unmarshal(msg.Body, &ev); err != nil {
		logger.S().Warnw("notification: malformed event, skipping",
			"subject", msg.Subject, "err", err)
		return nil
	}
	if ev.ID == "" || ev.Type == "" {
		logger.S().Warnw("notification: invalid event, skipping",
			"subject", msg.Subject)
		return nil
	}
	orgID := msg.Headers["organization_id"]
	if orgID == "" {
		logger.S().Warnw("notification: event without organization, skipping",
			"event_id", ev.ID)
		return nil
	}
	surface := surfaceForEventType(ev.Type)
	if surface == "" {
		logger.S().Warnw("notification: event type outside the catalog, skipping",
			"event_type", ev.Type)
		return nil
	}

	// Fan out to every enabled user (AC6).
	users, err := c.repo.FindEnabledUsersForEvent(ctx, orgID, surface, ev.Type)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, uid := range users {
		_, err := c.repo.InsertNotification(ctx, &Notification{
			OrganizationID: orgID,
			UserID:         uid,
			Surface:        surface,
			EventType:      ev.Type,
			Title:          titleForEvent(ev.Type),
			Body:           bodyForEvent(ev.Type, ev.Data),
			Severity:       severityForEvent(ev.Type),
			Read:           false,
			Data:           string(ev.Data),
			Link:           linkForEvent(ev.Type),
			CreatedAt:      now,
		})
		if err != nil {
			logger.S().Warnw("notification: insert notification failed",
				"event_id", ev.ID, "user_id", uid, "err", err)
		}
	}

	// Evaluate enabled thresholds against the event data (AC7).
	if err := c.evaluateThresholds(ctx, orgID, surface, ev.Type, ev.Data, now); err != nil {
		logger.S().Warnw("notification: threshold evaluation failed",
			"event_id", ev.ID, "err", err)
	}
	return nil
}

// evaluateThresholds evaluates the org's enabled thresholds for the
// surface against the event data and inserts a threshold notification
// when a metric crosses a threshold (AC7).
func (c *EventConsumer) evaluateThresholds(ctx context.Context, orgID, surface, eventType string, data json.RawMessage, now time.Time) error {
	thresholds, err := c.repo.FindEnabledThresholds(ctx, orgID, surface)
	if err != nil {
		return err
	}
	if len(thresholds) == 0 {
		return nil
	}
	// Decode the event data into a generic map for metric extraction.
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	for _, th := range thresholds {
		metricValue, ok := metricValueForEvent(th.Metric, eventType, payload)
		if !ok {
			continue
		}
		if !crossesThreshold(th.Operator, metricValue, th.Value) {
			continue
		}
		// The threshold notification is created for the threshold's owner.
		_, err := c.repo.InsertNotification(ctx, &Notification{
			OrganizationID: orgID,
			UserID:         th.UserID,
			Surface:        surface,
			EventType:      eventType,
			Title:          th.Name,
			Body:           thresholdBody(th, metricValue),
			Severity:       SeverityWarning,
			Read:           false,
			Data:           string(data),
			Link:           linkForEvent(eventType),
			CreatedAt:      now,
		})
		if err != nil {
			logger.S().Warnw("notification: threshold notification insert failed",
				"threshold_id", th.ThresholdID, "err", err)
		}
	}
	return nil
}

// surfaceForEventType returns the surface of an event type, or "" when
// the type is outside the catalog.
func surfaceForEventType(eventType string) string {
	if containsString(AdminEventTypes, eventType) {
		return SurfaceAdmin
	}
	if containsString(UserEventTypes, eventType) {
		return SurfaceUser
	}
	return ""
}

// titleForEvent returns the display title for an event type.
func titleForEvent(eventType string) string {
	switch eventType {
	case "deployment.status_changed":
		return "Deployment status changed"
	case "autoscaling.scaled":
		return "Service autoscaled"
	case "autoscaling.scale_to_zero":
		return "Service scaled to zero"
	case "billing.invoice_created":
		return "Invoice created"
	case "billing.invoice_paid":
		return "Invoice paid"
	case "billing.spend_limit_breached":
		return "Spend limit breached"
	case "billing.balance_low":
		return "Balance low"
	default:
		return eventType
	}
}

// bodyForEvent returns the display body for an event type.
func bodyForEvent(eventType string, data json.RawMessage) string {
	switch eventType {
	case "deployment.status_changed":
		return "An inference service changed state."
	case "autoscaling.scaled":
		return "An autoscaling policy scaled a service."
	case "autoscaling.scale_to_zero":
		return "An autoscaling policy scaled a service to zero."
	case "billing.invoice_created":
		return "An invoice was created."
	case "billing.invoice_paid":
		return "An invoice was paid."
	case "billing.spend_limit_breached":
		return "A spend limit was breached."
	case "billing.balance_low":
		return "An account balance ran low."
	default:
		return string(data)
	}
}

// severityForEvent returns the severity for an event type.
func severityForEvent(eventType string) string {
	switch eventType {
	case "deployment.status_changed", "billing.spend_limit_breached", "billing.balance_low":
		return SeverityWarning
	default:
		return SeverityInfo
	}
}

// linkForEvent returns the deep link for an event type (AD8).
func linkForEvent(eventType string) string {
	switch eventType {
	case "deployment.status_changed":
		return "/admin/inference-services"
	case "autoscaling.scaled", "autoscaling.scale_to_zero":
		return "/admin/autoscaling"
	case "billing.invoice_created", "billing.invoice_paid":
		return "/billing"
	case "billing.spend_limit_breached":
		return "/billing"
	case "billing.balance_low":
		return "/billing"
	default:
		return ""
	}
}

// metricValueForEvent extracts the metric value for a threshold from the
// event payload. It returns ok=false when the event does not carry the
// metric.
func metricValueForEvent(metric, eventType string, payload map[string]any) (float64, bool) {
	switch metric {
	case "balance_low":
		if v, ok := payload["balance_cents"]; ok {
			return toFloat(v), true
		}
	case "spend_limit":
		if v, ok := payload["spent_cents"]; ok {
			return toFloat(v), true
		}
	case "autoscaling_replicas":
		if v, ok := payload["replicas"]; ok {
			return toFloat(v), true
		}
	case "deployment_failure":
		// A deployment_failure threshold fires on any deployment failure
		// event (state == failed).
		if eventType == "deployment.status_changed" {
			if state, ok := payload["state"].(string); ok && state == "failed" {
				return 1, true
			}
		}
	}
	return 0, false
}

// crossesThreshold reports whether metricValue crosses the threshold
// under the operator.
func crossesThreshold(operator string, metricValue, threshold float64) bool {
	switch operator {
	case OperatorLT:
		return metricValue < threshold
	case OperatorGT:
		return metricValue > threshold
	default:
		return false
	}
}

// thresholdBody builds the body of a threshold notification.
func thresholdBody(th *NotificationThreshold, metricValue float64) string {
	return fmt.Sprintf("%s crossed: %s %s %.2f (current %.2f)", th.Name, th.Metric, th.Operator, th.Value, metricValue)
}

// toFloat converts a JSON number to float64.
func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}

package webhook

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-taas/go-taas/pkg/logger"
	"github.com/go-taas/go-taas/pkg/mq"
)

// EventType constants for the platform event catalog (feature #23,
// §3.4). These are the canonical event type strings published on
// webhook.events.
const (
	// Admin-surface events (published by infer).
	EventDeploymentStatusChanged = "deployment.status_changed"
	EventAutoscalingScaled       = "autoscaling.scaled"
	EventAutoscalingScaleToZero  = "autoscaling.scale_to_zero"
	// End-user-surface events (published by billing).
	EventInvoiceCreated     = "billing.invoice_created"
	EventInvoicePaid        = "billing.invoice_paid"
	EventSpendLimitBreached = "billing.spend_limit_breached"
	EventBalanceLow         = "billing.balance_low"
)

// PublishEvent publishes a canonical WebhookEvent envelope to
// webhook.events (AD10). A publish failure is logged and never fails the
// producing mutation (the audit best-effort pattern). The organization
// is carried in a header so the consumer can scope the routing without
// parsing the payload.
func PublishEvent(ctx context.Context, client mq.Client, orgID, eventType, eventID string, data any) {
	if client == nil {
		return
	}
	raw, err := json.Marshal(data)
	if err != nil {
		logger.S().Warnw("webhook: marshal event data failed",
			"event_type", eventType, "err", err)
		return
	}
	ev := webhookEvent{
		ID:        eventID,
		Type:      eventType,
		CreatedAt: time.Now().Unix(),
		Data:      raw,
	}
	body, err := json.Marshal(ev)
	if err != nil {
		logger.S().Warnw("webhook: marshal event failed",
			"event_type", eventType, "err", err)
		return
	}
	headers := map[string]string{
		"organization_id": orgID,
		"event_type":      eventType,
	}
	if err := client.Publish(ctx, mq.DefaultSubjects().WebhookEvents, body, headers); err != nil {
		logger.S().Warnw("webhook: publish event failed (best-effort)",
			"event_type", eventType, "org", orgID, "err", err)
	}
}

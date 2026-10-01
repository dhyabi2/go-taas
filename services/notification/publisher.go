package notification

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-taas/go-taas/pkg/logger"
	"github.com/go-taas/go-taas/pkg/mq"
)

// PublishEvent publishes a canonical NotificationEvent envelope to
// notification.events (AD9). A publish failure is logged and never fails
// the producing mutation (the audit best-effort pattern). The
// organization is carried in a header so the consumer can scope the
// routing without parsing the payload.
func PublishEvent(ctx context.Context, client mq.Client, orgID, eventType, eventID string, data any) {
	if client == nil {
		return
	}
	raw, err := json.Marshal(data)
	if err != nil {
		logger.S().Warnw("notification: marshal event data failed",
			"event_type", eventType, "err", err)
		return
	}
	ev := notificationEvent{
		ID:        eventID,
		Type:      eventType,
		CreatedAt: time.Now().Unix(),
		Data:      raw,
	}
	body, err := json.Marshal(ev)
	if err != nil {
		logger.S().Warnw("notification: marshal event failed",
			"event_type", eventType, "err", err)
		return
	}
	headers := map[string]string{
		"organization_id": orgID,
		"event_type":      eventType,
	}
	if err := client.Publish(ctx, mq.DefaultSubjects().NotificationEvents, body, headers); err != nil {
		logger.S().Warnw("notification: publish event failed (best-effort)",
			"event_type", eventType, "org", orgID, "err", err)
	}
}

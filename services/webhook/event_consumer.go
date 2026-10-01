package webhook

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

// EventConsumer subscribes to webhook.events and routes each event to
// the matching enabled webhooks by inserting a pending delivery row
// (AD10, AD11). It implements server.Runner.
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
		logger.S().Warn("webhook: event consumer disabled, mq or db component unavailable")
		return nil
	}
	client, ok := components.MQ().Client().(mq.Client)
	if !ok {
		logger.S().Fatalw("webhook: unexpected mq handle type",
			"type", fmt.Sprintf("%T", components.MQ().Client()))
		return nil
	}
	raw := components.DB().GormDB()
	db, ok := raw.(*gorm.DB)
	if !ok {
		logger.S().Fatalw("webhook: unexpected db handle type",
			"type", fmt.Sprintf("%T", raw))
		return nil
	}
	return NewEventConsumer(client, NewRepository(db), 1)
}

// Run implements server.Runner: it subscribes to webhook.events and
// blocks until ctx is cancelled or the subscription fails.
func (c *EventConsumer) Run(ctx context.Context) error {
	return c.client.Subscribe(ctx, mq.DefaultSubjects().WebhookEvents, func(msg mq.Message) error {
		return c.handle(ctx, msg)
	})
}

// HandleForTest applies one webhook event synchronously. It exists so
// full-verification tests can drive the consumer without a broker.
func (c *EventConsumer) HandleForTest(ctx context.Context, msg mq.Message) error {
	return c.handle(ctx, msg)
}

// handle applies one webhook event: for each matching enabled webhook it
// inserts a pending delivery row. Parse errors are logged and skipped.
func (c *EventConsumer) handle(ctx context.Context, msg mq.Message) error {
	var ev webhookEvent
	if err := json.Unmarshal(msg.Body, &ev); err != nil {
		logger.S().Warnw("webhook: malformed event, skipping",
			"subject", msg.Subject, "err", err)
		return nil
	}
	if ev.ID == "" || ev.Type == "" {
		logger.S().Warnw("webhook: invalid event, skipping",
			"subject", msg.Subject)
		return nil
	}
	orgID := msg.Headers["organization_id"]
	if orgID == "" {
		logger.S().Warnw("webhook: event without organization, skipping",
			"event_id", ev.ID)
		return nil
	}
	webhooks, err := c.repo.FindWebhooksForEvent(ctx, orgID, ev.Type)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, wh := range webhooks {
		payload, err := BuildPayload(&ev)
		if err != nil {
			logger.S().Warnw("webhook: build payload failed",
				"event_id", ev.ID, "err", err)
			continue
		}
		next := now.Add(time.Duration(wh.BackoffSeconds) * time.Second)
		_, err = c.repo.InsertDelivery(ctx, &WebhookDelivery{
			WebhookID:      wh.WebhookID,
			OrganizationID: orgID,
			EventID:        ev.ID,
			EventType:      ev.Type,
			Status:         DeliveryStatusPending,
			AttemptCount:   0,
			Payload:        string(payload),
			NextAttemptAt:  &next,
			CreatedAt:      now,
		})
		if err != nil {
			logger.S().Warnw("webhook: insert delivery failed",
				"event_id", ev.ID, "webhook_id", wh.WebhookID, "err", err)
		}
	}
	return nil
}

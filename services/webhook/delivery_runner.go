package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/config"
	"github.com/go-taas/go-taas/pkg/logger"
	"github.com/go-taas/go-taas/pkg/server"
)

// DeliveryRunner polls for due pending deliveries and attempts them,
// applying the retry policy (max_attempts × backoff_seconds) and marking
// each delivered or failed (AD11). It implements server.Runner.
type DeliveryRunner struct {
	repo         *Repository
	deliverer    *Deliverer
	secretKey    string
	pollInterval time.Duration
	workers      int
}

// NewDeliveryRunner constructs a DeliveryRunner.
func NewDeliveryRunner(repo *Repository, deliverer *Deliverer, secretKey string, pollInterval time.Duration, workers int) *DeliveryRunner {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	if workers <= 0 {
		workers = 4
	}
	return &DeliveryRunner{
		repo:         repo,
		deliverer:    deliverer,
		secretKey:    secretKey,
		pollInterval: pollInterval,
		workers:      workers,
	}
}

// NewDeliveryRunnerRunner builds the DeliveryRunner from the shared
// server components. It returns nil when the required components are
// unavailable, so callers can pass the result straight to AddRunner.
func NewDeliveryRunnerRunner(components server.Components) *DeliveryRunner {
	if components == nil {
		return nil
	}
	cfg := config.GetConfig()
	if components.DB() == nil {
		logger.S().Warn("webhook: delivery runner disabled, db component unavailable")
		return nil
	}
	raw := components.DB().GormDB()
	db, ok := raw.(*gorm.DB)
	if !ok {
		logger.S().Fatalw("webhook: unexpected db handle type",
			"type", fmt.Sprintf("%T", raw))
		return nil
	}
	return NewDeliveryRunner(
		NewRepository(db),
		NewDeliverer(cfg.Webhook.Delivery.Timeout),
		cfg.Webhook.SecretEncryptionKey,
		cfg.Webhook.Delivery.PollInterval,
		cfg.Webhook.Delivery.Workers,
	)
}

// Run implements server.Runner: it polls for due deliveries until ctx is
// cancelled.
func (r *DeliveryRunner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.DeliverOnce(ctx)
		}
	}
}

// DeliverOnce runs one delivery pass: fetch due pending deliveries and
// attempt each. It is extracted for tests.
func (r *DeliveryRunner) DeliverOnce(ctx context.Context) {
	due, err := r.repo.FindDueDeliveries(ctx, time.Now().UTC(), 100)
	if err != nil {
		logger.S().Warnw("webhook: find due deliveries failed", "err", err)
		return
	}
	for _, d := range due {
		r.attempt(ctx, d)
	}
}

// attempt performs one delivery attempt and persists the outcome per the
// retry policy (FR4.3).
func (r *DeliveryRunner) attempt(ctx context.Context, d *WebhookDelivery) {
	wh, err := r.repo.FindWebhookByID(ctx, d.OrganizationID, d.WebhookID)
	if err != nil {
		// The webhook was deleted; mark the delivery failed.
		now := time.Now().UTC()
		d.Status = DeliveryStatusFailed
		d.FailureReason = "webhook deleted"
		d.LastAttemptAt = &now
		d.AttemptCount++
		_ = r.repo.UpdateDelivery(ctx, d)
		return
	}
	secret, err := DecryptSecret(wh.SecretCiphertext, r.secretKey)
	if err != nil {
		logger.S().Warnw("webhook: decrypt secret failed",
			"webhook_id", wh.WebhookID, "err", err)
		return
	}
	var ev webhookEvent
	if err := unmarshalPayload(d.Payload, &ev); err != nil {
		now := time.Now().UTC()
		d.Status = DeliveryStatusFailed
		d.FailureReason = "invalid stored payload"
		d.LastAttemptAt = &now
		d.AttemptCount++
		_ = r.repo.UpdateDelivery(ctx, d)
		return
	}
	res := r.deliverer.Deliver(ctx, wh, secret, &ev)
	now := time.Now().UTC()
	d.AttemptCount++
	d.LastAttemptAt = &now
	if res.Err == nil {
		d.Status = DeliveryStatusDelivered
		d.HTTPStatusCode = &res.HTTPStatusCode
		d.FailureReason = ""
		d.NextAttemptAt = nil
	} else {
		code := res.HTTPStatusCode
		d.HTTPStatusCode = &code
		d.FailureReason = truncate(res.Err.Error(), 512)
		if d.AttemptCount < wh.MaxAttempts {
			d.Status = DeliveryStatusPending
			next := now.Add(time.Duration(wh.BackoffSeconds) * time.Second)
			d.NextAttemptAt = &next
		} else {
			d.Status = DeliveryStatusFailed
			d.NextAttemptAt = nil
		}
	}
	if err := r.repo.UpdateDelivery(ctx, d); err != nil {
		logger.S().Warnw("webhook: update delivery failed",
			"delivery_id", d.DeliveryID, "err", err)
	}
}

// unmarshalPayload decodes a stored delivery payload back into a
// webhookEvent for redelivery.
func unmarshalPayload(payload string, ev *webhookEvent) error {
	return json.Unmarshal([]byte(payload), ev)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

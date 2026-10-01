package webhook

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/config"
	"github.com/go-taas/go-taas/pkg/logger"
	"github.com/go-taas/go-taas/pkg/server"
)

// WebhookRetentionRunner deletes webhook_deliveries older than the
// configured TTL in batches (AD8). It is the only deleter of the
// delivery log and never touches request logs, vouchers, usage records,
// charge records, or audit events. It implements server.Runner.
type WebhookRetentionRunner struct { //nolint:revive // webhook.WebhookRetentionRunner is the domain name
	repo        *Repository
	deliveryTTL time.Duration
	batchSize   int
	interval    time.Duration
}

// NewWebhookRetentionRunner constructs a WebhookRetentionRunner.
func NewWebhookRetentionRunner(repo *Repository, deliveryTTL time.Duration, batchSize int, interval time.Duration) *WebhookRetentionRunner {
	if batchSize <= 0 {
		batchSize = 1000
	}
	if interval <= 0 {
		interval = time.Hour
	}
	return &WebhookRetentionRunner{
		repo:        repo,
		deliveryTTL: deliveryTTL,
		batchSize:   batchSize,
		interval:    interval,
	}
}

// NewWebhookRetentionRunnerRunner builds the runner from the shared
// server components. It returns nil when disabled or the DB component is
// unavailable, so callers can pass the result straight to AddRunner.
func NewWebhookRetentionRunnerRunner(components server.Components) *WebhookRetentionRunner {
	if components == nil {
		return nil
	}
	cfg := config.GetConfig()
	if !cfg.Webhook.Retention.Enabled {
		return nil
	}
	if components.DB() == nil {
		logger.S().Warn("webhook: retention runner disabled, db component unavailable")
		return nil
	}
	raw := components.DB().GormDB()
	db, ok := raw.(*gorm.DB)
	if !ok {
		logger.S().Fatalw("webhook: unexpected db handle type",
			"type", fmt.Sprintf("%T", raw))
		return nil
	}
	return NewWebhookRetentionRunner(
		NewRepository(db),
		cfg.Webhook.Retention.DeliveryTTL,
		cfg.Webhook.Retention.BatchSize,
		cfg.Webhook.Retention.Interval,
	)
}

// Run implements server.Runner: it loops on the retention ticker until
// ctx is cancelled.
func (r *WebhookRetentionRunner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.RetainOnce(ctx)
		}
	}
}

// RetainOnce runs one retention pass: drain deliveries older than the
// TTL in batches until a pass deletes fewer than batchSize rows.
func (r *WebhookRetentionRunner) RetainOnce(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-r.deliveryTTL)
	var total int64
	for {
		deleted, err := r.repo.DeleteDeliveriesBefore(ctx, cutoff, r.batchSize)
		if err != nil {
			logger.S().Warnw("webhook: retention delete failed", "err", err)
			break
		}
		total += deleted
		if deleted < int64(r.batchSize) {
			break
		}
	}
	if total > 0 {
		logger.S().Infow("webhook: retention pass complete", "deleted", total)
	}
}

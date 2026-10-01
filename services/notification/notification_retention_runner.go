package notification

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/config"
	"github.com/go-taas/go-taas/pkg/logger"
	"github.com/go-taas/go-taas/pkg/server"
)

// NotificationRetentionRunner deletes notifications older than the
// configured TTL in batches (AD3). It is the only deleter of the
// notifications table and never touches request logs, vouchers, usage
// records, charge records, audit events, or webhook deliveries. It
// implements server.Runner.
type NotificationRetentionRunner struct { //nolint:revive // notification.NotificationRetentionRunner is the domain name
	repo            *Repository
	notificationTTL time.Duration
	batchSize       int
	interval        time.Duration
}

// NewNotificationRetentionRunner constructs a NotificationRetentionRunner.
func NewNotificationRetentionRunner(repo *Repository, notificationTTL time.Duration, batchSize int, interval time.Duration) *NotificationRetentionRunner {
	if batchSize <= 0 {
		batchSize = 1000
	}
	if interval <= 0 {
		interval = time.Hour
	}
	return &NotificationRetentionRunner{
		repo:            repo,
		notificationTTL: notificationTTL,
		batchSize:       batchSize,
		interval:        interval,
	}
}

// NewNotificationRetentionRunnerRunner builds the runner from the shared
// server components. It returns nil when disabled or the DB component is
// unavailable, so callers can pass the result straight to AddRunner.
func NewNotificationRetentionRunnerRunner(components server.Components) *NotificationRetentionRunner {
	if components == nil {
		return nil
	}
	cfg := config.GetConfig()
	if !cfg.Notification.Retention.Enabled {
		return nil
	}
	if components.DB() == nil {
		logger.S().Warn("notification: retention runner disabled, db component unavailable")
		return nil
	}
	raw := components.DB().GormDB()
	db, ok := raw.(*gorm.DB)
	if !ok {
		logger.S().Fatalw("notification: unexpected db handle type",
			"type", fmt.Sprintf("%T", raw))
		return nil
	}
	return NewNotificationRetentionRunner(
		NewRepository(db),
		cfg.Notification.Retention.NotificationTTL,
		cfg.Notification.Retention.BatchSize,
		cfg.Notification.Retention.Interval,
	)
}

// Run implements server.Runner: it loops on the retention ticker until
// ctx is cancelled.
func (r *NotificationRetentionRunner) Run(ctx context.Context) error {
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

// RetainOnce runs one retention pass: drain notifications older than the
// TTL in batches until a pass deletes fewer than batchSize rows.
func (r *NotificationRetentionRunner) RetainOnce(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-r.notificationTTL)
	var total int64
	for {
		deleted, err := r.repo.DeleteNotificationsBefore(ctx, cutoff, r.batchSize)
		if err != nil {
			logger.S().Warnw("notification: retention delete failed", "err", err)
			break
		}
		total += deleted
		if deleted < int64(r.batchSize) {
			break
		}
	}
	if total > 0 {
		logger.S().Infow("notification: retention pass complete", "deleted", total)
	}
}

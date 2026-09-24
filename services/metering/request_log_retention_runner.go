package metering

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/config"
	"github.com/go-taas/go-taas/pkg/logger"
	"github.com/go-taas/go-taas/pkg/server"
)

// RequestLogRetentionRunner deletes request logs older than the
// configured TTL in batches (feature #12, AD4). Request logs are
// diagnostic metadata with a shorter retention than vouchers; the
// runner reuses the metering.retention enabled/batchSize/interval
// settings and adds requestLogTTL. It implements server.Runner.
type RequestLogRetentionRunner struct {
	repo         *Repository
	requestLogTTL time.Duration
	batchSize    int
	interval     time.Duration
}

// NewRequestLogRetentionRunner constructs a RequestLogRetentionRunner.
func NewRequestLogRetentionRunner(repo *Repository, requestLogTTL time.Duration, batchSize int, interval time.Duration) *RequestLogRetentionRunner {
	if batchSize <= 0 {
		batchSize = 1000
	}
	if interval <= 0 {
		interval = time.Hour
	}
	return &RequestLogRetentionRunner{
		repo:          repo,
		requestLogTTL: requestLogTTL,
		batchSize:     batchSize,
		interval:      interval,
	}
}

// NewRequestLogRetentionRunnerRunner builds the runner from the shared
// server components. It returns nil when disabled or the DB component
// is unavailable, so callers can pass the result straight to AddRunner.
func NewRequestLogRetentionRunnerRunner(components server.Components) *RequestLogRetentionRunner {
	if components == nil {
		return nil
	}
	cfg := config.GetConfig()
	if !cfg.Metering.Retention.Enabled {
		return nil
	}
	if components.DB() == nil {
		logger.S().Warn("metering: request-log retention runner disabled, db component unavailable")
		return nil
	}
	raw := components.DB().GormDB()
	db, ok := raw.(*gorm.DB)
	if !ok {
		logger.S().Fatalw("metering: unexpected db handle type",
			"type", fmt.Sprintf("%T", raw))
		return nil
	}
	return NewRequestLogRetentionRunner(
		NewRepository(db),
		cfg.Metering.Retention.RequestLogTTL,
		cfg.Metering.Retention.BatchSize,
		cfg.Metering.Retention.Interval,
	)
}

// Run implements server.Runner: it loops on the retention ticker until
// ctx is cancelled.
func (r *RequestLogRetentionRunner) Run(ctx context.Context) error {
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

// RetainOnce runs one retention pass: drain request logs older than the
// TTL in batches until a pass deletes fewer than batchSize rows.
func (r *RequestLogRetentionRunner) RetainOnce(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-r.requestLogTTL)
	var total int64
	for {
		deleted, err := r.repo.DeleteRequestLogsBefore(ctx, cutoff, r.batchSize)
		if err != nil {
			logger.S().Warnw("metering: request-log retention delete failed", "err", err)
			break
		}
		total += deleted
		if deleted < int64(r.batchSize) {
			break
		}
	}
	if total > 0 {
		logger.S().Infow("metering: request-log retention pass complete", "deleted", total)
	}
}
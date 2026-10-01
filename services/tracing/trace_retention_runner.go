package tracing

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/config"
	"github.com/go-taas/go-taas/pkg/logger"
	"github.com/go-taas/go-taas/pkg/server"
)

// TraceRetentionRunner deletes traces (and their spans) older than the
// configured TTL in batches (feature #27, AD5). It reuses the
// metering.retention enabled/batchSize/interval settings and adds
// tracing.retention.traceTTL. It implements server.Runner.
type TraceRetentionRunner struct {
	repo      *Repository
	traceTTL  time.Duration
	batchSize int
	interval  time.Duration
}

// NewTraceRetentionRunner constructs a TraceRetentionRunner.
func NewTraceRetentionRunner(repo *Repository, traceTTL time.Duration, batchSize int, interval time.Duration) *TraceRetentionRunner {
	if batchSize <= 0 {
		batchSize = 1000
	}
	if interval <= 0 {
		interval = time.Hour
	}
	return &TraceRetentionRunner{
		repo:      repo,
		traceTTL:  traceTTL,
		batchSize: batchSize,
		interval:  interval,
	}
}

// NewTraceRetentionRunnerRunner builds the runner from the shared server
// components. It returns nil when disabled or the DB component is
// unavailable, so callers can pass the result straight to AddRunner.
func NewTraceRetentionRunnerRunner(components server.Components) *TraceRetentionRunner {
	if components == nil {
		return nil
	}
	cfg := config.GetConfig()
	if !cfg.Metering.Retention.Enabled {
		return nil
	}
	if components.DB() == nil {
		logger.S().Warn("tracing: trace retention runner disabled, db component unavailable")
		return nil
	}
	raw := components.DB().GormDB()
	db, ok := raw.(*gorm.DB)
	if !ok {
		logger.S().Fatalw("tracing: unexpected db handle type",
			"type", fmt.Sprintf("%T", raw))
		return nil
	}
	return NewTraceRetentionRunner(
		NewRepository(db),
		cfg.Tracing.Retention.TraceTTL,
		cfg.Metering.Retention.BatchSize,
		cfg.Metering.Retention.Interval,
	)
}

// Run implements server.Runner: it loops on the retention ticker until
// ctx is cancelled.
func (r *TraceRetentionRunner) Run(ctx context.Context) error {
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

// RetainOnce runs one retention pass: drain traces older than the TTL in
// batches until a pass deletes fewer than batchSize rows.
func (r *TraceRetentionRunner) RetainOnce(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-r.traceTTL)
	var total int64
	for {
		deleted, err := r.repo.DeleteTracesBefore(ctx, cutoff, r.batchSize)
		if err != nil {
			logger.S().Warnw("tracing: trace retention delete failed", "err", err)
			break
		}
		total += deleted
		if deleted < int64(r.batchSize) {
			break
		}
	}
	if total > 0 {
		logger.S().Infow("tracing: trace retention pass complete", "deleted", total)
	}
}
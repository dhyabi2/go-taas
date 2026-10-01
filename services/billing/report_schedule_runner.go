package billing

import (
	"context"
	"time"

	"github.com/go-taas/go-taas/pkg/logger"
)

// ScheduleRunner periodically finds due schedules, materializes each
// relative range into since/until, creates a pending report (the run)
// and lets the generator produce it (feature-25 AD4). It is a
// server.Runner.
type ScheduleRunner struct {
	service  *Service
	interval time.Duration
}

// NewScheduleRunner constructs the runner.
func NewScheduleRunner(service *Service, interval time.Duration) *ScheduleRunner {
	return &ScheduleRunner{service: service, interval: interval}
}

// Run implements server.Runner: it ticks until the context is cancelled.
func (r *ScheduleRunner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.RunOnce(ctx); err != nil {
				logger.S().Warnw("billing: schedule run pass failed", "err", err)
			}
		}
	}
}

// RunOnce performs one schedule pass: for each due schedule, materialize
// its relative range into since/until, create a pending report (the run,
// schedule_id set) and update last_run_at. Extracted for tests.
func (r *ScheduleRunner) RunOnce(ctx context.Context) error {
	repo, err := r.service.reportRepository()
	if err != nil {
		return err
	}
	schedules, err := repo.DueSchedules(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, sc := range schedules {
		since, until := materializeRelativeRange(sc.RelativeRange, now)
		report := &Report{
			OrganizationID: sc.OrganizationID,
			Name:           sc.Name,
			Dimension:      sc.Dimension,
			Since:          since,
			Until:          until,
			Granularity:    sc.Granularity,
			Timezone:       sc.Timezone,
			Status:         ReportStatusPending,
			ScheduleID:     &sc.ID,
		}
		if _, err := repo.CreateReport(ctx, report); err != nil {
			logger.S().Warnw("billing: schedule run create failed", "schedule_id", sc.ID, "err", err)
			continue
		}
		if err := repo.MarkScheduleRun(ctx, sc.ID, now); err != nil {
			logger.S().Warnw("billing: schedule last_run update failed", "schedule_id", sc.ID, "err", err)
		}
	}
	return nil
}

// materializeRelativeRange converts a relative range into a since/until
// pair (unix seconds) relative to now.
func materializeRelativeRange(relativeRange string, now time.Time) (int64, int64) {
	until := now.Unix()
	switch relativeRange {
	case RelativeRangeLast7Days:
		return until - 7*24*3600, until
	case RelativeRangeLast30Days:
		return until - 30*24*3600, until
	case RelativeRangeLastMonth:
		// The previous calendar month.
		firstOfThisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		lastMonthEnd := firstOfThisMonth.Add(-time.Second)
		firstOfLastMonth := time.Date(lastMonthEnd.Year(), lastMonthEnd.Month(), 1, 0, 0, 0, 0, now.Location())
		return firstOfLastMonth.Unix(), lastMonthEnd.Unix()
	}
	return until - 7*24*3600, until
}

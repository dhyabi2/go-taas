package billing

import (
	"context"
	"time"

	"github.com/go-taas/go-taas/pkg/logger"
)

// ReportGeneratorRunner periodically picks up pending reports, aggregates
// charge_records, renders the CSV and marks each report ready or failed
// (feature-25 AD1). It is a server.Runner; generation is a pure DB
// aggregation within the billing module, so an in-process runner is
// appropriate (no K8s resource involved).
type ReportGeneratorRunner struct {
	service  *Service
	interval time.Duration
}

// NewReportGeneratorRunner constructs the runner.
func NewReportGeneratorRunner(service *Service, interval time.Duration) *ReportGeneratorRunner {
	return &ReportGeneratorRunner{service: service, interval: interval}
}

// Run implements server.Runner: it ticks until the context is cancelled.
func (r *ReportGeneratorRunner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.RunOnce(ctx); err != nil {
				logger.S().Warnw("billing: report generation pass failed", "err", err)
			}
		}
	}
}

// RunOnce performs one report-generation pass: for each pending report,
// aggregate charge_records, render the CSV and mark the report ready or
// failed. Extracted for tests.
func (r *ReportGeneratorRunner) RunOnce(ctx context.Context) error {
	repo, err := r.service.reportRepository()
	if err != nil {
		return err
	}
	for {
		report, err := repo.NextPendingReport(ctx)
		if err != nil {
			return err
		}
		if report == nil {
			return nil
		}
		if err := r.service.generateReport(ctx, report); err != nil {
			logger.S().Warnw("billing: report generation failed", "report_id", report.ID, "err", err)
		}
	}
}

// generateReport aggregates charge_records for one pending report,
// renders the CSV and marks the report ready or failed.
func (s *Service) generateReport(ctx context.Context, report *Report) error {
	repo, err := s.reportRepository()
	if err != nil {
		return err
	}
	bucketSize := int64(86400)
	if report.Granularity == ReportGranularityHourly {
		bucketSize = 3600
	}
	rows, err := repo.AggregateChargeRecords(ctx, report.OrganizationID, report.Since, report.Until, bucketSize, report.Dimension)
	if err != nil {
		_ = repo.UpdateReportStatus(ctx, report.ID, ReportStatusFailed, 0, 0, "", "aggregation failed")
		return err
	}
	watermark, err := repo.ChargeWatermark(ctx, report.OrganizationID, report.Since, report.Until, bucketSize)
	if err != nil {
		_ = repo.UpdateReportStatus(ctx, report.ID, ReportStatusFailed, 0, 0, "", "watermark failed")
		return err
	}
	csv, err := renderCSV(report, rows)
	if err != nil {
		_ = repo.UpdateReportStatus(ctx, report.ID, ReportStatusFailed, 0, 0, "", "csv render failed")
		return err
	}
	return repo.UpdateReportStatus(ctx, report.ID, ReportStatusReady, int64(len(rows)), watermark, csv, "")
}
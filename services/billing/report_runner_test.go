package billing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AC1/AC3: the generator runner picks up a pending report, aggregates
// charge_records, renders the CSV and marks the report ready.
func TestReportGeneratorRunnerRunOnce(t *testing.T) {
	svc := newBillingTestService(t)
	db := svc.repo.db.DB(context.Background())
	repo := NewReportRepository(db)
	ctx := context.Background()

	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC).Unix()
	seedCharge(t, db, "org-a", "key-1", "model-a", day, 10, 20, 0, 0, 1, 1.25, "USD")

	report, err := repo.CreateReport(ctx, &Report{
		OrganizationID: "org-a", Name: "r", Dimension: ReportDimensionModel,
		Since: day, Until: day + 86400, Granularity: ReportGranularityDaily,
	})
	require.NoError(t, err)

	runner := NewReportGeneratorRunner(svc, time.Second)
	require.NoError(t, runner.RunOnce(ctx))

	updated, err := repo.FindReportByID(ctx, report.ID)
	require.NoError(t, err)
	assert.Equal(t, ReportStatusReady, updated.Status)
	assert.Equal(t, int64(1), updated.RowCount)
	assert.Contains(t, updated.CSV, "model-a")
}

// AC1: the generator runner marks a report failed when the aggregation
// fails (here: the charge_records table is dropped).
func TestReportGeneratorRunnerFailed(t *testing.T) {
	svc := newBillingTestService(t)
	db := svc.repo.db.DB(context.Background())
	repo := NewReportRepository(db)
	ctx := context.Background()

	report, err := repo.CreateReport(ctx, &Report{
		OrganizationID: "org-a", Name: "r", Dimension: ReportDimensionModel,
		Since: 1000, Until: 2000, Granularity: ReportGranularityDaily,
	})
	require.NoError(t, err)

	// Drop the charge_records table so the aggregation fails.
	require.NoError(t, db.Migrator().DropTable(&ChargeRecord{}))

	runner := NewReportGeneratorRunner(svc, time.Second)
	require.NoError(t, runner.RunOnce(ctx))

	updated, err := repo.FindReportByID(ctx, report.ID)
	require.NoError(t, err)
	assert.Equal(t, ReportStatusFailed, updated.Status)
}

// AC3: the generator runner marks a report ready with 0 rows when no
// charge records match the range.
func TestReportGeneratorRunnerEmpty(t *testing.T) {
	svc := newBillingTestService(t)
	db := svc.repo.db.DB(context.Background())
	repo := NewReportRepository(db)
	ctx := context.Background()

	report, err := repo.CreateReport(ctx, &Report{
		OrganizationID: "org-a", Name: "r", Dimension: ReportDimensionModel,
		Since: 1000, Until: 2000, Granularity: ReportGranularityDaily,
	})
	require.NoError(t, err)

	runner := NewReportGeneratorRunner(svc, time.Second)
	require.NoError(t, runner.RunOnce(ctx))

	updated, err := repo.FindReportByID(ctx, report.ID)
	require.NoError(t, err)
	assert.Equal(t, ReportStatusReady, updated.Status)
	assert.Equal(t, int64(0), updated.RowCount)
}

// AC5: the schedule runner materializes a due schedule into a pending
// report run and updates last_run_at.
func TestScheduleRunnerRunOnce(t *testing.T) {
	svc := newBillingTestService(t)
	db := svc.repo.db.DB(context.Background())
	repo := NewReportRepository(db)
	ctx := context.Background()

	sched, err := repo.CreateSchedule(ctx, &ReportSchedule{
		OrganizationID: "org-a", Name: "Weekly", Dimension: ReportDimensionModel,
		RelativeRange: RelativeRangeLast7Days, Granularity: ReportGranularityDaily,
		Frequency: ReportFrequencyWeekly,
	})
	require.NoError(t, err)

	runner := NewScheduleRunner(svc, time.Minute)
	require.NoError(t, runner.RunOnce(ctx))

	// A pending run was created for the schedule.
	runs, total, err := repo.ListScheduleRuns(ctx, sched.ID, 0, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, runs, 1)
	assert.Equal(t, ReportStatusPending, runs[0].Status)

	// last_run_at was updated.
	updated, err := repo.FindScheduleByID(ctx, sched.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.LastRunAt)
}

// Run returns when the context is cancelled (the ticker loop exits).
func TestReportGeneratorRunnerRunCancelled(t *testing.T) {
	svc := newBillingTestService(t)
	runner := NewReportGeneratorRunner(svc, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runner.Run(ctx)
	assert.Error(t, err)
}

// Run processes a pending report on a real tick.
func TestReportGeneratorRunnerRunTick(t *testing.T) {
	svc := newBillingTestService(t)
	db := svc.repo.db.DB(context.Background())
	repo := NewReportRepository(db)
	ctx := context.Background()

	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC).Unix()
	seedCharge(t, db, "org-a", "key-1", "model-a", day, 10, 20, 0, 0, 1, 1.25, "USD")
	report, err := repo.CreateReport(ctx, &Report{
		OrganizationID: "org-a", Name: "r", Dimension: ReportDimensionModel,
		Since: day, Until: day + 86400, Granularity: ReportGranularityDaily,
	})
	require.NoError(t, err)

	runner := NewReportGeneratorRunner(svc, time.Millisecond)
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(runCtx) }()
	// Let the ticker fire a few times.
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	updated, err := repo.FindReportByID(ctx, report.ID)
	require.NoError(t, err)
	assert.Equal(t, ReportStatusReady, updated.Status)
}

// Run returns when the context is cancelled (the ticker loop exits).
func TestScheduleRunnerRunCancelled(t *testing.T) {
	svc := newBillingTestService(t)
	runner := NewScheduleRunner(svc, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runner.Run(ctx)
	assert.Error(t, err)
}

// materializeRelativeRange returns the expected since/until pairs.
func TestMaterializeRelativeRange(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	until := now.Unix()

	// last_7_days.
	since, u := materializeRelativeRange(RelativeRangeLast7Days, now)
	assert.Equal(t, until-7*24*3600, since)
	assert.Equal(t, until, u)

	// last_30_days.
	since, _ = materializeRelativeRange(RelativeRangeLast30Days, now)
	assert.Equal(t, until-30*24*3600, since)

	// last_month: the previous calendar month (August 2026).
	since, u = materializeRelativeRange(RelativeRangeLastMonth, now)
	augStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).Unix()
	augEnd := time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC).Unix()
	assert.Equal(t, augStart, since)
	assert.Equal(t, augEnd, u)
}
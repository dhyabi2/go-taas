package billing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// seedCharge inserts a charge record directly into the shared DB.
func seedCharge(t *testing.T, db *gorm.DB, orgID, keyID, modelID string, periodStart int64, prompt, completion, cached, reasoning, reqCount int64, amount float64, currency string) {
	t.Helper()
	row := ChargeRecord{
		ID:               uuid.NewString(),
		OrganizationID:   orgID,
		APIKeyID:         keyID,
		ModelID:          modelID,
		AcceleratorType:  "default",
		PeriodStart:      periodStart,
		PeriodEnd:        periodStart + 3600,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CachedTokens:     cached,
		ReasoningTokens:  reasoning,
		RequestCount:     reqCount,
		Amount:           amount,
		Currency:         currency,
		TierIndex:        -1,
		Priced:           true,
	}
	require.NoError(t, db.Create(&row).Error)
}

// AC1/AC2: CreateReport/FindReportByID/ListReports/UpdateReportStatus.
func TestReportRepositoryLifecycle(t *testing.T) {
	db := newBillingTestDB(t)
	repo := NewReportRepository(db)
	ctx := context.Background()

	created, err := repo.CreateReport(ctx, &Report{
		OrganizationID: "org-a",
		Name:           "Monthly",
		Dimension:      ReportDimensionModel,
		Since:          1000,
		Until:          2000,
		Granularity:    ReportGranularityDaily,
		Timezone:       "UTC",
	})
	require.NoError(t, err)
	assert.Equal(t, ReportStatusPending, created.Status)
	assert.NotEmpty(t, created.ID)

	found, err := repo.FindReportByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "Monthly", found.Name)

	// Update to ready.
	require.NoError(t, repo.UpdateReportStatus(ctx, created.ID, ReportStatusReady, 5, 1500, "csv-content", ""))
	found, err = repo.FindReportByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, ReportStatusReady, found.Status)
	assert.Equal(t, int64(5), found.RowCount)
	assert.Equal(t, int64(1500), found.DataThrough)
	assert.Equal(t, "csv-content", found.CSV)

	// ListReports filters by org.
	rows, total, err := repo.ListReports(ctx, "org-a", "", "", 0, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, rows, 1)

	// Unknown id returns nil.
	missing, err := repo.FindReportByID(ctx, "nope")
	require.NoError(t, err)
	assert.Nil(t, missing)
}

// AC1: NextPendingReport returns the oldest pending report.
func TestReportRepositoryNextPending(t *testing.T) {
	db := newBillingTestDB(t)
	repo := NewReportRepository(db)
	ctx := context.Background()

	_, err := repo.CreateReport(ctx, &Report{OrganizationID: "org-a", Name: "A", Dimension: ReportDimensionModel, Since: 1, Until: 2, Granularity: ReportGranularityDaily})
	require.NoError(t, err)
	_, err = repo.CreateReport(ctx, &Report{OrganizationID: "org-a", Name: "B", Dimension: ReportDimensionModel, Since: 1, Until: 2, Granularity: ReportGranularityDaily})
	require.NoError(t, err)

	next, err := repo.NextPendingReport(ctx)
	require.NoError(t, err)
	require.NotNil(t, next)
	assert.Equal(t, "A", next.Name)
}

// AC4/AC5: CreateSchedule/FindScheduleByID/ListSchedules/UpdateSchedule/
// DeleteSchedule/ListScheduleRuns.
func TestReportRepositoryScheduleLifecycle(t *testing.T) {
	db := newBillingTestDB(t)
	repo := NewReportRepository(db)
	ctx := context.Background()

	sched, err := repo.CreateSchedule(ctx, &ReportSchedule{
		OrganizationID: "org-a",
		Name:           "Weekly",
		Dimension:      ReportDimensionAPIKey,
		RelativeRange:  RelativeRangeLast7Days,
		Granularity:    ReportGranularityDaily,
		Frequency:      ReportFrequencyWeekly,
	})
	require.NoError(t, err)
	assert.Equal(t, ScheduleStatusActive, sched.Status)

	found, err := repo.FindScheduleByID(ctx, sched.ID)
	require.NoError(t, err)
	require.NotNil(t, found)

	// Duplicate name check.
	exists, err := repo.ScheduleNameExists(ctx, "org-a", "Weekly", "")
	require.NoError(t, err)
	assert.True(t, exists)
	exists, err = repo.ScheduleNameExists(ctx, "org-a", "Weekly", sched.ID)
	require.NoError(t, err)
	assert.False(t, exists)

	// Update.
	require.NoError(t, repo.UpdateSchedule(ctx, sched.ID, "Monthly", ReportFrequencyMonthly, RelativeRangeLastMonth))
	found, err = repo.FindScheduleByID(ctx, sched.ID)
	require.NoError(t, err)
	assert.Equal(t, ReportFrequencyMonthly, found.Frequency)

	// List.
	rows, total, err := repo.ListSchedules(ctx, "org-a", 0, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, rows, 1)

	// Create a run and list runs.
	_, err = repo.CreateReport(ctx, &Report{
		OrganizationID: "org-a", Name: "run", Dimension: ReportDimensionAPIKey,
		Since: 1, Until: 2, Granularity: ReportGranularityDaily, ScheduleID: &sched.ID,
	})
	require.NoError(t, err)
	runs, runTotal, err := repo.ListScheduleRuns(ctx, sched.ID, 0, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), runTotal)
	assert.Len(t, runs, 1)

	// Delete.
	require.NoError(t, repo.DeleteSchedule(ctx, sched.ID))
	missing, err := repo.FindScheduleByID(ctx, sched.ID)
	require.NoError(t, err)
	assert.Nil(t, missing)
}

// AC3: AggregateChargeRecords returns correct bucket rows for a seeded
// charge_records set; ChargeWatermark returns the last complete bucket.
func TestReportRepositoryAggregate(t *testing.T) {
	db := newBillingTestDB(t)
	repo := NewReportRepository(db)
	ctx := context.Background()

	// Two hours in the same day, two models.
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC).Unix()
	h1 := day
	h2 := day + 3600
	seedCharge(t, db, "org-a", "key-1", "model-a", h1, 10, 20, 0, 0, 1, 1.25, "USD")
	seedCharge(t, db, "org-a", "key-1", "model-a", h2, 5, 5, 0, 0, 1, 0.5, "USD")
	seedCharge(t, db, "org-a", "key-2", "model-b", h1, 100, 200, 0, 0, 2, 3.0, "USD")

	// Daily aggregation by model.
	rows, err := repo.AggregateChargeRecords(ctx, "org-a", day, day+86400, 86400, ReportDimensionModel)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	// model-a: 15 prompt + 25 completion = 40 total, cost 1.75.
	// model-b: 100 prompt + 200 completion = 300 total, cost 3.0.
	byModel := map[string]ReportBucketRow{}
	for _, r := range rows {
		byModel[r.ModelID] = r
	}
	ma := byModel["model-a"]
	assert.Equal(t, int64(40), ma.TotalTokens)
	assert.Equal(t, int64(2), ma.RequestCount)
	assert.Equal(t, int64(175), ma.CostCents)
	mb := byModel["model-b"]
	assert.Equal(t, int64(300), mb.TotalTokens)
	assert.Equal(t, int64(300), mb.CostCents)

	// Hourly aggregation by api_key.
	rows, err = repo.AggregateChargeRecords(ctx, "org-a", day, day+86400, 3600, ReportDimensionAPIKey)
	require.NoError(t, err)
	require.Len(t, rows, 3) // key-1 h1, key-1 h2, key-2 h1

	// Watermark: last complete bucket = day (the last period_start is h2,
	// truncated to daily = day, minus one day = day-86400).
	wm, err := repo.ChargeWatermark(ctx, "org-a", day, day+86400, 86400)
	require.NoError(t, err)
	assert.Equal(t, day-86400, wm)
}

// AC1: DueSchedules returns active schedules.
func TestReportRepositoryDueSchedules(t *testing.T) {
	db := newBillingTestDB(t)
	repo := NewReportRepository(db)
	ctx := context.Background()

	_, err := repo.CreateSchedule(ctx, &ReportSchedule{
		OrganizationID: "org-a", Name: "S", Dimension: ReportDimensionModel,
		RelativeRange: RelativeRangeLast7Days, Granularity: ReportGranularityDaily,
		Frequency: ReportFrequencyDaily,
	})
	require.NoError(t, err)
	rows, err := repo.DueSchedules(ctx)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
}
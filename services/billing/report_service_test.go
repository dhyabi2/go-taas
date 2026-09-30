package billing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	billingv1 "github.com/go-taas/go-taas/proto/taas/billing/v1"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
	"github.com/go-taas/go-taas/services/tenancy"
)

// adminCtx returns a context carrying the admin request path metadata so
// isAdminSurface resolves true.
func adminCtx() context.Context {
	return metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-request-path", "/api/v1/admin/billing/reports"))
}

// userCtx returns a context carrying the user request path metadata and
// the caller org.
func userCtx(orgID string) context.Context {
	return metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-request-path", "/api/v1/billing/reports", organizationMetadataKey, orgID))
}

// AC1: CreateReport validates the range (10404), dimension (10903) and
// granularity (10904).
func TestCreateReportValidation(t *testing.T) {
	svc := newBillingTestService(t)

	// Invalid dimension.
	_, err := svc.CreateReport(adminCtx(), &billingv1.CreateReportRequest{
		Name: "r", Dimension: billingv1.ReportDimension_REPORT_DIMENSION_UNSPECIFIED,
		Granularity: billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY,
	})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeReportInvalidDimension, apierrors.CodeOf(err))

	// Invalid granularity.
	_, err = svc.CreateReport(adminCtx(), &billingv1.CreateReportRequest{
		Name: "r", Dimension: billingv1.ReportDimension_REPORT_DIMENSION_MODEL,
		Granularity: billingv1.ReportGranularity_REPORT_GRANULARITY_UNSPECIFIED,
	})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeReportInvalidGranularity, apierrors.CodeOf(err))

	// since > until.
	_, err = svc.CreateReport(adminCtx(), &billingv1.CreateReportRequest{
		Name: "r", Dimension: billingv1.ReportDimension_REPORT_DIMENSION_MODEL,
		Granularity: billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY,
		Since: 2000, Until: 1000,
	})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeMeteringRangeInvalid, apierrors.CodeOf(err))

	// Range > 366 days.
	_, err = svc.CreateReport(adminCtx(), &billingv1.CreateReportRequest{
		Name: "r", Dimension: billingv1.ReportDimension_REPORT_DIMENSION_MODEL,
		Granularity: billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY,
		Since: 1000, Until: 1000 + 400*24*3600,
	})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeMeteringRangeInvalid, apierrors.CodeOf(err))
}

// AC1: CreateReport returns a pending report.
func TestCreateReportPending(t *testing.T) {
	svc := newBillingTestService(t)
	resp, err := svc.CreateReport(adminCtx(), &billingv1.CreateReportRequest{
		Name: "Monthly", Dimension: billingv1.ReportDimension_REPORT_DIMENSION_MODEL,
		Granularity: billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY,
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Report)
	assert.Equal(t, ReportStatusPending, resp.Report.Status)
	assert.Equal(t, billingv1.ReportDimension_REPORT_DIMENSION_MODEL, resp.Report.Dimension)
}

// AC2: GetReport returns the report; an unknown report_id returns 10901.
func TestGetReportNotFound(t *testing.T) {
	svc := newBillingTestService(t)
	_, err := svc.GetReport(adminCtx(), &billingv1.GetReportRequest{ReportId: "nope"})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeReportNotFound, apierrors.CodeOf(err))
}

// AC3: DownloadReport before ready returns 10907.
func TestDownloadReportNotReady(t *testing.T) {
	svc := newBillingTestService(t)
	created, err := svc.CreateReport(adminCtx(), &billingv1.CreateReportRequest{
		Name: "r", Dimension: billingv1.ReportDimension_REPORT_DIMENSION_MODEL,
		Granularity: billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY,
	})
	require.NoError(t, err)
	_, err = svc.DownloadReport(adminCtx(), &billingv1.DownloadReportRequest{ReportId: created.Report.ReportId})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeReportNotReady, apierrors.CodeOf(err))
}

// AC4: CreateSchedule validates frequency (10905) and duplicate name
// (10906); UpdateSchedule/DeleteSchedule work and an unknown schedule_id
// returns 10902.
func TestScheduleLifecycle(t *testing.T) {
	svc := newBillingTestService(t)

	// Invalid frequency.
	_, err := svc.CreateSchedule(adminCtx(), &billingv1.CreateScheduleRequest{
		Name: "s", Dimension: billingv1.ReportDimension_REPORT_DIMENSION_MODEL,
		RelativeRange: billingv1.RelativeRange_RELATIVE_RANGE_LAST_7_DAYS,
		Granularity:   billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY,
		Frequency:     billingv1.ReportFrequency_REPORT_FREQUENCY_UNSPECIFIED,
	})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeReportInvalidFrequency, apierrors.CodeOf(err))

	// Valid create.
	resp, err := svc.CreateSchedule(adminCtx(), &billingv1.CreateScheduleRequest{
		Name: "Weekly", Dimension: billingv1.ReportDimension_REPORT_DIMENSION_MODEL,
		RelativeRange: billingv1.RelativeRange_RELATIVE_RANGE_LAST_7_DAYS,
		Granularity:   billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY,
		Frequency:     billingv1.ReportFrequency_REPORT_FREQUENCY_WEEKLY,
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Schedule)
	assert.Equal(t, ScheduleStatusActive, resp.Schedule.Status)

	// Duplicate name.
	_, err = svc.CreateSchedule(adminCtx(), &billingv1.CreateScheduleRequest{
		Name: "Weekly", Dimension: billingv1.ReportDimension_REPORT_DIMENSION_MODEL,
		RelativeRange: billingv1.RelativeRange_RELATIVE_RANGE_LAST_7_DAYS,
		Granularity:   billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY,
		Frequency:     billingv1.ReportFrequency_REPORT_FREQUENCY_WEEKLY,
	})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeReportNameConflict, apierrors.CodeOf(err))

	// Update.
	upd, err := svc.UpdateSchedule(adminCtx(), &billingv1.UpdateScheduleRequest{
		ScheduleId: resp.Schedule.ScheduleId, Name: "Monthly",
		Frequency: billingv1.ReportFrequency_REPORT_FREQUENCY_MONTHLY,
	})
	require.NoError(t, err)
	assert.Equal(t, "Monthly", upd.Schedule.Name)
	assert.Equal(t, billingv1.ReportFrequency_REPORT_FREQUENCY_MONTHLY, upd.Schedule.Frequency)

	// Unknown schedule id.
	_, err = svc.UpdateSchedule(adminCtx(), &billingv1.UpdateScheduleRequest{ScheduleId: "nope"})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeScheduleNotFound, apierrors.CodeOf(err))

	// Delete.
	_, err = svc.DeleteSchedule(adminCtx(), &billingv1.DeleteScheduleRequest{ScheduleId: resp.Schedule.ScheduleId})
	require.NoError(t, err)
	_, err = svc.DeleteSchedule(adminCtx(), &billingv1.DeleteScheduleRequest{ScheduleId: resp.Schedule.ScheduleId})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeScheduleNotFound, apierrors.CodeOf(err))
}

// AC6: CreateReport on the user prefix is tenant-scoped — the caller's
// org is authoritative and the requested organization_id is ignored.
func TestCreateReportUserTenantScoped(t *testing.T) {
	svc := newBillingTestService(t)
	resp, err := svc.CreateReport(userCtx("org-a"), &billingv1.CreateReportRequest{
		Name: "r", Dimension: billingv1.ReportDimension_REPORT_DIMENSION_MODEL,
		Granularity: billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY,
		// A requested org filter is ignored on the user surface.
		OrganizationId: "org-b",
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Report)

	// The stored report belongs to org-a (the caller's org).
	repo, err := svc.reportRepository()
	require.NoError(t, err)
	stored, err := repo.FindReportByID(context.Background(), resp.Report.ReportId)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "org-a", stored.OrganizationID)
}

// AC2/AC3: GetReport returns the report; DownloadReport returns the CSV
// once ready.
func TestGetAndDownloadReport(t *testing.T) {
	svc := newBillingTestService(t)
	db := svc.repo.db.DB(context.Background())
	ctx := context.Background()

	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC).Unix()
	seedCharge(t, db, "org-a", "key-1", "model-a", day, 10, 20, 0, 0, 1, 1.25, "USD")

	created, err := svc.CreateReport(adminCtx(), &billingv1.CreateReportRequest{
		Name: "r", Dimension: billingv1.ReportDimension_REPORT_DIMENSION_MODEL,
		Granularity: billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY,
		Since: day, Until: day + 86400,
	})
	require.NoError(t, err)

	// Generate the report.
	runner := NewReportGeneratorRunner(svc, time.Second)
	require.NoError(t, runner.RunOnce(ctx))

	// GetReport returns the ready report.
	got, err := svc.GetReport(adminCtx(), &billingv1.GetReportRequest{ReportId: created.Report.ReportId})
	require.NoError(t, err)
	assert.Equal(t, ReportStatusReady, got.Report.Status)
	assert.Equal(t, int64(1), got.Report.RowCount)

	// DownloadReport returns the CSV.
	dl, err := svc.DownloadReport(adminCtx(), &billingv1.DownloadReportRequest{ReportId: created.Report.ReportId})
	require.NoError(t, err)
	assert.Contains(t, dl.Csv, "model-a")
	assert.Contains(t, dl.Filename, ".csv")

	// ListReports returns the history.
	list, err := svc.ListReports(adminCtx(), &billingv1.ListReportsRequest{})
	require.NoError(t, err)
	require.Len(t, list.Reports, 1)
	assert.Equal(t, created.Report.ReportId, list.Reports[0].ReportId)
}

// AC4/AC5: ListSchedules and ListScheduleRuns return the caller's data.
func TestListSchedulesAndRuns(t *testing.T) {
	svc := newBillingTestService(t)
	ctx := context.Background()

	created, err := svc.CreateSchedule(adminCtx(), &billingv1.CreateScheduleRequest{
		Name: "Weekly", Dimension: billingv1.ReportDimension_REPORT_DIMENSION_MODEL,
		RelativeRange: billingv1.RelativeRange_RELATIVE_RANGE_LAST_7_DAYS,
		Granularity:   billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY,
		Frequency:     billingv1.ReportFrequency_REPORT_FREQUENCY_WEEKLY,
	})
	require.NoError(t, err)

	// ListSchedules.
	list, err := svc.ListSchedules(adminCtx(), &billingv1.ListSchedulesRequest{})
	require.NoError(t, err)
	require.Len(t, list.Schedules, 1)
	assert.Equal(t, created.Schedule.ScheduleId, list.Schedules[0].ScheduleId)

	// Run the schedule runner so last_run_at is set, then re-list to
	// cover summarizeSchedule's non-nil LastRunAt branch.
	runner := NewScheduleRunner(svc, time.Minute)
	require.NoError(t, runner.RunOnce(ctx))
	list, err = svc.ListSchedules(adminCtx(), &billingv1.ListSchedulesRequest{})
	require.NoError(t, err)
	require.Len(t, list.Schedules, 1)
	assert.NotZero(t, list.Schedules[0].LastRunAt)

	// List runs (the schedule runner created a run named after the
	// schedule).
	runs, err := svc.ListScheduleRuns(adminCtx(), &billingv1.ListScheduleRunsRequest{ScheduleId: created.Schedule.ScheduleId})
	require.NoError(t, err)
	require.Len(t, runs.Runs, 1)
	assert.Equal(t, "Weekly", runs.Runs[0].Name)

	// Unknown schedule returns 10902.
	_, err = svc.ListScheduleRuns(adminCtx(), &billingv1.ListScheduleRunsRequest{ScheduleId: "nope"})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeScheduleNotFound, apierrors.CodeOf(err))
}

// AC6: the user-surface list RPCs are hard-scoped to the caller's org.
func TestUserSurfaceScopedLists(t *testing.T) {
	svc := newBillingTestService(t)
	db := svc.repo.db.DB(context.Background())
	repo := NewReportRepository(db)
	ctx := context.Background()

	// Create reports for two orgs.
	for _, org := range []string{"org-a", "org-b"} {
		_, err := repo.CreateReport(ctx, &Report{
			OrganizationID: org, Name: "r-" + org, Dimension: ReportDimensionModel,
			Since: 1, Until: 2, Granularity: ReportGranularityDaily,
		})
		require.NoError(t, err)
		_, err = repo.CreateSchedule(ctx, &ReportSchedule{
			OrganizationID: org, Name: "s-" + org, Dimension: ReportDimensionModel,
			RelativeRange: RelativeRangeLast7Days, Granularity: ReportGranularityDaily,
			Frequency: ReportFrequencyDaily,
		})
		require.NoError(t, err)
	}

	// User surface for org-a sees only org-a's reports and schedules.
	list, err := svc.ListReports(userCtx("org-a"), &billingv1.ListReportsRequest{})
	require.NoError(t, err)
	require.Len(t, list.Reports, 1)
	assert.Equal(t, "r-org-a", list.Reports[0].Name)

	scheds, err := svc.ListSchedules(userCtx("org-a"), &billingv1.ListSchedulesRequest{})
	require.NoError(t, err)
	require.Len(t, scheds.Schedules, 1)
	assert.Equal(t, "s-org-a", scheds.Schedules[0].Name)
}

// AC6: a user-surface caller cannot read another org's report.
func TestUserSurfaceCrossOrgDenied(t *testing.T) {
	svc := newBillingTestService(t)
	db := svc.repo.db.DB(context.Background())
	repo := NewReportRepository(db)
	ctx := context.Background()

	// org-b's report.
	created, err := repo.CreateReport(ctx, &Report{
		OrganizationID: "org-b", Name: "r", Dimension: ReportDimensionModel,
		Since: 1, Until: 2, Granularity: ReportGranularityDaily,
	})
	require.NoError(t, err)

	// org-a caller cannot read it.
	_, err = svc.GetReport(userCtx("org-a"), &billingv1.GetReportRequest{ReportId: created.ID})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeReportNotFound, apierrors.CodeOf(err))
}

// AC14: admin org scoping validates the requested org; an unknown org
// returns 10005 (the org guard's not-found code).
func TestCreateReportAdminOrgScoping(t *testing.T) {
	svc := newBillingTestService(t)
	// Migrate the organizations table so the org guard can query it.
	require.NoError(t, tenancy.MigrateSchemaForFVT(svc.repo.db.DB(context.Background())))
	// Wire a real org guard over the test DB (which has no org-b).
	svc.SetOrgGuard(tenancy.NewOrgGuard(svc.repo.db.DB(context.Background())))
	_, err := svc.CreateReport(adminCtx(), &billingv1.CreateReportRequest{
		Name: "r", Dimension: billingv1.ReportDimension_REPORT_DIMENSION_MODEL,
		Granularity: billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY,
		OrganizationId: "org-b",
	})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeOrganizationNotFound, apierrors.CodeOf(err))
}

// fakeSessionUserResolver returns a fixed user id for the admin role
// check.
type fakeSessionUserResolver struct{ user string }

func (f fakeSessionUserResolver) SessionUserID(context.Context) (string, error) {
	return f.user, nil
}

// errSessionUserResolver returns an error from SessionUserID.
type errSessionUserResolver struct{}

func (errSessionUserResolver) SessionUserID(context.Context) (string, error) {
	return "", apierrors.New(apierrors.CodeInternal)
}

// invalidSessionUserResolver returns a session-invalid error, which the
// role check treats as "no session".
type invalidSessionUserResolver struct{}

func (invalidSessionUserResolver) SessionUserID(context.Context) (string, error) {
	return "", apierrors.New(apierrors.CodeSessionInvalid)
}

// fakeSessionOrgResolver returns a fixed org for the user binding.
type fakeSessionOrgResolver struct{ org string }

func (f fakeSessionOrgResolver) SessionActiveOrg(context.Context) (string, error) {
	return f.org, nil
}

// fakeRoleGuard enforces a minimum role; it returns CodeForbidden when
// the caller is not allowed.
type fakeRoleGuard struct {
	allowed bool
}

func (f fakeRoleGuard) RequireRole(_ context.Context, _, _, _ string) error {
	if !f.allowed {
		return apierrors.New(apierrors.CodeForbidden)
	}
	return nil
}

// AC14: the admin billing-report RPCs are gated by the caller's org
// role. A non-member session receives 10036; a member succeeds. The
// user surface is tenant-scoped and never role-gated.
func TestAdminReportRoleGuard(t *testing.T) {
	svc := newBillingTestService(t)
	svc.SetSessionUserResolver(fakeSessionUserResolver{user: "user-1"})
	svc.SetSessionOrgResolver(fakeSessionOrgResolver{org: "org-a"})

	// Non-member: the role guard denies the admin RPCs with 10036.
	svc.SetRoleGuard(fakeRoleGuard{allowed: false})
	_, err := svc.ListReports(adminCtx(), &billingv1.ListReportsRequest{})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeForbidden, apierrors.CodeOf(err))

	_, err = svc.CreateReport(adminCtx(), &billingv1.CreateReportRequest{
		Name: "r", Dimension: billingv1.ReportDimension_REPORT_DIMENSION_MODEL,
		Granularity: billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY,
	})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeForbidden, apierrors.CodeOf(err))

	_, err = svc.ListSchedules(adminCtx(), &billingv1.ListSchedulesRequest{})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeForbidden, apierrors.CodeOf(err))

	// Member: the admin RPCs succeed.
	svc.SetRoleGuard(fakeRoleGuard{allowed: true})
	resp, err := svc.ListReports(adminCtx(), &billingv1.ListReportsRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// The user surface is never role-gated: a non-member caller on the
	// user prefix is not denied by the role guard.
	svc.SetRoleGuard(fakeRoleGuard{allowed: false})
	_, err = svc.ListReports(userCtx("org-a"), &billingv1.ListReportsRequest{})
	require.NoError(t, err)
}

// AC14: the admin role check is a no-op when the guard is not wired, the
// org context is empty, or no session is present (transitional path).
func TestAdminReportRoleGuardNoop(t *testing.T) {
	svc := newBillingTestService(t)

	// No role guard wired: the admin RPCs are not role-gated.
	_, err := svc.ListReports(adminCtx(), &billingv1.ListReportsRequest{})
	require.NoError(t, err)

	// Role guard wired but no session user resolver: no session, so the
	// check is skipped.
	svc.SetRoleGuard(fakeRoleGuard{allowed: false})
	_, err = svc.ListReports(adminCtx(), &billingv1.ListReportsRequest{})
	require.NoError(t, err)

	// Session user resolver wired but no session org: the org context is
	// empty, so the check is skipped.
	svc.SetSessionUserResolver(fakeSessionUserResolver{user: "user-1"})
	_, err = svc.ListReports(adminCtx(), &billingv1.ListReportsRequest{})
	require.NoError(t, err)

	// A session-user resolver error propagates.
	svc.SetSessionUserResolver(errSessionUserResolver{})
	svc.SetSessionOrgResolver(fakeSessionOrgResolver{org: "org-a"})
	_, err = svc.ListReports(adminCtx(), &billingv1.ListReportsRequest{})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeInternal, apierrors.CodeOf(err))

	// A session-invalid error is treated as "no session": the check is
	// skipped.
	svc.SetSessionUserResolver(invalidSessionUserResolver{})
	_, err = svc.ListReports(adminCtx(), &billingv1.ListReportsRequest{})
	require.NoError(t, err)
}
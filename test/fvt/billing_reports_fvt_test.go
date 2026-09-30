package fvt

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/config"
	"github.com/go-taas/go-taas/pkg/grpcmiddleware"
	"github.com/go-taas/go-taas/pkg/server"
	billingv1 "github.com/go-taas/go-taas/proto/taas/billing/v1"
	"github.com/go-taas/go-taas/services/billing"
	"github.com/go-taas/go-taas/services/tenancy"
)

// billingReportsEnv is the in-process stack for the billing-reports
// feature: the billing service over one gRPC server with the gateway mux
// in front (including the path-metadata annotator so the surface binding
// works), a shared file-backed SQLite database, and the organizations
// table for the org guard.
type billingReportsEnv struct {
	db    *gorm.DB
	gwSrv *httptest.Server

	svc *billing.Service
}

func newBillingReportsEnv(t *testing.T) *billingReportsEnv {
	t.Helper()

	cfg := &config.Configuration{}
	cfg.Billing.Currency = "USD"
	config.SetConfigForTest(cfg)
	t.Cleanup(func() { config.SetConfigForTest(&config.Configuration{}) })

	dbPath := filepath.Join(t.TempDir(), "billing_reports.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, billing.MigrateSchemaForFVT(db))
	require.NoError(t, tenancy.MigrateSchemaForFVT(db))
	for _, orgID := range []string{"org-fvt", "org-a", "org-b"} {
		require.NoError(t, db.Create(&tenancy.Organization{
			ID: orgID, DisplayName: orgID, State: tenancy.StateActive,
		}).Error)
	}
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcSrv := grpc.NewServer(grpc.ChainUnaryInterceptor(grpcmiddleware.UnaryServerInterceptor()))
	t.Cleanup(grpcSrv.Stop)

	svc := billing.NewForFVT(db, nil)
	svc.SetOrgGuard(tenancy.NewOrgGuard(db))
	billingv1.RegisterBillingServiceServer(grpcSrv, svc)
	go func() { _ = grpcSrv.Serve(ln) }()

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	mux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(server.FVTHeaderMatcher),
		runtime.WithMetadata(server.PathMetadataAnnotator),
	)
	require.NoError(t, billingv1.RegisterBillingServiceHandler(context.Background(), mux, conn))

	gwSrv := httptest.NewServer(mux)
	t.Cleanup(gwSrv.Close)

	return &billingReportsEnv{db: db, gwSrv: gwSrv, svc: svc}
}

// call issues a JSON request against the gateway and decodes the body.
func (e *billingReportsEnv) call(t *testing.T, method, path string, body any, org string) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.gwSrv.URL+path, rdr)
	require.NoError(t, err)
	if org != "" {
		req.Header.Set("X-Organization-Id", org)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return resp.StatusCode, out
}

// seedCharge inserts a charge record directly into the shared DB.
func (e *billingReportsEnv) seedCharge(t *testing.T, orgID, keyID, modelID string, periodStart int64, prompt, completion int64, amount float64) {
	t.Helper()
	row := billing.ChargeRecord{
		ID:               uuid.NewString(),
		OrganizationID:   orgID,
		APIKeyID:         keyID,
		ModelID:          modelID,
		AcceleratorType:  "default",
		PeriodStart:      periodStart,
		PeriodEnd:        periodStart + 3600,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CachedTokens:     0,
		ReasoningTokens:  0,
		RequestCount:     1,
		Amount:           amount,
		Currency:         "USD",
		TierIndex:        -1,
		Priced:           true,
	}
	require.NoError(t, e.db.Create(&row).Error)
}

// generate runs the report generator once so pending reports become
// ready.
func (e *billingReportsEnv) generate(t *testing.T) {
	t.Helper()
	runner := billing.NewReportGeneratorRunner(e.svc, time.Second)
	require.NoError(t, runner.RunOnce(context.Background()))
}

// AC1: CreateReport returns a pending report; a range > 366 days returns
// 10404; an invalid dimension returns 10903.
func TestFVTReportCreateAndValidation(t *testing.T) {
	env := newBillingReportsEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC).Unix()

	// Valid create on the admin surface.
	code, body := env.call(t, "POST", "/api/v1/admin/billing/reports", map[string]any{
		"name": "Monthly", "dimension": "REPORT_DIMENSION_MODEL",
		"granularity": "REPORT_GRANULARITY_DAILY",
		"since":       day, "until": day + 86400,
	}, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	report := body["report"].(map[string]any)
	assert.Equal(t, "pending", report["status"])

	// Range > 366 days -> 10404.
	code, body = env.call(t, "POST", "/api/v1/admin/billing/reports", map[string]any{
		"name": "r", "dimension": "REPORT_DIMENSION_MODEL",
		"granularity": "REPORT_GRANULARITY_DAILY",
		"since":       1000, "until": 1000 + 400*24*3600,
	}, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(10404), body["code"])

	// Invalid dimension -> 10903.
	code, body = env.call(t, "POST", "/api/v1/admin/billing/reports", map[string]any{
		"name": "r", "dimension": "REPORT_DIMENSION_UNSPECIFIED",
		"granularity": "REPORT_GRANULARITY_DAILY",
	}, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(10903), body["code"])
}

// AC2/AC3: GetReport returns the report; DownloadReport returns the BOM
// CSV once ready and 10907 before ready; an unknown report_id returns
// 10901.
func TestFVTReportLifecycleAndDownload(t *testing.T) {
	env := newBillingReportsEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC).Unix()
	env.seedCharge(t, "org-fvt", "key-1", "model-a", day, 10, 20, 1.25)
	env.seedCharge(t, "org-fvt", "key-2", "model-b", day, 100, 200, 3.00)

	// Create.
	code, body := env.call(t, "POST", "/api/v1/admin/billing/reports", map[string]any{
		"name": "Monthly", "dimension": "REPORT_DIMENSION_MODEL",
		"granularity": "REPORT_GRANULARITY_DAILY",
		"since":       day, "until": day + 86400,
	}, "org-fvt")
	require.Equal(t, 200, code)
	reportID := body["report"].(map[string]any)["reportId"].(string)

	// Download before ready -> 10907.
	code, body = env.call(t, "GET", "/api/v1/admin/billing/reports/"+reportID+"/download", nil, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(10907), body["code"])

	// Generate.
	env.generate(t)

	// GetReport returns ready with row count.
	code, body = env.call(t, "GET", "/api/v1/admin/billing/reports/"+reportID, nil, "org-fvt")
	require.Equal(t, 200, code)
	report := body["report"].(map[string]any)
	assert.Equal(t, "ready", report["status"])
	assert.Equal(t, "2", report["rowCount"])

	// Download returns the BOM CSV.
	code, body = env.call(t, "GET", "/api/v1/admin/billing/reports/"+reportID+"/download", nil, "org-fvt")
	require.Equal(t, 200, code)
	csv := body["csv"].(string)
	assert.Contains(t, csv, "\xEF\xBB\xBF")
	assert.Contains(t, csv, "model-a")
	assert.Contains(t, csv, "model-b")

	// Unknown report -> 10901.
	code, body = env.call(t, "GET", "/api/v1/admin/billing/reports/nope", nil, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(10901), body["code"])
}

// AC4/AC5: CreateSchedule/UpdateSchedule/DeleteSchedule and
// ListScheduleRuns; duplicate name returns 10906, invalid frequency
// returns 10905, unknown schedule returns 10902.
func TestFVTScheduleLifecycle(t *testing.T) {
	env := newBillingReportsEnv(t)

	// Invalid frequency -> 10905.
	code, body := env.call(t, "POST", "/api/v1/admin/billing/reports/schedules", map[string]any{
		"name": "s", "dimension": "REPORT_DIMENSION_MODEL",
		"relativeRange": "RELATIVE_RANGE_LAST_7_DAYS",
		"granularity":   "REPORT_GRANULARITY_DAILY",
		"frequency":     "REPORT_FREQUENCY_UNSPECIFIED",
	}, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(10905), body["code"])

	// Valid create.
	code, body = env.call(t, "POST", "/api/v1/admin/billing/reports/schedules", map[string]any{
		"name": "Weekly", "dimension": "REPORT_DIMENSION_MODEL",
		"relativeRange": "RELATIVE_RANGE_LAST_7_DAYS",
		"granularity":   "REPORT_GRANULARITY_DAILY",
		"frequency":     "REPORT_FREQUENCY_WEEKLY",
	}, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	scheduleID := body["schedule"].(map[string]any)["scheduleId"].(string)
	assert.Equal(t, "active", body["schedule"].(map[string]any)["status"])

	// Duplicate name -> 10906.
	code, body = env.call(t, "POST", "/api/v1/admin/billing/reports/schedules", map[string]any{
		"name": "Weekly", "dimension": "REPORT_DIMENSION_MODEL",
		"relativeRange": "RELATIVE_RANGE_LAST_7_DAYS",
		"granularity":   "REPORT_GRANULARITY_DAILY",
		"frequency":     "REPORT_FREQUENCY_WEEKLY",
	}, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(10906), body["code"])

	// Update.
	code, body = env.call(t, "PATCH", "/api/v1/admin/billing/reports/schedules/"+scheduleID, map[string]any{
		"name": "Monthly", "frequency": "REPORT_FREQUENCY_MONTHLY",
	}, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	assert.Equal(t, "Monthly", body["schedule"].(map[string]any)["name"])

	// Run the schedule runner to create a run.
	runner := billing.NewScheduleRunner(env.svc, time.Minute)
	require.NoError(t, runner.RunOnce(context.Background()))

	// ListScheduleRuns.
	code, body = env.call(t, "GET", "/api/v1/admin/billing/reports/schedules/"+scheduleID+"/runs", nil, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	runs := body["runs"].([]any)
	require.Len(t, runs, 1)

	// Unknown schedule -> 10902.
	code, body = env.call(t, "GET", "/api/v1/admin/billing/reports/schedules/nope/runs", nil, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(10902), body["code"])

	// Delete.
	code, body = env.call(t, "DELETE", "/api/v1/admin/billing/reports/schedules/"+scheduleID, nil, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
}

// AC6: the user surface is tenant-scoped — a user-surface report
// aggregates only the caller's org.
func TestFVTReportUserTenantScoped(t *testing.T) {
	env := newBillingReportsEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC).Unix()
	env.seedCharge(t, "org-a", "key-1", "model-a", day, 10, 20, 1.25)
	env.seedCharge(t, "org-b", "key-2", "model-b", day, 100, 200, 3.00)

	// User surface for org-a.
	code, body := env.call(t, "POST", "/api/v1/billing/reports", map[string]any{
		"name": "r", "dimension": "REPORT_DIMENSION_MODEL",
		"granularity": "REPORT_GRANULARITY_DAILY",
		"since":       day, "until": day + 86400,
	}, "org-a")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	reportID := body["report"].(map[string]any)["reportId"].(string)

	env.generate(t)

	// The report contains only org-a's model-a.
	code, body = env.call(t, "GET", "/api/v1/billing/reports/"+reportID+"/download", nil, "org-a")
	require.Equal(t, 200, code)
	csv := body["csv"].(string)
	assert.Contains(t, csv, "model-a")
	assert.NotContains(t, csv, "model-b")
}
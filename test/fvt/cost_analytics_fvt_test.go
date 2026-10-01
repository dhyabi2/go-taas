package fvt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// costEnv is the in-process stack for the cost-analytics feature: the
// billing service over one gRPC server with the gateway mux in front, a
// shared file-backed SQLite database, and the organizations table.
type costEnv struct {
	db    *gorm.DB
	gwSrv *httptest.Server

	svc *billing.Service
}

func newCostEnv(t *testing.T) *costEnv {
	t.Helper()

	cfg := &config.Configuration{}
	cfg.Billing.Currency = "USD"
	config.SetConfigForTest(cfg)
	t.Cleanup(func() { config.SetConfigForTest(&config.Configuration{}) })

	dbPath := filepath.Join(t.TempDir(), "cost.db")
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

	return &costEnv{db: db, gwSrv: gwSrv, svc: svc}
}

// call issues a JSON request against the gateway and decodes the body.
func (e *costEnv) call(t *testing.T, method, path string, body any, org string) (int, map[string]any) {
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

// seedCostCharge inserts a charge record directly into the shared DB.
func (e *costEnv) seedCostCharge(t *testing.T, orgID, keyID, modelID string, periodStart int64, amount float64, prompt, completion int64) {
	t.Helper()
	row := billing.ChargeRecord{
		ID:               uuid.NewString(),
		OrganizationID:   orgID,
		APIKeyID:         keyID,
		ModelID:          modelID,
		AcceleratorType:  "A800",
		PeriodStart:      periodStart,
		PeriodEnd:        periodStart + 3600,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		RequestCount:     1,
		Amount:           amount,
		Currency:         "USD",
		Priced:           true,
	}
	require.NoError(t, e.db.Create(&row).Error)
}

// AC1: GetCostAnalyticsOverview with a valid range returns cards,
// breakdown and a series; a range > 92 days returns 10404.
func TestFVTCostAnalyticsOverview(t *testing.T) {
	env := newCostEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	env.seedCostCharge(t, "org-fvt", fvtKey1, fvtModelA, day.Unix(), 1.5, 10, 20)
	env.seedCostCharge(t, "org-fvt", fvtKey2, fvtModelB, day.Unix(), 2.5, 5, 10)

	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/cost?since=%d&until=%d", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	cards := body["cards"].(map[string]any)
	assert.Equal(t, "400", cards["totalCostCents"])
	breakdown := body["breakdown"].([]any)
	require.Len(t, breakdown, 1)

	// Range > 92 days -> 10404.
	code, body = env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/cost?since=%d&until=%d", day.Add(-93*24*time.Hour).Unix(), day.Unix()),
		nil, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(10404), body["code"])
}

// AC2: an unsupported dimension returns 11301.
func TestFVTCostAnalyticsInvalidDimension(t *testing.T) {
	env := newCostEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/cost?since=%d&until=%d&dimension=bogus", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(11301), body["code"])
}

// AC3: GetCostAnalytics (admin) returns single-dimension-value cards and
// a trend; an unknown dimension value returns 11302.
func TestFVTCostAnalyticsGet(t *testing.T) {
	env := newCostEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	env.seedCostCharge(t, "org-fvt", fvtKey1, fvtModelA, day.Unix(), 1.5, 10, 20)

	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/cost/model/%s?since=%d&until=%d", fvtModelA, day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	cards := body["cards"].(map[string]any)
	assert.Equal(t, "150", cards["totalCostCents"])

	// Unknown dimension value -> 11302.
	code, body = env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/cost/model/nope?since=%d&until=%d", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(11302), body["code"])
}

// AC10: GetCostAnalyticsOverview (user) returns only the caller's
// organization's cost cards, breakdown and trend.
func TestFVTCostAnalyticsOverviewUserScoped(t *testing.T) {
	env := newCostEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	env.seedCostCharge(t, "org-a", fvtKey1, fvtModelA, day.Unix(), 1.5, 10, 20)
	env.seedCostCharge(t, "org-a", fvtKey2, fvtModelB, day.Unix(), 2.5, 5, 10)
	env.seedCostCharge(t, "org-b", fvtKey2, fvtModelA, day.Unix(), 3.5, 20, 40)

	// Caller org-a sees only its own cost.
	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/cost?since=%d&until=%d", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-a")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	cards := body["cards"].(map[string]any)
	assert.Equal(t, "400", cards["totalCostCents"])
	breakdown := body["breakdown"].([]any)
	require.Len(t, breakdown, 1)
	assert.Equal(t, "org-a", breakdown[0].(map[string]any)["dimensionValue"])
}

// AC4: GetCostAnalytics (user) returns only the caller's organization's
// cost.
func TestFVTCostAnalyticsUserScoped(t *testing.T) {
	env := newCostEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	env.seedCostCharge(t, "org-a", fvtKey1, fvtModelA, day.Unix(), 1.5, 10, 20)
	env.seedCostCharge(t, "org-b", fvtKey2, fvtModelB, day.Unix(), 2.5, 5, 10)

	// Caller org-a sees only its own cost.
	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/cost/model/%s?since=%d&until=%d", fvtModelA, day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-a")
	require.Equal(t, 200, code)
	cards := body["cards"].(map[string]any)
	assert.Equal(t, "150", cards["totalCostCents"])

	// Caller org-a cannot see org-b's model.
	code, body = env.call(t, "GET",
		fmt.Sprintf("/api/v1/cost/model/%s?since=%d&until=%d", fvtModelB, day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-a")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(11302), body["code"])
}
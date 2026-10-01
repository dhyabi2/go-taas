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

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/grpcmiddleware"
	"github.com/go-taas/go-taas/pkg/server"
	meteringv1 "github.com/go-taas/go-taas/proto/taas/metering/v1"
	"github.com/go-taas/go-taas/services/metering"
	"github.com/go-taas/go-taas/services/tenancy"
)

// usageKeysEnv is the in-process stack for the usage-keys and
// error-analysis features: the metering service over one gRPC server with
// the gateway mux in front, a shared file-backed SQLite database, and the
// api_keys table for name resolution.
type usageKeysEnv struct {
	db    *gorm.DB
	gwSrv *httptest.Server

	svc *metering.Service
}

func newUsageKeysEnv(t *testing.T) *usageKeysEnv {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "usagekeys.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, metering.MigrateSchemaForFVT(db))
	require.NoError(t, tenancy.MigrateSchemaForFVT(db))
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS api_keys (id TEXT PRIMARY KEY, name TEXT)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS charge_records (id TEXT PRIMARY KEY, organization_id TEXT, api_key_id TEXT, model_id TEXT, period_start INTEGER, amount REAL, priced BOOLEAN)`).Error)
	for _, orgID := range []string{"org-fvt", "org-a", "org-b"} {
		require.NoError(t, db.Create(&tenancy.Organization{
			ID: orgID, DisplayName: orgID, State: tenancy.StateActive,
		}).Error)
	}
	require.NoError(t, db.Exec(`INSERT INTO api_keys (id, name) VALUES ('`+fvtKey1+`', 'Key One'), ('`+fvtKey2+`', 'Key Two')`).Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcSrv := grpc.NewServer(grpc.ChainUnaryInterceptor(grpcmiddleware.UnaryServerInterceptor()))
	t.Cleanup(grpcSrv.Stop)

	svc := metering.NewForFVT(db, nil)
	meteringv1.RegisterMeteringServiceServer(grpcSrv, svc)
	go func() { _ = grpcSrv.Serve(ln) }()

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	mux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(server.FVTHeaderMatcher),
		runtime.WithMetadata(server.PathMetadataAnnotator),
	)
	require.NoError(t, meteringv1.RegisterMeteringServiceHandler(context.Background(), mux, conn))

	gwSrv := httptest.NewServer(mux)
	t.Cleanup(gwSrv.Close)

	return &usageKeysEnv{db: db, gwSrv: gwSrv, svc: svc}
}

// call issues a JSON request against the gateway and decodes the body.
func (e *usageKeysEnv) call(t *testing.T, method, path string, body any, org string) (int, map[string]any) {
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

// seedUsageLog inserts a request-log row directly into the shared DB.
// errCode is the error code recorded for error-status rows ("" for
// success).
func (e *usageKeysEnv) seedUsageLog(t *testing.T, orgID, keyID, modelID string, at time.Time, latencyMs int64, status string, prompt, completion int64, errCode ...string) {
	t.Helper()
	code := ""
	if len(errCode) > 0 {
		code = errCode[0]
	}
	row := metering.RequestLog{
		ID:               uuid.NewString(),
		RequestID:        uuid.NewString(),
		OrganizationID:   orgID,
		APIKeyID:         keyID,
		ModelID:          modelID,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		LatencyMs:        latencyMs,
		Status:           status,
		Error:            code,
		CreatedAt:        at.UTC(),
	}
	require.NoError(t, e.db.Create(&row).Error)
}

// seedCharge inserts a charge record directly into the shared DB.
func (e *usageKeysEnv) seedCharge(t *testing.T, orgID, keyID string, periodStart int64, amount float64) {
	t.Helper()
	require.NoError(t, e.db.Exec(`INSERT INTO charge_records (id, organization_id, api_key_id, model_id, period_start, amount, priced) VALUES (?, ?, ?, 'm1', ?, ?, 1)`,
		uuid.NewString(), orgID, keyID, periodStart, amount).Error)
}

// AC1: GetUsageKeysOverview with a valid range returns cards, keys, top
// keys and a series; a range > 92 days returns 10404.
func TestFVTUsageKeysOverview(t *testing.T) {
	env := newUsageKeysEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	env.seedUsageLog(t, "org-fvt", fvtKey1, fvtModelA, day, 100, "success", 10, 20)
	env.seedUsageLog(t, "org-fvt", fvtKey2, fvtModelB, day, 50, "success", 5, 10)
	env.seedCharge(t, "org-fvt", fvtKey1, day.Unix(), 1.5)
	env.seedCharge(t, "org-fvt", fvtKey2, day.Unix(), 2.5)

	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/usage/keys?since=%d&until=%d", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	cards := body["cards"].(map[string]any)
	assert.Equal(t, "2", cards["requestCount"])
	keys := body["keys"].([]any)
	require.Len(t, keys, 2)
	topKeys := body["topKeys"].([]any)
	require.Len(t, topKeys, 2)
	// Top keys sorted by cost descending: key2 (250) before key1 (150).
	assert.Equal(t, fvtKey2, topKeys[0].(map[string]any)["apiKeyId"])

	// Range > 92 days -> 10404.
	code, body = env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/usage/keys?since=%d&until=%d", day.Add(-93*24*time.Hour).Unix(), day.Unix()),
		nil, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(10404), body["code"])
}

// AC10: GetUsageKeysOverview (user) returns only the caller's
// organization's cards, top keys, table and trend.
func TestFVTUsageKeysOverviewUserScoped(t *testing.T) {
	env := newUsageKeysEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	env.seedUsageLog(t, "org-a", fvtKey1, fvtModelA, day, 100, "success", 10, 20)
	env.seedUsageLog(t, "org-a", fvtKey2, fvtModelB, day, 50, "success", 5, 10)
	env.seedUsageLog(t, "org-b", fvtKey1, fvtModelA, day, 200, "success", 20, 40)
	env.seedCharge(t, "org-a", fvtKey1, day.Unix(), 1.5)
	env.seedCharge(t, "org-a", fvtKey2, day.Unix(), 2.5)

	// Caller org-a sees only its own keys' usage and cost.
	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/usage/keys?since=%d&until=%d", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-a")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	cards := body["cards"].(map[string]any)
	assert.Equal(t, "2", cards["requestCount"])
	keys := body["keys"].([]any)
	require.Len(t, keys, 2)
	topKeys := body["topKeys"].([]any)
	require.Len(t, topKeys, 2)
	// Top keys sorted by cost descending: key2 (250) before key1 (150).
	assert.Equal(t, fvtKey2, topKeys[0].(map[string]any)["apiKeyId"])
}

// AC3: GetUsageKeys (admin) returns single-key cards and a per-key trend;
// an unknown api_key_id returns 11201.
func TestFVTUsageKeysGet(t *testing.T) {
	env := newUsageKeysEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	env.seedUsageLog(t, "org-fvt", fvtKey1, fvtModelA, day, 100, "success", 10, 20)
	env.seedCharge(t, "org-fvt", fvtKey1, day.Unix(), 1.5)

	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/usage/keys/%s?since=%d&until=%d", fvtKey1, day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	cards := body["cards"].(map[string]any)
	assert.Equal(t, "1", cards["requestCount"])
	assert.Equal(t, "150", cards["totalCostCents"])

	// Unknown key -> 11201.
	code, body = env.call(t, "GET", "/api/v1/admin/usage/keys/nope", nil, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(11201), body["code"])
}

// AC4: GetUsageKeys (user) returns only the caller's organization's usage
// of the key.
func TestFVTUsageKeysUserScoped(t *testing.T) {
	env := newUsageKeysEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	env.seedUsageLog(t, "org-a", fvtKey1, fvtModelA, day, 100, "success", 10, 20)
	env.seedUsageLog(t, "org-b", fvtKey2, fvtModelB, day, 50, "success", 5, 10)

	// Caller org-a sees only its own key's usage.
	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/usage/keys/%s?since=%d&until=%d", fvtKey1, day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-a")
	require.Equal(t, 200, code)
	cards := body["cards"].(map[string]any)
	assert.Equal(t, "1", cards["requestCount"])

	// Caller org-a cannot see org-b's key.
	code, body = env.call(t, "GET",
		fmt.Sprintf("/api/v1/usage/keys/%s?since=%d&until=%d", fvtKey2, day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-a")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(11201), body["code"])
}

// AC1: GetErrorAnalysisOverview with a valid range returns cards, causes
// and a series; a range > 92 days returns 10404.
func TestFVTErrorAnalysisOverview(t *testing.T) {
	env := newUsageKeysEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	env.seedUsageLog(t, "org-fvt", fvtKey1, fvtModelA, day, 100, "error", 10, 20, "rate_limit_exceeded")
	env.seedUsageLog(t, "org-fvt", fvtKey1, fvtModelA, day, 100, "error", 10, 20, "rate_limit_exceeded")
	env.seedUsageLog(t, "org-fvt", fvtKey1, fvtModelA, day, 100, "success", 10, 20)

	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/errors?since=%d&until=%d", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	cards := body["cards"].(map[string]any)
	assert.Equal(t, "2", cards["errorCount"])
	assert.Equal(t, "3", cards["requestCount"])
	causes := body["causes"].([]any)
	require.Len(t, causes, 1)

	// Range > 92 days -> 10404.
	code, body = env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/errors?since=%d&until=%d", day.Add(-93*24*time.Hour).Unix(), day.Unix()),
		nil, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(10404), body["code"])
}

// AC10: GetErrorAnalysisOverview (user) returns only the caller's
// organization's cards, causes and trend.
func TestFVTErrorAnalysisOverviewUserScoped(t *testing.T) {
	env := newUsageKeysEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	env.seedUsageLog(t, "org-a", fvtKey1, fvtModelA, day, 100, "error", 10, 20, "rate_limit_exceeded")
	env.seedUsageLog(t, "org-a", fvtKey1, fvtModelA, day, 100, "error", 10, 20, "timeout")
	env.seedUsageLog(t, "org-b", fvtKey2, fvtModelB, day, 100, "error", 10, 20, "rate_limit_exceeded")

	// Caller org-a sees only its own errors.
	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/errors?since=%d&until=%d", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-a")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	cards := body["cards"].(map[string]any)
	assert.Equal(t, "2", cards["errorCount"])
	assert.Equal(t, "2", cards["requestCount"])
	causes := body["causes"].([]any)
	require.Len(t, causes, 2)
}

// AC3: GetErrorAnalysis (admin) returns single-error-code cards and a
// trend; an unknown error code returns 11501.
func TestFVTErrorAnalysisGet(t *testing.T) {
	env := newUsageKeysEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	env.seedUsageLog(t, "org-fvt", fvtKey1, fvtModelA, day, 100, "error", 10, 20, "rate_limit_exceeded")

	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/errors/rate_limit_exceeded?since=%d&until=%d", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	cards := body["cards"].(map[string]any)
	assert.Equal(t, "1", cards["errorCount"])

	// Unknown error code -> 11501.
	code, body = env.call(t, "GET", "/api/v1/admin/errors/nope", nil, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(11501), body["code"])
}

// AC4: GetErrorAnalysis (user) returns only the caller's organization's
// errors.
func TestFVTErrorAnalysisUserScoped(t *testing.T) {
	env := newUsageKeysEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	env.seedUsageLog(t, "org-a", fvtKey1, fvtModelA, day, 100, "error", 10, 20, "rate_limit_exceeded")
	env.seedUsageLog(t, "org-b", fvtKey2, fvtModelB, day, 100, "error", 10, 20, "rate_limit_exceeded")

	// Caller org-a sees only its own errors.
	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/errors/rate_limit_exceeded?since=%d&until=%d", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-a")
	require.Equal(t, 200, code)
	cards := body["cards"].(map[string]any)
	assert.Equal(t, "1", cards["errorCount"])
}
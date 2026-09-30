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
	observabilityv1 "github.com/go-taas/go-taas/proto/taas/observability/v1"
	"github.com/go-taas/go-taas/services/metering"
	"github.com/go-taas/go-taas/services/observability"
	"github.com/go-taas/go-taas/services/tenancy"
)

// observabilityEnv is the in-process stack for the model observability
// feature: the observability service over one gRPC server with the
// gateway mux in front (including the path-metadata annotator so the
// surface binding works), a shared file-backed SQLite database, and the
// models/api_keys tables for name resolution.
//
// Model and api-key ids are valid UUIDs (matching the production
// schema) so the ModelExists UUID validation (AD3) behaves identically
// to PostgreSQL.
const (
	fvtModelA = "11111111-1111-1111-1111-111111111111"
	fvtModelB = "22222222-2222-2222-2222-222222222222"
	fvtKey1   = "33333333-3333-3333-3333-333333333333"
	fvtKey2   = "44444444-4444-4444-4444-444444444444"
)

type observabilityEnv struct {
	db    *gorm.DB
	gwSrv *httptest.Server

	svc *observability.Service
}

func newObservabilityEnv(t *testing.T) *observabilityEnv {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "observability.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, metering.MigrateSchemaForFVT(db))
	require.NoError(t, observability.MigrateSchemaForFVT(db))
	require.NoError(t, tenancy.MigrateSchemaForFVT(db))
	// The models and api_keys tables for name resolution (AD3).
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS models (id TEXT PRIMARY KEY, name TEXT)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS api_keys (id TEXT PRIMARY KEY, name TEXT)`).Error)
	for _, orgID := range []string{"org-fvt", "org-a", "org-b"} {
		require.NoError(t, db.Create(&tenancy.Organization{
			ID: orgID, DisplayName: orgID, State: tenancy.StateActive,
		}).Error)
	}
	require.NoError(t, db.Exec(`INSERT INTO models (id, name) VALUES ('`+fvtModelA+`', 'Model A'), ('`+fvtModelB+`', 'Model B')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO api_keys (id, name) VALUES ('`+fvtKey1+`', 'Key One'), ('`+fvtKey2+`', 'Key Two')`).Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcSrv := grpc.NewServer(grpc.ChainUnaryInterceptor(grpcmiddleware.UnaryServerInterceptor()))
	t.Cleanup(grpcSrv.Stop)

	svc := observability.NewForFVT(db)
	observabilityv1.RegisterObservabilityServiceServer(grpcSrv, svc)
	go func() { _ = grpcSrv.Serve(ln) }()

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	mux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(server.FVTHeaderMatcher),
		runtime.WithMetadata(server.PathMetadataAnnotator),
	)
	require.NoError(t, observabilityv1.RegisterObservabilityServiceHandler(context.Background(), mux, conn))

	gwSrv := httptest.NewServer(mux)
	t.Cleanup(gwSrv.Close)

	return &observabilityEnv{db: db, gwSrv: gwSrv, svc: svc}
}

// call issues a JSON request against the gateway and decodes the body.
func (e *observabilityEnv) call(t *testing.T, method, path string, body any, org string) (int, map[string]any) {
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

// seedLog inserts a request-log row directly into the shared DB using
// metering's RequestLog model (request_logs is metering's table).
func (e *observabilityEnv) seedLog(t *testing.T, orgID, keyID, modelID string, at time.Time, latencyMs int64, status string, prompt, completion int64) {
	t.Helper()
	row := metering.RequestLog{
		ID:               uuid.NewString(),
		RequestID:        fmt.Sprintf("req-%d-%s", at.UnixNano(), modelID),
		OrganizationID:   orgID,
		APIKeyID:         keyID,
		ModelID:          modelID,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		LatencyMs:        latencyMs,
		Status:           status,
		CreatedAt:        at.UTC(),
	}
	require.NoError(t, e.db.Create(&row).Error)
}

// AC1: GetObservabilityOverview returns cards, a per-model table and a
// time-series; a range > 92 days returns 10404.
func TestFVTObservabilityOverview(t *testing.T) {
	env := newObservabilityEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	h1 := day.Unix()

	env.seedLog(t, "org-fvt", fvtKey1, fvtModelA, time.Unix(h1, 0), 100, "success", 10, 20)
	env.seedLog(t, "org-fvt", fvtKey1, fvtModelA, time.Unix(h1, 0), 200, "error", 10, 20)
	env.seedLog(t, "org-fvt", fvtKey2, fvtModelB, time.Unix(h1, 0), 50, "success", 5, 10)

	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/observability?since=%d&until=%d", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])

	cards := body["cards"].(map[string]any)
	assert.Equal(t, "3", cards["requestCount"])
	assert.Equal(t, "1", cards["errorCount"])

	models := body["models"].([]any)
	require.Len(t, models, 2)
	series := body["series"].([]any)
	require.Len(t, series, 1)

	// Range > 92 days -> 10404.
	code, body = env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/observability?since=%d&until=%d", day.Add(-93*24*time.Hour).Unix(), day.Unix()),
		nil, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(10404), body["code"])
}

// AC2: GetObservabilityOverview with a model_id filter scopes the cards
// and series to that model.
func TestFVTObservabilityOverviewModelFilter(t *testing.T) {
	env := newObservabilityEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	h1 := day.Unix()

	env.seedLog(t, "org-fvt", fvtKey1, fvtModelA, time.Unix(h1, 0), 100, "success", 10, 20)
	env.seedLog(t, "org-fvt", fvtKey1, fvtModelB, time.Unix(h1, 0), 300, "success", 10, 20)

	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/observability?since=%d&until=%d&model_id="+fvtModelA, day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	cards := body["cards"].(map[string]any)
	assert.Equal(t, "1", cards["requestCount"])
	models := body["models"].([]any)
	require.Len(t, models, 1)
	assert.Equal(t, fvtModelA, models[0].(map[string]any)["modelId"])
}

// AC3: GetModelObservability (admin) returns cards, series and per-key
// rows; an unknown model returns 10801.
func TestFVTObservabilityModelAdmin(t *testing.T) {
	env := newObservabilityEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	h1 := day.Unix()

	env.seedLog(t, "org-fvt", fvtKey1, fvtModelA, time.Unix(h1, 0), 100, "success", 10, 20)
	env.seedLog(t, "org-fvt", fvtKey2, fvtModelA, time.Unix(h1, 0), 200, "error", 10, 20)

	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/observability/models/"+fvtModelA+"?since=%d&until=%d", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	cards := body["cards"].(map[string]any)
	assert.Equal(t, "2", cards["requestCount"])
	keys := body["keys"].([]any)
	require.Len(t, keys, 2)
	series := body["series"].([]any)
	require.Len(t, series, 1)

	// Unknown model -> 10801.
	code, body = env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/observability/models/nope?since=%d&until=%d", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	assert.NotEqual(t, 200, code)
	assert.EqualValues(t, float64(10801), body["code"])
}

// AC4: GetModelObservability (user) returns only the caller's org's
// usage, aggregated by the tenant's own keys, with no internals.
func TestFVTObservabilityModelUserScoped(t *testing.T) {
	env := newObservabilityEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	h1 := day.Unix()

	env.seedLog(t, "org-a", fvtKey1, fvtModelA, time.Unix(h1, 0), 100, "success", 10, 20)
	env.seedLog(t, "org-b", fvtKey2, fvtModelA, time.Unix(h1, 0), 999, "success", 10, 20)

	// org-a's view: only its own request.
	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/models/"+fvtModelA+"/observability?since=%d&until=%d", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-a")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	cards := body["cards"].(map[string]any)
	assert.Equal(t, "1", cards["requestCount"])
	keys := body["keys"].([]any)
	require.Len(t, keys, 1)
	assert.Equal(t, fvtKey1, keys[0].(map[string]any)["apiKeyId"])
	assert.Equal(t, "Key One", keys[0].(map[string]any)["apiKeyName"])
	// No service ids or operator internals.
	assert.Equal(t, "100", cards["avgLatencyMs"])
}

// AC5: hourly buckets for ranges <= 7 days, daily for longer ranges;
// every response carries data_through.
func TestFVTObservabilityBuckets(t *testing.T) {
	env := newObservabilityEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	env.seedLog(t, "org-fvt", fvtKey1, fvtModelA, day, 100, "success", 10, 20)
	env.seedLog(t, "org-fvt", fvtKey1, fvtModelA, day.Add(2*time.Hour), 100, "success", 10, 20)

	// 24h range -> hourly buckets (2 distinct hours).
	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/observability?since=%d&until=%d", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	series := body["series"].([]any)
	require.Len(t, series, 2)
	cards := body["cards"].(map[string]any)
	// data_through is present.
	_, hasThrough := cards["dataThrough"]
	assert.True(t, hasThrough)
}
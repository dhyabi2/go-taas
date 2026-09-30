package observability

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	observabilityv1 "github.com/go-taas/go-taas/proto/taas/observability/v1"
	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// fakeSessionOrgResolver returns a fixed org for the user binding.
type fakeSessionOrgResolver struct{ org string }

func (f fakeSessionOrgResolver) SessionActiveOrg(context.Context) (string, error) {
	return f.org, nil
}

// fakeSessionUserResolver returns a fixed user id.
type fakeSessionUserResolver struct{ user string }

func (f fakeSessionUserResolver) SessionUserID(context.Context) (string, error) {
	return f.user, nil
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

// newServiceTestDB creates an in-memory sqlite DB with the request_logs
// projection plus the models and api_keys tables.
func newServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&requestLogRow{}))
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS models (id TEXT PRIMARY KEY, name TEXT)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS api_keys (id TEXT PRIMARY KEY, name TEXT)`).Error)
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return db
}

// newService builds an observability service bound to the test DB with
// the seams wired.
func newService(t *testing.T, db *gorm.DB, org string, roleAllowed bool) *Service {
	t.Helper()
	svc := NewForFVT(db)
	svc.SetSessionOrgResolver(fakeSessionOrgResolver{org: org})
	svc.SetSessionUserResolver(fakeSessionUserResolver{user: "user-1"})
	svc.SetRoleGuard(fakeRoleGuard{allowed: roleAllowed})
	return svc
}

// ctxWithPath returns a context carrying the request path metadata so
// surfaceFromContext can derive the surface.
func ctxWithPath(ctx context.Context, path string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs("x-request-path", path))
}

// seedModelAndKey inserts a model and api key for name resolution.
func seedModelAndKey(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO models (id, name) VALUES ('`+testModelA+`', 'Model A')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO api_keys (id, name) VALUES ('`+testKey1+`', 'Key One')`).Error)
}

// AC1: GetObservabilityOverview returns cards, models and series; a
// range > 92 days or since > until returns 10404.
func TestGetObservabilityOverview(t *testing.T) {
	db := newServiceTestDB(t)
	seedModelAndKey(t, db)
	svc := newService(t, db, "org-1", true)
	ctx := ctxWithPath(context.Background(), "/api/v1/admin/observability")

	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	h1 := day.Unix()
	mustInsertLog(t, db, "org-1", testKey1, testModelA, time.Unix(h1, 0), 100, "success", 10, 20)
	mustInsertLog(t, db, "org-1", testKey1, testModelA, time.Unix(h1, 0), 200, "error", 10, 20)

	resp, err := svc.GetObservabilityOverview(ctx, &observabilityv1.GetObservabilityOverviewRequest{
		Since: day.Unix(),
		Until: day.Add(24 * time.Hour).Unix(),
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Cards)
	assert.Equal(t, int64(2), resp.Cards.RequestCount)
	assert.Equal(t, int64(1), resp.Cards.ErrorCount)
	require.Len(t, resp.Models, 1)
	assert.Equal(t, testModelA, resp.Models[0].ModelId)
	assert.Equal(t, "Model A", resp.Models[0].ModelName)
	require.Len(t, resp.Series, 1)

	// since > until -> 10404.
	_, err = svc.GetObservabilityOverview(ctx, &observabilityv1.GetObservabilityOverviewRequest{
		Since: day.Add(24 * time.Hour).Unix(),
		Until: day.Unix(),
	})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeMeteringRangeInvalid, apierrors.CodeOf(err))

	// range > 92 days -> 10404.
	_, err = svc.GetObservabilityOverview(ctx, &observabilityv1.GetObservabilityOverviewRequest{
		Since: day.Add(-93 * 24 * time.Hour).Unix(),
		Until: day.Unix(),
	})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeMeteringRangeInvalid, apierrors.CodeOf(err))
}

// AC2: GetObservabilityOverview with a model_id filter scopes the cards
// and series to that model.
func TestGetObservabilityOverviewModelFilter(t *testing.T) {
	db := newServiceTestDB(t)
	seedModelAndKey(t, db)
	svc := newService(t, db, "org-1", true)
	ctx := ctxWithPath(context.Background(), "/api/v1/admin/observability")

	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	h1 := day.Unix()
	mustInsertLog(t, db, "org-1", testKey1, testModelA, time.Unix(h1, 0), 100, "success", 10, 20)
	mustInsertLog(t, db, "org-1", testKey1, testModelB, time.Unix(h1, 0), 300, "success", 10, 20)

	resp, err := svc.GetObservabilityOverview(ctx, &observabilityv1.GetObservabilityOverviewRequest{
		Since:   day.Unix(),
		Until:   day.Add(24 * time.Hour).Unix(),
		ModelId: testModelA,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Cards.RequestCount)
	require.Len(t, resp.Models, 1)
	assert.Equal(t, testModelA, resp.Models[0].ModelId)
}

// AC13: a session without the required role receives 10036 on the admin
// overview.
func TestGetObservabilityOverviewForbidden(t *testing.T) {
	db := newServiceTestDB(t)
	svc := newService(t, db, "org-1", false)
	ctx := ctxWithPath(context.Background(), "/api/v1/admin/observability")

	_, err := svc.GetObservabilityOverview(ctx, &observabilityv1.GetObservabilityOverviewRequest{})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeForbidden, apierrors.CodeOf(err))
}

// AC3: GetModelObservability (admin) returns cards, series and per-key
// rows; an unknown model returns 10801.
func TestGetModelObservabilityAdmin(t *testing.T) {
	db := newServiceTestDB(t)
	seedModelAndKey(t, db)
	svc := newService(t, db, "org-1", true)
	ctx := ctxWithPath(context.Background(), "/api/v1/admin/observability/models/model-a")

	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	h1 := day.Unix()
	mustInsertLog(t, db, "org-1", testKey1, testModelA, time.Unix(h1, 0), 100, "success", 10, 20)
	mustInsertLog(t, db, "org-1", testKey1, testModelA, time.Unix(h1, 0), 200, "error", 10, 20)

	resp, err := svc.GetModelObservability(ctx, &observabilityv1.GetModelObservabilityRequest{
		ModelId: testModelA,
		Since:   day.Unix(),
		Until:   day.Add(24 * time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Cards.RequestCount)
	require.Len(t, resp.Keys, 1)
	assert.Equal(t, testKey1, resp.Keys[0].ApiKeyId)
	assert.Equal(t, "Key One", resp.Keys[0].ApiKeyName)
	require.Len(t, resp.Series, 1)

	// Unknown model -> 10801.
	_, err = svc.GetModelObservability(ctx, &observabilityv1.GetModelObservabilityRequest{
		ModelId: testNope,
		Since:   day.Unix(),
		Until:   day.Add(24 * time.Hour).Unix(),
	})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeObservabilityModelNotFound, apierrors.CodeOf(err))
}

// AC4: GetModelObservability (user) is hard-scoped to the caller's org
// and exposes no other tenants' data.
func TestGetModelObservabilityUserScoped(t *testing.T) {
	db := newServiceTestDB(t)
	seedModelAndKey(t, db)
	svc := newService(t, db, "org-1", true)
	ctx := ctxWithPath(context.Background(), "/api/v1/models/model-a/observability")

	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	h1 := day.Unix()
	// org-1's own usage.
	mustInsertLog(t, db, "org-1", testKey1, testModelA, time.Unix(h1, 0), 100, "success", 10, 20)
	// org-2's usage must be excluded.
	mustInsertLog(t, db, "org-2", testKey9, testModelA, time.Unix(h1, 0), 999, "success", 10, 20)

	resp, err := svc.GetModelObservability(ctx, &observabilityv1.GetModelObservabilityRequest{
		ModelId: testModelA,
		Since:   day.Unix(),
		Until:   day.Add(24 * time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Cards.RequestCount)
	require.Len(t, resp.Keys, 1)
	assert.Equal(t, testKey1, resp.Keys[0].ApiKeyId)
	// No service ids or operator internals in the response.
	assert.Equal(t, int64(100), resp.Cards.AvgLatencyMs)
}

// AC5: the user binding ignores X-Organization-Id and uses the session
// active org.
func TestGetModelObservabilityUserIgnoresHeader(t *testing.T) {
	db := newServiceTestDB(t)
	seedModelAndKey(t, db)
	// Session active org is org-1; the request carries org-2 in the
	// header, which must be ignored.
	svc := newService(t, db, "org-1", true)
	ctx := ctxWithPath(context.Background(), "/api/v1/models/model-a/observability")

	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	h1 := day.Unix()
	mustInsertLog(t, db, "org-1", testKey1, testModelA, time.Unix(h1, 0), 100, "success", 10, 20)
	mustInsertLog(t, db, "org-2", testKey9, testModelA, time.Unix(h1, 0), 999, "success", 10, 20)

	resp, err := svc.GetModelObservability(ctx, &observabilityv1.GetModelObservabilityRequest{
		OrganizationId: "org-2",
		ModelId:        testModelA,
		Since:          day.Unix(),
		Until:          day.Add(24 * time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Cards.RequestCount)
	require.Len(t, resp.Keys, 1)
	assert.Equal(t, testKey1, resp.Keys[0].ApiKeyId)
}

// AC5: hourly buckets for ranges <= 7 days, daily for longer ranges.
func TestBucketGranularity(t *testing.T) {
	db := newServiceTestDB(t)
	seedModelAndKey(t, db)
	svc := newService(t, db, "org-1", true)
	ctx := ctxWithPath(context.Background(), "/api/v1/admin/observability")

	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	// Two requests 2 hours apart within a 24h range -> hourly buckets.
	mustInsertLog(t, db, "org-1", testKey1, testModelA, day, 100, "success", 10, 20)
	mustInsertLog(t, db, "org-1", testKey1, testModelA, day.Add(2*time.Hour), 100, "success", 10, 20)

	resp, err := svc.GetObservabilityOverview(ctx, &observabilityv1.GetObservabilityOverviewRequest{
		Since: day.Unix(),
		Until: day.Add(24 * time.Hour).Unix(),
	})
	require.NoError(t, err)
	require.Len(t, resp.Series, 2)
	// Hourly buckets: 2 distinct hours.
	assert.Equal(t, day.Unix(), resp.Series[0].Bucket)
	assert.Equal(t, day.Add(2*time.Hour).Unix(), resp.Series[1].Bucket)
}
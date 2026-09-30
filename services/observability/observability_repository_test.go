package observability

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newTestDB creates an in-memory sqlite DB with the request_logs
// projection plus the models and api_keys tables the name-resolution
// reads.
//
// Model and api-key ids are valid UUIDs (matching the production
// schema) so the ModelExists UUID validation (AD3) behaves identically
// to PostgreSQL.
const (
	testModelA = "11111111-1111-1111-1111-111111111111"
	testModelB = "22222222-2222-2222-2222-222222222222"
	testKey1   = "33333333-3333-3333-3333-333333333333"
	testKey2   = "44444444-4444-4444-4444-444444444444"
	testKey3   = "55555555-5555-5555-5555-555555555555"
	testKey9   = "66666666-6666-6666-6666-666666666666"
	testNope   = "99999999-9999-9999-9999-999999999999"
)

func newTestDB(t *testing.T) *gorm.DB {
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

// mustInsertLog inserts a request-log row directly.
func mustInsertLog(t *testing.T, db *gorm.DB, orgID, keyID, modelID string, at time.Time, latencyMs int64, status string, prompt, completion int64) {
	t.Helper()
	row := requestLogRow{
		OrganizationID:   orgID,
		APIKeyID:         keyID,
		ModelID:          modelID,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		LatencyMs:        latencyMs,
		Status:           status,
		CreatedAt:        at.UTC(),
	}
	require.NoError(t, db.Create(&row).Error)
}

// AC1/AC2: AggregateOverview returns the per-bucket series and per-model
// rows for the range, honoring the org and model filters.
func TestAggregateOverview(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	h1 := day.Unix()
	h2 := day.Add(time.Hour).Unix()

	// org-1, model-a, key-1: two requests in h1, one in h2.
	mustInsertLog(t, db, "org-1", testKey1, testModelA, time.Unix(h1, 0), 100, "success", 10, 20)
	mustInsertLog(t, db, "org-1", testKey1, testModelA, time.Unix(h1, 0), 200, "error", 10, 20)
	mustInsertLog(t, db, "org-1", testKey1, testModelA, time.Unix(h2, 0), 300, "success", 10, 20)
	// org-1, model-b, key-2: one request in h1.
	mustInsertLog(t, db, "org-1", testKey2, testModelB, time.Unix(h1, 0), 50, "success", 5, 10)
	// org-2, model-a: excluded by the org filter.
	mustInsertLog(t, db, "org-2", testKey3, testModelA, time.Unix(h1, 0), 999, "success", 1, 1)

	repo := NewRepository(db)

	// Fleet-wide (no org filter): all orgs.
	buckets, models, err := repo.AggregateOverview(ctx, "", "", day.Unix(), day.Add(24*time.Hour).Unix(), 3600)
	require.NoError(t, err)
	require.Len(t, buckets, 2)
	require.Len(t, models, 2)
	// model-a has 4 requests (3 org-1 + 1 org-2), model-b has 1.
	assert.Equal(t, int64(4), models[0].RequestCount)
	assert.Equal(t, testModelA, models[0].ModelID)
	assert.Equal(t, int64(1), models[1].RequestCount)

	// Org filter: only org-1.
	buckets, models, err = repo.AggregateOverview(ctx, "org-1", "", day.Unix(), day.Add(24*time.Hour).Unix(), 3600)
	require.NoError(t, err)
	require.Len(t, buckets, 2)
	require.Len(t, models, 2)
	byModel := map[string]ModelRow{}
	for _, m := range models {
		byModel[m.ModelID] = m
	}
	ma := byModel[testModelA]
	assert.Equal(t, int64(3), ma.RequestCount)
	assert.Equal(t, int64(1), ma.ErrorCount)
	// avg latency = (100+200+300)/3 = 200.
	assert.Equal(t, int64(200), ma.AvgLatencyMs)
	// p95 of [100,200,300] = 300.
	assert.Equal(t, int64(300), ma.P95LatencyMs)
	// output tokens = 20*3 = 60 (raw sum; tokens/sec derived in service).
	assert.Equal(t, int64(60), ma.OutputTokens)

	// Model filter: only model-a.
	buckets, models, err = repo.AggregateOverview(ctx, "org-1", testModelA, day.Unix(), day.Add(24*time.Hour).Unix(), 3600)
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.Equal(t, testModelA, models[0].ModelID)
	// h1 bucket has 2 requests, h2 has 1.
	require.Len(t, buckets, 2)
	assert.Equal(t, int64(2), buckets[0].RequestCount)
	assert.Equal(t, int64(1), buckets[1].RequestCount)
}

// AC3: AggregateModel returns the per-bucket series and per-key rows.
func TestAggregateModel(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	h1 := day.Unix()

	mustInsertLog(t, db, "org-1", testKey1, testModelA, time.Unix(h1, 0), 100, "success", 10, 20)
	mustInsertLog(t, db, "org-1", testKey2, testModelA, time.Unix(h1, 0), 200, "error", 10, 20)
	mustInsertLog(t, db, "org-2", testKey3, testModelA, time.Unix(h1, 0), 300, "success", 10, 20)

	repo := NewRepository(db)
	buckets, keys, err := repo.AggregateModel(ctx, "org-1", testModelA, day.Unix(), day.Add(24*time.Hour).Unix(), 3600)
	require.NoError(t, err)
	require.Len(t, buckets, 1)
	require.Len(t, keys, 2)
	byKey := map[string]KeyRow{}
	for _, k := range keys {
		byKey[k.APIKeyID] = k
	}
	assert.Equal(t, int64(1), byKey[testKey1].RequestCount)
	assert.Equal(t, int64(0), byKey[testKey1].ErrorCount)
	assert.Equal(t, int64(1), byKey[testKey2].RequestCount)
	assert.Equal(t, int64(1), byKey[testKey2].ErrorCount)
}

// AC5: DataThrough returns the last complete bucket (max created_at
// truncated to the bucket boundary, minus one bucket).
func TestDataThrough(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	h1 := day.Unix()
	h2 := day.Add(time.Hour).Unix()

	mustInsertLog(t, db, "org-1", testKey1, testModelA, time.Unix(h1, 0), 100, "success", 10, 20)
	mustInsertLog(t, db, "org-1", testKey1, testModelA, time.Unix(h2, 0), 100, "success", 10, 20)

	repo := NewRepository(db)
	// Max created_at = h2 (3600). Bucket start = 3600. Last complete = 0.
	dt, err := repo.DataThrough(ctx, "org-1", testModelA, day.Unix(), day.Add(24*time.Hour).Unix(), 3600)
	require.NoError(t, err)
	assert.Equal(t, h1, dt)

	// No rows -> 0.
	dt, err = repo.DataThrough(ctx, "org-1", testModelB, day.Unix(), day.Add(24*time.Hour).Unix(), 3600)
	require.NoError(t, err)
	assert.Equal(t, int64(0), dt)
}

// AC5: the bucket size switches at the 7-day boundary.
func TestBucketSizeForRange(t *testing.T) {
	now := time.Now().Unix()
	// <= 7 days -> hourly.
	assert.Equal(t, int64(3600), bucketSizeForRange(now-7*24*3600, now))
	// > 7 days -> daily.
	assert.Equal(t, int64(24*3600), bucketSizeForRange(now-8*24*3600, now))
}

// AD3: ModelName and APIKeyName resolve display names read-only.
func TestNameResolution(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	require.NoError(t, db.Exec(`INSERT INTO models (id, name) VALUES ('`+testModelA+`', 'Model A')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO api_keys (id, name) VALUES ('`+testKey1+`', 'Key One')`).Error)

	repo := NewRepository(db)
	name, err := repo.ModelName(ctx, testModelA)
	require.NoError(t, err)
	assert.Equal(t, "Model A", name)
	name, err = repo.APIKeyName(ctx, testKey1)
	require.NoError(t, err)
	assert.Equal(t, "Key One", name)

	// Unknown ids resolve to "".
	name, err = repo.ModelName(ctx, testNope)
	require.NoError(t, err)
	assert.Equal(t, "", name)
}

// AD3: ModelExists reports model existence for the 10801 check.
func TestModelExists(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	require.NoError(t, db.Exec(`INSERT INTO models (id, name) VALUES ('`+testModelA+`', 'Model A')`).Error)

	repo := NewRepository(db)
	ok, err := repo.ModelExists(ctx, testModelA)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = repo.ModelExists(ctx, testNope)
	require.NoError(t, err)
	assert.False(t, ok)
}

// percentile returns the nearest-rank percentile (0 when empty).
func TestPercentile(t *testing.T) {
	assert.Equal(t, int64(0), percentile(nil, 0.95))
	assert.Equal(t, int64(100), percentile([]int64{100}, 0.95))
	// [100,200,300] p95 -> rank ceil(2.85)=3 -> 300 (nearest-rank).
	assert.Equal(t, int64(300), percentile([]int64{100, 200, 300}, 0.95))
}
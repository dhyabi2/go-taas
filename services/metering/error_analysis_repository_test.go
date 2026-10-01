package metering

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

func newErrorAnalysisTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, MigrateSchemaForFVT(db))
	return db
}

var errorLogSeq int

func seedErrorLog(t *testing.T, db *gorm.DB, org, key, model, status, errCode string, at time.Time) {
	t.Helper()
	errorLogSeq++
	row := RequestLog{
		ID:             fmt.Sprintf("id-%d", errorLogSeq),
		RequestID:      fmt.Sprintf("req-%d", errorLogSeq),
		OrganizationID: org,
		APIKeyID:       key,
		ModelID:        model,
		Status:         status,
		Error:          errCode,
		CreatedAt:      at.UTC(),
	}
	require.NoError(t, db.Create(&row).Error)
}

func TestAggregateErrors(t *testing.T) {
	db := newErrorAnalysisTestDB(t)
	now := time.Now().UTC()
	seedErrorLog(t, db, "org-a", "k1", "m1", "error", "rate_limit_exceeded", now)
	seedErrorLog(t, db, "org-a", "k1", "m1", "error", "timeout", now)
	seedErrorLog(t, db, "org-a", "k1", "m1", "success", "", now)

	repo := NewErrorAnalysisRepository(db)
	buckets, causes, err := repo.AggregateErrors(context.Background(), "org-a", "", now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix(), 3600)
	require.NoError(t, err)
	assert.Len(t, buckets, 1)
	assert.Equal(t, int64(3), buckets[0].RequestCount)
	assert.Equal(t, int64(2), buckets[0].ErrorCount)
	assert.Len(t, causes, 2)
	// Sorted by error count descending.
	assert.Equal(t, "rate_limit_exceeded", causes[0].ErrorCode)
}

func TestAggregateError(t *testing.T) {
	db := newErrorAnalysisTestDB(t)
	now := time.Now().UTC()
	seedErrorLog(t, db, "org-a", "k1", "m1", "error", "rate_limit_exceeded", now)
	seedErrorLog(t, db, "org-a", "k1", "m1", "error", "timeout", now)

	repo := NewErrorAnalysisRepository(db)
	buckets, err := repo.AggregateError(context.Background(), "org-a", "rate_limit_exceeded", now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix(), 3600)
	require.NoError(t, err)
	assert.Len(t, buckets, 1)
	assert.Equal(t, int64(1), buckets[0].ErrorCount)
}

func TestErrorAnalysisDataThrough(t *testing.T) {
	db := newErrorAnalysisTestDB(t)
	now := time.Now().UTC()
	seedErrorLog(t, db, "org-a", "k1", "m1", "error", "timeout", now)

	repo := NewErrorAnalysisRepository(db)
	watermark, err := repo.DataThrough(context.Background(), "org-a", "", now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix(), 3600)
	require.NoError(t, err)
	lastBucket := (now.Unix() / 3600) * 3600
	assert.Equal(t, lastBucket-3600, watermark)
}

func TestErrorRateAndShareHelpers(t *testing.T) {
	assert.Equal(t, int64(50), errorRatePct(5, 10))
	assert.Equal(t, int64(0), errorRatePct(5, 0))
	assert.Equal(t, int64(25), sharePct(25, 100))
	assert.Equal(t, int64(0), sharePct(25, 0))
}
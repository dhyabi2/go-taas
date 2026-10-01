package tracing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTraceRetentionRunnerRetainOnce(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	seedTrace(t, db, "trace-1", "org-a", "k1", "success", now.Add(-48*time.Hour))
	seedTrace(t, db, "trace-2", "org-a", "k1", "success", now)

	repo := NewRepository(db)
	runner := NewTraceRetentionRunner(repo, 24*time.Hour, 10, time.Hour)
	runner.RetainOnce(context.Background())

	var count int64
	require.NoError(t, db.Model(&Trace{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
	var spanCount int64
	require.NoError(t, db.Model(&TraceSpan{}).Count(&spanCount).Error)
	assert.Equal(t, int64(2), spanCount)
}

func TestTraceRetentionRunnerDefaults(t *testing.T) {
	db := newTestDB(t)
	repo := NewRepository(db)
	// batchSize <= 0 and interval <= 0 fall back to defaults.
	runner := NewTraceRetentionRunner(repo, 24*time.Hour, 0, 0)
	assert.Equal(t, 1000, runner.batchSize)
	assert.Equal(t, time.Hour, runner.interval)
}

func TestPaginationHelpers(t *testing.T) {
	assert.Equal(t, 0, func() int { o, _ := normalizePagination(0); return o }())
	_, limit := normalizePagination(0)
	assert.Equal(t, 20, limit)
	_, limit = normalizePagination(500)
	assert.Equal(t, 100, limit)
	_, limit = normalizePagination(50)
	assert.Equal(t, 50, limit)

	assert.Equal(t, "0", itoa(0))
	assert.Equal(t, "123", itoa(123))
	assert.Equal(t, "-5", itoa(-5))

	assert.Equal(t, "", nextPageToken(0, 20, 10))
	assert.Equal(t, "20", nextPageToken(0, 20, 30))
	assert.Equal(t, "", nextPageToken(20, 20, 30))
	assert.Equal(t, "40", nextPageToken(20, 20, 50))
}

func TestMaskServiceID(t *testing.T) {
	assert.Equal(t, "", maskServiceID(""))
	assert.Equal(t, "gateway", maskServiceID("gateway"))
	assert.Equal(t, "inference", maskServiceID("svc-1"))
	assert.Equal(t, "inference", maskServiceID("anything"))
}

func TestDeref(t *testing.T) {
	assert.Equal(t, "", deref(nil))
	v := "x"
	assert.Equal(t, "x", deref(&v))
}

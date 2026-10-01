package tracing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
	"github.com/go-taas/go-taas/services/metering"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, MigrateSchemaForFVT(db))
	require.NoError(t, metering.MigrateSchemaForFVT(db))
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS models (id TEXT PRIMARY KEY, name TEXT)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS api_keys (id TEXT PRIMARY KEY, name TEXT)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO models (id, name) VALUES ('m1', 'Model One'), ('m2', 'Model Two')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO api_keys (id, name) VALUES ('k1', 'Key One'), ('k2', 'Key Two')`).Error)
	return db
}

func seedTrace(t *testing.T, db *gorm.DB, traceID, org, key, status string, created time.Time) {
	t.Helper()
	repo := NewRepository(db)
	sid := "svc-1"
	err := repo.InsertTrace(context.Background(), &Trace{
		TraceID:          traceID,
		OrganizationID:   org,
		APIKeyID:         key,
		ModelID:          "m1",
		ServiceID:        &sid,
		Status:           status,
		Error:            "boom",
		TotalLatencyMs:   1000,
		TTFTMs:           400,
		GenerationMs:     600,
		PromptTokens:     10,
		CompletionTokens: 20,
		CreatedAt:        created,
	}, []*TraceSpan{
		{TraceID: traceID, Name: "gateway", Kind: "server", StartOffsetMs: 0, DurationMs: 1000, Status: status, Error: "boom", Attributes: "{}"},
		{TraceID: traceID, Name: "inference", Kind: "internal", StartOffsetMs: 0, DurationMs: 1000, Status: status, Error: "boom", Attributes: "{}"},
	})
	require.NoError(t, err)
}

func TestInsertTraceIdempotent(t *testing.T) {
	db := newTestDB(t)
	repo := NewRepository(db)
	now := time.Now().UTC()
	seedTrace(t, db, "trace-1", "org-a", "k1", "success", now)

	// Duplicate insert writes no second trace.
	sid := "svc-1"
	err := repo.InsertTrace(context.Background(), &Trace{
		TraceID:        "trace-1",
		OrganizationID: "org-a",
		APIKeyID:       "k1",
		ModelID:        "m1",
		ServiceID:      &sid,
		Status:         "success",
		TotalLatencyMs: 1000,
		CreatedAt:      now,
	}, []*TraceSpan{{TraceID: "trace-1", Name: "gateway", Kind: "server", StartOffsetMs: 0, DurationMs: 1000, Status: "success", Attributes: "{}"}})
	require.NoError(t, err)

	var count int64
	require.NoError(t, db.Model(&Trace{}).Where("trace_id = ?", "trace-1").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	var spanCount int64
	require.NoError(t, db.Model(&TraceSpan{}).Where("trace_id = ?", "trace-1").Count(&spanCount).Error)
	assert.Equal(t, int64(2), spanCount)
}

func TestListTracesFiltersAndRange(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	seedTrace(t, db, "trace-1", "org-a", "k1", "success", now.Add(-time.Hour))
	seedTrace(t, db, "trace-2", "org-a", "k2", "error", now)
	seedTrace(t, db, "trace-3", "org-b", "k1", "success", now.Add(-2*time.Hour))

	repo := NewRepository(db)

	// Org filter.
	rows, total, err := repo.ListTraces(context.Background(), TraceFilter{
		OrganizationID: "org-a",
		Since:          now.Add(-3 * time.Hour).Unix(),
		Until:          now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, rows, 2)

	// Request id exact match.
	rows, total, err = repo.ListTraces(context.Background(), TraceFilter{
		RequestID: "trace-2",
		Since:     now.Add(-3 * time.Hour).Unix(),
		Until:     now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "trace-2", rows[0].TraceID)

	// Unknown request id returns empty.
	rows, total, err = repo.ListTraces(context.Background(), TraceFilter{
		RequestID: "nope",
		Since:     now.Add(-3 * time.Hour).Unix(),
		Until:     now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Len(t, rows, 0)

	// Status filter.
	rows, total, err = repo.ListTraces(context.Background(), TraceFilter{
		Status: "error",
		Since:  now.Add(-3 * time.Hour).Unix(),
		Until:  now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "trace-2", rows[0].TraceID)

	// Newest first ordering.
	rows, _, err = repo.ListTraces(context.Background(), TraceFilter{
		Since: now.Add(-3 * time.Hour).Unix(),
		Until: now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, "trace-2", rows[0].TraceID)
}

func TestFindTraceByID(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	seedTrace(t, db, "trace-1", "org-a", "k1", "success", now)

	repo := NewRepository(db)

	// Found with spans.
	trace, spans, err := repo.FindTraceByID(context.Background(), "", "trace-1")
	require.NoError(t, err)
	assert.Equal(t, "trace-1", trace.TraceID)
	assert.Len(t, spans, 2)

	// Org-scoped miss.
	_, _, err = repo.FindTraceByID(context.Background(), "org-b", "trace-1")
	assert.Error(t, err)
	assert.Equal(t, 11101, int(apierrors.CodeOf(err)))

	// Unknown trace id.
	_, _, err = repo.FindTraceByID(context.Background(), "", "nope")
	assert.Error(t, err)
	assert.Equal(t, 11101, int(apierrors.CodeOf(err)))
}

func TestDeleteTracesBefore(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	seedTrace(t, db, "trace-1", "org-a", "k1", "success", now.Add(-48*time.Hour))
	seedTrace(t, db, "trace-2", "org-a", "k1", "success", now)

	repo := NewRepository(db)
	deleted, err := repo.DeleteTracesBefore(context.Background(), now.Add(-24*time.Hour), 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	var count int64
	require.NoError(t, db.Model(&Trace{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
	var spanCount int64
	require.NoError(t, db.Model(&TraceSpan{}).Count(&spanCount).Error)
	assert.Equal(t, int64(2), spanCount)
}

func TestNameResolution(t *testing.T) {
	db := newTestDB(t)
	repo := NewRepository(db)
	name, err := repo.ModelName(context.Background(), "m1")
	require.NoError(t, err)
	assert.Equal(t, "Model One", name)
	keyName, err := repo.APIKeyName(context.Background(), "k1")
	require.NoError(t, err)
	assert.Equal(t, "Key One", keyName)
}

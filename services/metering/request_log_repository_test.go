package metering

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// AC-A2: IngestRequestLog is idempotent — a duplicate request_id writes
// no second row.
func TestRepositoryIngestRequestLogIdempotent(t *testing.T) {
	db := newMeteringTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	log := &RequestLog{
		RequestID:      "req-1",
		OrganizationID: "org-1",
		APIKeyID:       "key-1",
		ModelID:        "model-a",
		PromptTokens:   100,
		CompletionTokens: 50,
		LatencyMs:      120,
		Status:         "success",
		CreatedAt:      time.Now().UTC(),
	}
	require.NoError(t, repo.IngestRequestLog(ctx, log))

	// Duplicate request_id.
	dup := &RequestLog{
		RequestID:      "req-1",
		OrganizationID: "org-1",
		APIKeyID:       "key-1",
		ModelID:        "model-a",
		PromptTokens:   999,
		Status:         "error",
		CreatedAt:      time.Now().UTC(),
	}
	require.NoError(t, repo.IngestRequestLog(ctx, dup))

	var count int64
	require.NoError(t, repo.DB(ctx).Model(&RequestLog{}).Count(&count).Error)
	assert.Equal(t, int64(1), count, "exactly one row after duplicate delivery")
}

// AC-A4: ListRequestLogs filters by key/model/status/range, newest
// first, paginated.
func TestRepositoryListRequestLogs(t *testing.T) {
	db := newMeteringTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	now := time.Now().UTC()
	for i, status := range []string{"success", "error", "success"} {
		require.NoError(t, repo.IngestRequestLog(ctx, &RequestLog{
			RequestID:      "req-" + string(rune('a'+i)),
			OrganizationID: "org-1",
			APIKeyID:       "key-1",
			ModelID:        "model-a",
			Status:         status,
			CreatedAt:      now.Add(time.Duration(i) * time.Minute),
		}))
	}
	// A second key's log.
	require.NoError(t, repo.IngestRequestLog(ctx, &RequestLog{
		RequestID:      "req-x",
		OrganizationID: "org-1",
		APIKeyID:       "key-2",
		ModelID:        "model-b",
		Status:         "success",
		CreatedAt:      now,
	}))

	// Filter by status.
	rows, total, err := repo.ListRequestLogs(ctx, RequestLogFilter{
		OrganizationID: "org-1", Status: "error", Offset: 0, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, rows, 1)
	assert.Equal(t, "error", rows[0].Status)

	// Filter by key.
	rows, total, err = repo.ListRequestLogs(ctx, RequestLogFilter{
		OrganizationID: "org-1", APIKeyID: "key-2", Offset: 0, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "model-b", rows[0].ModelID)

	// All rows for org-1.
	rows, total, err = repo.ListRequestLogs(ctx, RequestLogFilter{
		OrganizationID: "org-1", Offset: 0, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	assert.Len(t, rows, 4)
}

// AC-A5: FindRequestLogByID returns the row; unknown id -> 10405.
func TestRepositoryFindRequestLogByID(t *testing.T) {
	db := newMeteringTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	log := &RequestLog{
		RequestID:      "req-1",
		OrganizationID: "org-1",
		APIKeyID:       "key-1",
		ModelID:        "model-a",
		Status:         "success",
		CreatedAt:      time.Now().UTC(),
	}
	require.NoError(t, repo.IngestRequestLog(ctx, log))

	got, err := repo.FindRequestLogByID(ctx, log.ID)
	require.NoError(t, err)
	assert.Equal(t, "req-1", got.RequestID)

	_, err = repo.FindRequestLogByID(ctx, "00000000-0000-0000-0000-000000000000")
	ae, ok := apierrors.As(err)
	require.True(t, ok)
	assert.Equal(t, apierrors.CodeRequestLogNotFound, ae.Code)
}

// AC-A7: DeleteRequestLogsBefore deletes old rows in batches.
func TestRepositoryDeleteRequestLogsBefore(t *testing.T) {
	db := newMeteringTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	old := time.Now().UTC().Add(-48 * time.Hour)
	recent := time.Now().UTC()
	require.NoError(t, repo.IngestRequestLog(ctx, &RequestLog{
		RequestID: "old-1", OrganizationID: "org-1", APIKeyID: "key-1", ModelID: "m", Status: "success", CreatedAt: old,
	}))
	require.NoError(t, repo.IngestRequestLog(ctx, &RequestLog{
		RequestID: "old-2", OrganizationID: "org-1", APIKeyID: "key-1", ModelID: "m", Status: "success", CreatedAt: old,
	}))
	require.NoError(t, repo.IngestRequestLog(ctx, &RequestLog{
		RequestID: "new-1", OrganizationID: "org-1", APIKeyID: "key-1", ModelID: "m", Status: "success", CreatedAt: recent,
	}))

	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	deleted, err := repo.DeleteRequestLogsBefore(ctx, cutoff, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	deleted, err = repo.DeleteRequestLogsBefore(ctx, cutoff, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	var count int64
	require.NoError(t, repo.DB(ctx).Model(&RequestLog{}).Count(&count).Error)
	assert.Equal(t, int64(1), count, "only the recent row remains")
}
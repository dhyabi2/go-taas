package metering

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	meteringv1 "github.com/go-taas/go-taas/proto/taas/metering/v1"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// AC-A1: handleEvent writes both a voucher and a request log with the
// same request_id.
func TestServiceIngestWritesRequestLog(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withOrg(context.Background(), "org-1")

	_, err := svc.IngestMeteringEvent(ctx, &meteringv1.IngestMeteringEventRequest{
		RequestId:      "req-1",
		OrganizationId: "org-1",
		ApiKeyId:       "key-1",
		ModelId:        "model-a",
		CompletedAt:    time.Now().Add(-time.Hour).Unix(),
		LatencyMs:      120,
		Status:         meteringv1.RequestLogStatus_REQUEST_LOG_STATUS_SUCCESS,
		Usage: &meteringv1.TokenUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
		},
	})
	require.NoError(t, err)

	// The request log was written.
	repo := svc.repo
	var logCount int64
	require.NoError(t, repo.DB(ctx).Model(&RequestLog{}).Count(&logCount).Error)
	assert.Equal(t, int64(1), logCount)

	// The voucher was written too.
	var voucherCount int64
	require.NoError(t, repo.DB(ctx).Model(&Voucher{}).Count(&voucherCount).Error)
	assert.Equal(t, int64(1), voucherCount)
}

// AC-A6: ListRequestLogs validates the range (10404).
func TestServiceListRequestLogsRangeInvalid(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withOrg(context.Background(), "org-1")
	now := time.Now().Unix()

	_, err := svc.ListRequestLogs(ctx, &meteringv1.ListRequestLogsRequest{
		Since: now, Until: now - 10,
	})
	require.Error(t, err)
	ae, ok := apierrors.As(err)
	require.True(t, ok)
	assert.Equal(t, apierrors.CodeMeteringRangeInvalid, ae.Code)
}

// AC-A5: GetRequestLog returns the row; unknown id -> 10405.
func TestServiceGetRequestLog(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withOrg(context.Background(), "org-1")

	_, err := svc.IngestMeteringEvent(ctx, &meteringv1.IngestMeteringEventRequest{
		RequestId:      "req-1",
		OrganizationId: "org-1",
		ApiKeyId:       "key-1",
		ModelId:        "model-a",
		CompletedAt:    time.Now().Add(-time.Hour).Unix(),
		LatencyMs:      120,
		Status:         meteringv1.RequestLogStatus_REQUEST_LOG_STATUS_SUCCESS,
		Usage: &meteringv1.TokenUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
		},
	})
	require.NoError(t, err)

	// Find the log id.
	repo := svc.repo
	var log RequestLog
	require.NoError(t, repo.DB(ctx).First(&log).Error)

	resp, err := svc.GetRequestLog(ctx, &meteringv1.GetRequestLogRequest{RequestLogId: log.ID})
	require.NoError(t, err)
	assert.Equal(t, "req-1", resp.GetRequestLog().GetRequestId())
	assert.Equal(t, int64(120), resp.GetRequestLog().GetLatencyMs())
	assert.Equal(t, meteringv1.RequestLogStatus_REQUEST_LOG_STATUS_SUCCESS, resp.GetRequestLog().GetStatus())

	// Unknown id -> 10405.
	_, err = svc.GetRequestLog(ctx, &meteringv1.GetRequestLogRequest{RequestLogId: "00000000-0000-0000-0000-000000000000"})
	require.Error(t, err)
	ae, ok := apierrors.As(err)
	require.True(t, ok)
	assert.Equal(t, apierrors.CodeRequestLogNotFound, ae.Code)
}

// AC-B5: org scoping — one org never sees another org's request logs.
func TestServiceRequestLogOrgScoping(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withOrg(context.Background(), "org-1")

	_, err := svc.IngestMeteringEvent(ctx, &meteringv1.IngestMeteringEventRequest{
		RequestId:      "req-1",
		OrganizationId: "org-1",
		ApiKeyId:       "key-1",
		ModelId:        "model-a",
		CompletedAt:    time.Now().Add(-time.Hour).Unix(),
		Usage:          &meteringv1.TokenUsage{PromptTokens: 100},
	})
	require.NoError(t, err)

	// org-2 sees no logs.
	ctx2 := withOrg(context.Background(), "org-2")
	resp, err := svc.ListRequestLogs(ctx2, &meteringv1.ListRequestLogsRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.GetRequestLogs(), 0)
}
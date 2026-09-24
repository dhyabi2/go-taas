package fvt

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	meteringv1 "github.com/go-taas/go-taas/proto/taas/metering/v1"
)

// TestFVTRequestLogs walks the request-log acceptance criteria through
// the gateway: ingest an extended event, assert a request-log row is
// written alongside the voucher, list/filter/drill, and validate ranges.
func TestFVTRequestLogs(t *testing.T) {
	env := newMeteringEnv(t)
	ctx := context.Background()

	// AC-A1: ingest an extended event -> voucher + request log share the
	// request_id.
	_, err := env.svc.IngestMeteringEvent(ctx, &meteringv1.IngestMeteringEventRequest{
		RequestId:      "req-1",
		OrganizationId: "org-fvt",
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

	// AC-A4: ListRequestLogs returns the row.
	code, body := env.call(t, http.MethodGet, "/api/v1/admin/metering/request-logs", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	logs, _ := body["requestLogs"].([]any)
	require.Len(t, logs, 1)
	log, _ := logs[0].(map[string]any)
	assert.Equal(t, "req-1", log["requestId"])
	assert.EqualValues(t, "120", fmt.Sprint(log["latencyMs"]))
	assert.Equal(t, "REQUEST_LOG_STATUS_SUCCESS", log["status"])

	// AC-A5: GetRequestLog returns the full metadata.
	logID, _ := log["requestLogId"].(string)
	code, body = env.call(t, http.MethodGet, "/api/v1/admin/metering/request-logs/"+logID, nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	detail, _ := body["requestLog"].(map[string]any)
	assert.Equal(t, "req-1", detail["requestId"])
	assert.EqualValues(t, "100", fmt.Sprint(detail["promptTokens"]))

	// AC-A6: range validation -> 10404.
	now := time.Now().Unix()
	code, body = env.call(t, http.MethodGet,
		fmt.Sprintf("/api/v1/admin/metering/request-logs?since=%d&until=%d", now, now-10), nil, "org-fvt")
	assert.NotEqual(t, http.StatusOK, code)
	assert.EqualValues(t, float64(10404), body["code"])

	// AC-B5: org scoping — org-a sees no rows.
	code, body = env.call(t, http.MethodGet, "/api/v1/admin/metering/request-logs", nil, "org-a")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	logs, _ = body["requestLogs"].([]any)
	assert.Len(t, logs, 0)
}

// TestFVTRequestLogsFilter walks the status filter.
func TestFVTRequestLogsFilter(t *testing.T) {
	env := newMeteringEnv(t)
	ctx := context.Background()

	_, err := env.svc.IngestMeteringEvent(ctx, &meteringv1.IngestMeteringEventRequest{
		RequestId:      "req-ok",
		OrganizationId: "org-fvt",
		ApiKeyId:       "key-1",
		ModelId:        "model-a",
		CompletedAt:    time.Now().Add(-time.Hour).Unix(),
		Status:         meteringv1.RequestLogStatus_REQUEST_LOG_STATUS_SUCCESS,
		Usage:          &meteringv1.TokenUsage{PromptTokens: 100},
	})
	require.NoError(t, err)
	_, err = env.svc.IngestMeteringEvent(ctx, &meteringv1.IngestMeteringEventRequest{
		RequestId:      "req-err",
		OrganizationId: "org-fvt",
		ApiKeyId:       "key-1",
		ModelId:        "model-a",
		CompletedAt:    time.Now().Add(-time.Hour).Unix(),
		Status:         meteringv1.RequestLogStatus_REQUEST_LOG_STATUS_ERROR,
		Error:          "model timeout",
		Usage:          &meteringv1.TokenUsage{PromptTokens: 100},
	})
	require.NoError(t, err)

	// Filter by status=error.
	code, body := env.call(t, http.MethodGet,
		"/api/v1/admin/metering/request-logs?status=REQUEST_LOG_STATUS_ERROR", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	logs, _ := body["requestLogs"].([]any)
	require.Len(t, logs, 1)
	log, _ := logs[0].(map[string]any)
	assert.Equal(t, "req-err", log["requestId"])
	assert.Equal(t, "model timeout", log["error"])
}
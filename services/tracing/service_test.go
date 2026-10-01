package tracing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
	tracingv1 "github.com/go-taas/go-taas/proto/taas/tracing/v1"
	"github.com/go-taas/go-taas/services/metering"
)

type fakeOrgResolver struct{ org string }

func (f *fakeOrgResolver) SessionActiveOrg(_ context.Context) (string, error) { return f.org, nil }

type fakeUserResolver struct{ user string }

func (f *fakeUserResolver) SessionUserID(_ context.Context) (string, error) { return f.user, nil }

type fakeRoleGuard struct{ allowed bool }

func (f *fakeRoleGuard) RequireRole(_ context.Context, _, _, _ string) error {
	if f.allowed {
		return nil
	}
	return apierrors.New(apierrors.CodeForbidden)
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	db := newTestDB(t)
	svc := NewForFVT(db)
	svc.SetMaxRangeSeconds(92 * 24 * 3600)
	return svc
}

func adminCtx() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-request-path", "/api/v1/admin/traces"))
}

func userCtx() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-request-path", "/api/v1/traces"))
}

func TestListTracesRangeValidation(t *testing.T) {
	svc := newTestService(t)
	now := time.Now().Unix()

	// since > until.
	_, err := svc.ListTraces(adminCtx(), &tracingv1.ListTracesRequest{Since: now, Until: now - 100})
	require.Error(t, err)
	assert.Equal(t, 10404, int(apierrors.CodeOf(err)))

	// Range > 92 days.
	_, err = svc.ListTraces(adminCtx(), &tracingv1.ListTracesRequest{Since: now - 93*24*3600, Until: now})
	require.Error(t, err)
	assert.Equal(t, 10404, int(apierrors.CodeOf(err)))
}

func TestListTracesAdminFleet(t *testing.T) {
	svc := newTestService(t)
	db := svc.repo.db.DB(context.Background())
	now := time.Now().UTC()
	seedTrace(t, db, "trace-1", "org-a", "k1", "success", now)
	seedTrace(t, db, "trace-2", "org-b", "k2", "error", now)

	resp, err := svc.ListTraces(adminCtx(), &tracingv1.ListTracesRequest{
		Since: now.Add(-time.Hour).Unix(),
		Until: now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Len(t, resp.Traces, 2)
	// Admin surface carries the operator service id.
	assert.Equal(t, "svc-1", resp.Traces[0].ServiceId)
}

func TestListTracesUserScopedAndMasked(t *testing.T) {
	svc := newTestService(t)
	svc.SetSessionOrgResolver(&fakeOrgResolver{org: "org-a"})
	db := svc.repo.db.DB(context.Background())
	now := time.Now().UTC()
	seedTrace(t, db, "trace-1", "org-a", "k1", "success", now)
	seedTrace(t, db, "trace-2", "org-b", "k2", "error", now)

	resp, err := svc.ListTraces(userCtx(), &tracingv1.ListTracesRequest{
		Since: now.Add(-time.Hour).Unix(),
		Until: now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	// Only the caller's org traces.
	assert.Len(t, resp.Traces, 1)
	assert.Equal(t, "trace-1", resp.Traces[0].TraceId)
	// Service id masked to a phase label.
	assert.Equal(t, "inference", resp.Traces[0].ServiceId)
}

func TestGetTraceAdmin(t *testing.T) {
	svc := newTestService(t)
	db := svc.repo.db.DB(context.Background())
	now := time.Now().UTC()
	seedTrace(t, db, "trace-1", "org-a", "k1", "success", now)

	resp, err := svc.GetTrace(adminCtx(), &tracingv1.GetTraceRequest{TraceId: "trace-1"})
	require.NoError(t, err)
	assert.Equal(t, "trace-1", resp.Trace.TraceId)
	assert.Len(t, resp.Trace.Spans, 2)
	assert.Equal(t, "Model One", resp.Trace.ModelName)
	assert.Equal(t, "Key One", resp.Trace.ApiKeyName)
	assert.Equal(t, "svc-1", resp.Trace.ServiceId)
	assert.Equal(t, int64(400), resp.Trace.TtftMs)
	assert.Equal(t, int64(600), resp.Trace.GenerationMs)
}

func TestGetTraceNotFound(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.GetTrace(adminCtx(), &tracingv1.GetTraceRequest{TraceId: "nope"})
	require.Error(t, err)
	assert.Equal(t, 11101, int(apierrors.CodeOf(err)))
}

func TestGetTraceUserScoped(t *testing.T) {
	svc := newTestService(t)
	svc.SetSessionOrgResolver(&fakeOrgResolver{org: "org-a"})
	db := svc.repo.db.DB(context.Background())
	now := time.Now().UTC()
	seedTrace(t, db, "trace-1", "org-a", "k1", "success", now)
	seedTrace(t, db, "trace-2", "org-b", "k2", "error", now)

	// Caller's own trace.
	resp, err := svc.GetTrace(userCtx(), &tracingv1.GetTraceRequest{TraceId: "trace-1"})
	require.NoError(t, err)
	assert.Equal(t, "trace-1", resp.Trace.TraceId)
	assert.Equal(t, "inference", resp.Trace.ServiceId)

	// Another org's trace is not found.
	_, err = svc.GetTrace(userCtx(), &tracingv1.GetTraceRequest{TraceId: "trace-2"})
	require.Error(t, err)
	assert.Equal(t, 11101, int(apierrors.CodeOf(err)))
}

func TestAdminRoleGuard(t *testing.T) {
	svc := newTestService(t)
	svc.SetSessionOrgResolver(&fakeOrgResolver{org: "org-a"})
	svc.SetSessionUserResolver(&fakeUserResolver{user: "u1"})
	svc.SetRoleGuard(&fakeRoleGuard{allowed: false})

	_, err := svc.ListTraces(adminCtx(), &tracingv1.ListTracesRequest{})
	require.Error(t, err)
	assert.Equal(t, 10036, int(apierrors.CodeOf(err)))
}

func TestCaptureTrace(t *testing.T) {
	svc := newTestService(t)
	svc.CaptureTrace(context.Background(), &metering.TraceCaptureEvent{
		RequestID: "req-1", OrganizationID: "org-a", APIKeyID: "k1", ModelID: "m1",
		ServiceID: "svc-1", LatencyMs: 1000, TTFTMs: 400, GenerationMs: 600,
		Status: "success", CompletedAt: time.Now().Unix(),
	})

	resp, err := svc.GetTrace(adminCtx(), &tracingv1.GetTraceRequest{TraceId: "req-1"})
	require.NoError(t, err)
	assert.Equal(t, "req-1", resp.Trace.TraceId)
	assert.Len(t, resp.Trace.Spans, 2)
	assert.Equal(t, int64(400), resp.Trace.TtftMs)
}

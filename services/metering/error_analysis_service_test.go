package metering

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
	meteringv1 "github.com/go-taas/go-taas/proto/taas/metering/v1"
)

func errorAdminCtx() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-request-path", "/api/v1/admin/errors"))
}

func errorUserCtx() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-request-path", "/api/v1/errors"))
}

func newErrorAnalysisService(t *testing.T) *Service {
	t.Helper()
	db := newErrorAnalysisTestDB(t)
	return NewForFVT(db, nil)
}

func TestGetErrorAnalysisOverviewRangeValidation(t *testing.T) {
	svc := newErrorAnalysisService(t)
	now := time.Now().Unix()
	_, err := svc.GetErrorAnalysisOverview(errorAdminCtx(), &meteringv1.GetErrorAnalysisOverviewRequest{Since: now, Until: now - 100})
	require.Error(t, err)
	assert.Equal(t, 10404, int(apierrors.CodeOf(err)))
}

func TestGetErrorAnalysisOverview(t *testing.T) {
	svc := newErrorAnalysisService(t)
	db := svc.repo.db.DB(context.Background())
	now := time.Now().UTC()
	seedErrorLog(t, db, "org-a", "k1", "m1", "error", "rate_limit_exceeded", now)
	seedErrorLog(t, db, "org-a", "k1", "m1", "error", "timeout", now)
	seedErrorLog(t, db, "org-a", "k1", "m1", "success", "", now)

	resp, err := svc.GetErrorAnalysisOverview(errorAdminCtx(), &meteringv1.GetErrorAnalysisOverviewRequest{
		Since: now.Add(-time.Hour).Unix(),
		Until: now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Cards.ErrorCount)
	assert.Equal(t, int64(3), resp.Cards.RequestCount)
	assert.Equal(t, "rate_limit_exceeded", resp.Cards.TopCause)
	require.Len(t, resp.Causes, 2)
	assert.Equal(t, "rate_limit_exceeded", resp.Causes[0].ErrorCode)
}

func TestGetErrorAnalysisAdmin(t *testing.T) {
	svc := newErrorAnalysisService(t)
	db := svc.repo.db.DB(context.Background())
	now := time.Now().UTC()
	seedErrorLog(t, db, "org-a", "k1", "m1", "error", "rate_limit_exceeded", now)

	resp, err := svc.GetErrorAnalysis(errorAdminCtx(), &meteringv1.GetErrorAnalysisRequest{
		ErrorCode: "rate_limit_exceeded",
		Since:     now.Add(-time.Hour).Unix(),
		Until:     now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Cards.ErrorCount)
}

func TestGetErrorAnalysisNotFound(t *testing.T) {
	svc := newErrorAnalysisService(t)
	now := time.Now().Unix()
	_, err := svc.GetErrorAnalysis(errorAdminCtx(), &meteringv1.GetErrorAnalysisRequest{
		ErrorCode: "nope",
		Since:     now - 3600,
		Until:     now + 3600,
	})
	require.Error(t, err)
	assert.Equal(t, 11501, int(apierrors.CodeOf(err)))
}

func TestGetErrorAnalysisUserScoped(t *testing.T) {
	svc := newErrorAnalysisService(t)
	svc.SetSessionOrgResolver(&fakeMeteringOrgResolver{org: "org-a"})
	db := svc.repo.db.DB(context.Background())
	now := time.Now().UTC()
	seedErrorLog(t, db, "org-a", "k1", "m1", "error", "rate_limit_exceeded", now)
	seedErrorLog(t, db, "org-b", "k2", "m1", "error", "rate_limit_exceeded", now)

	// Caller org-a sees only its own errors.
	resp, err := svc.GetErrorAnalysis(errorUserCtx(), &meteringv1.GetErrorAnalysisRequest{
		ErrorCode: "rate_limit_exceeded",
		Since:     now.Add(-time.Hour).Unix(),
		Until:     now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Cards.ErrorCount)
}

func TestGetErrorAnalysisAdminRoleGuard(t *testing.T) {
	svc := newErrorAnalysisService(t)
	svc.SetSessionOrgResolver(&fakeMeteringOrgResolver{org: "org-a"})
	svc.SetSessionUserResolver(&fakeMeteringUserResolver{user: "u1"})
	svc.SetRoleGuard(&fakeMeteringRoleGuard{allowed: false})

	_, err := svc.GetErrorAnalysisOverview(errorAdminCtx(), &meteringv1.GetErrorAnalysisOverviewRequest{})
	require.Error(t, err)
	assert.Equal(t, 10036, int(apierrors.CodeOf(err)))
}
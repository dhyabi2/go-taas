package metering

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"gorm.io/gorm"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
	meteringv1 "github.com/go-taas/go-taas/proto/taas/metering/v1"
)

type fakeMeteringUserResolver struct{ user string }

func (f *fakeMeteringUserResolver) SessionUserID(_ context.Context) (string, error) { return f.user, nil }

type fakeMeteringRoleGuard struct{ allowed bool }

func (f *fakeMeteringRoleGuard) RequireRole(_ context.Context, _, _, _ string) error {
	if f.allowed {
		return nil
	}
	return apierrors.New(apierrors.CodeForbidden)
}

func newUsageKeysService(t *testing.T) *Service {
	t.Helper()
	db := newUsageKeysTestDB(t)
	svc := NewForFVT(db, nil)
	return svc
}

func usageAdminCtx() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-request-path", "/api/v1/admin/usage/keys"))
}

func usageUserCtx() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-request-path", "/api/v1/usage/keys"))
}

func TestGetUsageKeysOverviewRangeValidation(t *testing.T) {
	svc := newUsageKeysService(t)
	now := time.Now().Unix()
	_, err := svc.GetUsageKeysOverview(usageAdminCtx(), &meteringv1.GetUsageKeysOverviewRequest{Since: now, Until: now - 100})
	require.Error(t, err)
	assert.Equal(t, 10404, int(apierrors.CodeOf(err)))
}

func TestGetUsageKeysOverview(t *testing.T) {
	svc := newUsageKeysService(t)
	db := svc.repo.db.DB(context.Background())
	now := time.Now().UTC()
	seedUsageLog(t, db, "org-a", "k1", "m1", now, 100, "success", 10, 20)
	seedUsageLog(t, db, "org-a", "k2", "m1", now, 50, "success", 5, 10)
	seedCharge(t, db, "org-a", "k1", now.Unix(), 1.5)
	seedCharge(t, db, "org-a", "k2", now.Unix(), 2.5)

	resp, err := svc.GetUsageKeysOverview(usageAdminCtx(), &meteringv1.GetUsageKeysOverviewRequest{
		Since: now.Add(-time.Hour).Unix(),
		Until: now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Cards.RequestCount)
	assert.Len(t, resp.Keys, 2)
	// Top keys sorted by cost descending: k2 (250) before k1 (150).
	require.Len(t, resp.TopKeys, 2)
	assert.Equal(t, "k2", resp.TopKeys[0].ApiKeyId)
	assert.Equal(t, int64(250), resp.TopKeys[0].TotalCostCents)
}

func TestGetUsageKeysAdmin(t *testing.T) {
	svc := newUsageKeysService(t)
	db := svc.repo.db.DB(context.Background())
	now := time.Now().UTC()
	seedUsageLog(t, db, "org-a", "k1", "m1", now, 100, "success", 10, 20)
	seedCharge(t, db, "org-a", "k1", now.Unix(), 1.5)

	resp, err := svc.GetUsageKeys(usageAdminCtx(), &meteringv1.GetUsageKeysRequest{
		ApiKeyId: "k1",
		Since:    now.Add(-time.Hour).Unix(),
		Until:    now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Cards.RequestCount)
	assert.Equal(t, int64(150), resp.Cards.TotalCostCents)
}

func TestGetUsageKeysNotFound(t *testing.T) {
	svc := newUsageKeysService(t)
	now := time.Now().Unix()
	_, err := svc.GetUsageKeys(usageAdminCtx(), &meteringv1.GetUsageKeysRequest{
		ApiKeyId: "nope",
		Since:    now - 3600,
		Until:    now + 3600,
	})
	require.Error(t, err)
	assert.Equal(t, 11201, int(apierrors.CodeOf(err)))
}

func TestGetUsageKeysUserScoped(t *testing.T) {
	svc := newUsageKeysService(t)
	svc.SetSessionOrgResolver(&fakeMeteringOrgResolver{org: "org-a"})
	db := svc.repo.db.DB(context.Background())
	now := time.Now().UTC()
	seedUsageLog(t, db, "org-a", "k1", "m1", now, 100, "success", 10, 20)
	seedUsageLog(t, db, "org-b", "k2", "m1", now, 50, "success", 5, 10)

	// Caller org-a sees only its own key's usage.
	resp, err := svc.GetUsageKeys(usageUserCtx(), &meteringv1.GetUsageKeysRequest{
		ApiKeyId: "k1",
		Since:    now.Add(-time.Hour).Unix(),
		Until:    now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Cards.RequestCount)

	// Caller org-a cannot see org-b's key.
	_, err = svc.GetUsageKeys(usageUserCtx(), &meteringv1.GetUsageKeysRequest{
		ApiKeyId: "k2",
		Since:    now.Add(-time.Hour).Unix(),
		Until:    now.Add(time.Hour).Unix(),
	})
	require.Error(t, err)
	assert.Equal(t, 11201, int(apierrors.CodeOf(err)))
}

func TestGetUsageKeysAdminRoleGuard(t *testing.T) {
	svc := newUsageKeysService(t)
	svc.SetSessionOrgResolver(&fakeMeteringOrgResolver{org: "org-a"})
	svc.SetSessionUserResolver(&fakeMeteringUserResolver{user: "u1"})
	svc.SetRoleGuard(&fakeMeteringRoleGuard{allowed: false})

	_, err := svc.GetUsageKeysOverview(usageAdminCtx(), &meteringv1.GetUsageKeysOverviewRequest{})
	require.Error(t, err)
	assert.Equal(t, 10036, int(apierrors.CodeOf(err)))
}

type fakeMeteringOrgResolver struct{ org string }

func (f *fakeMeteringOrgResolver) SessionActiveOrg(_ context.Context) (string, error) { return f.org, nil }

var _ = gorm.ErrRecordNotFound
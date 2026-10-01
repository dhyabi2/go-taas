package billing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
	billingv1 "github.com/go-taas/go-taas/proto/taas/billing/v1"
)

func costAdminCtx() context.Context {
	return metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-request-path", "/api/v1/admin/cost"))
}

func costUserCtx() context.Context {
	return metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-request-path", "/api/v1/cost"))
}

func TestGetCostAnalyticsOverviewRangeValidation(t *testing.T) {
	svc := newBillingTestService(t)
	now := time.Now().Unix()
	_, err := svc.GetCostAnalyticsOverview(costAdminCtx(), &billingv1.GetCostAnalyticsOverviewRequest{Since: now, Until: now - 100})
	require.Error(t, err)
	assert.Equal(t, 10404, int(apierrors.CodeOf(err)))
}

func TestGetCostAnalyticsOverview(t *testing.T) {
	svc := newBillingTestService(t)
	db := svc.repo.db.DB(context.Background())
	now := time.Now().Unix()
	seedCostCharge(t, db, "org-a", "k1", "m1", now, 1.5, 10, 20)
	seedCostCharge(t, db, "org-a", "k2", "m2", now, 2.5, 5, 10)

	resp, err := svc.GetCostAnalyticsOverview(costAdminCtx(), &billingv1.GetCostAnalyticsOverviewRequest{
		Since: now - 3600,
		Until: now + 3600,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(400), resp.Cards.TotalCostCents)
	assert.Equal(t, int64(45), resp.Cards.TotalTokens)
	assert.Len(t, resp.Breakdown, 1)
	assert.Len(t, resp.Series, 1)
}

func TestGetCostAnalyticsOverviewInvalidDimension(t *testing.T) {
	svc := newBillingTestService(t)
	now := time.Now().Unix()
	_, err := svc.GetCostAnalyticsOverview(costAdminCtx(), &billingv1.GetCostAnalyticsOverviewRequest{
		Since:     now - 3600,
		Until:     now + 3600,
		Dimension: "bogus",
	})
	require.Error(t, err)
	assert.Equal(t, 11301, int(apierrors.CodeOf(err)))
}

func TestGetCostAnalyticsAdmin(t *testing.T) {
	svc := newBillingTestService(t)
	db := svc.repo.db.DB(context.Background())
	now := time.Now().Unix()
	seedCostCharge(t, db, "org-a", "k1", "m1", now, 1.5, 10, 20)

	resp, err := svc.GetCostAnalytics(costAdminCtx(), &billingv1.GetCostAnalyticsRequest{
		Dimension: "model",
		Value:     "m1",
		Since:     now - 3600,
		Until:     now + 3600,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(150), resp.Cards.TotalCostCents)
}

func TestGetCostAnalyticsInvalidDimension(t *testing.T) {
	svc := newBillingTestService(t)
	now := time.Now().Unix()
	_, err := svc.GetCostAnalytics(costAdminCtx(), &billingv1.GetCostAnalyticsRequest{
		Dimension: "bogus",
		Value:     "m1",
		Since:     now - 3600,
		Until:     now + 3600,
	})
	require.Error(t, err)
	assert.Equal(t, 11301, int(apierrors.CodeOf(err)))
}

func TestGetCostAnalyticsNotFound(t *testing.T) {
	svc := newBillingTestService(t)
	now := time.Now().Unix()
	_, err := svc.GetCostAnalytics(costAdminCtx(), &billingv1.GetCostAnalyticsRequest{
		Dimension: "model",
		Value:     "nope",
		Since:     now - 3600,
		Until:     now + 3600,
	})
	require.Error(t, err)
	assert.Equal(t, 11302, int(apierrors.CodeOf(err)))
}

func TestGetCostAnalyticsUserScoped(t *testing.T) {
	svc := newBillingTestService(t)
	svc.SetSessionOrgResolver(fakeSessionOrgResolver{org: "org-a"})
	db := svc.repo.db.DB(context.Background())
	now := time.Now().Unix()
	seedCostCharge(t, db, "org-a", "k1", "m1", now, 1.5, 10, 20)
	seedCostCharge(t, db, "org-b", "k2", "m2", now, 2.5, 5, 10)

	// Caller org-a sees only its own cost.
	resp, err := svc.GetCostAnalytics(costUserCtx(), &billingv1.GetCostAnalyticsRequest{
		Dimension: "model",
		Value:     "m1",
		Since:     now - 3600,
		Until:     now + 3600,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(150), resp.Cards.TotalCostCents)

	// Caller org-a cannot see org-b's model.
	_, err = svc.GetCostAnalytics(costUserCtx(), &billingv1.GetCostAnalyticsRequest{
		Dimension: "model",
		Value:     "m2",
		Since:     now - 3600,
		Until:     now + 3600,
	})
	require.Error(t, err)
	assert.Equal(t, 11302, int(apierrors.CodeOf(err)))
}

func TestGetCostAnalyticsAdminRoleGuard(t *testing.T) {
	svc := newBillingTestService(t)
	svc.SetSessionOrgResolver(fakeSessionOrgResolver{org: "org-a"})
	svc.SetSessionUserResolver(fakeSessionUserResolver{user: "u1"})
	svc.SetRoleGuard(fakeRoleGuard{allowed: false})

	_, err := svc.GetCostAnalyticsOverview(costAdminCtx(), &billingv1.GetCostAnalyticsOverviewRequest{})
	require.Error(t, err)
	assert.Equal(t, 10036, int(apierrors.CodeOf(err)))
}

func TestGetCostAnalyticsOverviewUserScoped(t *testing.T) {
	svc := newBillingTestService(t)
	svc.SetSessionOrgResolver(fakeSessionOrgResolver{org: "org-a"})
	db := svc.repo.db.DB(context.Background())
	now := time.Now().Unix()
	seedCostCharge(t, db, "org-a", "k1", "m1", now, 1.5, 10, 20)
	seedCostCharge(t, db, "org-a", "k2", "m2", now, 2.5, 5, 10)
	seedCostCharge(t, db, "org-b", "k3", "m3", now, 3.5, 20, 40)

	// Caller org-a sees only its own cost.
	resp, err := svc.GetCostAnalyticsOverview(costUserCtx(), &billingv1.GetCostAnalyticsOverviewRequest{
		Since: now - 3600,
		Until: now + 3600,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(400), resp.Cards.TotalCostCents)
	assert.Equal(t, int64(45), resp.Cards.TotalTokens)
	require.Len(t, resp.Breakdown, 1)
	// org-b's dimension value must not leak into the tenant view.
	assert.Equal(t, "org-a", resp.Breakdown[0].DimensionValue)
}

func TestDimensionName(t *testing.T) {
	svc := newBillingTestService(t)
	db := svc.repo.db.DB(context.Background())
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS models (id TEXT PRIMARY KEY, name TEXT)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS api_keys (id TEXT PRIMARY KEY, name TEXT)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO models (id, name) VALUES ('m1', 'Model One')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO api_keys (id, name) VALUES ('k1', 'Key One')`).Error)

	assert.Equal(t, "Model One", svc.dimensionName("model", "m1"))
	assert.Equal(t, "Key One", svc.dimensionName("api_key", "k1"))
	assert.Equal(t, "org-a", svc.dimensionName("organization", "org-a"))
	assert.Equal(t, "unknown", svc.dimensionName("model", "unknown"))
}

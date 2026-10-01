package billing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-taas/go-taas/pkg/config"
	apierrors "github.com/go-taas/go-taas/pkg/errors"
	billingv1 "github.com/go-taas/go-taas/proto/taas/billing/v1"
)

// TestFitLinearTrend verifies the least-squares fit (feature #36, AD3).
func TestFitLinearTrend(t *testing.T) {
	// A perfectly linear series y = 2x + 1.
	trend := fitLinearTrend([]int64{1, 3, 5, 7, 9})
	assert.InDelta(t, 2.0, trend.slope, 0.001)
	assert.InDelta(t, 1.0, trend.intercept, 0.001)

	// Single point → flat.
	flat := fitLinearTrend([]int64{5})
	assert.Equal(t, 0.0, flat.slope)
	assert.Equal(t, 5.0, flat.intercept)

	// Empty → zero.
	empty := fitLinearTrend(nil)
	assert.Equal(t, 0.0, empty.slope)
}

// TestExtrapolateForecastClamped verifies the horizon is clamped to the
// history length (feature #36, AC3).
func TestExtrapolateForecastClamped(t *testing.T) {
	history := []forecastBucket{
		{Bucket: 1000, TotalTokens: 10},
		{Bucket: 2000, TotalTokens: 20},
		{Bucket: 3000, TotalTokens: 30},
	}
	trend := fitLinearTrend([]int64{10, 20, 30})
	// horizon 30 days with daily buckets = 30 buckets, but history is 3
	// buckets → clamped to 3.
	out := extrapolateForecast(history, trend, 30, 24*3600)
	require.Len(t, out, 3)
	// The forecast continues the trend upward.
	assert.Greater(t, out[0].TotalTokens, int64(30))
	assert.Greater(t, out[2].TotalTokens, out[0].TotalTokens)
}

// TestDeriveConfidenceBandWidens verifies the band widens with the
// forecast distance (feature #36, AC3).
func TestDeriveConfidenceBandWidens(t *testing.T) {
	history := []forecastBucket{
		{Bucket: 1000, TotalTokens: 10},
		{Bucket: 2000, TotalTokens: 20},
		{Bucket: 3000, TotalTokens: 30},
	}
	forecast := []forecastBucket{
		{Bucket: 4000, TotalTokens: 40},
		{Bucket: 5000, TotalTokens: 50},
		{Bucket: 6000, TotalTokens: 60},
	}
	widths := deriveConfidenceBand(history, forecast, func(b forecastBucket) int64 { return b.TotalTokens })
	require.Len(t, widths, 3)
	// The band widens with distance.
	assert.Greater(t, widths[2], widths[0])
	assert.Greater(t, widths[1], widths[0])
}

// TestComputeDataThrough verifies the watermark (feature #36, AD6).
func TestComputeDataThrough(t *testing.T) {
	history := []forecastBucket{
		{Bucket: 1000, TotalTokens: 10},
		{Bucket: 2000, TotalTokens: 20},
	}
	assert.Equal(t, int64(1000), computeDataThrough(history, 1000))
	assert.Equal(t, int64(0), computeDataThrough(nil, 1000))
}

// TestGetForecast verifies the happy path returns history/forecast/
// summary/data_through (feature #36, AC1).
func TestGetForecast(t *testing.T) {
	svc := newBillingTestService(t)
	db := svc.repo.db.DB(context.Background())
	now := time.Now().Unix()
	// Seed several hourly charges over the range.
	for i := 0; i < 5; i++ {
		seedCostCharge(t, db, "org-a", "k1", "m1", now-int64(4-i)*3600, 1.0, 10, 10)
	}

	resp, err := svc.GetForecast(costAdminCtx(), &billingv1.GetForecastRequest{
		Since: now - 5*3600,
		Until: now,
	})
	require.NoError(t, err)
	assert.Equal(t, "linear_trend", resp.GetMethod())
	assert.Equal(t, int32(30), resp.GetHorizonDays())
	assert.NotEmpty(t, resp.GetHistory())
	assert.NotEmpty(t, resp.GetForecast())
	assert.NotNil(t, resp.GetSummary())
	// The forecast points carry the confidence band.
	for _, p := range resp.GetForecast() {
		assert.GreaterOrEqual(t, p.GetUpperTokens(), p.GetTotalTokens())
		assert.LessOrEqual(t, p.GetLowerTokens(), p.GetTotalTokens())
	}
}

// TestGetForecastValidation verifies the validation matrix (feature #36,
// AC2): invalid range, unsupported dimension, unknown value, horizon.
func TestGetForecastValidation(t *testing.T) {
	svc := newBillingTestService(t)
	now := time.Now().Unix()

	// since > until → 10404.
	_, err := svc.GetForecast(costAdminCtx(), &billingv1.GetForecastRequest{Since: now, Until: now - 100})
	require.Error(t, err)
	assert.Equal(t, 10404, int(apierrors.CodeOf(err)))

	// Unsupported dimension → 11301.
	_, err = svc.GetForecast(costAdminCtx(), &billingv1.GetForecastRequest{
		Since: now - 3600, Until: now, Dimension: "bogus",
	})
	require.Error(t, err)
	assert.Equal(t, 11301, int(apierrors.CodeOf(err)))

	// User surface with organization dimension → 11301.
	svc.SetSessionOrgResolver(fakeSessionOrgResolver{org: "org-a"})
	_, err = svc.GetForecast(costUserCtx(), &billingv1.GetForecastRequest{
		Since: now - 3600, Until: now, Dimension: "organization",
	})
	require.Error(t, err)
	assert.Equal(t, 11301, int(apierrors.CodeOf(err)))

	// horizon > 90 → 10404.
	_, err = svc.GetForecast(costAdminCtx(), &billingv1.GetForecastRequest{
		Since: now - 3600, Until: now, HorizonDays: 91,
	})
	require.Error(t, err)
	assert.Equal(t, 10404, int(apierrors.CodeOf(err)))

	// Unknown dimension value → 11302.
	_, err = svc.GetForecast(costAdminCtx(), &billingv1.GetForecastRequest{
		Since: now - 3600, Until: now, Dimension: "model", DimensionValue: "does-not-exist",
	})
	require.Error(t, err)
	assert.Equal(t, 11302, int(apierrors.CodeOf(err)))
}

// TestForecastConfigBounds verifies the forecast config-read helpers
// honor the configured bounds (feature #36, AD3/AD4).
func TestForecastConfigBounds(t *testing.T) {
	cfg := &config.Configuration{}
	cfg.Billing.Currency = "USD"
	cfg.Billing.Forecast.HorizonDefaultDays = 14
	cfg.Billing.Forecast.HorizonMaxDays = 60
	cfg.Billing.Forecast.RangeMaxDays = 45
	config.SetConfigForTest(cfg)
	t.Cleanup(func() { config.SetConfigForTest(&config.Configuration{}) })

	svc := NewForFVT(newBillingTestDB(t), nil)
	assert.Equal(t, 14, svc.forecastHorizonDefaultDays())
	assert.Equal(t, 60, svc.forecastHorizonMaxDays())
	assert.Equal(t, 45, svc.forecastRangeMaxDays())

	// The configured max horizon is honored by GetForecast.
	now := time.Now().Unix()
	_, err := svc.GetForecast(costAdminCtx(), &billingv1.GetForecastRequest{
		Since: now - 3600, Until: now, HorizonDays: 61,
	})
	require.Error(t, err)
	assert.Equal(t, 10404, int(apierrors.CodeOf(err)))
}

// TestGetForecastUserScoped verifies the user binding is tenant-scoped
// (feature #36, AC7).
func TestGetForecastUserScoped(t *testing.T) {
	svc := newBillingTestService(t)
	svc.SetSessionOrgResolver(fakeSessionOrgResolver{org: "org-a"})
	db := svc.repo.db.DB(context.Background())
	now := time.Now().Unix()
	seedCostCharge(t, db, "org-a", "k1", "m1", now, 1.0, 10, 10)
	seedCostCharge(t, db, "org-b", "k2", "m2", now, 1.0, 10, 10)

	// User context resolves org-a via the session.
	resp, err := svc.GetForecast(costUserCtx(), &billingv1.GetForecastRequest{
		Since: now - 3600, Until: now + 3600, Dimension: "model",
	})
	require.NoError(t, err)
	// Only org-a's charge is in the history.
	var total int64
	for _, h := range resp.GetHistory() {
		total += h.GetTotalTokens()
	}
	assert.Equal(t, int64(20), total)
}

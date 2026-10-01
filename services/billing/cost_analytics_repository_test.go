package billing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedCostCharge(t *testing.T, db *gorm.DB, org, key, model string, periodStart int64, amount float64, prompt, completion int64) {
	t.Helper()
	row := ChargeRecord{
		ID:               "c-" + key + "-" + itoa(periodStart),
		OrganizationID:   org,
		APIKeyID:         key,
		ModelID:          model,
		AcceleratorType:  "A800",
		PeriodStart:      periodStart,
		PeriodEnd:        periodStart + 3600,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		RequestCount:     1,
		Amount:           amount,
		Currency:         "USD",
		Priced:           true,
	}
	require.NoError(t, db.Create(&row).Error)
}

func TestAggregateCost(t *testing.T) {
	db := newBillingTestDB(t)
	now := time.Now().Unix()
	seedCostCharge(t, db, "org-a", "k1", "m1", now, 1.5, 10, 20)
	seedCostCharge(t, db, "org-a", "k2", "m2", now, 2.5, 5, 10)
	seedCostCharge(t, db, "org-b", "k3", "m1", now, 3.5, 10, 10)

	repo := NewCostAnalyticsRepository(db)
	buckets, breakdown, err := repo.AggregateCost(context.Background(), "", "organization", "", now-3600, now+3600, 3600)
	require.NoError(t, err)
	assert.Len(t, buckets, 1)
	assert.Equal(t, int64(750), buckets[0].TotalCostCents)
	assert.Len(t, breakdown, 2)
	// Sorted by cost descending: org-b (350) before org-a (400? no, org-a=400, org-b=350).
	assert.Equal(t, "org-a", breakdown[0].DimensionValue)
	assert.Equal(t, int64(400), breakdown[0].TotalCostCents)
}

func TestAggregateCostByModel(t *testing.T) {
	db := newBillingTestDB(t)
	now := time.Now().Unix()
	seedCostCharge(t, db, "org-a", "k1", "m1", now, 1.5, 10, 20)
	seedCostCharge(t, db, "org-a", "k2", "m2", now, 2.5, 5, 10)

	repo := NewCostAnalyticsRepository(db)
	_, breakdown, err := repo.AggregateCost(context.Background(), "org-a", "model", "", now-3600, now+3600, 3600)
	require.NoError(t, err)
	assert.Len(t, breakdown, 2)
	assert.Equal(t, "m2", breakdown[0].DimensionValue)
	assert.Equal(t, int64(250), breakdown[0].TotalCostCents)
}

func TestAggregateCostSingleValue(t *testing.T) {
	db := newBillingTestDB(t)
	now := time.Now().Unix()
	seedCostCharge(t, db, "org-a", "k1", "m1", now, 1.5, 10, 20)
	seedCostCharge(t, db, "org-a", "k2", "m2", now, 2.5, 5, 10)

	repo := NewCostAnalyticsRepository(db)
	buckets, _, err := repo.AggregateCost(context.Background(), "org-a", "model", "m1", now-3600, now+3600, 3600)
	require.NoError(t, err)
	assert.Len(t, buckets, 1)
	assert.Equal(t, int64(150), buckets[0].TotalCostCents)
}

func TestCostDataThrough(t *testing.T) {
	db := newBillingTestDB(t)
	now := time.Now().Unix()
	seedCostCharge(t, db, "org-a", "k1", "m1", now, 1.5, 10, 20)

	repo := NewCostAnalyticsRepository(db)
	watermark, err := repo.DataThrough(context.Background(), "org-a", now-3600, now+3600, 3600)
	require.NoError(t, err)
	lastBucket := (now / 3600) * 3600
	assert.Equal(t, lastBucket-3600, watermark)
}

func TestCostBucketSizeForRange(t *testing.T) {
	assert.Equal(t, int64(3600), bucketSizeForRange(0, 7*24*3600))
	assert.Equal(t, int64(24*3600), bucketSizeForRange(0, 8*24*3600))
}

func TestCostDimensionValue(t *testing.T) {
	row := costChargeRow{OrganizationID: "org-a", APIKeyID: "k1", ModelID: "m1"}
	assert.Equal(t, "org-a", dimensionValue(row, "organization"))
	assert.Equal(t, "m1", dimensionValue(row, "model"))
	assert.Equal(t, "k1", dimensionValue(row, "api_key"))
	assert.Equal(t, "", dimensionValue(row, "bogus"))
}

var _ = gorm.ErrRecordNotFound
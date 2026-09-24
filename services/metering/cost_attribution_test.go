package metering

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newCostTestDB creates an in-memory sqlite DB with the price_entries
// projection plus the metering tables.
func newCostTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Voucher{}, &UsageRecord{}, &priceEntryRow{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return db
}

// mustInsertPrice inserts a price entry row directly.
func mustInsertPrice(t *testing.T, db *gorm.DB, modelID, card string, effectiveFrom int64, in, out, cached float64) {
	t.Helper()
	row := priceEntryRow{
		ModelID:               modelID,
		AcceleratorType:       card,
		InputPricePerMillion:  in,
		OutputPricePerMillion: out,
		CachedPricePerMillion: cached,
		Currency:              "USD",
		EffectiveFrom:         effectiveFrom,
		TiersJSON:             []byte("[]"),
	}
	require.NoError(t, db.Create(&row).Error)
}

// AC4: the estimate mirrors the D2 formula (prompt x in + completion x
// out + cached x cache, per 1M, rounded to 2 decimals -> cents).
func TestEstimateCostFormula(t *testing.T) {
	db := newCostTestDB(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	mustInsertPrice(t, db, "model-a", "default", 0, 3.00, 6.00, 1.00)

	attributor := NewCostAttributor(db)
	v := &Voucher{
		ID:               "v-1",
		ModelID:          "model-a",
		PromptTokens:     1000,
		CompletionTokens: 500,
		CachedTokens:     100,
		CompletedAt:      at,
	}
	cost, priced, err := attributor.estimateCost(ctx, v)
	require.NoError(t, err)
	assert.True(t, priced)
	// 1000*3/1M + 500*6/1M + 100*1/1M = 0.003 + 0.003 + 0.0001 = 0.0061
	// rounded to 2 decimals = 0.01 -> 1 cent.
	assert.Equal(t, int64(1), cost)
}

// AC4: the (model, default) fallback applies when the specific card has
// no price entry.
func TestEstimateCostDefaultFallback(t *testing.T) {
	db := newCostTestDB(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	mustInsertPrice(t, db, "model-a", "default", 0, 3.00, 6.00, 1.00)

	attributor := NewCostAttributor(db)
	svc := "A800"
	v := &Voucher{
		ID:               "v-1",
		ModelID:          "model-a",
		ServiceID:        &svc,
		PromptTokens:     1000,
		CompletionTokens: 500,
		CachedTokens:     100,
		CompletedAt:      at,
	}
	cost, priced, err := attributor.estimateCost(ctx, v)
	require.NoError(t, err)
	assert.True(t, priced)
	assert.Equal(t, int64(1), cost)
}

// AC4: no price entry -> priced false, cost 0.
func TestEstimateCostUnpriced(t *testing.T) {
	db := newCostTestDB(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

	attributor := NewCostAttributor(db)
	v := &Voucher{
		ID:               "v-1",
		ModelID:          "model-unknown",
		PromptTokens:     1000,
		CompletionTokens: 500,
		CompletedAt:      at,
	}
	cost, priced, err := attributor.estimateCost(ctx, v)
	require.NoError(t, err)
	assert.False(t, priced)
	assert.Equal(t, int64(0), cost)
}

// FR2.4: attributePage caches price lookups per (model, card, day) and
// returns per-voucher costs.
func TestAttributePage(t *testing.T) {
	db := newCostTestDB(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	mustInsertPrice(t, db, "model-a", "default", 0, 3.00, 6.00, 1.00)

	attributor := NewCostAttributor(db)
	vouchers := []*Voucher{
		{ID: "v-1", ModelID: "model-a", PromptTokens: 1000, CompletionTokens: 500, CachedTokens: 100, CompletedAt: at},
		{ID: "v-2", ModelID: "model-a", PromptTokens: 1000, CompletionTokens: 500, CachedTokens: 100, CompletedAt: at},
		{ID: "v-3", ModelID: "model-unknown", PromptTokens: 1000, CompletionTokens: 500, CompletedAt: at},
	}
	costs, priced, err := attributor.attributePage(ctx, vouchers)
	require.NoError(t, err)
	assert.Equal(t, int64(1), costs["v-1"])
	assert.True(t, priced["v-1"])
	assert.Equal(t, int64(1), costs["v-2"])
	assert.True(t, priced["v-2"])
	assert.Equal(t, int64(0), costs["v-3"])
	assert.False(t, priced["v-3"])
}

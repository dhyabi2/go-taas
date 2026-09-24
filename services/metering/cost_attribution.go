package metering

import (
	"context"
	"errors"
	"math"
	"time"

	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/database"
)

// defaultCardType is the per-model fallback sentinel, mirroring the
// billing module's D6 chain (AD3).
const defaultCardType = "default"

// priceEntryRow is the read-only projection of billing's price_entries
// table used for per-request cost attribution (AD3). Defined locally so
// the metering module reads billing's table without importing billing.
type priceEntryRow struct {
	ModelID               string
	AcceleratorType       string
	InputPricePerMillion  float64
	OutputPricePerMillion float64
	CachedPricePerMillion float64
	Currency              string
	EffectiveFrom         int64
	TiersJSON             []byte
}

// TableName overrides the default GORM table name to billing's table.
func (priceEntryRow) TableName() string { return "price_entries" }

// CostAttributor computes the per-request estimated cost of a voucher
// on read, mirroring the billing module's computeAmount D2 formula
// against the effective-dated price at completed_at (AD3/AD4). Vouchers
// stay immutable; the estimate is never stored.
type CostAttributor struct {
	db *database.Manager
}

// NewCostAttributor constructs a CostAttributor bound to a database
// Manager.
func NewCostAttributor(db *gorm.DB) *CostAttributor {
	return &CostAttributor{db: database.NewManager(db)}
}

// applicablePrice returns the price version in effect for (model, card)
// at the given unix time: the greatest effective_from <= at. nil when
// absent.
func (c *CostAttributor) applicablePrice(ctx context.Context, modelID, cardType string, at int64) (*priceEntryRow, error) {
	var row priceEntryRow
	err := c.db.DB(ctx).
		Where("model_id = ? AND accelerator_type = ? AND effective_from <= ?",
			modelID, cardType, at).
		Order("effective_from DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// estimateCost computes the per-request estimated cost in minor units
// for a voucher. It mirrors billing's computeAmount (AD4): prompt x in
// + completion x out + cached x cache, per 1M, rounded to 2 decimals,
// then converted to integer cents. priced is false (cost 0) when no
// price entry applies.
func (c *CostAttributor) estimateCost(ctx context.Context, v *Voucher) (costCents int64, priced bool, err error) {
	at := v.CompletedAt.Unix()
	card := defaultCardType
	if v.ServiceID != nil && *v.ServiceID != "" {
		card = *v.ServiceID
	}
	entry, err := c.applicablePrice(ctx, v.ModelID, card, at)
	if err != nil {
		return 0, false, err
	}
	if entry == nil && card != defaultCardType {
		entry, err = c.applicablePrice(ctx, v.ModelID, defaultCardType, at)
		if err != nil {
			return 0, false, err
		}
	}
	if entry == nil {
		return 0, false, nil
	}
	amount := float64(v.PromptTokens)*entry.InputPricePerMillion/1_000_000 +
		float64(v.CompletionTokens)*entry.OutputPricePerMillion/1_000_000 +
		float64(v.CachedTokens)*entry.CachedPricePerMillion/1_000_000
	amount = math.Round(amount*100) / 100
	return int64(math.Round(amount * 100)), true, nil
}

// attributePage computes the estimated cost for a page of vouchers,
// caching price lookups per (model, card, day) so one lookup serves
// many rows (FR2.4).
func (c *CostAttributor) attributePage(ctx context.Context, vouchers []*Voucher) (map[string]int64, map[string]bool, error) {
	costs := make(map[string]int64, len(vouchers))
	priced := make(map[string]bool, len(vouchers))
	type cacheKey struct {
		model string
		card  string
		day   int64
	}
	cache := make(map[cacheKey]*priceEntryRow)
	for _, v := range vouchers {
		card := defaultCardType
		if v.ServiceID != nil && *v.ServiceID != "" {
			card = *v.ServiceID
		}
		key := cacheKey{v.ModelID, card, dayStart(v.CompletedAt.Unix())}
		entry, ok := cache[key]
		if !ok {
			at := v.CompletedAt.Unix()
			e, err := c.applicablePrice(ctx, v.ModelID, card, at)
			if err != nil {
				return nil, nil, err
			}
			if e == nil && card != defaultCardType {
				e, err = c.applicablePrice(ctx, v.ModelID, defaultCardType, at)
				if err != nil {
					return nil, nil, err
				}
			}
			cache[key] = e
			entry = e
		}
		if entry == nil {
			costs[v.ID] = 0
			priced[v.ID] = false
			continue
		}
		amount := float64(v.PromptTokens)*entry.InputPricePerMillion/1_000_000 +
			float64(v.CompletionTokens)*entry.OutputPricePerMillion/1_000_000 +
			float64(v.CachedTokens)*entry.CachedPricePerMillion/1_000_000
		amount = math.Round(amount*100) / 100
		costs[v.ID] = int64(math.Round(amount * 100))
		priced[v.ID] = true
	}
	return costs, priced, nil
}

// dayStart returns the UTC day start (unix seconds) of a unix time.
func dayStart(ts int64) int64 {
	return time.Unix(ts, 0).UTC().Truncate(24 * time.Hour).Unix()
}

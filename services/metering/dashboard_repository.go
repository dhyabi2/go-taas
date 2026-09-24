package metering

import (
	"context"

	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/database"
)

// DashboardGroupBy is the aggregation dimension of the usage dashboard.
type DashboardGroupBy string

// Supported dashboard group-by dimensions (feature #9, AD2).
const (
	GroupByAPIKey          DashboardGroupBy = "api_key"
	GroupByModel           DashboardGroupBy = "model"
	GroupByAcceleratorType DashboardGroupBy = "accelerator_type"
)

// chargeRecordRow is the read-only projection of billing's
// charge_records table that the dashboard aggregates over (AD1). It is
// defined locally so the metering module reads billing's table without
// importing the billing package.
type chargeRecordRow struct {
	OrganizationID   string
	APIKeyID         string
	ModelID          string
	AcceleratorType  string
	PeriodStart      int64
	PromptTokens     int64
	CompletionTokens int64
	CachedTokens     int64
	ReasoningTokens  int64
	RequestCount     int64
	Amount           float64
	Priced           bool
}

// TableName overrides the default GORM table name to billing's table.
func (chargeRecordRow) TableName() string { return "charge_records" }

// DashboardAggregateRow is one (day, group) aggregate of the dashboard
// query: cost cents plus the token/request sums and the priced flag.
type DashboardAggregateRow struct {
	Day              int64
	GroupKey         string
	CostCents        int64
	PromptTokens     int64
	CompletionTokens int64
	CachedTokens     int64
	ReasoningTokens  int64
	RequestCount     int64
	// PricedRaw is the MIN(CASE WHEN priced THEN 1 ELSE 0 END) result
	// (0/1), portable across sqlite and Postgres; converted to Priced.
	PricedRaw int
	Priced    bool
}

// DashboardRepository aggregates charge records into the usage
// dashboard's daily buckets and resolves the charge watermark. It reads
// billing's charge_records read-only over the shared database (AD1).
type DashboardRepository struct {
	db *database.Manager
}

// NewDashboardRepository constructs a DashboardRepository bound to a
// database Manager.
func NewDashboardRepository(db *gorm.DB) *DashboardRepository {
	return &DashboardRepository{db: database.NewManager(db)}
}

// groupColumn maps a group-by dimension to the charge_records column.
func groupColumn(g DashboardGroupBy) string {
	switch g {
	case GroupByModel:
		return "model_id"
	case GroupByAcceleratorType:
		return "accelerator_type"
	default:
		return "api_key_id"
	}
}

// DashboardAggregate aggregates charge_records for one organization and
// range, grouped by UTC day x group dimension (AD2). The cost is
// SUM(amount) * 100 rounded to integer cents; priced is bool_and(priced)
// so an understated cost is never silent (FR1.3).
func (r *DashboardRepository) DashboardAggregate(ctx context.Context, orgID string, since, until int64, groupBy DashboardGroupBy) ([]DashboardAggregateRow, error) {
	col := groupColumn(groupBy)
	var rows []DashboardAggregateRow
	err := r.db.DB(ctx).Model(&chargeRecordRow{}).
		Select("(period_start / 86400) * 86400 AS day, "+
			col+" AS group_key, "+
			"ROUND(SUM(amount) * 100) AS cost_cents, "+
			"COALESCE(SUM(prompt_tokens),0) AS prompt_tokens, "+
			"COALESCE(SUM(completion_tokens),0) AS completion_tokens, "+
			"COALESCE(SUM(cached_tokens),0) AS cached_tokens, "+
			"COALESCE(SUM(reasoning_tokens),0) AS reasoning_tokens, "+
			"COALESCE(SUM(request_count),0) AS request_count, "+
			"MIN(CASE WHEN priced THEN 1 ELSE 0 END) AS priced_raw").
		Where("organization_id = ? AND period_start >= ? AND period_start < ?",
			orgID, since, until).
		Group("day, " + col).
		Order("day ASC, group_key ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Priced = rows[i].PricedRaw != 0
	}
	return rows, nil
}

// ChargeWatermark returns the last complete hour covered by charging
// for the organization: the max period_start of its charge records. 0
// when the org has no charge records yet.
func (r *DashboardRepository) ChargeWatermark(ctx context.Context, orgID string) (int64, error) {
	var agg struct {
		Max int64
	}
	err := r.db.DB(ctx).Model(&chargeRecordRow{}).
		Select("COALESCE(MAX(period_start),0) AS max").
		Where("organization_id = ?", orgID).
		Scan(&agg).Error
	if err != nil {
		return 0, err
	}
	return agg.Max, nil
}

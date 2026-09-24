package metering

import (
	"context"
	"time"

	meteringv1 "github.com/go-taas/go-taas/proto/taas/metering/v1"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// dashboardMaxRangeSeconds caps the dashboard range at 92 days (AD7).
const dashboardMaxRangeSeconds = 92 * 24 * 3600

// GetUsageDashboard returns summary cards and daily time buckets grouped
// by a dimension, joining metering usage with billing charge records for
// a unified usage x cost view (feature #9, AC1-AC8).
func (s *Service) GetUsageDashboard(ctx context.Context, req *meteringv1.GetUsageDashboardRequest) (*meteringv1.GetUsageDashboardResponse, error) {
	orgID, err := resolveOrganizationID(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.checkOrg(ctx, orgID, false); err != nil {
		return nil, err
	}
	since, until, err := validateDashboardRange(req.GetSince(), req.GetUntil())
	if err != nil {
		return nil, err
	}
	groupBy := dashboardGroupBy(req.GetGroupBy())
	if groupBy == "" {
		groupBy = GroupByAPIKey
	}
	repo, err := s.dashboardRepo()
	if err != nil {
		return nil, err
	}
	rows, err := repo.DashboardAggregate(ctx, orgID, since, until, groupBy)
	if err != nil {
		return nil, err
	}
	watermark, err := repo.ChargeWatermark(ctx, orgID)
	if err != nil {
		return nil, err
	}

	// Build the daily buckets, one per UTC day in the range (AD7).
	buckets := make(map[int64][]*meteringv1.DashboardGroup)
	for _, row := range rows {
		day := dayStart(row.Day)
		buckets[day] = append(buckets[day], &meteringv1.DashboardGroup{
			GroupKey:         row.GroupKey,
			CostCents:        row.CostCents,
			PromptTokens:     row.PromptTokens,
			CompletionTokens: row.CompletionTokens,
			CachedTokens:     row.CachedTokens,
			ReasoningTokens:  row.ReasoningTokens,
			RequestCount:     row.RequestCount,
			Priced:           row.Priced,
		})
	}
	daily := make([]*meteringv1.DailyBucket, 0)
	for day := dayStart(since); day < until; day += 24 * 3600 {
		daily = append(daily, &meteringv1.DailyBucket{
			Date:   day,
			Groups: buckets[day],
		})
	}

	// Summary cards derived from the same rows.
	cards := &meteringv1.DashboardCard{
		Currency:    "USD",
		DataThrough: watermark,
	}
	for _, row := range rows {
		cards.TotalCostCents += row.CostCents
		cards.PromptTokens += row.PromptTokens
		cards.CompletionTokens += row.CompletionTokens
		cards.CachedTokens += row.CachedTokens
		cards.ReasoningTokens += row.ReasoningTokens
		cards.RequestCount += row.RequestCount
		if !row.Priced {
			cards.UnpricedRequestCount += row.RequestCount
		}
	}

	return &meteringv1.GetUsageDashboardResponse{
		Response:     okResponse(),
		Cards:        cards,
		DailyBuckets: daily,
	}, nil
}

// validateDashboardRange checks and defaults the since/until pair for
// the dashboard: until defaults to now, since to until-24h; since >
// until or a range > 92 days returns 10404 (AD7, AC7).
func validateDashboardRange(since, until int64) (int64, int64, error) {
	if until <= 0 {
		until = time.Now().Unix()
	}
	if since <= 0 {
		since = until - defaultRangeHours*3600
	}
	if since > until {
		return 0, 0, apierrors.New(apierrors.CodeMeteringRangeInvalid)
	}
	if until-since > dashboardMaxRangeSeconds {
		return 0, 0, apierrors.New(apierrors.CodeMeteringRangeInvalid)
	}
	return since, until, nil
}

// dashboardGroupBy maps the request string to the repository dimension.
func dashboardGroupBy(g string) DashboardGroupBy {
	switch g {
	case "model":
		return GroupByModel
	case "accelerator_type":
		return GroupByAcceleratorType
	default:
		return GroupByAPIKey
	}
}

// dashboardRepo lazily wires and returns the dashboard repository.
func (s *Service) dashboardRepo() (*DashboardRepository, error) {
	db, err := s.gormDB()
	if err != nil {
		return nil, err
	}
	return NewDashboardRepository(db), nil
}

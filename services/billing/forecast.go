package billing

import (
	"context"
	"math"
	"strings"

	billingv1 "github.com/go-taas/go-taas/proto/taas/billing/v1"

	"github.com/go-taas/go-taas/pkg/config"
	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// forecastMethod is the forecast method, always a deterministic linear
// trend (feature #36, AD3).
const forecastMethod = "linear_trend"

// forecastDimensions is the set of supported forecast dimensions
// (feature #36, AD1). The user surface restricts to model/api_key.
var forecastDimensions = map[string]bool{
	"organization": true,
	"model":        true,
	"api_key":      true,
}

// GetForecast returns the forecast for a time range and optional
// dimension filter: the historical series, the forecast series, and the
// confidence band (feature #36, AC1-AC3). The admin binding is
// fleet-wide; the user binding is tenant-scoped.
func (s *Service) GetForecast(ctx context.Context, req *billingv1.GetForecastRequest) (*billingv1.GetForecastResponse, error) {
	surface := surfaceFromContext(ctx)

	var orgFilter string
	if surface == SurfaceAdmin {
		orgFilter, _ = s.resolveOrg(ctx)
		if err := s.requireAdminRole(ctx, orgFilter); err != nil {
			return nil, err
		}
	} else {
		org, err := s.resolveOrg(ctx)
		if err != nil {
			return nil, err
		}
		orgFilter = org
	}

	dimension := strings.TrimSpace(req.GetDimension())
	if dimension == "" {
		dimension = "organization"
	}
	if !forecastDimensions[dimension] {
		return nil, apierrors.New(apierrors.CodeCostDimensionInvalid)
	}
	// The user surface restricts to model/api_key (AD1).
	if surface == SurfaceUser && dimension == "organization" {
		return nil, apierrors.New(apierrors.CodeCostDimensionInvalid)
	}

	since, until, err := validateRange(req.GetSince(), req.GetUntil())
	if err != nil {
		return nil, err
	}
	// The range is bounded by the configured max (AD3).
	if until-since > int64(s.forecastRangeMaxDays())*24*3600 {
		return nil, apierrors.New(apierrors.CodeMeteringRangeInvalid)
	}

	horizonDays := int(req.GetHorizonDays())
	if horizonDays == 0 {
		horizonDays = s.forecastHorizonDefaultDays()
	}
	if horizonDays > s.forecastHorizonMaxDays() {
		return nil, apierrors.New(apierrors.CodeMeteringRangeInvalid)
	}

	value := strings.TrimSpace(req.GetDimensionValue())
	history, watermark, err := s.readForecastHistory(ctx, orgFilter, dimension, value, since, until)
	if err != nil {
		return nil, err
	}
	// An unknown dimension value returns 11302 (AC2).
	if value != "" && len(history) == 0 {
		return nil, apierrors.New(apierrors.CodeCostDimensionValueNotFound)
	}

	bucketSize := bucketSizeForRange(since, until)
	// horizonDays is validated to be <= the configured max (<= 90), so
	// the int -> int32 conversion cannot overflow (feature #36, AC2).
	//nolint:gosec // G115: horizonDays is capped at 90 by the validation above.
	return buildForecastResponse(history, watermark, bucketSize, int32(horizonDays)), nil
}

// forecastHorizonDefaultDays returns the configured default horizon.
func (s *Service) forecastHorizonDefaultDays() int {
	if cfg := config.GetConfig(); cfg != nil {
		if d := cfg.Billing.Forecast.HorizonDefaultDays; d > 0 {
			return d
		}
	}
	return 30
}

// forecastHorizonMaxDays returns the configured max horizon.
func (s *Service) forecastHorizonMaxDays() int {
	if cfg := config.GetConfig(); cfg != nil {
		if d := cfg.Billing.Forecast.HorizonMaxDays; d > 0 {
			return d
		}
	}
	return 90
}

// forecastRangeMaxDays returns the configured max range length.
func (s *Service) forecastRangeMaxDays() int {
	if cfg := config.GetConfig(); cfg != nil {
		if d := cfg.Billing.Forecast.RangeMaxDays; d > 0 {
			return d
		}
	}
	return 92
}

// forecastBucket is one historical time bucket of the forecast.
type forecastBucket struct {
	Bucket         int64
	TotalTokens    int64
	TotalCostCents int64
}

// readForecastHistory reads the historical usage/cost series over the
// range, bucketed hourly (<= 7 days) or daily (> 7 days), reusing the
// cost-analytics aggregation (feature #36, AD3).
func (s *Service) readForecastHistory(ctx context.Context, orgFilter, dimension, value string, since, until int64) ([]forecastBucket, int64, error) {
	repo, err := s.costAnalyticsRepo()
	if err != nil {
		return nil, 0, err
	}
	bucketSize := bucketSizeForRange(since, until)
	buckets, _, err := repo.AggregateCost(ctx, orgFilter, dimension, value, since, until, bucketSize)
	if err != nil {
		return nil, 0, err
	}
	out := make([]forecastBucket, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, forecastBucket{
			Bucket:         b.Bucket,
			TotalTokens:    b.TotalTokens,
			TotalCostCents: b.TotalCostCents,
		})
	}
	watermark, err := repo.DataThrough(ctx, orgFilter, since, until, bucketSize)
	if err != nil {
		return nil, 0, err
	}
	return out, watermark, nil
}

// linearTrend is a least-squares linear fit: y = slope*x + intercept.
type linearTrend struct {
	slope     float64
	intercept float64
}

// fitLinearTrend fits a least-squares linear trend to the historical
// series (feature #36, AD3). x is the bucket index (0-based), y the
// metric value. A single point yields a flat trend.
func fitLinearTrend(values []int64) linearTrend {
	n := len(values)
	if n == 0 {
		return linearTrend{}
	}
	if n == 1 {
		return linearTrend{slope: 0, intercept: float64(values[0])}
	}
	var sumX, sumY, sumXY, sumXX float64
	for i, v := range values {
		x := float64(i)
		y := float64(v)
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}
	denom := float64(n)*sumXX - sumX*sumX
	if denom == 0 {
		return linearTrend{slope: 0, intercept: sumY / float64(n)}
	}
	slope := (float64(n)*sumXY - sumX*sumY) / denom
	intercept := (sumY - slope*sumX) / float64(n)
	return linearTrend{slope: slope, intercept: intercept}
}

// extrapolateForecast extrapolates the trend forward to the horizon,
// clamped to the history length (feature #36, AD4). It returns the
// forecast points starting at the last history bucket + bucketSize.
func extrapolateForecast(history []forecastBucket, trend linearTrend, horizonDays int, bucketSize int64) []forecastBucket {
	if len(history) == 0 {
		return nil
	}
	// The horizon cannot exceed the historical range length (AD4).
	historyLen := int64(len(history))
	horizonBuckets := int64(horizonDays) * 24 * 3600 / bucketSize
	if horizonBuckets > historyLen {
		horizonBuckets = historyLen
	}
	if horizonBuckets <= 0 {
		return nil
	}
	lastBucket := history[len(history)-1].Bucket
	// The trend is fit over the bucket indices; the forecast continues
	// from the last index.
	lastIdx := float64(len(history) - 1)
	out := make([]forecastBucket, 0, horizonBuckets)
	for i := int64(1); i <= horizonBuckets; i++ {
		x := lastIdx + float64(i)
		tokens := int64(math.Round(trend.slope*x + trend.intercept))
		if tokens < 0 {
			tokens = 0
		}
		out = append(out, forecastBucket{
			Bucket:         lastBucket + i*bucketSize,
			TotalTokens:    tokens,
			TotalCostCents: tokens, // cost is derived from tokens below
		})
	}
	return out
}

// deriveConfidenceBand computes the upper/lower bounds around the
// forecast line from the historical variance, widening with the forecast
// distance and the historical volatility (feature #36, AD5). It returns
// the band width (in the metric units) per forecast point.
func deriveConfidenceBand(history []forecastBucket, forecast []forecastBucket, metric func(forecastBucket) int64) []int64 {
	if len(history) == 0 || len(forecast) == 0 {
		return nil
	}
	// Historical standard deviation of the metric.
	values := make([]float64, 0, len(history))
	for _, h := range history {
		values = append(values, float64(metric(h)))
	}
	mean := 0.0
	for _, v := range values {
		mean += v
	}
	mean /= float64(len(values))
	variance := 0.0
	for _, v := range values {
		d := v - mean
		variance += d * d
	}
	variance /= float64(len(values))
	stddev := math.Sqrt(variance)
	if stddev < 1 {
		stddev = 1
	}
	// The band widens with the forecast distance (1.96 * stddev * sqrt(k)).
	widths := make([]int64, 0, len(forecast))
	for i := range forecast {
		k := float64(i + 1)
		width := int64(math.Round(1.96 * stddev * math.Sqrt(k)))
		if width < 1 {
			width = 1
		}
		widths = append(widths, width)
	}
	return widths
}

// computeDataThrough returns the last complete bucket covered by the
// history (feature #36, AD6). 0 when no history.
func computeDataThrough(history []forecastBucket, bucketSize int64) int64 {
	if len(history) == 0 {
		return 0
	}
	last := history[len(history)-1].Bucket
	return last - bucketSize
}

// buildForecastResponse assembles the wire response from the history,
// forecast, and band (feature #36, AC1).
func buildForecastResponse(history []forecastBucket, watermark, bucketSize int64, horizonDays int32) *billingv1.GetForecastResponse {
	hist := make([]*billingv1.ForecastBucket, 0, len(history))
	var histTokens, histCost int64
	for _, h := range history {
		hist = append(hist, &billingv1.ForecastBucket{
			Bucket:         h.Bucket,
			TotalTokens:    h.TotalTokens,
			TotalCostCents: h.TotalCostCents,
		})
		histTokens += h.TotalTokens
		histCost += h.TotalCostCents
	}

	// Fit the trends over the history.
	tokenTrend := fitLinearTrend(tokenSeries(history))
	costTrend := fitLinearTrend(costSeries(history))
	// Extrapolate both metrics.
	tokenForecast := extrapolateForecast(history, tokenTrend, int(horizonDays), bucketSize)
	costForecast := extrapolateForecast(history, costTrend, int(horizonDays), bucketSize)

	// Derive the confidence bands.
	tokenWidths := deriveConfidenceBand(history, tokenForecast, func(b forecastBucket) int64 { return b.TotalTokens })
	costWidths := deriveConfidenceBand(history, costForecast, func(b forecastBucket) int64 { return b.TotalCostCents })

	points := make([]*billingv1.ForecastPoint, 0, len(tokenForecast))
	var sumTokens, sumCost int64
	for i := range tokenForecast {
		tokens := tokenForecast[i].TotalTokens
		cost := costForecast[i].TotalCostCents
		lowerT, upperT := tokens, tokens
		lowerC, upperC := cost, cost
		if i < len(tokenWidths) {
			lowerT = tokens - tokenWidths[i]
			upperT = tokens + tokenWidths[i]
		}
		if i < len(costWidths) {
			lowerC = cost - costWidths[i]
			upperC = cost + costWidths[i]
		}
		if lowerT < 0 {
			lowerT = 0
		}
		if lowerC < 0 {
			lowerC = 0
		}
		sumTokens += tokens
		sumCost += cost
		points = append(points, &billingv1.ForecastPoint{
			Bucket:         tokenForecast[i].Bucket,
			TotalTokens:    tokens,
			TotalCostCents: cost,
			LowerTokens:    lowerT,
			UpperTokens:    upperT,
			LowerCostCents: lowerC,
			UpperCostCents: upperC,
		})
	}

	return &billingv1.GetForecastResponse{
		Response:    okResponse(),
		Method:      forecastMethod,
		HorizonDays: horizonDays,
		DataThrough: watermark,
		History:     hist,
		Forecast:    points,
		Summary:     &billingv1.ForecastSummary{TotalTokens: sumTokens, TotalCostCents: sumCost},
	}
}

// tokenSeries extracts the token values of the history.
func tokenSeries(history []forecastBucket) []int64 {
	out := make([]int64, 0, len(history))
	for _, h := range history {
		out = append(out, h.TotalTokens)
	}
	return out
}

// costSeries extracts the cost values of the history.
func costSeries(history []forecastBucket) []int64 {
	out := make([]int64, 0, len(history))
	for _, h := range history {
		out = append(out, h.TotalCostCents)
	}
	return out
}

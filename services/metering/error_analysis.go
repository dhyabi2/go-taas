package metering

import (
	"context"
	"strings"

	meteringv1 "github.com/go-taas/go-taas/proto/taas/metering/v1"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// errorAnalysisMaxRangeSeconds caps the error-analysis range at 92 days
// (AD5).
const errorAnalysisMaxRangeSeconds = 92 * 24 * 3600

var _ = errorAnalysisMaxRangeSeconds

// GetErrorAnalysisOverview returns error analysis: summary cards, a
// top-causes ranking, and an error-rate trend (feature #31, AC1-AC2).
// The admin binding is the fleet-wide view (cross-org by default,
// optional org filter, gated by the caller's role, AD9); the user
// binding is hard-scoped to the caller's org (AD8).
func (s *Service) GetErrorAnalysisOverview(ctx context.Context, req *meteringv1.GetErrorAnalysisOverviewRequest) (*meteringv1.GetErrorAnalysisOverviewResponse, error) {
	surface := surfaceFromContext(ctx)

	var orgFilter string
	if surface == SurfaceAdmin {
		// The admin fleet view is cross-org by default (AD9).
		orgFilter = strings.TrimSpace(req.GetOrganizationId())
		if orgFilter == "" {
			orgFilter, _ = s.resolveOrg(ctx)
		}
		if err := s.requireAdminRole(ctx, orgFilter); err != nil {
			return nil, err
		}
	} else {
		// User binding: hard-scoped to the caller's org (AD8).
		org, err := s.resolveOrg(ctx)
		if err != nil {
			return nil, err
		}
		orgFilter = org
	}
	since, until, err := s.validateErrorAnalysisRange(req.GetSince(), req.GetUntil())
	if err != nil {
		return nil, err
	}
	modelFilter := strings.TrimSpace(req.GetModelId())
	repo, err := s.errorAnalysisRepo()
	if err != nil {
		return nil, err
	}
	bucketSize := bucketSizeForRange(since, until)
	buckets, causes, err := repo.AggregateErrors(ctx, orgFilter, modelFilter, since, until, bucketSize)
	if err != nil {
		return nil, err
	}
	watermark, err := repo.DataThrough(ctx, orgFilter, modelFilter, since, until, bucketSize)
	if err != nil {
		return nil, err
	}

	cards := buildErrorAnalysisCards(buckets, causes, watermark)
	causeRows := make([]*meteringv1.ErrorCauseRow, 0, len(causes))
	for _, c := range causes {
		causeRows = append(causeRows, &meteringv1.ErrorCauseRow{
			ErrorCode:   c.ErrorCode,
			ErrorCount:  c.ErrorCount,
			ErrorRate:   errorRatePct(c.ErrorCount, totalRequests(buckets)),
			SharePct:    sharePct(c.ErrorCount, cards.ErrorCount),
		})
	}
	series := buildErrorSeries(buckets)

	return &meteringv1.GetErrorAnalysisOverviewResponse{
		Response: okResponse(),
		Cards:    cards,
		Causes:   causeRows,
		Series:   series,
	}, nil
}

// GetErrorAnalysis returns single-error-code analysis: summary cards and
// an error-rate trend (feature #31, AC3-AC4). The admin binding covers
// any error (AD9); the user binding is hard-scoped to the caller's org
// (AD8).
func (s *Service) GetErrorAnalysis(ctx context.Context, req *meteringv1.GetErrorAnalysisRequest) (*meteringv1.GetErrorAnalysisResponse, error) {
	errorCode := strings.TrimSpace(req.GetErrorCode())
	if errorCode == "" {
		return nil, apierrors.New(apierrors.CodeErrorCauseNotFound)
	}
	surface := surfaceFromContext(ctx)

	var orgFilter string
	if surface == SurfaceAdmin {
		orgFilter = strings.TrimSpace(req.GetOrganizationId())
		if orgFilter == "" {
			orgFilter, _ = s.resolveOrg(ctx)
		}
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

	since, until, err := s.validateErrorAnalysisRange(req.GetSince(), req.GetUntil())
	if err != nil {
		return nil, err
	}
	repo, err := s.errorAnalysisRepo()
	if err != nil {
		return nil, err
	}
	bucketSize := bucketSizeForRange(since, until)
	buckets, err := repo.AggregateError(ctx, orgFilter, errorCode, since, until, bucketSize)
	if err != nil {
		return nil, err
	}
	watermark, err := repo.DataThrough(ctx, orgFilter, "", since, until, bucketSize)
	if err != nil {
		return nil, err
	}
	// An unknown error code returns 11501 (AD2).
	if len(buckets) == 0 {
		return nil, apierrors.New(apierrors.CodeErrorCauseNotFound)
	}

	cards := buildErrorAnalysisCards(buckets, nil, watermark)
	series := buildErrorSeries(buckets)

	return &meteringv1.GetErrorAnalysisResponse{
		Response: okResponse(),
		Cards:    cards,
		Series:   series,
	}, nil
}

// validateErrorAnalysisRange checks and defaults the since/until pair
// (AD5).
func (s *Service) validateErrorAnalysisRange(since, until int64) (int64, int64, error) {
	return validateRange(since, until)
}

// errorAnalysisRepo lazily wires and returns the error-analysis
// repository.
func (s *Service) errorAnalysisRepo() (*ErrorAnalysisRepository, error) {
	db, err := s.gormDB()
	if err != nil {
		return nil, err
	}
	return NewErrorAnalysisRepository(db), nil
}

// buildErrorAnalysisCards derives the headline summary cards from the
// bucket rows and the top cause. error_rate and top_cause_share_pct are
// derived client-side; the wire carries integer counts only (AD3).
func buildErrorAnalysisCards(buckets []ErrorBucketRow, causes []ErrorCauseAggRow, watermark int64) *meteringv1.ErrorAnalysisCard {
	card := &meteringv1.ErrorAnalysisCard{DataThrough: watermark}
	for _, b := range buckets {
		card.ErrorCount += b.ErrorCount
		card.RequestCount += b.RequestCount
	}
	if len(causes) > 0 {
		card.TopCause = causes[0].ErrorCode
	}
	return card
}

// buildErrorSeries maps the bucket rows to the wire series points.
func buildErrorSeries(buckets []ErrorBucketRow) []*meteringv1.ErrorSeriesPoint {
	out := make([]*meteringv1.ErrorSeriesPoint, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, &meteringv1.ErrorSeriesPoint{
			Bucket:       b.Bucket,
			ErrorCount:   b.ErrorCount,
			RequestCount: b.RequestCount,
		})
	}
	return out
}

// totalRequests sums the request counts across buckets.
func totalRequests(buckets []ErrorBucketRow) int64 {
	var total int64
	for _, b := range buckets {
		total += b.RequestCount
	}
	return total
}

// errorRatePct returns the error rate as an integer percentage.
func errorRatePct(errors, requests int64) int64 {
	if requests == 0 {
		return 0
	}
	return errors * 100 / requests
}

// sharePct returns the share as an integer percentage.
func sharePct(part, total int64) int64 {
	if total == 0 {
		return 0
	}
	return part * 100 / total
}
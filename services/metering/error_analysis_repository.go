package metering

import (
	"context"
	"sort"

	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/database"
)

// ErrorBucketRow is one time bucket of the error aggregation.
type ErrorBucketRow struct {
	Bucket        int64
	ErrorCount    int64
	RequestCount  int64
	BucketSeconds int64
}

// ErrorCauseAggRow is one error code's aggregate in the top-causes table.
type ErrorCauseAggRow struct {
	ErrorCode   string
	ErrorCount  int64
	RequestCount int64
}

// ErrorAnalysisRepository aggregates request_logs into the error
// analysis shapes (feature #31, AD1). It reads metering's request_logs
// read-only over the shared database.
type ErrorAnalysisRepository struct {
	db *database.Manager
}

// NewErrorAnalysisRepository constructs an ErrorAnalysisRepository bound
// to a GORM database.
func NewErrorAnalysisRepository(db *gorm.DB) *ErrorAnalysisRepository {
	return &ErrorAnalysisRepository{db: database.NewManager(db)}
}

// AggregateErrors aggregates request_logs for the fleet error view: the
// per-bucket series rows and the per-cause rows, within the optional org
// and model filters and the range.
func (r *ErrorAnalysisRepository) AggregateErrors(ctx context.Context, orgFilter, modelFilter string, since, until, bucketSize int64) ([]ErrorBucketRow, []ErrorCauseAggRow, error) {
	rows, err := r.fetchLogs(ctx, orgFilter, modelFilter, since, until)
	if err != nil {
		return nil, nil, err
	}
	buckets := aggregateErrorBuckets(rows, bucketSize)
	causes := aggregateErrorCauses(rows)
	return buckets, causes, nil
}

// AggregateError aggregates request_logs for one error code: the
// per-bucket series rows, within the org filter and the range.
func (r *ErrorAnalysisRepository) AggregateError(ctx context.Context, orgID, errorCode string, since, until, bucketSize int64) ([]ErrorBucketRow, error) {
	rows, err := r.fetchLogs(ctx, orgID, "", since, until)
	if err != nil {
		return nil, err
	}
	var filtered []RequestLog
	for _, row := range rows {
		if row.Status == "error" && row.Error == errorCode {
			filtered = append(filtered, row)
		}
	}
	return aggregateErrorBuckets(filtered, bucketSize), nil
}

// DataThrough returns the start of the last complete bucket covered by
// request logs for the filters and range (AD7). 0 when no request logs
// match.
func (r *ErrorAnalysisRepository) DataThrough(ctx context.Context, orgFilter, modelFilter string, since, until, bucketSize int64) (int64, error) {
	rows, err := r.fetchLogs(ctx, orgFilter, modelFilter, since, until)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	var max int64
	for _, row := range rows {
		ts := row.CreatedAt.Unix()
		if ts > max {
			max = ts
		}
	}
	last := bucketStart(max, bucketSize)
	return last - bucketSize, nil
}

// fetchLogs loads the request-log rows within the filters and range.
func (r *ErrorAnalysisRepository) fetchLogs(ctx context.Context, orgFilter, modelFilter string, since, until int64) ([]RequestLog, error) {
	base := r.db.DB(ctx).Model(&RequestLog{}).
		Where("created_at >= ? AND created_at < ?", unixTime(since), unixTime(until))
	if orgFilter != "" {
		base = base.Where("organization_id = ?", orgFilter)
	}
	if modelFilter != "" {
		base = base.Where("model_id = ?", modelFilter)
	}
	var rows []RequestLog
	if err := base.Order("created_at ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// aggregateErrorBuckets groups rows into time buckets and computes each
// bucket's error and request counts.
func aggregateErrorBuckets(rows []RequestLog, bucketSize int64) []ErrorBucketRow {
	byBucket := make(map[int64]*errorBucketAccum)
	for _, row := range rows {
		b := bucketStart(row.CreatedAt.Unix(), bucketSize)
		acc := byBucket[b]
		if acc == nil {
			acc = &errorBucketAccum{bucket: b}
			byBucket[b] = acc
		}
		acc.add(row)
	}
	out := make([]ErrorBucketRow, 0, len(byBucket))
	for _, acc := range byBucket {
		out = append(out, acc.row(bucketSize))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bucket < out[j].Bucket })
	return out
}

// aggregateErrorCauses groups error rows by error code and computes each
// cause's aggregate, sorted by error count descending.
func aggregateErrorCauses(rows []RequestLog) []ErrorCauseAggRow {
	byCause := make(map[string]*errorCauseAccum)
	for _, row := range rows {
		if row.Status != "error" {
			continue
		}
		acc := byCause[row.Error]
		if acc == nil {
			acc = &errorCauseAccum{errorCode: row.Error}
			byCause[row.Error] = acc
		}
		acc.add()
	}
	out := make([]ErrorCauseAggRow, 0, len(byCause))
	for _, acc := range byCause {
		out = append(out, acc.row())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ErrorCount != out[j].ErrorCount {
			return out[i].ErrorCount > out[j].ErrorCount
		}
		return out[i].ErrorCode < out[j].ErrorCode
	})
	return out
}

// errorBucketAccum accumulates one time bucket.
type errorBucketAccum struct {
	bucket       int64
	errorCount   int64
	requestCount int64
}

func (a *errorBucketAccum) add(row RequestLog) {
	a.requestCount++
	if row.Status == "error" {
		a.errorCount++
	}
}

func (a *errorBucketAccum) row(bucketSize int64) ErrorBucketRow {
	return ErrorBucketRow{
		Bucket:        a.bucket,
		ErrorCount:    a.errorCount,
		RequestCount:  a.requestCount,
		BucketSeconds: bucketSize,
	}
}

// errorCauseAccum accumulates one error code's aggregate.
type errorCauseAccum struct {
	errorCode  string
	errorCount int64
}

func (a *errorCauseAccum) add() { a.errorCount++ }

func (a *errorCauseAccum) row() ErrorCauseAggRow {
	return ErrorCauseAggRow{ErrorCode: a.errorCode, ErrorCount: a.errorCount}
}
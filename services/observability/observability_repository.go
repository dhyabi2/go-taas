package observability

import (
	"context"
	"sort"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/database"
)

// Repository aggregates request_logs into the observability shapes:
// the fleet overview (cards + per-model rows + series), the single-model
// view (cards + series + per-key rows), and the data_through watermark.
// It reads metering's request_logs read-only over the shared database
// (AD3).
//
// The aggregation is computed in Go over the range's request-log rows
// rather than in SQL. This keeps the queries portable across the SQLite
// FVT/unit-test database and the production PostgreSQL database (which
// differ in percentile and timestamp-dialect functions), at the cost of
// fetching the range's rows into memory — acceptable at the request-log
// retention scale (30 days, feature #12 AD4). The wire carries integer
// counts and integer milliseconds only (AD6).
type Repository struct {
	db *database.Manager
}

// NewRepository constructs an observability Repository bound to a GORM
// database.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: database.NewManager(db)}
}

// AggregateOverview aggregates request_logs for the fleet overview: the
// per-bucket series rows and the per-model rows, within the optional org
// and model filters and the range.
func (r *Repository) AggregateOverview(ctx context.Context, orgFilter, modelFilter string, since, until, bucketSize int64) ([]BucketRow, []ModelRow, error) {
	rows, err := r.fetchRows(ctx, orgFilter, modelFilter, since, until)
	if err != nil {
		return nil, nil, err
	}
	buckets := aggregateBuckets(rows, bucketSize)
	models := aggregateModels(rows, bucketSize)
	return buckets, models, nil
}

// AggregateModel aggregates request_logs for the single-model view: the
// per-bucket series rows and the per-key rows, within the org filter and
// the range.
func (r *Repository) AggregateModel(ctx context.Context, orgID, modelID string, since, until, bucketSize int64) ([]BucketRow, []KeyRow, error) {
	rows, err := r.fetchRows(ctx, orgID, modelID, since, until)
	if err != nil {
		return nil, nil, err
	}
	buckets := aggregateBuckets(rows, bucketSize)
	keys := aggregateKeys(rows, bucketSize)
	return buckets, keys, nil
}

// DataThrough returns the start of the last complete bucket covered by
// request logs for the filters and range (AD8): the most recent
// request-log created_at truncated to the bucket boundary, minus one
// bucket. 0 when no request logs match.
func (r *Repository) DataThrough(ctx context.Context, orgFilter, modelFilter string, since, until, bucketSize int64) (int64, error) {
	rows, err := r.fetchRows(ctx, orgFilter, modelFilter, since, until)
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

// fetchRows loads the request-log rows within the filters and range.
func (r *Repository) fetchRows(ctx context.Context, orgFilter, modelFilter string, since, until int64) ([]requestLogRow, error) {
	base := r.db.DB(ctx).Model(&requestLogRow{}).
		Where("created_at >= ? AND created_at < ?", unixTime(since), unixTime(until))
	if orgFilter != "" {
		base = base.Where("organization_id = ?", orgFilter)
	}
	if modelFilter != "" {
		base = base.Where("model_id = ?", modelFilter)
	}
	var rows []requestLogRow
	if err := base.Order("created_at ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ModelExists reports whether a model id exists in the models table
// (read-only, AD3). It is used to return 10801 for an unknown model_id.
// The models.id column is a UUID, so a non-UUID model id is treated as
// not-found rather than a database type error.
func (r *Repository) ModelExists(ctx context.Context, modelID string) (bool, error) {
	if _, err := uuid.Parse(modelID); err != nil {
		return false, nil
	}
	var count int64
	err := r.db.DB(ctx).Table("models").Where("id = ?", modelID).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ModelName resolves a model id to its display name (read-only, AD3).
// Unknown models return "".
func (r *Repository) ModelName(ctx context.Context, modelID string) (string, error) {
	var name string
	err := r.db.DB(ctx).Table("models").Select("name").Where("id = ?", modelID).Scan(&name).Error
	if err != nil {
		return "", err
	}
	return name, nil
}

// APIKeyName resolves an api key id to its display name (read-only,
// AD3). Unknown keys return "".
func (r *Repository) APIKeyName(ctx context.Context, apiKeyID string) (string, error) {
	var name string
	err := r.db.DB(ctx).Table("api_keys").Select("name").Where("id = ?", apiKeyID).Scan(&name).Error
	if err != nil {
		return "", err
	}
	return name, nil
}

// aggregateBuckets groups rows into time buckets and computes each
// bucket's counts, latency aggregates and token sums.
func aggregateBuckets(rows []requestLogRow, bucketSize int64) []BucketRow {
	byBucket := make(map[int64]*bucketAccum)
	for _, row := range rows {
		b := bucketStart(row.CreatedAt.Unix(), bucketSize)
		acc := byBucket[b]
		if acc == nil {
			acc = &bucketAccum{bucket: b}
			byBucket[b] = acc
		}
		acc.add(row)
	}
	out := make([]BucketRow, 0, len(byBucket))
	for _, acc := range byBucket {
		out = append(out, acc.row(bucketSize))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bucket < out[j].Bucket })
	return out
}

// aggregateModels groups rows by model and computes each model's
// aggregate, sorted by request count descending.
func aggregateModels(rows []requestLogRow, bucketSize int64) []ModelRow {
	byModel := make(map[string]*modelAccum)
	for _, row := range rows {
		acc := byModel[row.ModelID]
		if acc == nil {
			acc = &modelAccum{modelID: row.ModelID}
			byModel[row.ModelID] = acc
		}
		acc.add(row)
	}
	out := make([]ModelRow, 0, len(byModel))
	for _, acc := range byModel {
		out = append(out, acc.row(bucketSize))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RequestCount != out[j].RequestCount {
			return out[i].RequestCount > out[j].RequestCount
		}
		return out[i].ModelID < out[j].ModelID
	})
	return out
}

// aggregateKeys groups rows by api key and computes each key's
// aggregate, sorted by request count descending.
func aggregateKeys(rows []requestLogRow, bucketSize int64) []KeyRow {
	byKey := make(map[string]*keyAccum)
	for _, row := range rows {
		acc := byKey[row.APIKeyID]
		if acc == nil {
			acc = &keyAccum{apiKeyID: row.APIKeyID}
			byKey[row.APIKeyID] = acc
		}
		acc.add(row)
	}
	out := make([]KeyRow, 0, len(byKey))
	for _, acc := range byKey {
		out = append(out, acc.row(bucketSize))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RequestCount != out[j].RequestCount {
			return out[i].RequestCount > out[j].RequestCount
		}
		return out[i].APIKeyID < out[j].APIKeyID
	})
	return out
}

// bucketAccum accumulates one time bucket.
type bucketAccum struct {
	bucket       int64
	requestCount int64
	errorCount   int64
	latencySum   int64
	latencies    []int64
	outputTokens int64
	inputTokens  int64
}

func (a *bucketAccum) add(row requestLogRow) {
	a.requestCount++
	if row.Status == "error" {
		a.errorCount++
	}
	a.latencySum += row.LatencyMs
	a.latencies = append(a.latencies, row.LatencyMs)
	a.outputTokens += row.CompletionTokens
	a.inputTokens += row.PromptTokens
}

func (a *bucketAccum) row(bucketSize int64) BucketRow {
	return BucketRow{
		Bucket:        a.bucket,
		RequestCount:  a.requestCount,
		ErrorCount:    a.errorCount,
		AvgLatencyMs:  avg(a.latencySum, a.requestCount),
		P95LatencyMs:  percentile(a.latencies, 0.95),
		OutputTokens:  a.outputTokens,
		InputTokens:   a.inputTokens,
		BucketSeconds: bucketSize,
	}
}

// modelAccum accumulates one model's aggregate.
type modelAccum struct {
	modelID      string
	requestCount int64
	errorCount   int64
	latencySum   int64
	latencies    []int64
	outputTokens int64
}

func (a *modelAccum) add(row requestLogRow) {
	a.requestCount++
	if row.Status == "error" {
		a.errorCount++
	}
	a.latencySum += row.LatencyMs
	a.latencies = append(a.latencies, row.LatencyMs)
	a.outputTokens += row.CompletionTokens
}

func (a *modelAccum) row(bucketSize int64) ModelRow {
	return ModelRow{
		ModelID:       a.modelID,
		RequestCount:  a.requestCount,
		ErrorCount:    a.errorCount,
		AvgLatencyMs:  avg(a.latencySum, a.requestCount),
		P95LatencyMs:  percentile(a.latencies, 0.95),
		OutputTokens:  a.outputTokens,
		BucketSeconds: bucketSize,
	}
}

// keyAccum accumulates one api key's aggregate.
type keyAccum struct {
	apiKeyID     string
	requestCount int64
	errorCount   int64
	latencySum   int64
	latencies    []int64
	outputTokens int64
}

func (a *keyAccum) add(row requestLogRow) {
	a.requestCount++
	if row.Status == "error" {
		a.errorCount++
	}
	a.latencySum += row.LatencyMs
	a.latencies = append(a.latencies, row.LatencyMs)
	a.outputTokens += row.CompletionTokens
}

func (a *keyAccum) row(bucketSize int64) KeyRow {
	return KeyRow{
		APIKeyID:      a.apiKeyID,
		RequestCount:  a.requestCount,
		ErrorCount:    a.errorCount,
		AvgLatencyMs:  avg(a.latencySum, a.requestCount),
		P95LatencyMs:  percentile(a.latencies, 0.95),
		OutputTokens:  a.outputTokens,
		BucketSeconds: bucketSize,
	}
}

// avg returns the rounded integer average, or 0 when n is 0.
func avg(sum, n int64) int64 {
	if n == 0 {
		return 0
	}
	return (sum + n/2) / n
}

// percentile returns the nearest-rank percentile of the latency values
// (0 when empty). It matches percentile_cont's nearest-rank behaviour
// for the wire's integer milliseconds (AD6).
func percentile(values []int64, p float64) int64 {
	n := len(values)
	if n == 0 {
		return 0
	}
	sorted := make([]int64, n)
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	// Nearest-rank: rank = ceil(p * n), 1-indexed.
	rank := int(p*float64(n) + 0.999999)
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}

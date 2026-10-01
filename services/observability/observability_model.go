// Package observability implements the read-only model observability
// aggregation over request_logs (feature #24). It serves the admin fleet
// overview and the dual-bound single-model drill-down, deriving latency
// percentiles, throughput, error rate and token throughput from the
// request logs the metering module already writes.
package observability

import "time"

// unixTime converts a unix-seconds value to a UTC time for GORM range
// filters (portable across SQLite and Postgres).
func unixTime(ts int64) time.Time {
	return time.Unix(ts, 0).UTC()
}

// bucketSizeForRange returns the time-bucket size in seconds for a
// range: hourly for ranges <= 7 days, daily otherwise (AD7).
func bucketSizeForRange(since, until int64) int64 {
	if until-since <= 7*24*3600 {
		return 3600
	}
	return 24 * 3600
}

// bucketStart truncates a unix timestamp to the start of its bucket.
func bucketStart(ts, bucketSize int64) int64 {
	return (ts / bucketSize) * bucketSize
}

// BucketRow is one time bucket of the aggregation query:
// the per-bucket counts, latency aggregates and token sums. The latency
// percentiles are computed in SQL via percentile_cont (AD6).
type BucketRow struct {
	Bucket        int64
	RequestCount  int64
	ErrorCount    int64
	AvgLatencyMs  int64
	P95LatencyMs  int64
	OutputTokens  int64
	InputTokens   int64
	BucketSeconds int64
}

// ModelRow is one model's aggregate in the fleet table.
type ModelRow struct {
	ModelID       string
	RequestCount  int64
	ErrorCount    int64
	AvgLatencyMs  int64
	P95LatencyMs  int64
	OutputTokens  int64
	BucketSeconds int64
	DataThrough   int64
}

// KeyRow is one API key's aggregate in the per-key table.
type KeyRow struct {
	APIKeyID      string
	RequestCount  int64
	ErrorCount    int64
	AvgLatencyMs  int64
	P95LatencyMs  int64
	OutputTokens  int64
	BucketSeconds int64
}

// requestLogRow is the read-only projection of metering's request_logs
// table that the observability module aggregates over (AD3). It is
// defined locally so the observability module reads metering's table
// without importing the metering package.
//
// The two additive indexes (Section 4.2) are declared here so the
// observability module's Migrate/MigrateSchemaForFVT create them on the
// existing request_logs table via AutoMigrate: idx_request_logs_created
// serves the fleet-wide range scan, and idx_request_logs_model_created
// serves the model-filtered range scan (admin drill-down and user
// single-model view).
type requestLogRow struct {
	OrganizationID   string
	APIKeyID         string
	ModelID          string `gorm:"size:128;not null;index:idx_request_logs_model_created,priority:1"`
	PromptTokens     int64
	CompletionTokens int64
	LatencyMs        int64
	Status           string
	CreatedAt        time.Time `gorm:"not null;index:idx_request_logs_created;index:idx_request_logs_model_created,priority:2"`
}

// TableName overrides the default GORM table name to metering's table.
func (requestLogRow) TableName() string { return "request_logs" }

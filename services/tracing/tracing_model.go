// Package tracing implements the read-only inference request tracing
// (feature #27). It owns the traces and trace_spans tables, the
// best-effort capture alongside the request log, and the read-only query
// RPCs (ListTraces, GetTrace) dual-bound to the admin and user surfaces.
package tracing

import "time"

// Trace is one inference request's trace, keyed by trace_id (==
// request_id). It is written best-effort alongside the request log (AD3)
// and retained for 30 days by the extended request-log retention runner
// (AD5). Rows are immutable; the retention runner is the only deleter.
type Trace struct {
	// ID is the server-generated UUID v4, exposed as trace_id.
	ID string `gorm:"primaryKey;type:uuid"`
	// TraceID is the inference request id — the idempotency key (shared
	// with the request log and voucher).
	TraceID string `gorm:"size:128;not null;uniqueIndex"`
	// OrganizationID is the owning organization.
	OrganizationID string `gorm:"size:64;not null;index:idx_traces_org_created,priority:1"`
	// APIKeyID is the API Key that made the request.
	APIKeyID string `gorm:"size:64;not null;index:idx_traces_key_created,priority:1"`
	// ModelID is the model that served the request.
	ModelID string `gorm:"size:128;not null;index:idx_traces_model_created,priority:1"`
	// ServiceID optionally names the inference service (operator-internal;
	// masked on the user surface).
	ServiceID *string `gorm:"size:64"`
	// Status is success / error / streaming.
	Status string `gorm:"size:16;not null"`
	// Error is the failure reason; empty on success.
	Error string `gorm:"size:512;not null;default:''"`
	// TotalLatencyMs is the end-to-end latency.
	TotalLatencyMs int64 `gorm:"not null"`
	// TTFTMs is the time-to-first-token (prefill phase).
	TTFTMs int64 `gorm:"not null"`
	// GenerationMs is the generation (decode phase).
	GenerationMs int64 `gorm:"not null"`
	// PromptTokens is the input token count.
	PromptTokens int64 `gorm:"not null;default:0"`
	// CompletionTokens is the output token count.
	CompletionTokens int64 `gorm:"not null;default:0"`
	// CachedTokens is the cache-hit token count.
	CachedTokens int64 `gorm:"not null;default:0"`
	// ReasoningTokens is the reasoning-trace token count.
	ReasoningTokens int64 `gorm:"not null;default:0"`
	// CreatedAt is the trace write time (UTC).
	CreatedAt time.Time `gorm:"not null;index:idx_traces_org_created,priority:2;index:idx_traces_created;index:idx_traces_key_created,priority:2;index:idx_traces_model_created,priority:2"`
}

// TableName returns the traces table name.
func (Trace) TableName() string { return "traces" }

// TraceSpan is one span of a trace, ordered by start_offset_ms ascending.
type TraceSpan struct {
	// ID is the server-generated UUID v4, exposed as span_id.
	ID string `gorm:"primaryKey;type:uuid"`
	// TraceID is the owning trace (FK to traces.trace_id).
	TraceID string `gorm:"size:128;not null;index:idx_trace_spans_trace"`
	// ParentSpanID is the parent span; null for the root (gateway) span.
	ParentSpanID *string `gorm:"size:128"`
	// Name is gateway or inference.
	Name string `gorm:"size:64;not null"`
	// Kind is the span kind (e.g. server, internal).
	Kind string `gorm:"size:32;not null"`
	// StartOffsetMs is the offset from the trace start.
	StartOffsetMs int64 `gorm:"not null"`
	// DurationMs is the span duration.
	DurationMs int64 `gorm:"not null"`
	// Status is success / error.
	Status string `gorm:"size:16;not null"`
	// Error is the span's failure reason; empty on success.
	Error string `gorm:"size:512;not null;default:''"`
	// Attributes is a JSON object, e.g. {"model_id": "...", "service_id": "..."}.
	Attributes string `gorm:"type:jsonb;not null;default:'{}'"`
}

// TableName returns the trace_spans table name.
func (TraceSpan) TableName() string { return "trace_spans" }

// TraceFilter scopes a trace list query (feature #27, Section 5.1).
type TraceFilter struct {
	OrganizationID string
	RequestID      string
	ModelID        string
	APIKeyID       string
	Status         string
	Since          int64
	Until          int64
	Offset         int
	Limit          int
}
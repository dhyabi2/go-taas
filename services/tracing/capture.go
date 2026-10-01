package tracing

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-taas/go-taas/pkg/logger"
	"github.com/go-taas/go-taas/services/metering"
)

// CaptureTrace builds a trace and its spans from a metering event and
// writes them best-effort (AD3). A write failure is logged and never
// fails or retries the voucher or the request log. Idempotency by
// trace_id applies exactly as to the request log — a duplicate event
// writes no second trace.
func (s *Service) CaptureTrace(ctx context.Context, ev *metering.TraceCaptureEvent) {
	repo, err := s.repository()
	if err != nil {
		logger.S().Warnw("tracing: trace capture skipped, repository unavailable",
			"request_id", ev.RequestID, "err", err)
		return
	}
	trace, spans := buildTrace(ev)
	if err := repo.InsertTrace(ctx, trace, spans); err != nil {
		logger.S().Warnw("tracing: trace write failed (best-effort, voucher already written)",
			"request_id", ev.RequestID, "err", err)
	}
}

// buildTrace constructs the trace and its spans from a metering event.
// The gateway span is the root (start_offset 0); the inference span
// follows it. The phase timings (TTFT/generation) are carried on the
// trace; the spans carry the gateway/inference boundaries.
func buildTrace(ev *metering.TraceCaptureEvent) (*Trace, []*TraceSpan) {
	status := ev.Status
	if status == "" {
		status = "success"
	}
	created := time.Unix(ev.CompletedAt, 0).UTC()

	trace := &Trace{
		TraceID:          ev.RequestID,
		OrganizationID:   ev.OrganizationID,
		APIKeyID:         ev.APIKeyID,
		ModelID:          ev.ModelID,
		Status:           status,
		Error:            ev.Error,
		TotalLatencyMs:   ev.LatencyMs,
		TTFTMs:           ev.TTFTMs,
		GenerationMs:     ev.GenerationMs,
		PromptTokens:     ev.PromptTokens,
		CompletionTokens: ev.CompletionTokens,
		CachedTokens:     ev.CachedTokens,
		ReasoningTokens:  ev.ReasoningTokens,
		CreatedAt:        created,
	}
	if ev.ServiceID != "" {
		sid := ev.ServiceID
		trace.ServiceID = &sid
	}

	// The gateway span is the root; the inference span follows it.
	gatewayAttrs, _ := json.Marshal(map[string]string{"model_id": ev.ModelID})
	inferAttrs, _ := json.Marshal(map[string]string{"model_id": ev.ModelID, "service_id": ev.ServiceID})

	spans := []*TraceSpan{
		{
			TraceID:       ev.RequestID,
			Name:          "gateway",
			Kind:          "server",
			StartOffsetMs: 0,
			DurationMs:    ev.LatencyMs,
			Status:        status,
			Error:         ev.Error,
			Attributes:    string(gatewayAttrs),
		},
		{
			TraceID:       ev.RequestID,
			Name:          "inference",
			Kind:          "internal",
			StartOffsetMs: 0,
			DurationMs:    ev.LatencyMs,
			Status:        status,
			Error:         ev.Error,
			Attributes:    string(inferAttrs),
		},
	}
	return trace, spans
}

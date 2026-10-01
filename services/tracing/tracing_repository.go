package tracing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/go-taas/go-taas/pkg/database"
	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// Repository persists traces and trace spans and serves the read-only
// trace queries. It owns the traces + trace_spans tables (AD1).
type Repository struct {
	db *database.Manager
}

// listDefaultLimit is the default page size when the filter's limit is
// unset (Section 5.1).
const listDefaultLimit = 20

// NewRepository constructs a tracing Repository bound to a GORM database.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: database.NewManager(db)}
}

// InsertTrace writes a trace and its spans idempotently: INSERT with ON
// CONFLICT DO NOTHING on the trace_id unique index (AD3). A duplicate
// delivery writes no second trace. The spans are written only when the
// trace insert succeeds (a duplicate trace skips the spans too).
func (r *Repository) InsertTrace(ctx context.Context, trace *Trace, spans []*TraceSpan) error {
	if trace.ID == "" {
		trace.ID = uuid.NewString()
	}
	result := r.db.DB(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(trace)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// Duplicate trace_id: no second trace, no spans.
		return nil
	}
	if len(spans) == 0 {
		return nil
	}
	for _, span := range spans {
		if span.ID == "" {
			span.ID = uuid.NewString()
		}
	}
	return r.db.DB(ctx).Create(&spans).Error
}

// ListTraces returns one page of traces, newest first, and the total
// count. Empty filter values match everything. When RequestID is set,
// at most one trace is returned (the exact match, AD7).
func (r *Repository) ListTraces(ctx context.Context, filter TraceFilter) ([]*Trace, int64, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = listDefaultLimit
	}
	query := r.db.DB(ctx).Model(&Trace{})
	if filter.OrganizationID != "" {
		query = query.Where("organization_id = ?", filter.OrganizationID)
	}
	if filter.RequestID != "" {
		query = query.Where("trace_id = ?", filter.RequestID)
	}
	if filter.ModelID != "" {
		query = query.Where("model_id = ?", filter.ModelID)
	}
	if filter.APIKeyID != "" {
		query = query.Where("api_key_id = ?", filter.APIKeyID)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Since > 0 {
		query = query.Where("created_at >= ?", time.Unix(filter.Since, 0).UTC())
	}
	if filter.Until > 0 {
		query = query.Where("created_at < ?", time.Unix(filter.Until, 0).UTC())
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*Trace
	if err := query.Order("created_at DESC").Order("id DESC").
		Offset(filter.Offset).Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// FindTraceByID returns one trace and its spans. A miss maps to 11101.
// When orgFilter is non-empty, the trace must belong to that org (the
// user binding's hard scope, AD8).
func (r *Repository) FindTraceByID(ctx context.Context, orgFilter, traceID string) (*Trace, []*TraceSpan, error) {
	query := r.db.DB(ctx).Model(&Trace{}).Where("trace_id = ?", traceID)
	if orgFilter != "" {
		query = query.Where("organization_id = ?", orgFilter)
	}
	var trace Trace
	err := query.First(&trace).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, apierrors.New(apierrors.CodeTraceNotFound)
	}
	if err != nil {
		return nil, nil, err
	}
	var spans []*TraceSpan
	if err := r.db.DB(ctx).Model(&TraceSpan{}).
		Where("trace_id = ?", traceID).
		Order("start_offset_ms ASC").Order("id ASC").
		Find(&spans).Error; err != nil {
		return nil, nil, err
	}
	return &trace, spans, nil
}

// DeleteTracesBefore deletes up to batch traces (and their spans) older
// than the cutoff — the trace retention primitive (AD5). It deletes the
// spans first, then the traces, in batches.
func (r *Repository) DeleteTracesBefore(ctx context.Context, before time.Time, batch int) (int64, error) {
	if batch <= 0 {
		batch = 1
	}
	var ids []string
	if err := r.db.DB(ctx).Model(&Trace{}).
		Where("created_at < ?", before.UTC()).
		Limit(batch).
		Pluck("trace_id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := r.db.DB(ctx).Where("trace_id IN ?", ids).Delete(&TraceSpan{}).Error; err != nil {
		return 0, err
	}
	result := r.db.DB(ctx).Where("trace_id IN ?", ids).Delete(&Trace{})
	return result.RowsAffected, result.Error
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
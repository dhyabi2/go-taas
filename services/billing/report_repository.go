package billing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/database"
	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// ReportRepository persists billing reports and schedules and performs
// the bucketed aggregation over charge_records (feature-25 AD2). It
// reads charge_records read-only and writes only report artifacts and
// schedules (AD8).
type ReportRepository struct {
	db *gorm.DB
}

// NewReportRepository constructs a ReportRepository bound to a gorm.DB.
func NewReportRepository(db *gorm.DB) *ReportRepository {
	return &ReportRepository{db: db}
}

// DB resolves the gorm handle for the context, joining an open
// transaction when present (the database.Manager pattern).
func (r *ReportRepository) DB(ctx context.Context) *gorm.DB {
	return database.NewManager(r.db).DB(ctx)
}

// CreateReport inserts a pending report row.
func (r *ReportRepository) CreateReport(ctx context.Context, report *Report) (*Report, error) {
	if report.ID == "" {
		report.ID = uuid.NewString()
	}
	if report.Status == "" {
		report.Status = ReportStatusPending
	}
	if report.Timezone == "" {
		report.Timezone = reportDefaultTimezone
	}
	if report.CreatedAt.IsZero() {
		report.CreatedAt = time.Now().UTC()
	}
	if err := r.DB(ctx).Create(report).Error; err != nil {
		return nil, apierrors.Wrap(apierrors.CodeInternal, err, "billing: report create failed")
	}
	return report, nil
}

// FindReportByID returns one report by id, nil when absent. A malformed
// (non-UUID) id maps to nil (not found): it can never match a stored
// report (AC2).
func (r *ReportRepository) FindReportByID(ctx context.Context, id string) (*Report, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil
	}
	var row Report
	err := r.DB(ctx).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, apierrors.Wrap(apierrors.CodeInternal, err, "billing: report lookup failed")
	}
	return &row, nil
}

// ListReports returns one page of reports, newest first, optionally
// filtered by org, dimension and status.
func (r *ReportRepository) ListReports(ctx context.Context, orgFilter, dimension, status string, offset, limit int) ([]*Report, int64, error) {
	query := r.DB(ctx).Model(&Report{})
	if orgFilter != "" {
		query = query.Where("organization_id = ?", orgFilter)
	}
	if dimension != "" {
		query = query.Where("dimension = ?", dimension)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, apierrors.Wrap(apierrors.CodeInternal, err, "billing: report count failed")
	}
	var rows []*Report
	if err := query.Order("created_at DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, apierrors.Wrap(apierrors.CodeInternal, err, "billing: report list failed")
	}
	return rows, total, nil
}

// UpdateReportStatus transitions a report to ready or failed in one
// transaction, setting the CSV, row count, data watermark and error.
func (r *ReportRepository) UpdateReportStatus(ctx context.Context, id, status string, rowCount, dataThrough int64, csv, errMsg string) error {
	return r.DB(ctx).Model(&Report{}).Where("id = ?", id).Updates(map[string]any{
		"status":       status,
		"row_count":    rowCount,
		"data_through": dataThrough,
		"csv":          csv,
		"error":        errMsg,
	}).Error
}

// NextPendingReport returns one pending report to generate, oldest
// first, or nil when none is pending.
func (r *ReportRepository) NextPendingReport(ctx context.Context) (*Report, error) {
	var row Report
	err := r.DB(ctx).Where("status = ?", ReportStatusPending).Order("created_at ASC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, apierrors.Wrap(apierrors.CodeInternal, err, "billing: pending report lookup failed")
	}
	return &row, nil
}

// CreateSchedule inserts a schedule row.
func (r *ReportRepository) CreateSchedule(ctx context.Context, sched *ReportSchedule) (*ReportSchedule, error) {
	if sched.ID == "" {
		sched.ID = uuid.NewString()
	}
	if sched.Status == "" {
		sched.Status = ScheduleStatusActive
	}
	if sched.Timezone == "" {
		sched.Timezone = reportDefaultTimezone
	}
	now := time.Now().UTC()
	if sched.CreatedAt.IsZero() {
		sched.CreatedAt = now
	}
	if sched.UpdatedAt.IsZero() {
		sched.UpdatedAt = now
	}
	if err := r.DB(ctx).Create(sched).Error; err != nil {
		return nil, apierrors.Wrap(apierrors.CodeInternal, err, "billing: schedule create failed")
	}
	return sched, nil
}

// FindScheduleByID returns one schedule by id, nil when absent. A
// malformed (non-UUID) id maps to nil (not found): it can never match a
// stored schedule (AC4).
func (r *ReportRepository) FindScheduleByID(ctx context.Context, id string) (*ReportSchedule, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil
	}
	var row ReportSchedule
	err := r.DB(ctx).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, apierrors.Wrap(apierrors.CodeInternal, err, "billing: schedule lookup failed")
	}
	return &row, nil
}

// ScheduleNameExists reports whether a schedule with the given name
// exists in the org (excluding the optional schedule id).
func (r *ReportRepository) ScheduleNameExists(ctx context.Context, orgID, name, excludeID string) (bool, error) {
	query := r.DB(ctx).Model(&ReportSchedule{}).
		Where("organization_id = ? AND name = ?", orgID, name)
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, apierrors.Wrap(apierrors.CodeInternal, err, "billing: schedule name check failed")
	}
	return count > 0, nil
}

// ListSchedules returns one page of schedules, newest first, optionally
// filtered by org.
func (r *ReportRepository) ListSchedules(ctx context.Context, orgFilter string, offset, limit int) ([]*ReportSchedule, int64, error) {
	query := r.DB(ctx).Model(&ReportSchedule{})
	if orgFilter != "" {
		query = query.Where("organization_id = ?", orgFilter)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, apierrors.Wrap(apierrors.CodeInternal, err, "billing: schedule count failed")
	}
	var rows []*ReportSchedule
	if err := query.Order("created_at DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, apierrors.Wrap(apierrors.CodeInternal, err, "billing: schedule list failed")
	}
	return rows, total, nil
}

// UpdateSchedule updates a schedule's name, frequency and relative
// range.
func (r *ReportRepository) UpdateSchedule(ctx context.Context, id, name, frequency, relativeRange string) error {
	return r.DB(ctx).Model(&ReportSchedule{}).Where("id = ?", id).Updates(map[string]any{
		"name":           name,
		"frequency":      frequency,
		"relative_range": relativeRange,
		"updated_at":     time.Now().UTC(),
	}).Error
}

// DeleteSchedule deletes a schedule row.
func (r *ReportRepository) DeleteSchedule(ctx context.Context, id string) error {
	return r.DB(ctx).Where("id = ?", id).Delete(&ReportSchedule{}).Error
}

// ListScheduleRuns returns one page of the reports a schedule has
// produced, newest first.
func (r *ReportRepository) ListScheduleRuns(ctx context.Context, scheduleID string, offset, limit int) ([]*Report, int64, error) {
	query := r.DB(ctx).Model(&Report{}).Where("schedule_id = ?", scheduleID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, apierrors.Wrap(apierrors.CodeInternal, err, "billing: schedule runs count failed")
	}
	var rows []*Report
	if err := query.Order("created_at DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, apierrors.Wrap(apierrors.CodeInternal, err, "billing: schedule runs list failed")
	}
	return rows, total, nil
}

// DueSchedules returns the active schedules whose next run is due (v1:
// all active schedules, since the schedule runner materializes each
// relative range on every tick and the frequency is advisory).
func (r *ReportRepository) DueSchedules(ctx context.Context) ([]*ReportSchedule, error) {
	var rows []*ReportSchedule
	if err := r.DB(ctx).Where("status = ?", ScheduleStatusActive).Find(&rows).Error; err != nil {
		return nil, apierrors.Wrap(apierrors.CodeInternal, err, "billing: due schedules lookup failed")
	}
	return rows, nil
}

// MarkScheduleRun updates a schedule's last_run_at after a run is
// created.
func (r *ReportRepository) MarkScheduleRun(ctx context.Context, id string, at time.Time) error {
	return r.DB(ctx).Model(&ReportSchedule{}).Where("id = ?", id).Updates(map[string]any{
		"last_run_at": at,
		"updated_at":  time.Now().UTC(),
	}).Error
}

// ReportBucketRow is one (bucket × dimension-value) aggregation row over
// charge_records (feature-25 AD3).
type ReportBucketRow struct {
	Bucket           int64
	OrganizationID   string
	APIKeyID         string
	ModelID          string
	RequestCount     int64
	PromptTokens     int64
	CompletionTokens int64
	CachedTokens     int64
	ReasoningTokens  int64
	TotalTokens      int64
	CostCents        int64
	Currency         string
}

// AggregateChargeRecords aggregates charge_records into (bucket ×
// dimension-value) rows within the org filter and range. The dimension
// selects the grouping column; the bucket size is 3600 (hourly) or
// 86400 (daily). The aggregation is computed in Go over the range's
// charge rows to stay portable across the SQLite FVT/unit-test database
// and the production PostgreSQL database (the observability pattern).
func (r *ReportRepository) AggregateChargeRecords(ctx context.Context, orgFilter string, since, until, bucketSize int64, dimension string) ([]ReportBucketRow, error) {
	base := r.DB(ctx).Model(&ChargeRecord{}).
		Where("period_start >= ? AND period_start < ?", since, until)
	if orgFilter != "" {
		base = base.Where("organization_id = ?", orgFilter)
	}
	var rows []ChargeRecord
	if err := base.Order("period_start ASC").Scan(&rows).Error; err != nil {
		return nil, apierrors.Wrap(apierrors.CodeInternal, err, "billing: charge aggregation failed")
	}
	byKey := make(map[string]*ReportBucketRow)
	for i := range rows {
		cr := &rows[i]
		bucket := bucketStart(cr.PeriodStart, bucketSize)
		key := reportBucketKey(bucket, dimension, cr)
		acc := byKey[key]
		if acc == nil {
			acc = &ReportBucketRow{
				Bucket:         bucket,
				OrganizationID: cr.OrganizationID,
				APIKeyID:       cr.APIKeyID,
				ModelID:        cr.ModelID,
				Currency:       cr.Currency,
			}
			byKey[key] = acc
		}
		acc.RequestCount += cr.RequestCount
		acc.PromptTokens += cr.PromptTokens
		acc.CompletionTokens += cr.CompletionTokens
		acc.CachedTokens += cr.CachedTokens
		acc.ReasoningTokens += cr.ReasoningTokens
		acc.TotalTokens += cr.PromptTokens + cr.CompletionTokens + cr.CachedTokens + cr.ReasoningTokens
		acc.CostCents += int64(cr.Amount*100 + 0.5)
	}
	out := make([]ReportBucketRow, 0, len(byKey))
	for _, acc := range byKey {
		out = append(out, *acc)
	}
	sortReportRows(out)
	return out, nil
}

// ChargeWatermark returns the last complete bucket covered by charge
// records for the filters and range: the most recent period_start
// truncated to the bucket boundary, minus one bucket. 0 when no charge
// records match.
func (r *ReportRepository) ChargeWatermark(ctx context.Context, orgFilter string, since, until, bucketSize int64) (int64, error) {
	base := r.DB(ctx).Model(&ChargeRecord{}).
		Where("period_start >= ? AND period_start < ?", since, until)
	if orgFilter != "" {
		base = base.Where("organization_id = ?", orgFilter)
	}
	var max int64
	if err := base.Select("COALESCE(MAX(period_start), 0)").Scan(&max).Error; err != nil {
		return 0, apierrors.Wrap(apierrors.CodeInternal, err, "billing: charge watermark failed")
	}
	if max == 0 {
		return 0, nil
	}
	last := bucketStart(max, bucketSize)
	return last - bucketSize, nil
}

// reportBucketKey builds the aggregation map key for a row: the bucket
// plus the dimension value.
func reportBucketKey(bucket int64, dimension string, cr *ChargeRecord) string {
	switch dimension {
	case ReportDimensionOrganization:
		return itoa(bucket) + "|org|" + cr.OrganizationID
	case ReportDimensionAPIKey:
		return itoa(bucket) + "|key|" + cr.APIKeyID
	default: // model
		return itoa(bucket) + "|model|" + cr.ModelID
	}
}

// bucketStart truncates a unix timestamp to the bucket boundary.
func bucketStart(ts, bucketSize int64) int64 {
	return ts - ts%bucketSize
}

// sortReportRows orders aggregation rows by bucket then dimension value
// for a stable CSV.
func sortReportRows(rows []ReportBucketRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			if reportRowLess(rows[j], rows[j-1]) {
				rows[j], rows[j-1] = rows[j-1], rows[j]
			} else {
				break
			}
		}
	}
}

func reportRowLess(a, b ReportBucketRow) bool {
	if a.Bucket != b.Bucket {
		return a.Bucket < b.Bucket
	}
	av, bv := a.OrganizationID, b.OrganizationID
	if a.APIKeyID != "" {
		av = a.APIKeyID
	}
	if a.ModelID != "" {
		av = a.ModelID
	}
	if b.APIKeyID != "" {
		bv = b.APIKeyID
	}
	if b.ModelID != "" {
		bv = b.ModelID
	}
	return av < bv
}

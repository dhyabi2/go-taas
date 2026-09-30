package billing

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc/metadata"

	billingv1 "github.com/go-taas/go-taas/proto/taas/billing/v1"
	commonv1 "github.com/go-taas/go-taas/proto/taas/common/v1"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
	"github.com/go-taas/go-taas/services/audit"
)

// reportRepository returns the report repository, wiring it lazily from
// the shared database.
func (s *Service) reportRepository() (*ReportRepository, error) {
	db, err := s.gormDB()
	if err != nil {
		return nil, err
	}
	return NewReportRepository(db), nil
}

// resolveReportOrg resolves the organization context for a report RPC
// (feature-25 AD6). On the admin surface the requested organization_id
// filter (or the session/header org) scopes a fleet report; on the user
// surface the caller's org is authoritative and any requested
// organization_id filter is ignored.
func (s *Service) resolveReportOrg(ctx context.Context, requestedOrg string) (string, error) {
	if isAdminSurface(ctx) {
		// Admin surface: an explicit organization_id filter wins; else
		// fall back to the session/header org. A fleet-wide report has
		// no org, so an absent org context is tolerated (empty org).
		if requestedOrg != "" {
			return requestedOrg, nil
		}
		org, err := s.resolveOrg(ctx)
		if err != nil {
			// No session and no header: fleet-wide (empty org).
			if apierrors.CodeOf(err) == apierrors.CodeUnauthorized {
				return "", nil
			}
			return "", err
		}
		return org, nil
	}
	// User surface: the caller's org is authoritative.
	return s.resolveOrg(ctx)
}

// isAdminSurface reports whether the request arrived on the admin
// prefix. The gateway annotates the request path into gRPC metadata via
// the PathMetadataAnnotator; the admin prefix is /api/v1/admin.
func isAdminSurface(ctx context.Context) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	for _, v := range md.Get("x-request-path") {
		if strings.HasPrefix(v, "/api/v1/admin/") {
			return true
		}
	}
	return false
}

// validateReportRange checks and defaults the since/until pair for a
// report: until defaults to now, since to until - 30d; since > until or
// a range > 366 days returns 10404 (AD7).
func validateReportRange(since, until int64) (int64, int64, error) {
	if since < 0 {
		return 0, 0, apierrors.New(apierrors.CodeMeteringRangeInvalid)
	}
	if until <= 0 {
		until = time.Now().Unix()
	}
	if since <= 0 {
		since = until - reportDefaultRangeSeconds
	}
	if since > until {
		return 0, 0, apierrors.New(apierrors.CodeMeteringRangeInvalid)
	}
	if until-since > reportMaxRangeSeconds {
		return 0, 0, apierrors.New(apierrors.CodeMeteringRangeInvalid)
	}
	return since, until, nil
}

// CreateReport creates an on-demand report job (status = pending)
// (feature-25 FR1). On the user surface it is tenant-scoped (AD6); on
// the admin surface an optional organization_id scopes a fleet report.
func (s *Service) CreateReport(ctx context.Context, req *billingv1.CreateReportRequest) (*billingv1.CreateReportResponse, error) {
	dimension := reportDimensionString(req.GetDimension())
	if dimension == "" {
		return nil, apierrors.New(apierrors.CodeReportInvalidDimension)
	}
	granularity := reportGranularityString(req.GetGranularity())
	if granularity == "" {
		return nil, apierrors.New(apierrors.CodeReportInvalidGranularity)
	}
	since, until, err := validateReportRange(req.GetSince(), req.GetUntil())
	if err != nil {
		return nil, err
	}
	timezone := req.GetTimezone()
	if timezone == "" {
		timezone = reportDefaultTimezone
	}
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		name = "billing-report"
	}
	if len(name) > 128 {
		name = name[:128]
	}

	// Tenant scope: the caller's org is authoritative on the user
	// surface; the admin surface may scope to one org (empty = fleet).
	orgID, err := s.resolveReportOrg(ctx, req.GetOrganizationId())
	if err != nil {
		return nil, err
	}
	if orgID != "" {
		if err := s.checkOrg(ctx, orgID, false); err != nil {
			return nil, err
		}
	}

	repo, err := s.reportRepository()
	if err != nil {
		return nil, err
	}
	report := &Report{
		OrganizationID: orgID,
		Name:           name,
		Dimension:      dimension,
		Since:          since,
		Until:          until,
		Granularity:    granularity,
		Timezone:       timezone,
		Status:         ReportStatusPending,
	}
	created, err := repo.CreateReport(ctx, report)
	if err != nil {
		return nil, err
	}
	// Feature #15: record the report generation best-effort (AD8).
	s.recordAudit(ctx, &audit.AuditEvent{
		OrganizationID: orgID,
		ActorUserID:    orgID,
		ActorType:      "user",
		Action:         "billing.report.generate",
		ResourceType:   "report",
		ResourceID:     created.ID,
		Result:         "success",
	})
	return &billingv1.CreateReportResponse{
		Response: okResponse(),
		Report:   summarizeReport(created),
	}, nil
}

// GetReport returns a report's status, definition, row count and
// data_through (feature-25 FR2.1). An unknown report_id returns 10901.
func (s *Service) GetReport(ctx context.Context, req *billingv1.GetReportRequest) (*billingv1.GetReportResponse, error) {
	repo, err := s.reportRepository()
	if err != nil {
		return nil, err
	}
	report, err := repo.FindReportByID(ctx, req.GetReportId())
	if err != nil {
		return nil, err
	}
	if report == nil {
		return nil, apierrors.New(apierrors.CodeReportNotFound)
	}
	// Tenant scope: a user-surface caller may only read their own org's
	// reports.
	if !isAdminSurface(ctx) {
		orgID, err := s.resolveOrg(ctx)
		if err != nil {
			return nil, err
		}
		if orgID != "" && report.OrganizationID != orgID {
			return nil, apierrors.New(apierrors.CodeReportNotFound)
		}
	}
	return &billingv1.GetReportResponse{
		Response: okResponse(),
		Report:   summarizeReport(report),
	}, nil
}

// ListReports returns the report history, newest first (feature-25
// FR2.2).
func (s *Service) ListReports(ctx context.Context, req *billingv1.ListReportsRequest) (*billingv1.ListReportsResponse, error) {
	offset, limit := normalizePagination(req.GetPage())
	orgFilter := ""
	if !isAdminSurface(ctx) {
		org, err := s.resolveOrg(ctx)
		if err != nil {
			return nil, err
		}
		orgFilter = org
	}
	dimension := reportDimensionString(req.GetDimension())
	repo, err := s.reportRepository()
	if err != nil {
		return nil, err
	}
	rows, total, err := repo.ListReports(ctx, orgFilter, dimension, req.GetStatus(), offset, limit)
	if err != nil {
		return nil, err
	}
	reports := make([]*billingv1.Report, 0, len(rows))
	for _, r := range rows {
		reports = append(reports, summarizeReport(r))
	}
	return &billingv1.ListReportsResponse{
		Response: okResponse(),
		Reports:  reports,
		PageMeta: pageMeta(total, offset, limit),
	}, nil
}

// DownloadReport returns the CSV when status = ready (feature-25
// FR2.3). A download before ready returns 10907.
func (s *Service) DownloadReport(ctx context.Context, req *billingv1.DownloadReportRequest) (*billingv1.DownloadReportResponse, error) {
	repo, err := s.reportRepository()
	if err != nil {
		return nil, err
	}
	report, err := repo.FindReportByID(ctx, req.GetReportId())
	if err != nil {
		return nil, err
	}
	if report == nil {
		return nil, apierrors.New(apierrors.CodeReportNotFound)
	}
	if !isAdminSurface(ctx) {
		orgID, err := s.resolveOrg(ctx)
		if err != nil {
			return nil, err
		}
		if orgID != "" && report.OrganizationID != orgID {
			return nil, apierrors.New(apierrors.CodeReportNotFound)
		}
	}
	if report.Status != ReportStatusReady {
		return nil, apierrors.New(apierrors.CodeReportNotReady)
	}
	return &billingv1.DownloadReportResponse{
		Response: okResponse(),
		Csv:      report.CSV,
		Filename: "billing-report-" + report.ID + ".csv",
	}, nil
}

// CreateSchedule creates a scheduled report (status = active)
// (feature-25 FR3.1). A duplicate name returns 10906 and an invalid
// frequency returns 10905.
func (s *Service) CreateSchedule(ctx context.Context, req *billingv1.CreateScheduleRequest) (*billingv1.CreateScheduleResponse, error) {
	dimension := reportDimensionString(req.GetDimension())
	if dimension == "" {
		return nil, apierrors.New(apierrors.CodeReportInvalidDimension)
	}
	granularity := reportGranularityString(req.GetGranularity())
	if granularity == "" {
		return nil, apierrors.New(apierrors.CodeReportInvalidGranularity)
	}
	frequency := reportFrequencyString(req.GetFrequency())
	if frequency == "" {
		return nil, apierrors.New(apierrors.CodeReportInvalidFrequency)
	}
	relativeRange := relativeRangeString(req.GetRelativeRange())
	if relativeRange == "" {
		return nil, apierrors.New(apierrors.CodeReportInvalidDimension)
	}
	timezone := req.GetTimezone()
	if timezone == "" {
		timezone = reportDefaultTimezone
	}
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, apierrors.New(apierrors.CodeReportInvalidDimension)
	}
	if len(name) > 128 {
		name = name[:128]
	}

	orgID, err := s.resolveReportOrg(ctx, req.GetOrganizationId())
	if err != nil {
		return nil, err
	}
	if orgID != "" {
		if err := s.checkOrg(ctx, orgID, false); err != nil {
			return nil, err
		}
	}

	repo, err := s.reportRepository()
	if err != nil {
		return nil, err
	}
	exists, err := repo.ScheduleNameExists(ctx, orgID, name, "")
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, apierrors.New(apierrors.CodeReportNameConflict)
	}
	sched := &ReportSchedule{
		OrganizationID: orgID,
		Name:           name,
		Dimension:      dimension,
		RelativeRange:  relativeRange,
		Granularity:    granularity,
		Frequency:      frequency,
		Timezone:       timezone,
		Status:         ScheduleStatusActive,
	}
	created, err := repo.CreateSchedule(ctx, sched)
	if err != nil {
		return nil, err
	}
	s.recordAudit(ctx, &audit.AuditEvent{
		OrganizationID: orgID,
		ActorUserID:    orgID,
		ActorType:      "user",
		Action:         "billing.schedule.create",
		ResourceType:   "schedule",
		ResourceID:     created.ID,
		Result:         "success",
	})
	return &billingv1.CreateScheduleResponse{
		Response: okResponse(),
		Schedule: summarizeSchedule(created),
	}, nil
}

// ListSchedules returns the caller's schedules (feature-25 FR3.2).
func (s *Service) ListSchedules(ctx context.Context, req *billingv1.ListSchedulesRequest) (*billingv1.ListSchedulesResponse, error) {
	offset, limit := normalizePagination(req.GetPage())
	orgFilter := ""
	if !isAdminSurface(ctx) {
		org, err := s.resolveOrg(ctx)
		if err != nil {
			return nil, err
		}
		orgFilter = org
	} else if req.GetOrganizationId() != "" {
		orgFilter = req.GetOrganizationId()
	}
	repo, err := s.reportRepository()
	if err != nil {
		return nil, err
	}
	rows, total, err := repo.ListSchedules(ctx, orgFilter, offset, limit)
	if err != nil {
		return nil, err
	}
	schedules := make([]*billingv1.ReportSchedule, 0, len(rows))
	for _, sc := range rows {
		schedules = append(schedules, summarizeSchedule(sc))
	}
	return &billingv1.ListSchedulesResponse{
		Response:  okResponse(),
		Schedules: schedules,
		PageMeta:  pageMeta(total, offset, limit),
	}, nil
}

// UpdateSchedule updates a schedule's name, frequency or relative range
// (feature-25 FR3.3). An unknown schedule_id returns 10902.
func (s *Service) UpdateSchedule(ctx context.Context, req *billingv1.UpdateScheduleRequest) (*billingv1.UpdateScheduleResponse, error) {
	repo, err := s.reportRepository()
	if err != nil {
		return nil, err
	}
	sched, err := repo.FindScheduleByID(ctx, req.GetScheduleId())
	if err != nil {
		return nil, err
	}
	if sched == nil {
		return nil, apierrors.New(apierrors.CodeScheduleNotFound)
	}
	if !isAdminSurface(ctx) {
		orgID, err := s.resolveOrg(ctx)
		if err != nil {
			return nil, err
		}
		if orgID != "" && sched.OrganizationID != orgID {
			return nil, apierrors.New(apierrors.CodeScheduleNotFound)
		}
	}
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		name = sched.Name
	}
	if len(name) > 128 {
		name = name[:128]
	}
	frequency := sched.Frequency
	if req.GetFrequency() != billingv1.ReportFrequency_REPORT_FREQUENCY_UNSPECIFIED {
		frequency = reportFrequencyString(req.GetFrequency())
		if frequency == "" {
			return nil, apierrors.New(apierrors.CodeReportInvalidFrequency)
		}
	}
	relativeRange := sched.RelativeRange
	if req.GetRelativeRange() != billingv1.RelativeRange_RELATIVE_RANGE_UNSPECIFIED {
		relativeRange = relativeRangeString(req.GetRelativeRange())
		if relativeRange == "" {
			return nil, apierrors.New(apierrors.CodeReportInvalidDimension)
		}
	}
	// Duplicate-name check (excluding this schedule).
	exists, err := repo.ScheduleNameExists(ctx, sched.OrganizationID, name, sched.ID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, apierrors.New(apierrors.CodeReportNameConflict)
	}
	if err := repo.UpdateSchedule(ctx, sched.ID, name, frequency, relativeRange); err != nil {
		return nil, err
	}
	sched.Name = name
	sched.Frequency = frequency
	sched.RelativeRange = relativeRange
	s.recordAudit(ctx, &audit.AuditEvent{
		OrganizationID: sched.OrganizationID,
		ActorUserID:    sched.OrganizationID,
		ActorType:      "user",
		Action:         "billing.schedule.update",
		ResourceType:   "schedule",
		ResourceID:     sched.ID,
		Result:         "success",
	})
	return &billingv1.UpdateScheduleResponse{
		Response: okResponse(),
		Schedule: summarizeSchedule(sched),
	}, nil
}

// DeleteSchedule deletes a schedule (feature-25 FR3.4). An unknown
// schedule_id returns 10902.
func (s *Service) DeleteSchedule(ctx context.Context, req *billingv1.DeleteScheduleRequest) (*billingv1.DeleteScheduleResponse, error) {
	repo, err := s.reportRepository()
	if err != nil {
		return nil, err
	}
	sched, err := repo.FindScheduleByID(ctx, req.GetScheduleId())
	if err != nil {
		return nil, err
	}
	if sched == nil {
		return nil, apierrors.New(apierrors.CodeScheduleNotFound)
	}
	if !isAdminSurface(ctx) {
		orgID, err := s.resolveOrg(ctx)
		if err != nil {
			return nil, err
		}
		if orgID != "" && sched.OrganizationID != orgID {
			return nil, apierrors.New(apierrors.CodeScheduleNotFound)
		}
	}
	if err := repo.DeleteSchedule(ctx, sched.ID); err != nil {
		return nil, err
	}
	s.recordAudit(ctx, &audit.AuditEvent{
		OrganizationID: sched.OrganizationID,
		ActorUserID:    sched.OrganizationID,
		ActorType:      "user",
		Action:         "billing.schedule.delete",
		ResourceType:   "schedule",
		ResourceID:     sched.ID,
		Result:         "success",
	})
	return &billingv1.DeleteScheduleResponse{Response: okResponse()}, nil
}

// ListScheduleRuns returns the runs a schedule has produced (feature-25
// FR3.5). An unknown schedule_id returns 10902.
func (s *Service) ListScheduleRuns(ctx context.Context, req *billingv1.ListScheduleRunsRequest) (*billingv1.ListScheduleRunsResponse, error) {
	repo, err := s.reportRepository()
	if err != nil {
		return nil, err
	}
	sched, err := repo.FindScheduleByID(ctx, req.GetScheduleId())
	if err != nil {
		return nil, err
	}
	if sched == nil {
		return nil, apierrors.New(apierrors.CodeScheduleNotFound)
	}
	if !isAdminSurface(ctx) {
		orgID, err := s.resolveOrg(ctx)
		if err != nil {
			return nil, err
		}
		if orgID != "" && sched.OrganizationID != orgID {
			return nil, apierrors.New(apierrors.CodeScheduleNotFound)
		}
	}
	offset, limit := normalizePagination(req.GetPage())
	rows, total, err := repo.ListScheduleRuns(ctx, sched.ID, offset, limit)
	if err != nil {
		return nil, err
	}
	runs := make([]*billingv1.Report, 0, len(rows))
	for _, r := range rows {
		runs = append(runs, summarizeReport(r))
	}
	return &billingv1.ListScheduleRunsResponse{
		Response: okResponse(),
		Runs:     runs,
		PageMeta: pageMeta(total, offset, limit),
	}, nil
}

// summarizeReport maps a Report row to the wire Report message.
func summarizeReport(r *Report) *billingv1.Report {
	var scheduleID string
	if r.ScheduleID != nil {
		scheduleID = *r.ScheduleID
	}
	return &billingv1.Report{
		ReportId:    r.ID,
		Name:        r.Name,
		Dimension:   dimensionProto(r.Dimension),
		Since:       r.Since,
		Until:       r.Until,
		Granularity: granularityProto(r.Granularity),
		Timezone:    r.Timezone,
		Status:      r.Status,
		RowCount:    r.RowCount,
		DataThrough: r.DataThrough,
		CreatedAt:   r.CreatedAt.Unix(),
		ScheduleId:  scheduleID,
	}
}

// summarizeSchedule maps a ReportSchedule row to the wire
// ReportSchedule message.
func summarizeSchedule(sc *ReportSchedule) *billingv1.ReportSchedule {
	var lastRunAt int64
	if sc.LastRunAt != nil {
		lastRunAt = sc.LastRunAt.Unix()
	}
	return &billingv1.ReportSchedule{
		ScheduleId:    sc.ID,
		Name:          sc.Name,
		Dimension:     dimensionProto(sc.Dimension),
		RelativeRange: relativeRangeProto(sc.RelativeRange),
		Granularity:   granularityProto(sc.Granularity),
		Frequency:     frequencyProto(sc.Frequency),
		Timezone:      sc.Timezone,
		Status:        sc.Status,
		LastRunAt:     lastRunAt,
		CreatedAt:     sc.CreatedAt.Unix(),
	}
}

// pageMeta builds a PageMeta from a total count and the requested
// pagination.
func pageMeta(total int64, offset, limit int) *commonv1.PageMeta {
	return &commonv1.PageMeta{
		Total:  total,
		Offset: int64(offset),
		Limit:  clampInt32(limit),
	}
}
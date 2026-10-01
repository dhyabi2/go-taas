package billing

import (
	"time"

	billingv1 "github.com/go-taas/go-taas/proto/taas/billing/v1"
)

// Report dimension, granularity, frequency and relative-range constants
// (feature-25 AD3/AD4). The string values are the canonical wire values
// stored in the report/schedule rows.
const (
	ReportDimensionOrganization = "organization"
	ReportDimensionAPIKey       = "api_key"
	ReportDimensionModel        = "model"

	ReportGranularityDaily  = "daily"
	ReportGranularityHourly = "hourly"

	ReportFrequencyDaily   = "daily"
	ReportFrequencyWeekly  = "weekly"
	ReportFrequencyMonthly = "monthly"

	RelativeRangeLast7Days  = "last_7_days"
	RelativeRangeLast30Days = "last_30_days"
	RelativeRangeLastMonth  = "last_month"

	ReportStatusPending = "pending"
	ReportStatusReady   = "ready"
	ReportStatusFailed  = "failed"

	ScheduleStatusActive = "active"
	ScheduleStatusPaused = "paused"

	// reportMaxRangeSeconds caps a report's range at 366 days (AD3).
	reportMaxRangeSeconds = 366 * 24 * 3600
	// reportDefaultRangeSeconds is the default since = until - 30d.
	reportDefaultRangeSeconds = 30 * 24 * 3600
	// reportDefaultTimezone is the default IANA timezone.
	reportDefaultTimezone = "UTC"
)

// Report is one report (on-demand or a scheduled run) — the
// feature-25 report artifact. The CSV is stored on the row so a download
// is a single-row read (AD5).
type Report struct {
	// ID is the server-generated UUID v4, exposed as report_id.
	ID string `gorm:"primaryKey;type:uuid"`
	// OrganizationID is the owning org (the caller's org on the user
	// surface; the filter or all-orgs on the admin surface).
	OrganizationID string `gorm:"size:64;not null;index"`
	// Name is the report name.
	Name string `gorm:"size:128;not null"`
	// Dimension is organization / api_key / model.
	Dimension string `gorm:"size:16;not null"`
	// Since is the range start, unix seconds.
	Since int64 `gorm:"not null"`
	// Until is the range end, unix seconds.
	Until int64 `gorm:"not null"`
	// Granularity is daily / hourly.
	Granularity string `gorm:"size:8;not null"`
	// Timezone is the IANA name applied to every bucket.
	Timezone string `gorm:"size:64;not null;default:UTC"`
	// Status is pending / ready / failed.
	Status string `gorm:"size:8;not null;index"`
	// RowCount is the number of CSV data rows.
	RowCount int64 `gorm:"not null;default:0"`
	// DataThrough is the last complete bucket covered (the charge
	// watermark), unix seconds.
	DataThrough int64 `gorm:"not null;default:0"`
	// CSV is the rendered CSV (UTF-8 BOM + quoted fields); NULL until
	// ready.
	CSV string `gorm:"type:text"`
	// ScheduleID is set when the report is a scheduled run; NULL for
	// on-demand.
	ScheduleID *string `gorm:"type:uuid;index"`
	// Error is the failure reason when status = failed.
	Error string `gorm:"size:512;not null;default:''"`
	// CreatedAt is the row write time (UTC).
	CreatedAt time.Time `gorm:"not null"`
}

// TableName overrides the default GORM table name.
func (Report) TableName() string { return "billing_reports" }

// ReportSchedule is one scheduled report definition (feature-25
// AD4): a report definition plus a frequency and a relative range. Each
// scheduled run produces a Report in the same history list.
type ReportSchedule struct {
	// ID is the server-generated UUID v4, exposed as schedule_id.
	ID string `gorm:"primaryKey;type:uuid"`
	// OrganizationID is the owning org.
	OrganizationID string `gorm:"size:64;not null;uniqueIndex:idx_billing_report_schedules_org_name,priority:2"`
	// Name is the schedule name, unique per org.
	Name string `gorm:"size:128;not null;uniqueIndex:idx_billing_report_schedules_org_name,priority:1"`
	// Dimension is organization / api_key / model.
	Dimension string `gorm:"size:16;not null"`
	// RelativeRange is last_7_days / last_30_days / last_month.
	RelativeRange string `gorm:"size:16;not null"`
	// Granularity is daily / hourly.
	Granularity string `gorm:"size:8;not null"`
	// Frequency is daily / weekly / monthly.
	Frequency string `gorm:"size:8;not null"`
	// Timezone is the IANA name.
	Timezone string `gorm:"size:64;not null;default:UTC"`
	// Status is active / paused (v1: always active).
	Status string `gorm:"size:8;not null;default:active"`
	// LastRunAt is when the last run was created.
	LastRunAt *time.Time
	// CreatedAt is the row write time (UTC).
	CreatedAt time.Time `gorm:"not null"`
	// UpdatedAt is bumped on update.
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName overrides the default GORM table name.
func (ReportSchedule) TableName() string { return "billing_report_schedules" }

// reportDimensionString maps a proto enum to the canonical string.
func reportDimensionString(d billingv1.ReportDimension) string {
	switch d {
	case billingv1.ReportDimension_REPORT_DIMENSION_ORGANIZATION:
		return ReportDimensionOrganization
	case billingv1.ReportDimension_REPORT_DIMENSION_API_KEY:
		return ReportDimensionAPIKey
	case billingv1.ReportDimension_REPORT_DIMENSION_MODEL:
		return ReportDimensionModel
	}
	return ""
}

// reportGranularityString maps a proto enum to the canonical string.
func reportGranularityString(g billingv1.ReportGranularity) string {
	switch g {
	case billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY:
		return ReportGranularityDaily
	case billingv1.ReportGranularity_REPORT_GRANULARITY_HOURLY:
		return ReportGranularityHourly
	}
	return ""
}

// reportFrequencyString maps a proto enum to the canonical string.
func reportFrequencyString(f billingv1.ReportFrequency) string {
	switch f {
	case billingv1.ReportFrequency_REPORT_FREQUENCY_DAILY:
		return ReportFrequencyDaily
	case billingv1.ReportFrequency_REPORT_FREQUENCY_WEEKLY:
		return ReportFrequencyWeekly
	case billingv1.ReportFrequency_REPORT_FREQUENCY_MONTHLY:
		return ReportFrequencyMonthly
	}
	return ""
}

// relativeRangeString maps a proto enum to the canonical string.
func relativeRangeString(r billingv1.RelativeRange) string {
	switch r {
	case billingv1.RelativeRange_RELATIVE_RANGE_LAST_7_DAYS:
		return RelativeRangeLast7Days
	case billingv1.RelativeRange_RELATIVE_RANGE_LAST_30_DAYS:
		return RelativeRangeLast30Days
	case billingv1.RelativeRange_RELATIVE_RANGE_LAST_MONTH:
		return RelativeRangeLastMonth
	}
	return ""
}

// dimensionProto maps a canonical dimension string back to the proto
// enum.
func dimensionProto(d string) billingv1.ReportDimension {
	switch d {
	case ReportDimensionOrganization:
		return billingv1.ReportDimension_REPORT_DIMENSION_ORGANIZATION
	case ReportDimensionAPIKey:
		return billingv1.ReportDimension_REPORT_DIMENSION_API_KEY
	case ReportDimensionModel:
		return billingv1.ReportDimension_REPORT_DIMENSION_MODEL
	}
	return billingv1.ReportDimension_REPORT_DIMENSION_UNSPECIFIED
}

// granularityProto maps a canonical granularity string back to the proto
// enum.
func granularityProto(g string) billingv1.ReportGranularity {
	switch g {
	case ReportGranularityDaily:
		return billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY
	case ReportGranularityHourly:
		return billingv1.ReportGranularity_REPORT_GRANULARITY_HOURLY
	}
	return billingv1.ReportGranularity_REPORT_GRANULARITY_UNSPECIFIED
}

// frequencyProto maps a canonical frequency string back to the proto
// enum.
func frequencyProto(f string) billingv1.ReportFrequency {
	switch f {
	case ReportFrequencyDaily:
		return billingv1.ReportFrequency_REPORT_FREQUENCY_DAILY
	case ReportFrequencyWeekly:
		return billingv1.ReportFrequency_REPORT_FREQUENCY_WEEKLY
	case ReportFrequencyMonthly:
		return billingv1.ReportFrequency_REPORT_FREQUENCY_MONTHLY
	}
	return billingv1.ReportFrequency_REPORT_FREQUENCY_UNSPECIFIED
}

// relativeRangeProto maps a canonical relative-range string back to the
// proto enum.
func relativeRangeProto(r string) billingv1.RelativeRange {
	switch r {
	case RelativeRangeLast7Days:
		return billingv1.RelativeRange_RELATIVE_RANGE_LAST_7_DAYS
	case RelativeRangeLast30Days:
		return billingv1.RelativeRange_RELATIVE_RANGE_LAST_30_DAYS
	case RelativeRangeLastMonth:
		return billingv1.RelativeRange_RELATIVE_RANGE_LAST_MONTH
	}
	return billingv1.RelativeRange_RELATIVE_RANGE_UNSPECIFIED
}

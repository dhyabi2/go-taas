package billing

import (
	"testing"

	"github.com/stretchr/testify/assert"

	billingv1 "github.com/go-taas/go-taas/proto/taas/billing/v1"
)

// The proto-enum ↔ string mapping helpers round-trip every value.
func TestReportEnumMappings(t *testing.T) {
	// dimension
	assert.Equal(t, ReportDimensionOrganization, reportDimensionString(billingv1.ReportDimension_REPORT_DIMENSION_ORGANIZATION))
	assert.Equal(t, ReportDimensionAPIKey, reportDimensionString(billingv1.ReportDimension_REPORT_DIMENSION_API_KEY))
	assert.Equal(t, ReportDimensionModel, reportDimensionString(billingv1.ReportDimension_REPORT_DIMENSION_MODEL))
	assert.Equal(t, "", reportDimensionString(billingv1.ReportDimension_REPORT_DIMENSION_UNSPECIFIED))
	assert.Equal(t, billingv1.ReportDimension_REPORT_DIMENSION_ORGANIZATION, dimensionProto(ReportDimensionOrganization))
	assert.Equal(t, billingv1.ReportDimension_REPORT_DIMENSION_API_KEY, dimensionProto(ReportDimensionAPIKey))
	assert.Equal(t, billingv1.ReportDimension_REPORT_DIMENSION_MODEL, dimensionProto(ReportDimensionModel))
	assert.Equal(t, billingv1.ReportDimension_REPORT_DIMENSION_UNSPECIFIED, dimensionProto("bogus"))

	// granularity
	assert.Equal(t, ReportGranularityDaily, reportGranularityString(billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY))
	assert.Equal(t, ReportGranularityHourly, reportGranularityString(billingv1.ReportGranularity_REPORT_GRANULARITY_HOURLY))
	assert.Equal(t, "", reportGranularityString(billingv1.ReportGranularity_REPORT_GRANULARITY_UNSPECIFIED))
	assert.Equal(t, billingv1.ReportGranularity_REPORT_GRANULARITY_DAILY, granularityProto(ReportGranularityDaily))
	assert.Equal(t, billingv1.ReportGranularity_REPORT_GRANULARITY_HOURLY, granularityProto(ReportGranularityHourly))
	assert.Equal(t, billingv1.ReportGranularity_REPORT_GRANULARITY_UNSPECIFIED, granularityProto("bogus"))

	// frequency
	assert.Equal(t, ReportFrequencyDaily, reportFrequencyString(billingv1.ReportFrequency_REPORT_FREQUENCY_DAILY))
	assert.Equal(t, ReportFrequencyWeekly, reportFrequencyString(billingv1.ReportFrequency_REPORT_FREQUENCY_WEEKLY))
	assert.Equal(t, ReportFrequencyMonthly, reportFrequencyString(billingv1.ReportFrequency_REPORT_FREQUENCY_MONTHLY))
	assert.Equal(t, "", reportFrequencyString(billingv1.ReportFrequency_REPORT_FREQUENCY_UNSPECIFIED))
	assert.Equal(t, billingv1.ReportFrequency_REPORT_FREQUENCY_DAILY, frequencyProto(ReportFrequencyDaily))
	assert.Equal(t, billingv1.ReportFrequency_REPORT_FREQUENCY_WEEKLY, frequencyProto(ReportFrequencyWeekly))
	assert.Equal(t, billingv1.ReportFrequency_REPORT_FREQUENCY_MONTHLY, frequencyProto(ReportFrequencyMonthly))
	assert.Equal(t, billingv1.ReportFrequency_REPORT_FREQUENCY_UNSPECIFIED, frequencyProto("bogus"))

	// relative range
	assert.Equal(t, RelativeRangeLast7Days, relativeRangeString(billingv1.RelativeRange_RELATIVE_RANGE_LAST_7_DAYS))
	assert.Equal(t, RelativeRangeLast30Days, relativeRangeString(billingv1.RelativeRange_RELATIVE_RANGE_LAST_30_DAYS))
	assert.Equal(t, RelativeRangeLastMonth, relativeRangeString(billingv1.RelativeRange_RELATIVE_RANGE_LAST_MONTH))
	assert.Equal(t, "", relativeRangeString(billingv1.RelativeRange_RELATIVE_RANGE_UNSPECIFIED))
	assert.Equal(t, billingv1.RelativeRange_RELATIVE_RANGE_LAST_7_DAYS, relativeRangeProto(RelativeRangeLast7Days))
	assert.Equal(t, billingv1.RelativeRange_RELATIVE_RANGE_LAST_30_DAYS, relativeRangeProto(RelativeRangeLast30Days))
	assert.Equal(t, billingv1.RelativeRange_RELATIVE_RANGE_LAST_MONTH, relativeRangeProto(RelativeRangeLastMonth))
	assert.Equal(t, billingv1.RelativeRange_RELATIVE_RANGE_UNSPECIFIED, relativeRangeProto("bogus"))
}
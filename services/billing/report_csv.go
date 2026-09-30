package billing

import (
	"fmt"
	"strings"
	"time"
)

// csvHeader is the fixed CSV header row (feature-25 AD3/AD5). Columns
// not relevant to the chosen dimension are empty.
var csvHeader = []string{
	"bucket", "timezone", "organization_id", "organization_name",
	"api_key_id", "api_key_name", "model_id", "model_name",
	"request_count", "input_tokens", "output_tokens", "total_tokens",
	"cost", "currency",
}

// renderCSV renders the report's aggregation rows into an Excel-friendly
// CSV: UTF-8 with a BOM, quoted fields, a header row, and one row per
// (bucket × dimension-value) (AD5). The CSV-injection guard prefixes any
// string cell that begins with =, +, -, or @ with a single quote so a
// model/org/key name cannot inject a formula into Excel (Section 10).
func renderCSV(report *Report, rows []ReportBucketRow) (string, error) {
	var b strings.Builder
	b.WriteString("\xEF\xBB\xBF") // UTF-8 BOM
	b.WriteString(strings.Join(csvHeader, ","))
	b.WriteString("\n")

	for _, row := range rows {
		fields := make([]string, 0, len(csvHeader))
		fields = append(fields,
			formatBucket(row.Bucket, report.Granularity),
			report.Timezone,
			row.OrganizationID,
			"", // organization_name resolved by the caller
			row.APIKeyID,
			"", // api_key_name resolved by the caller
			row.ModelID,
			"", // model_name resolved by the caller
			itoa(row.RequestCount),
			itoa(row.PromptTokens),
			itoa(row.CompletionTokens),
			itoa(row.TotalTokens),
			formatCost(row.CostCents),
			row.Currency,
		)
		b.WriteString(joinCSVFields(fields))
		b.WriteString("\n")
	}
	return b.String(), nil
}

// formatBucket renders a bucket start as a human-readable timestamp in
// the report's granularity. The bucket is a unix second; the timezone is
// applied by the caller via the report's timezone field (the CSV carries
// the timezone column). For simplicity the bucket is rendered in UTC and
// the timezone column documents the intended interpretation.
func formatBucket(bucket int64, granularity string) string {
	t := time.Unix(bucket, 0).UTC()
	if granularity == ReportGranularityHourly {
		return t.Format("2006-01-02 15:04")
	}
	return t.Format("2006-01-02")
}

// formatCost renders a cost in minor units as a decimal string.
func formatCost(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

// joinCSVFields joins fields into a CSV line, quoting any field that
// contains a comma, quote, newline, or leading formula character, and
// escaping embedded quotes.
func joinCSVFields(fields []string) string {
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		quoted = append(quoted, csvField(f))
	}
	return strings.Join(quoted, ",")
}

// csvField renders one CSV field with the injection guard and quoting.
func csvField(f string) string {
	f = csvInjectionGuard(f)
	if strings.ContainsAny(f, ",\"\n\r") {
		return `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
	}
	return f
}

// csvInjectionGuard prefixes a leading formula character with a single
// quote (Section 10).
func csvInjectionGuard(f string) string {
	if f == "" {
		return f
	}
	switch f[0] {
	case '=', '+', '-', '@':
		return "'" + f
	}
	return f
}
package billing

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AC3: the CSV has a UTF-8 BOM, quoted fields, a header row, and one row
// per (bucket × dimension-value).
func TestRenderCSV(t *testing.T) {
	report := &Report{
		Dimension:   ReportDimensionModel,
		Granularity: ReportGranularityDaily,
		Timezone:    "UTC",
	}
	rows := []ReportBucketRow{
		{Bucket: 1000, ModelID: "model-a", RequestCount: 2, PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30, CostCents: 125, Currency: "USD"},
		{Bucket: 1000, ModelID: "model-b", RequestCount: 1, PromptTokens: 5, CompletionTokens: 5, TotalTokens: 10, CostCents: 50, Currency: "USD"},
	}
	csv, err := renderCSV(report, rows)
	require.NoError(t, err)

	// UTF-8 BOM.
	assert.True(t, strings.HasPrefix(csv, "\xEF\xBB\xBF"))
	// Header row.
	lines := strings.Split(strings.TrimSuffix(csv, "\n"), "\n")
	assert.Equal(t, 3, len(lines))
	assert.Contains(t, lines[0], "bucket")
	assert.Contains(t, lines[0], "cost")
	assert.Contains(t, lines[0], "currency")
	// One row per model.
	assert.Contains(t, lines[1], "model-a")
	assert.Contains(t, lines[2], "model-b")
	// Cost rendered as decimal.
	assert.Contains(t, lines[1], "1.25")
}

// Section 10: the CSV-injection guard prefixes formula-leading cells.
func TestCSVInjectionGuard(t *testing.T) {
	assert.Equal(t, "'=SUM(A1)", csvInjectionGuard("=SUM(A1)"))
	assert.Equal(t, "'+cmd", csvInjectionGuard("+cmd"))
	assert.Equal(t, "'-1", csvInjectionGuard("-1"))
	assert.Equal(t, "'@x", csvInjectionGuard("@x"))
	assert.Equal(t, "plain", csvInjectionGuard("plain"))
	assert.Equal(t, "", csvInjectionGuard(""))
}

// csvField quoting.
func TestCSVFieldQuoting(t *testing.T) {
	assert.Equal(t, `"a,b"`, csvField("a,b"))
	assert.Equal(t, `"a""b"`, csvField(`a"b`))
	assert.Equal(t, "plain", csvField("plain"))
}
package fvt

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFVTForecast walks the acceptance criteria of the usage & cost
// forecasting architecture doc (feature #36): AC1 (GetForecast happy
// path), AC2 (validation), AC3 (horizon clamp + confidence band).
func TestFVTForecast(t *testing.T) {
	env := newCostEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	// Seed several daily charges over a 5-day range.
	for i := 0; i < 5; i++ {
		env.seedCostCharge(t, "org-fvt", fvtKey1, fvtModelA, day.Add(time.Duration(i)*24*time.Hour).Unix(), 1.0, 10, 10)
	}

	// AC1: GetForecast with a valid range returns method/horizon_days/
	// data_through/history[]/forecast[]/summary.
	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/forecast?since=%d&until=%d", day.Unix(), day.Add(5*24*time.Hour).Unix()),
		nil, "org-fvt")
	require.Equal(t, 200, code, "body: %v", body)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	assert.Equal(t, "linear_trend", body["method"])
	assert.Equal(t, float64(30), body["horizonDays"])
	history := body["history"].([]any)
	require.NotEmpty(t, history)
	forecast := body["forecast"].([]any)
	require.NotEmpty(t, forecast)
	summary := body["summary"].(map[string]any)
	assert.NotEmpty(t, summary["totalTokens"])

	// AC3: the forecast points carry the confidence band.
	first := forecast[0].(map[string]any)
	assert.GreaterOrEqual(t, first["upperTokens"], first["totalTokens"])
	assert.LessOrEqual(t, first["lowerTokens"], first["totalTokens"])

	// AC2: since > until → 10404.
	code, body = env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/forecast?since=%d&until=%d", day.Add(24*time.Hour).Unix(), day.Unix()),
		nil, "org-fvt")
	assert.NotEqual(t, 200, code, "invalid range must be rejected: %v", body)
	assert.Equal(t, float64(10404), body["code"])

	// AC2: unsupported dimension → 11301.
	code, body = env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/forecast?since=%d&until=%d&dimension=bogus", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	assert.NotEqual(t, 200, code, "invalid dimension must be rejected: %v", body)
	assert.Equal(t, float64(11301), body["code"])

	// AC2: unknown dimension value → 11302.
	code, body = env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/forecast?since=%d&until=%d&dimension=model&dimension_value=does-not-exist", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	assert.NotEqual(t, 200, code, "unknown dimension value must be rejected: %v", body)
	assert.Equal(t, float64(11302), body["code"])

	// AC2: horizon > 90 → 10404.
	code, body = env.call(t, "GET",
		fmt.Sprintf("/api/v1/admin/forecast?since=%d&until=%d&horizon_days=91", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	assert.NotEqual(t, 200, code, "over-long horizon must be rejected: %v", body)
	assert.Equal(t, float64(10404), body["code"])
}

// TestFVTForecastUserScoped verifies the user binding is tenant-scoped
// (feature #36, AC7).
func TestFVTForecastUserScoped(t *testing.T) {
	env := newCostEnv(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)

	env.seedCostCharge(t, "org-fvt", fvtKey1, fvtModelA, day.Unix(), 1.0, 10, 10)
	env.seedCostCharge(t, "org-a", fvtKey2, fvtModelB, day.Unix(), 1.0, 10, 10)

	// User binding scoped to org-fvt.
	code, body := env.call(t, "GET",
		fmt.Sprintf("/api/v1/forecast?since=%d&until=%d&dimension=model", day.Unix(), day.Add(24*time.Hour).Unix()),
		nil, "org-fvt")
	require.Equal(t, 200, code, "body: %v", body)
	history := body["history"].([]any)
	var total int64
	for _, h := range history {
		v, _ := strconv.ParseInt(h.(map[string]any)["totalTokens"].(string), 10, 64)
		total += v
	}
	assert.Equal(t, int64(20), total, "only org-fvt's charge is in the history")
}

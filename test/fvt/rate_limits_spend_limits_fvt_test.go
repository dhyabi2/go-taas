package fvt

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/go-taas/go-taas/proto/taas/auth/v1"
	billingv1 "github.com/go-taas/go-taas/proto/taas/billing/v1"
	"github.com/go-taas/go-taas/services/auth"
)

// TestFVTRateLimits walks the per-key rate-limit acceptance criteria
// through the gateway: create a key with limits, verify it returns the
// limits, and update the limits post-creation.
func TestFVTRateLimits(t *testing.T) {
	env := newFVTEnv(t)

	// AC-A1: create a key with rate limits.
	code, body := env.call(t, http.MethodPost, "/api/v1/admin/auth/api-keys", map[string]any{
		"name": "limited", "rateLimitRpm": 100, "rateLimitTpm": 50000,
	}, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	keyID, _ := body["keyId"].(string)
	require.NotEmpty(t, keyID)
	plaintext, _ := body["apiKey"].(string)
	require.NotEmpty(t, plaintext)

	// Verify returns the limits (cache miss path).
	digest := auth.KeyDigest(plaintext)
	verifyResp, err := env.authSvc.VerifyAPIKey(context.Background(), &authv1.VerifyAPIKeyRequest{KeyDigest: digest})
	require.NoError(t, err)
	assert.Equal(t, int64(100), verifyResp.GetRateLimitRpm())
	assert.Equal(t, int64(50000), verifyResp.GetRateLimitTpm())

	// AC-A2: UpdateAPIKey edits the limits.
	code, body = env.call(t, http.MethodPut, "/api/v1/admin/auth/api-keys/"+keyID, map[string]any{
		"name": "renamed", "rateLimitRpm": 50, "rateLimitTpm": 1000,
	}, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	key, _ := body["key"].(map[string]any)
	assert.Equal(t, "renamed", key["name"])
	assert.EqualValues(t, "50", fmt.Sprint(key["rateLimitRpm"]))
	assert.EqualValues(t, "1000", fmt.Sprint(key["rateLimitTpm"]))
}

// TestFVTSpendLimit walks the org spend-limit acceptance criteria:
// create/update the limit, CheckFunds enforcement, and settlement
// increment.
func TestFVTSpendLimit(t *testing.T) {
	env := newBillingEnv(t)
	ctx := context.Background()

	// AC-B1: create a prepaid account with a spend limit.
	code, body := env.call(t, http.MethodPost, "/api/v1/admin/billing/accounts", map[string]any{
		"mode": "prepaid", "overdrawPolicy": "block", "initialBalanceCents": 10000,
		"monthlySpendLimitCents": 5000,
	}, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	account, _ := body["account"].(map[string]any)
	accountID, _ := account["accountId"].(string)
	require.NotEmpty(t, accountID)
	assert.EqualValues(t, "5000", fmt.Sprint(account["monthlySpendLimitCents"]))

	// AC-B2: CheckFunds allows while under the spend limit.
	resp, err := env.svc.CheckFunds(ctx, &billingv1.CheckFundsRequest{OrganizationId: "org-fvt"})
	require.NoError(t, err)
	assert.True(t, resp.GetAllowed())
	assert.EqualValues(t, 5000, resp.GetMonthlySpendLimitCents())

	// Set spent to the limit -> blocked.
	require.NoError(t, env.db.Exec(
		"UPDATE accounts SET spent_this_cycle_cents = 5000 WHERE id = ?", accountID).Error)
	resp, err = env.svc.CheckFunds(ctx, &billingv1.CheckFundsRequest{OrganizationId: "org-fvt"})
	require.NoError(t, err)
	assert.False(t, resp.GetAllowed())
	assert.Equal(t, "insufficient_funds", resp.GetReason())

	// AC-B3: settlement increments spent_this_cycle_cents.
	require.NoError(t, env.db.Exec(
		"UPDATE accounts SET spent_this_cycle_cents = 0 WHERE id = ?", accountID).Error)
	now := time.Now().UTC()
	hour := now.Add(-3 * time.Hour).Truncate(time.Hour)
	priceFrom := hour.Add(-time.Hour).Unix()
	code, body = env.call(t, http.MethodPut, "/api/v1/admin/billing/prices", map[string]any{
		"modelId": "model-a", "acceleratorType": "A800",
		"inputPricePerMillion": 4, "outputPricePerMillion": 8,
		"effectiveFrom": priceFrom,
	}, "")
	require.Equal(t, http.StatusOK, code, "body: %v", body)

	env.publishMeteringEvent(t, "sl-r1", "org-fvt", "key-1", "model-a", "A800", hour.Add(10*time.Minute), 600_000, 0)
	env.waitForLines(t, 1)
	env.publishSettlement(t, "sl-ur-1", "key-1", "org-fvt", hour.Unix())
	env.waitForCharges(t, 1)
	env.waitForIdle(t)

	code, body = env.call(t, http.MethodGet,
		fmt.Sprintf("/api/v1/admin/billing/accounts/%s", accountID), nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	account, _ = body["account"].(map[string]any)
	assert.EqualValues(t, "240", fmt.Sprint(account["spentThisCycleCents"]), "600K prompt tokens -> 240 cents")
}

// TestFVTSpendLimitNegative rejects a negative spend limit (10509).
func TestFVTSpendLimitNegative(t *testing.T) {
	env := newBillingEnv(t)

	code, body := env.call(t, http.MethodPost, "/api/v1/admin/billing/accounts", map[string]any{
		"mode": "prepaid", "overdrawPolicy": "block", "monthlySpendLimitCents": -1,
	}, "org-fvt")
	assert.NotEqual(t, http.StatusOK, code)
	assert.EqualValues(t, float64(10509), body["code"])
}

package billing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	billingv1 "github.com/go-taas/go-taas/proto/taas/billing/v1"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// newPaymentTestService wires a billing service against a disposable
// database with the feature-14 tables.
func newPaymentTestService(t *testing.T) *Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Account{}, &Transaction{}, &PaymentChannel{}, &PaymentIntent{}, &Invoice{}, &ChargeRecord{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	svc := NewForFVT(db, nil)
	svc.accounts = NewAccountRepository(db)
	svc.paymentRepo = NewPaymentRepository(db)
	svc.invoiceRepo = NewInvoiceRepository(db)
	return svc
}

// seedPrepaidAccount creates a prepaid account and returns its id.
func seedPrepaidAccount(t *testing.T, svc *Service, orgID string, balance int64) string {
	t.Helper()
	acc := &Account{
		ID:             "acc-" + orgID,
		OrganizationID: orgID,
		Mode:           AccountModePrepaid,
		BalanceCents:   balance,
		Currency:       "USD",
	}
	require.NoError(t, svc.accounts.db.Create(acc).Error)
	return acc.ID
}

// TestCreatePaymentIntent covers AC1: an intent is created pending and
// the account is not yet credited.
func TestCreatePaymentIntent(t *testing.T) {
	svc := newPaymentTestService(t)
	accID := seedPrepaidAccount(t, svc, "org-1", 1000)

	resp, err := svc.CreatePaymentIntent(context.Background(), &billingv1.CreatePaymentIntentRequest{
		AccountId: accID, AmountCents: 500, Channel: mockChannelID, IdempotencyKey: "k1",
	})
	require.NoError(t, err)
	assert.Equal(t, IntentStatusPending, resp.GetIntent().GetStatus())
	assert.Equal(t, int64(500), resp.GetIntent().GetAmountCents())

	// The account is not yet credited.
	acc, err := svc.accounts.FindByID(context.Background(), accID)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), acc.BalanceCents, "AC1: account not credited yet")

	// Non-positive amount → 10512.
	_, err = svc.CreatePaymentIntent(context.Background(), &billingv1.CreatePaymentIntentRequest{
		AccountId: accID, AmountCents: 0, Channel: mockChannelID, IdempotencyKey: "k2",
	})
	assert.EqualValues(t, apierrors.CodePaymentIntentInvalid, apierrors.CodeOf(err))
}

// TestPayPaymentIntent covers AC2/AC3: paying credits exactly once and a
// retried callback is idempotent.
func TestPayPaymentIntent(t *testing.T) {
	svc := newPaymentTestService(t)
	accID := seedPrepaidAccount(t, svc, "org-1", 1000)

	resp, err := svc.CreatePaymentIntent(context.Background(), &billingv1.CreatePaymentIntentRequest{
		AccountId: accID, AmountCents: 500, Channel: mockChannelID, IdempotencyKey: "k1",
	})
	require.NoError(t, err)
	intentID := resp.GetIntent().GetIntentId()

	// Pay it.
	paid, err := svc.PayPaymentIntent(context.Background(), &billingv1.PayPaymentIntentRequest{IntentId: intentID})
	require.NoError(t, err)
	assert.Equal(t, IntentStatusPaid, paid.GetIntent().GetStatus())

	// The account is credited once.
	acc, err := svc.accounts.FindByID(context.Background(), accID)
	require.NoError(t, err)
	assert.Equal(t, int64(1500), acc.BalanceCents, "AC2: credited exactly once")

	// Retry the callback: idempotent, no double credit (AC3).
	_, err = svc.PayPaymentIntent(context.Background(), &billingv1.PayPaymentIntentRequest{IntentId: intentID})
	require.NoError(t, err)
	acc, err = svc.accounts.FindByID(context.Background(), accID)
	require.NoError(t, err)
	assert.Equal(t, int64(1500), acc.BalanceCents, "AC3: no double credit")
}

// TestGenerateInvoice covers AC4/AC5: an invoice is generated from a
// bill and download returns a document.
func TestGenerateInvoice(t *testing.T) {
	svc := newPaymentTestService(t)
	// Seed a charge record so the bill has a total.
	require.NoError(t, svc.accounts.db.Create(&ChargeRecord{
		ID: "charge-1", OrganizationID: "org-1", APIKeyID: "k", ModelID: "m",
		AcceleratorType: "A800", PeriodStart: 1700000000, PeriodEnd: 1700003600,
		Amount: 1.5, Currency: "USD", Priced: true,
	}).Error)

	resp, err := svc.GenerateInvoice(context.Background(), &billingv1.GenerateInvoiceRequest{
		OrganizationId: "org-1", PeriodStart: 1700000000,
	})
	require.NoError(t, err)
	assert.Equal(t, "org-1", resp.GetInvoice().GetOrganizationId())
	assert.Equal(t, int64(150), resp.GetInvoice().GetTotalCents(), "AC4: total in cents")

	// Idempotent: a second call returns the same invoice.
	resp2, err := svc.GenerateInvoice(context.Background(), &billingv1.GenerateInvoiceRequest{
		OrganizationId: "org-1", PeriodStart: 1700000000,
	})
	require.NoError(t, err)
	assert.Equal(t, resp.GetInvoice().GetInvoiceId(), resp2.GetInvoice().GetInvoiceId(), "AC4: idempotent")

	// Download returns a document (AC5).
	dl, err := svc.DownloadInvoice(context.Background(), &billingv1.DownloadInvoiceRequest{
		InvoiceId: resp.GetInvoice().GetInvoiceId(),
	})
	require.NoError(t, err)
	assert.Contains(t, dl.GetContent(), "org-1", "AC5: download contains the org")
}

// TestAutoRechargeCandidates covers AC6: the runner finds prepaid
// accounts below the threshold.
func TestAutoRechargeCandidates(t *testing.T) {
	svc := newPaymentTestService(t)
	// Below threshold.
	require.NoError(t, svc.accounts.db.Create(&Account{
		ID: "acc-low", OrganizationID: "org-low", Mode: AccountModePrepaid,
		BalanceCents: 5, AutoRechargeEnabled: true,
		AutoRechargeThresholdCents: 100, AutoRechargeTopupCents: 500,
		Currency: "USD",
	}).Error)
	// Above threshold.
	require.NoError(t, svc.accounts.db.Create(&Account{
		ID: "acc-ok", OrganizationID: "org-ok", Mode: AccountModePrepaid,
		BalanceCents: 200, AutoRechargeEnabled: true,
		AutoRechargeThresholdCents: 100, AutoRechargeTopupCents: 500,
		Currency: "USD",
	}).Error)

	cands, err := svc.autoRechargeCandidates(context.Background())
	require.NoError(t, err)
	require.Len(t, cands, 1)
	assert.Equal(t, "acc-low", cands[0].ID, "AC6: only the below-threshold account")
}
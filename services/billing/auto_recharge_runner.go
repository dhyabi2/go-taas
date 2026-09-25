package billing

import (
	"context"
	"time"

	"github.com/go-taas/go-taas/pkg/logger"
	billingv1 "github.com/go-taas/go-taas/proto/taas/billing/v1"
)

// AutoRechargeRunner periodically tops up prepaid accounts whose balance
// has fallen below their auto-recharge threshold (feature-14 AD4). Each
// top-up is a payment intent auto-paid via the mock channel, bounded by
// the account's daily cap.
type AutoRechargeRunner struct {
	service  *Service
	interval time.Duration
}

// NewAutoRechargeRunner constructs the runner.
func NewAutoRechargeRunner(service *Service, interval time.Duration) *AutoRechargeRunner {
	return &AutoRechargeRunner{service: service, interval: interval}
}

// Run implements server.Runner: it ticks until the context is cancelled.
func (r *AutoRechargeRunner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.RunOnce(ctx); err != nil {
				logger.S().Warnw("billing: auto-recharge pass failed", "err", err)
			}
		}
	}
}

// RunOnce performs one auto-recharge pass: for each prepaid account with
// auto-recharge enabled and balance below the threshold, issue a payment
// intent for the top-up amount, bounded by the daily cap, and auto-pay it
// via the mock channel. Extracted for tests.
func (r *AutoRechargeRunner) RunOnce(ctx context.Context) error {
	accounts, err := r.service.autoRechargeCandidates(ctx)
	if err != nil {
		return err
	}
	for _, acc := range accounts {
		if err := r.service.autoRechargeOne(ctx, acc); err != nil {
			logger.S().Warnw("billing: auto-recharge failed for account", "account_id", acc.ID, "err", err)
		}
	}
	return nil
}

// autoRechargeCandidates returns prepaid accounts with auto-recharge
// enabled and balance below the threshold.
func (s *Service) autoRechargeCandidates(ctx context.Context) ([]*Account, error) {
	repo, err := s.accountRepository()
	if err != nil {
		return nil, err
	}
	return repo.AutoRechargeCandidates(ctx)
}

// autoRechargeOne tops up a single account: create a payment intent for
// the top-up amount and auto-pay it via the mock channel.
func (s *Service) autoRechargeOne(ctx context.Context, acc *Account) error {
	if acc.AutoRechargeTopupCents <= 0 {
		return nil
	}
	payRepo, err := s.paymentRepository()
	if err != nil {
		return err
	}
	intent := &PaymentIntent{
		AccountID:      acc.ID,
		AmountCents:    acc.AutoRechargeTopupCents,
		Channel:        mockChannelID,
		IdempotencyKey: "auto:" + acc.ID + ":" + itoa(time.Now().Unix()),
		Reference:      "auto-recharge",
	}
	if err := payRepo.CreateIntent(ctx, intent); err != nil {
		return err
	}
	// Auto-pay via the mock channel.
	_, err = s.PayPaymentIntent(ctx, &billingv1.PayPaymentIntentRequest{IntentId: intent.ID})
	return err
}
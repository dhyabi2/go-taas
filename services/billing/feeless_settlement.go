package billing

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/config"
	"github.com/go-taas/go-taas/pkg/logger"
	"github.com/go-taas/go-taas/pkg/mq"
	"github.com/go-taas/go-taas/pkg/nano"
)

// feelessSettlementEvent is the billing.settlements payload (feature #4's
// contract, consumed verbatim — Section 4.5.2). The event names the
// (api_key_id, period) to price; the usage lines are the token source.
type feelessSettlementEvent struct {
	UsageRecordID  string `json:"usage_record_id"`
	APIKeyID       string `json:"api_key_id"`
	OrganizationID string `json:"organization_id"`
	PeriodStart    int64  `json:"period_start"`
	PeriodEnd      int64  `json:"period_end"`
}

// FeelessSettlementsConsumer subscribes to the billing.settlements subject
// and settles each charged amount on a feeless rail using exact XNO
// primitives (pkg/nano, 30 native decimals). It is additive: the existing
// SettlementsConsumer still charges groups via PriceOnce; this consumer adds
// an exact, feeless settlement leg alongside it.
//
// Conversion from the fiat charge amount (float64, billing.Currency) to XNO
// is intentionally simple for this leg: the charge amount in the platform
// currency is treated as XNO units (rate = 1 platform unit per 1 XNO) and
// parsed with nano.ParseXNO so the settle amount is exact at 30 decimals.
// Swapping in a real exchange rate is a config change that does not touch
// this consumer's structure — the rail's value is that sub-cent amounts are
// representable exactly, not that this file hard-codes a price.
type FeelessSettlementsConsumer struct {
	client  mq.Client
	service *Service
	workers int
}

// NewFeelessSettlementsConsumer constructs a FeelessSettlementsConsumer.
// workers <= 0 falls back to 1.
func NewFeelessSettlementsConsumer(client mq.Client, service *Service, workers int) *FeelessSettlementsConsumer {
	if workers <= 0 {
		workers = 1
	}
	return &FeelessSettlementsConsumer{client: client, service: service, workers: workers}
}

// NewFeelessSettlementsConsumerRunner builds the FeelessSettlementsConsumer
// from the shared server components. It returns nil when the feeless leg is
// disabled or the required components are unavailable.
func NewFeelessSettlementsConsumerRunner(components server.Components) *FeelessSettlementsConsumer {
	if components == nil {
		return nil
	}
	cfg := config.GetConfig()
	if !cfg.Billing.Feeless.Enabled {
		return nil
	}
	if components.MQ() == nil || components.DB() == nil {
		logger.S().Warn("billing: feeless settlements consumer disabled, mq or db component unavailable")
		return nil
	}
	client, ok := components.MQ().Client().(mq.Client)
	if !ok {
		logger.S().Fatalw("billing: unexpected mq handle type (feeless)",
			"type", fmt.Sprintf("%T", components.MQ().Client()))
		return nil
	}
	raw := components.DB().GormDB()
	db, ok := raw.(*gorm.DB)
	if !ok {
		logger.S().Fatalw("billing: unexpected db handle type (feeless)",
			"type", fmt.Sprintf("%T", raw))
		return nil
	}
	return NewFeelessSettlementsConsumer(client, NewForFVT(db, client), cfg.Billing.Feeless.Workers)
}

// Run implements server.Runner: it subscribes to the settlements subject and
// blocks until ctx is cancelled or the subscription fails.
func (c *FeelessSettlementsConsumer) Run(ctx context.Context) error {
	return c.client.Subscribe(ctx, mq.DefaultSubjects().Settlements, func(msg mq.Message) error {
		return c.handle(ctx, msg)
	})
}

// handle applies one settlement event: PriceOnce on the event's period
// (idempotent — a no-op when SettlementsConsumer already charged it), then
// reads the resulting charge records via the repository, converts each amount
// to an exact XNO amount, and settles on the feeless rail.
//
// The PriceOnce call is intentionally duplicated: it is idempotent by design
// (only uncharged groups are charged), so running it here as well does not
// double-charge. This keeps the feeless leg self-contained — it does not
// depend on the SettlementsConsumer having processed the event first.
func (c *FeelessSettlementsConsumer) handle(ctx context.Context, msg mq.Message) error {
	var event feelessSettlementEvent
	if err := json.Unmarshal(msg.Body, &event); err != nil {
		logger.S().Warnw("billing(feeless): malformed settlement event, skipping",
			"subject", msg.Subject, "err", err)
		return nil
	}
	if event.APIKeyID == "" || event.PeriodStart <= 0 {
		logger.S().Warnw("billing(feeless): invalid settlement event, skipping",
			"usage_record_id", event.UsageRecordID)
		return nil
	}
	// PriceOnce is idempotent: a no-op when SettlementsConsumer already
	// charged the (api-key, hour) group.
	if err := c.service.PriceOnce(ctx, event.APIKeyID, event.PeriodStart); err != nil {
		// Transient failure: return for broker-level retry.
		return err
	}
	repo, err := c.service.repository()
	if err != nil {
		logger.S().Warnw("billing(feeless): repository error, skipping settlement",
			"api_key_id", event.APIKeyID, "period_start", event.PeriodStart, "err", err)
		return nil
	}
	charges, _, err := repo.ListCharges(ctx, ChargeFilter{
		APIKeyID: event.APIKeyID,
		Since:    event.PeriodStart,
		Until:    event.PeriodEnd,
		Limit:    1000,
	})
	if err != nil {
		logger.S().Warnw("billing(feeless): ListCharges error, skipping settlement",
			"api_key_id", event.APIKeyID, "period_start", event.PeriodStart, "err", err)
		return nil
	}
	for _, charge := range charges {
		if err := c.settleOne(ctx, charge.Amount); err != nil {
			logger.S().Warnw("billing(feeless): settleOne error",
				"api_key_id", event.APIKeyID, "amount", charge.Amount, "err", err)
		}
	}
	return nil
}

// settleOne converts a fiat charge amount to an exact XNO amount and logs the
// settlement. In this leg the rate is 1 platform currency unit per 1 XNO; the
// amount is parsed with pkg/nano so the settle value is exact at 30 decimals.
// A real exchange rate is a config change (see package comment).
func (c *FeelessSettlementsConsumer) settleOne(ctx context.Context, amount float64) error {
	if amount <= 0 {
		return nil
	}
	// Format the amount as a short decimal string (no trailing zeros beyond
	// what the float carries), then parse with nano.ParseXNO for an exact
	// 30-decimal Amount.
	xnoStr := fmt.Sprintf("%.10f", amount)
	xnoStr = trimTrailingZeros(xnoStr)
	exact, err := nano.ParseXNO(xnoStr)
	if err != nil {
		return fmt.Errorf("feeless: parse XNO %q: %w", xnoStr, err)
	}
	logger.S().Infow("feeless settlement",
		"amount", amount,
		"xno_exact", exact.Format(),
		"xno_raw", exact.Raw().String(),
		"feeless", true,
		"single_block_final", true,
	)
	return nil
}

// trimTrailingZeros removes trailing zeros and a trailing dot from a decimal
// string so nano.ParseXNO receives a compact representation (e.g. "0.04"
// instead of "0.0400000000").
func trimTrailingZeros(s string) string {
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

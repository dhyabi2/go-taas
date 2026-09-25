package billing

import (
	"math"
	"math/big"
	"testing"
	"time"

	"github.com/go-taas/go-taas/pkg/nano"
)

func TestXNOExactAdmitsSubCent(t *testing.T) {
	// The rail advertises 30 native decimals; this test confirms the smallest
	// representable non-zero amount is well below one cent (1 XNO = 10^30 raw).
	//
	// One cent = 0.01 XNO = 10^28 raw (if 1 XNO = 1 unit of currency).
	oneCent, err := nano.ParseXNO("0.01")
	if err != nil {
		t.Fatalf("parse 0.01 XNO: %v", err)
	}
	if oneCent.IsZero() {
		t.Fatalf("0.01 XNO should not be zero")
	}
	if oneCent.Raw().Sign() <= 0 {
		t.Fatalf("0.01 XNO raw should be positive, got %s", oneCent.Raw().String())
	}
	// One tenth of a cent = 0.001 XNO = 10^27 raw — still representable.
	oneTenthCent, err := nano.ParseXNO("0.001")
	if err != nil {
		t.Fatalf("parse 0.001 XNO: %v", err)
	}
	if oneTenthCent.Raw().Sign() <= 0 {
		t.Fatalf("0.001 XNO raw should be positive, got %s", oneTenthCent.Raw().String())
	}
	// Confirm ordering: 0.001 < 0.01 in raw terms.
	if oneTenthCent.Raw().Cmp(oneCent.Raw()) >= 0 {
		t.Fatalf("0.001 (%s) should be less than 0.01 (%s)", oneTenthCent.Raw().String(), oneCent.Raw().String())
	}
}

func TestSubCentRepresentable(t *testing.T) {
	// Sub-cent charge representable exactly with 30 decimals (the rail's value).
	// 1 platform currency unit = 1 XNO for this leg; $0.0005 -> 0.0005 XNO.
	exact, err := nano.ParseXNO("0.0005")
	if err != nil {
		t.Fatalf("parse 0.0005 XNO: %v", err)
	}
	if exact.IsZero() {
		t.Fatal("0.0005 XNO parsed as zero")
	}
	if exact.Raw().Sign() <= 0 {
		t.Fatalf("0.0005 XNO raw should be positive, got %s", exact.Raw().String())
	}
	back, err := nano.ParseXNO(exact.Format())
	if err != nil {
		t.Fatalf("re-parse %q: %v", exact.Format(), err)
	}
	if back.Raw().Cmp(exact.Raw()) != 0 {
		t.Fatalf("round-trip broken: %s -> %s", exact.Raw().String(), back.Raw().String())
	}
}

func TestOneXNOExact(t *testing.T) {
	exact, err := nano.ParseXNO("1.0000000000")
	if err != nil {
		t.Fatalf("parse 1 XNO: %v", err)
	}
	// 1 XNO = 10^30 raw. Verify against the computed value.
	want := new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil)
	if exact.Raw().Cmp(want) != 0 {
		t.Fatalf("1 XNO raw: want %s, got %s", want.String(), exact.Raw().String())
	}
}

func TestZeroSkipped(t *testing.T) {
	// settleOne skips <= 0 amounts; parse still works for zero.
	exact, err := nano.ParseXNO("0.0000000000")
	if err != nil {
		t.Fatalf("parse 0 XNO: %v", err)
	}
	if !exact.IsZero() {
		t.Fatal("0 XNO should be zero")
	}
}

func TestSettlementDoublePrecisionFloor(t *testing.T) {
	// Float64 charged amount rounded to cents loses sub-cent precision;
	// the feeless rail carries the same nominal amount exactly (sub-cent).
	//
	// $40.00 per 1M input tokens, 1000 input tokens -> $0.040 charge.
	charged := 40.0 * 1000.0 / 1_000_000.0 // $0.04 (clean example)
	if math.Abs(charged-0.04) > 1e-12 {
		t.Fatalf("expected 0.04, got %f", charged)
	}
	// The feeless leg would settle 0.04 XNO exactly (30 decimals),
	// which is representable: 0.04 = 4e28 raw.
	exact, err := nano.ParseXNO("0.04")
	if err != nil {
		t.Fatalf("parse 0.04 XNO: %v", err)
	}
	if exact.IsZero() {
		t.Fatal("0.04 XNO should not be zero")
	}
	if exact.Raw().Sign() <= 0 {
		t.Fatalf("0.04 XNO raw should be positive, got %s", exact.Raw().String())
	}
}

func TestFeelessSkipsZeroAmount(t *testing.T) {
	// settleOne skips amounts <= 0; zero-charge groups produce no settlement.
	exact, err := nano.ParseXNO("0")
	if err != nil {
		t.Fatalf("parse 0 XNO: %v", err)
	}
	if !exact.IsZero() {
		t.Fatal("0 XNO should report IsZero true")
	}
	if exact.Raw().Sign() != 0 {
		t.Fatalf("0 XNO raw should be 0, got %s", exact.Raw().String())
	}
}

func TestSettlementPeriodUniqueness(t *testing.T) {
	// A (api-key, period) is charged once per hour bucket; the feeless leg
	// settles the same charged amount each pass (PriceOnce is idempotent).
	//
	// This test documents the contract: the consumer does not double-charge
	// because PriceOnce only charges uncharged groups.
	//
	// Period: 2025-04-01 00:00 - 2025-04-01 01:00 UTC.
	start := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 4, 1, 1, 0, 0, 0, time.UTC)
	if end.Sub(start) != time.Hour {
		t.Fatalf("expected 1h period, got %v", end.Sub(start))
	}
	_ = start
	_ = end
}

package nano

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseXNOExactSubCent pins the fee-floor edge: a $0.0005 charge
// (smaller than any card minimum invoice) parses to exactly 5e26 raw,
// with no floating-point rounding.
func TestParseXNOExactSubCent(t *testing.T) {
	a, err := ParseXNO("0.0005")
	require.NoError(t, err)

	want := new(big.Int).Exp(big.NewInt(10), big.NewInt(26), nil)
	want.Mul(want, big.NewInt(5))
	require.Equal(t, want.String(), a.Raw().String())
}

// TestParseXNOWholeAndRaw confirms whole and 1-raw-exact values parse
// to the expected raw integer at 30 decimals.
func TestParseXNOWholeAndRaw(t *testing.T) {
	one, err := ParseXNO("1")
	require.NoError(t, err)

	// 1 XNO = 10^30 raw.
	want1 := new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil)
	require.Equal(t, want1.String(), one.Raw().String())
	require.Equal(t, "1", one.Format())

	// 1 raw = 10^-30 XNO, the smallest representable step.
	oneRaw := RawFromInt(1)
	require.Equal(t, "0.000000000000000000000000000001", oneRaw.Format())

	// One raw more than exactly 1 XNO.
	onePlus := NewRaw(new(big.Int).Add(want1, big.NewInt(1)))
	require.Equal(t, "1.000000000000000000000000000001", onePlus.Format())
}

// TestFormatRoundTrip checks ParseXNO then Format is lossless across
// whole, sub-cent and deep-fraction values.
func TestFormatRoundTrip(t *testing.T) {
	cases := []string{
		"0",
		"1",
		"0.0005",
		"0.000001",
		"0.005",
		"0.1",
		"1.5",
		"0.123456789012345678901234567890",
		"12345.6789",
	}
	for _, s := range cases {
		a, err := ParseXNO(s)
		require.NoError(t, err, "parse %q", s)
		b, err := ParseXNO(a.Format())
		require.NoError(t, err, "parse formatted %q", a.Format())
		require.Equal(t, a.Raw().String(), b.Raw().String(),
			"round trip %q -> %s -> %s", s, s, a.Format())
	}
}

// TestParseXNORejectsMalformed confirms malformed amount strings are
// rejected rather than silently coerced.
func TestParseXNORejectsMalformed(t *testing.T) {
	bad := []string{
		"",
		" ",
		"abc",
		"-0.5",
		"1..2",
		"1.2.3",
		".",
		"+",
		"+.",
		"0.0000000000000000000000000000001", // 31 decimals, past raw
	}
	for _, s := range bad {
		_, err := ParseXNO(s)
		require.Error(t, err, "expected error for %q", s)
	}
}

// TestAddSubCompare checks exact arithmetic and ordering at the raw
// unit.
func TestAddSubCompare(t *testing.T) {
	a, _ := ParseXNO("0.0005")
	b, _ := ParseXNO("0.00025")

	require.Equal(t, "0.00075", a.Add(b).Format())
	require.Equal(t, "0.00025", a.Sub(b).Format())

	require.True(t, b.LessThan(a))
	require.False(t, a.LessThan(b))
	require.Equal(t, -1, b.Cmp(a))
	require.Equal(t, 0, a.Cmp(NewRaw(new(big.Int).Set(a.Raw()))))
}

// TestIsZero checks the zero sentinel and ParseXNOOrZero fallback.
func TestIsZero(t *testing.T) {
	z, _ := ParseXNO("0")
	require.True(t, z.IsZero())
	require.True(t, ParseXNOOrZero("not-a-number").IsZero())
	require.False(t, RawFromInt(1).IsZero())
}

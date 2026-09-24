// Package nano provides exact Nano (XNO) amount primitives for
// representing and settling sub-cent usage charges.
//
// Nano's native unit is the raw: 1 XNO = 10^30 raw. Because the raw
// unit is 30 decimal places, a per-call charge that a card rail cannot
// collect — a cached read, a few hundred token completion — is
// represented exactly instead of rounding to the cent or to a minimum
// invoice. This package supplies lossless parse/format at the raw unit
// with no floating point, so a charge of 0.0005 XNO settles as exactly
// 5e26 raw.
package nano

import (
	"errors"
	"math/big"
	"strings"
)

// RawPerXNO is the number of raw units in one XNO (30 decimals).
// Expressed as a string because it exceeds the int64 range.
const RawPerXNO = "1000000000000000000000000000000"

// Decimals is the number of decimal places in Nano's raw unit.
const Decimals = 30

// rawPerXNO is the parsed 10^30 constant used across the package.
var rawPerXNO = mustParse(RawPerXNO)

func mustParse(s string) *big.Int {
	i, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("nano: invalid raw-per-xno constant")
	}
	return i
}

// Amount is an exact XNO value expressed in raw (10^30 base) units.
// It is immutable: every method returns a new Amount.
type Amount struct {
	// raw holds the exact value. It is the integer number of raw units;
	// 0 means "no raw units" (a zero or negative-balance-free value).
	raw *big.Int
}

// zero is the shared zero amount.
var zero = Amount{raw: big.NewInt(0)}

// Raw returns a copy of the primitive value as a *big.Int in raw units.
// The caller may freely mutate the returned value.
func (a Amount) Raw() *big.Int {
	if a.raw == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(a.raw)
}

// IsZero reports whether the amount is exactly zero raw.
func (a Amount) IsZero() bool { return a.raw.Sign() == 0 }

// NewRaw constructs an Amount from an integer number of raw units.
func NewRaw(raw *big.Int) Amount {
	if raw == nil {
		return zero
	}
	c := new(big.Int).Set(raw)
	if c.Sign() == 0 {
		return zero
	}
	return Amount{raw: c}
}

// RawFromInt constructs an Amount from a non-negative uint64 raw count.
func RawFromInt(raw uint64) Amount { return NewRaw(new(big.Int).SetUint64(raw)) }

// errParse is returned for malformed decimal strings.
var errParse = errors.New("nano: invalid XNO amount string")

// parseDecimal splits an integer/raw decimal string into the raw
// value, rejecting negatives and values with more than Decimals
// fractional digits. It never uses float64.
func parseDecimal(s string) (*big.Int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errParse
	}
	switch s[0] {
	case '+':
		s = s[1:]
		if s == "" || s[0] == '.' {
			return nil, errParse // just "+" or "+." is malformed
		}
	case '-':
		return nil, errParse // amount strings here are non-negative
	}
	if s == "" || s[0] == '.' {
		// A bare "." or a string that became empty after trimming the
		// leading plus sign.
		return nil, errParse
	}
	intPart, fracPart := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart = s[:dot]
		fracPart = s[dot+1:]
	}
	if strings.IndexByte(fracPart, '.') >= 0 {
		return nil, errParse
	}
	if intPart == "" {
		intPart = "0"
	}
	for _, c := range intPart {
		if c < '0' || c > '9' {
			return nil, errParse
		}
	}
	for _, c := range fracPart {
		if c < '0' || c > '9' {
			return nil, errParse
		}
	}
	if len(fracPart) > Decimals {
		return nil, errParse
	}
	// raw = intPart * 10^Decimals + fracPart padded to Decimals digits.
	raw := new(big.Int).SetUint64(0)
	intVal, ok := new(big.Int).SetString(intPart, 10)
	if !ok {
		return nil, errParse
	}
	raw.Mul(intVal, rawPerXNO)
	if fracPart != "" {
		fracVal, ok := new(big.Int).SetString(fracPart, 10)
		if !ok {
			return nil, errParse
		}
		// pad on the right to Decimals digits and add.
		pad := Decimals - len(fracPart)
		fracVal.Mul(fracVal, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(pad)), nil))
		raw.Add(raw, fracVal)
	}
	return raw, nil
}

// ParseXNO parses a human decimal string (e.g. "0.0005") into an
// Amount with exact 30-decimal resolution. It is lossless: no floating
// point is involved.
func ParseXNO(s string) (Amount, error) {
	raw, err := parseDecimal(s)
	if err != nil {
		return Amount{}, err
	}
	return NewRaw(raw), nil
}

// ParseXNOOrZero is ParseXNO with a zero fallback for callers that
// treat a malformed string as zero.
func ParseXNOOrZero(s string) Amount {
	a, err := ParseXNO(s)
	if err != nil {
		return zero
	}
	return a
}

// Format renders the amount as a trimmed human decimal string, dropping
// trailing zeros and the decimal point when the fraction is zero.
func (a Amount) Format() string {
	q, r := new(big.Int).QuoRem(a.raw, rawPerXNO, new(big.Int))
	intPart := q.String()
	if r.Sign() == 0 {
		return intPart
	}
	frac := r.String()
	for len(frac) < Decimals {
		frac = "0" + frac
	}
	frac = strings.TrimRight(frac, "0")
	return intPart + "." + frac
}

// Add returns the exact sum of a and b (raw units).
func (a Amount) Add(b Amount) Amount {
	return NewRaw(new(big.Int).Add(a.raw, b.raw))
}

// Sub returns the exact difference a - b (raw units); a negative raw
// result is represented as negative.
func (a Amount) Sub(b Amount) Amount {
	return Amount{raw: new(big.Int).Sub(a.raw, b.raw)}
}

// LessThan reports whether a is strictly less than b.
func (a Amount) LessThan(b Amount) bool { return a.raw.Cmp(b.raw) < 0 }

// Cmp compares a and b: -1, 0, or 1.
func (a Amount) Cmp(b Amount) int { return a.raw.Cmp(b.raw) }

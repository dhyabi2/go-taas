package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

// signatureVersion is the v1 signature scheme (AD5).
const signatureVersion = "v1"

// Sign computes the X-Go-Taas-Signature header value over the raw body
// with HMAC-SHA256 and the endpoint secret (AD5). The header is
// t=<ts>,v1=<sig>.
func Sign(body []byte, secret string, ts int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("t=%d,%s=%s", ts, signatureVersion, sig)
}

// SignNow signs the body with the current unix timestamp.
func SignNow(body []byte, secret string) string {
	return Sign(body, secret, time.Now().Unix())
}

// VerifySignature checks a t=<ts>,v1=<sig> header against the body and
// secret, and that the timestamp is within the given skew window (in
// seconds). It is used by tests and by consumers to validate a delivery.
func VerifySignature(header string, body []byte, secret string, maxSkewSeconds int64) bool {
	ts, _, ok := parseSignatureHeader(header)
	if !ok {
		return false
	}
	now := time.Now().Unix()
	if ts < now-maxSkewSeconds || ts > now+maxSkewSeconds {
		return false
	}
	expected := Sign(body, secret, ts)
	return hmac.Equal([]byte(expected), []byte(header))
}

// parseSignatureHeader parses "t=<ts>,v1=<sig>".
func parseSignatureHeader(header string) (int64, string, bool) {
	var ts int64
	var sig string
	parts := splitComma(header)
	for _, part := range parts {
		kv := splitEquals(part)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			v, err := strconv.ParseInt(kv[1], 10, 64)
			if err != nil {
				return 0, "", false
			}
			ts = v
		case signatureVersion:
			sig = kv[1]
		}
	}
	if ts == 0 || sig == "" {
		return 0, "", false
	}
	return ts, sig, true
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func splitEquals(s string) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}

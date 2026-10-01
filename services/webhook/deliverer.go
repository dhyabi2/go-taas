package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// signatureHeader is the HTTP header carrying the delivery signature
// (AD5).
const signatureHeader = "X-Go-Taas-Signature"

// webhookEvent is the canonical event envelope published on
// webhook.events (AD10) and delivered as the payload data.
type webhookEvent struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	CreatedAt int64           `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

// deliveryPayload is the signed payload sent to the endpoint (FR4.2):
// {"id","type","created_at","data"}.
type deliveryPayload struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	CreatedAt int64           `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

// BuildPayload builds the delivery payload envelope from an event.
func BuildPayload(ev *webhookEvent) ([]byte, error) {
	return json.Marshal(deliveryPayload{
		ID:        ev.ID,
		Type:      ev.Type,
		CreatedAt: ev.CreatedAt,
		Data:      ev.Data,
	})
}

// Deliverer posts signed payloads to webhook endpoints (AD11). It is
// shared by the delivery runner, TestWebhook and ResendWebhookDelivery.
type Deliverer struct {
	client  *http.Client
	timeout time.Duration
}

// NewDeliverer constructs a Deliverer with the given per-attempt timeout.
func NewDeliverer(timeout time.Duration) *Deliverer {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Deliverer{
		client:  &http.Client{Timeout: timeout},
		timeout: timeout,
	}
}

// DeliverResult is the outcome of one delivery attempt.
type DeliverResult struct {
	HTTPStatusCode int
	Err            error
}

// Deliver posts a signed payload to the webhook's URL. It decrypts the
// secret, builds the payload, signs it and POSTs it with the
// X-Go-Taas-Signature header (AD5). A non-2xx response or a network
// error is a failed attempt (FR4.3).
func (d *Deliverer) Deliver(ctx context.Context, wh *Webhook, secret string, ev *webhookEvent) DeliverResult {
	body, err := BuildPayload(ev)
	if err != nil {
		return DeliverResult{Err: fmt.Errorf("webhook: build payload: %w", err)}
	}
	ts := time.Now().Unix()
	sig := Sign(body, secret, ts)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wh.URL, bytes.NewReader(body))
	if err != nil {
		return DeliverResult{Err: fmt.Errorf("webhook: build request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(signatureHeader, sig)
	resp, err := d.client.Do(req)
	if err != nil {
		return DeliverResult{Err: fmt.Errorf("webhook: deliver: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return DeliverResult{
			HTTPStatusCode: resp.StatusCode,
			Err:            fmt.Errorf("webhook: non-2xx status %d", resp.StatusCode),
		}
	}
	return DeliverResult{HTTPStatusCode: resp.StatusCode}
}

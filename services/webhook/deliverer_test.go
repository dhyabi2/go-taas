package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AC8: a 2xx response marks delivered; a non-2xx or network error is a
// failed attempt.
func TestDelivererSuccess(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(signatureHeader)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewDeliverer(5 * time.Second)
	wh := &Webhook{URL: srv.URL}
	ev := &webhookEvent{ID: "evt-1", Type: EventDeploymentStatusChanged, CreatedAt: time.Now().Unix(), Data: json.RawMessage(`{}`)}
	res := d.Deliver(context.Background(), wh, "whsec_secret", ev)
	assert.NoError(t, res.Err)
	assert.Equal(t, http.StatusOK, res.HTTPStatusCode)
	assert.Contains(t, gotHeader, "t=")
	assert.Contains(t, gotHeader, "v1=")
}

func TestDelivererNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	d := NewDeliverer(5 * time.Second)
	wh := &Webhook{URL: srv.URL}
	ev := &webhookEvent{ID: "evt-1", Type: EventDeploymentStatusChanged, CreatedAt: time.Now().Unix(), Data: json.RawMessage(`{}`)}
	res := d.Deliver(context.Background(), wh, "whsec_secret", ev)
	assert.Error(t, res.Err)
	assert.Equal(t, http.StatusInternalServerError, res.HTTPStatusCode)
}

func TestDelivererNetworkError(t *testing.T) {
	// A closed listener produces a connection error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	url := srv.URL
	srv.Close()

	d := NewDeliverer(5 * time.Second)
	wh := &Webhook{URL: url}
	ev := &webhookEvent{ID: "evt-1", Type: EventDeploymentStatusChanged, CreatedAt: time.Now().Unix(), Data: json.RawMessage(`{}`)}
	res := d.Deliver(context.Background(), wh, "whsec_secret", ev)
	assert.Error(t, res.Err)
}

// BuildPayload produces the {"id","type","created_at","data"} envelope.
func TestBuildPayload(t *testing.T) {
	ev := &webhookEvent{ID: "evt-1", Type: EventDeploymentStatusChanged, CreatedAt: 1700000000, Data: json.RawMessage(`{"service_id":"s1"}`)}
	body, err := BuildPayload(ev)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out))
	assert.Equal(t, "evt-1", out["id"])
	assert.Equal(t, EventDeploymentStatusChanged, out["type"])
	assert.EqualValues(t, 1700000000, out["created_at"])
	assert.Equal(t, map[string]any{"service_id": "s1"}, out["data"])
}

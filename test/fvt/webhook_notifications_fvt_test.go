package fvt

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/grpcmiddleware"
	"github.com/go-taas/go-taas/pkg/mq"
	"github.com/go-taas/go-taas/pkg/server"
	webhookv1 "github.com/go-taas/go-taas/proto/taas/webhook/v1"
	"github.com/go-taas/go-taas/services/tenancy"
	"github.com/go-taas/go-taas/services/webhook"
)

// webhookEnv is the in-process stack for the webhook feature: the
// webhook service over one gRPC server with the gateway mux in front, a
// shared SQLite database, and the tenancy guard.
type webhookEnv struct {
	db     *gorm.DB
	gwSrv  *httptest.Server
	grpcLn net.Listener

	svc *webhook.Service
}

func newWebhookEnv(t *testing.T) *webhookEnv {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "webhook.db")
	db, err := gorm.Open(sqlite.Open(dbPath+"?_busy_timeout=10000&_txlock=immediate&_journal_mode=WAL"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, webhook.MigrateSchemaForFVT(db))
	require.NoError(t, tenancy.MigrateSchemaForFVT(db))
	for _, orgID := range []string{"org-fvt", "org-a", "org-b"} {
		require.NoError(t, db.Create(&tenancy.Organization{
			ID: orgID, DisplayName: orgID, State: tenancy.StateActive,
		}).Error)
		require.NoError(t, db.Create(&tenancy.OrgMember{
			OrganizationID: orgID, UserID: orgID, Role: tenancy.RoleMember, JoinedAt: time.Now().UTC(),
		}).Error)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcSrv := grpc.NewServer(grpc.ChainUnaryInterceptor(grpcmiddleware.UnaryServerInterceptor()))
	t.Cleanup(grpcSrv.Stop)

	svc := webhook.NewForFVT(db)
	svc.SetRoleGuard(tenancy.NewRoleGuard(db))
	webhookv1.RegisterWebhookServiceServer(grpcSrv, svc)
	go func() { _ = grpcSrv.Serve(ln) }()

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	mux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(server.FVTHeaderMatcher),
		runtime.WithMetadata(server.PathMetadataAnnotator),
	)
	require.NoError(t, webhookv1.RegisterWebhookServiceHandler(context.Background(), mux, conn))

	gwSrv := httptest.NewServer(mux)
	t.Cleanup(gwSrv.Close)

	return &webhookEnv{db: db, gwSrv: gwSrv, grpcLn: ln, svc: svc}
}

// call issues a JSON request against the gateway and decodes the body.
func (e *webhookEnv) call(t *testing.T, method, path string, body any, org string) (int, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.gwSrv.URL+path, rdr)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if org != "" {
		req.Header.Set("X-Organization-Id", org)
	}
	resp, err := e.gwSrv.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestFVTWebhookNotifications walks the webhook acceptance criteria
// through the gateway (AC1-AC8, AC16).
func TestFVTWebhookNotifications(t *testing.T) {
	env := newWebhookEnv(t)

	// A local endpoint that records the signature header and returns 200.
	var gotSignature string
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSignature = r.Header.Get("X-Go-Taas-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer endpoint.Close()

	// AC1: CreateWebhook returns webhook_id and a plaintext secret once.
	code, body := env.call(t, http.MethodPost, "/api/v1/admin/webhooks", map[string]any{
		"name": "ops", "url": endpoint.URL,
		"enabledEventTypes": []string{"deployment.status_changed"},
		"maxAttempts":       5, "backoffSeconds": 60,
	}, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	wh, _ := body["webhook"].(map[string]any)
	webhookID, _ := wh["webhookId"].(string)
	require.NotEmpty(t, webhookID)
	secret, _ := body["plaintextSecret"].(string)
	require.Contains(t, secret, "whsec_")

	// AC1: GetWebhook never returns the secret.
	code, body = env.call(t, http.MethodGet, "/api/v1/admin/webhooks/"+webhookID, nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	detail, _ := body["webhook"].(map[string]any)
	assert.Equal(t, "ops", detail["name"])
	assert.NotContains(t, body, "plaintextSecret")

	// AC2: invalid config -> 10702; bad event type -> 10705.
	_, body = env.call(t, http.MethodPost, "/api/v1/admin/webhooks", map[string]any{
		"name": "bad", "url": "not-a-url", "enabledEventTypes": []string{"deployment.status_changed"},
	}, "org-fvt")
	assert.EqualValues(t, float64(10702), body["code"])
	_, body = env.call(t, http.MethodPost, "/api/v1/admin/webhooks", map[string]any{
		"name": "bad", "url": "https://example.com", "enabledEventTypes": []string{"billing.invoice_created"},
	}, "org-fvt")
	assert.EqualValues(t, float64(10705), body["code"])

	// AC3: ListWebhooks returns the webhook with a delivery summary.
	code, body = env.call(t, http.MethodGet, "/api/v1/admin/webhooks", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	webhooks, _ := body["webhooks"].([]any)
	require.Len(t, webhooks, 1)

	// AC6: TestWebhook sends a webhook.ping and records a delivered
	// delivery.
	code, body = env.call(t, http.MethodPost, "/api/v1/admin/webhooks/"+webhookID+":test", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	delivery, _ := body["delivery"].(map[string]any)
	assert.Equal(t, "WEBHOOK_DELIVERY_STATUS_DELIVERED", delivery["status"])
	assert.Contains(t, gotSignature, "t=")
	assert.Contains(t, gotSignature, "v1=")

	// AC7: ListWebhookDeliveries returns the log.
	code, body = env.call(t, http.MethodGet, "/api/v1/admin/webhooks/"+webhookID+"/deliveries", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	deliveries, _ := body["deliveries"].([]any)
	require.Len(t, deliveries, 1)
	deliveryID, _ := deliveries[0].(map[string]any)["deliveryId"].(string)

	// AC7: ResendWebhookDelivery resends a delivered delivery.
	code, body = env.call(t, http.MethodPost,
		"/api/v1/admin/webhooks/"+webhookID+"/deliveries/"+deliveryID+":resend", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	assert.Equal(t, "WEBHOOK_DELIVERY_STATUS_DELIVERED", body["delivery"].(map[string]any)["status"])

	// AC7: unknown delivery -> 10704.
	_, body = env.call(t, http.MethodPost,
		"/api/v1/admin/webhooks/"+webhookID+"/deliveries/00000000-0000-0000-0000-000000000000:resend", nil, "org-fvt")
	assert.EqualValues(t, float64(10704), body["code"])

	// AC4: SetWebhookEnabled pauses; DeleteWebhook removes.
	code, body = env.call(t, http.MethodPost,
		"/api/v1/admin/webhooks/"+webhookID+":set-enabled", map[string]any{"enabled": false}, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	assert.Equal(t, false, body["webhook"].(map[string]any)["enabled"])

	// AC5: RollWebhookSecret returns a new plaintext once.
	code, body = env.call(t, http.MethodPost,
		"/api/v1/admin/webhooks/"+webhookID+":roll-secret", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	newSecret, _ := body["plaintextSecret"].(string)
	require.NotEmpty(t, newSecret)
	assert.NotEqual(t, secret, newSecret)

	// AC4: DeleteWebhook removes the webhook.
	code, body = env.call(t, http.MethodDelete, "/api/v1/admin/webhooks/"+webhookID, nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	_, body = env.call(t, http.MethodGet, "/api/v1/admin/webhooks/"+webhookID, nil, "org-fvt")
	assert.EqualValues(t, float64(10701), body["code"])
}

// TestFVTWebhookEventDelivery publishes an event on webhook.events and
// asserts a signed delivery is produced (AC8).
func TestFVTWebhookEventDelivery(t *testing.T) {
	env := newWebhookEnv(t)

	var gotSignature string
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSignature = r.Header.Get("X-Go-Taas-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer endpoint.Close()

	// Create a webhook subscribed to deployment.status_changed with a
	// short backoff so the delivery is due quickly.
	code, body := env.call(t, http.MethodPost, "/api/v1/admin/webhooks", map[string]any{
		"name": "ops", "url": endpoint.URL,
		"enabledEventTypes": []string{"deployment.status_changed"},
		"backoffSeconds": 1,
	}, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	webhookID := body["webhook"].(map[string]any)["webhookId"].(string)

	// Publish a deployment.status_changed event on webhook.events.
	client := mq.NewFake()
	webhook.PublishEvent(context.Background(), client, "org-fvt",
		webhook.EventDeploymentStatusChanged, "deploy-svc-1",
		map[string]any{"service_id": "svc-1", "state": "running"})

	// The event consumer routes it to the webhook by inserting a pending
	// delivery.
	repo := webhook.NewRepository(env.db)
	consumer := webhook.NewEventConsumer(client, repo, 1)
	// Re-publish through the consumer's handle to route it.
	ev := webhookEventForTest("deploy-svc-1", webhook.EventDeploymentStatusChanged)
	bodyBytes, err := json.Marshal(ev)
	require.NoError(t, err)
	require.NoError(t, consumer.HandleForTest(context.Background(), mq.Message{
		Subject: "webhook.events", Body: bodyBytes,
		Headers: map[string]string{"organization_id": "org-fvt"},
	}))

	// Force the pending delivery to be due so the runner picks it up.
	deliveries, _, err := repo.ListDeliveries(context.Background(), webhook.DeliveryFilter{
		OrganizationID: "org-fvt", WebhookID: webhookID, Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	past := time.Now().UTC().Add(-time.Minute)
	deliveries[0].NextAttemptAt = &past
	require.NoError(t, repo.UpdateDelivery(context.Background(), deliveries[0]))

	// The delivery runner delivers it.
	runner := webhook.NewDeliveryRunner(repo, webhook.NewDeliverer(5*time.Second), "dev-webhook-key-fvt", time.Second, 1)
	runner.DeliverOnce(context.Background())

	// The delivery is delivered with a signature.
	code, body = env.call(t, http.MethodGet, "/api/v1/admin/webhooks/"+webhookID+"/deliveries", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	deliveriesResp, _ := body["deliveries"].([]any)
	require.Len(t, deliveriesResp, 1)
	assert.Equal(t, "WEBHOOK_DELIVERY_STATUS_DELIVERED", deliveriesResp[0].(map[string]any)["status"])
	assert.Contains(t, gotSignature, "v1=")
}

// webhookEventForTest builds a webhook event envelope.
func webhookEventForTest(id, eventType string) map[string]any {
	return map[string]any{
		"id": id, "type": eventType, "created_at": time.Now().Unix(), "data": map[string]any{},
	}
}

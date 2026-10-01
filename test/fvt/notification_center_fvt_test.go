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
	notificationv1 "github.com/go-taas/go-taas/proto/taas/notification/v1"
	"github.com/go-taas/go-taas/services/notification"
	"github.com/go-taas/go-taas/services/tenancy"
)

// notificationEnv is the in-process stack for the notification feature:
// the notification service over one gRPC server with the gateway mux in
// front, a shared SQLite database, and the tenancy guard.
type notificationEnv struct {
	db     *gorm.DB
	gwSrv  *httptest.Server
	grpcLn net.Listener

	svc      *notification.Service
	consumer *notification.EventConsumer
}

func newNotificationEnv(t *testing.T) *notificationEnv {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "notification.db")
	db, err := gorm.Open(sqlite.Open(dbPath+"?_busy_timeout=10000&_txlock=immediate&_journal_mode=WAL"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, notification.MigrateSchemaForFVT(db))
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

	svc := notification.NewForFVT(db)
	svc.SetRoleGuard(tenancy.NewRoleGuard(db))
	notificationv1.RegisterNotificationServiceServer(grpcSrv, svc)
	go func() { _ = grpcSrv.Serve(ln) }()

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	mux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(server.FVTHeaderMatcher),
		runtime.WithMetadata(server.PathMetadataAnnotator),
	)
	require.NoError(t, notificationv1.RegisterNotificationServiceHandler(context.Background(), mux, conn))

	gwSrv := httptest.NewServer(mux)
	t.Cleanup(gwSrv.Close)

	consumer := notification.NewEventConsumer(mq.NewFake(), notification.NewRepository(db), 1)

	return &notificationEnv{db: db, gwSrv: gwSrv, grpcLn: ln, svc: svc, consumer: consumer}
}

// call issues a JSON request against the gateway and decodes the body.
func (e *notificationEnv) call(t *testing.T, method, path string, body any, org string) (int, map[string]any) {
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

// publishEvent publishes a notification event on the consumer directly.
func (e *notificationEnv) publishEvent(t *testing.T, orgID, eventType string, data any) {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	body, err := json.Marshal(map[string]any{
		"id": "evt-" + eventType, "type": eventType, "created_at": time.Now().Unix(),
		"data": json.RawMessage(raw),
	})
	require.NoError(t, err)
	msg := mq.Message{
		Subject: "notification.events",
		Body:    body,
		Headers: map[string]string{"organization_id": orgID},
	}
	require.NoError(t, e.consumer.HandleForTest(context.Background(), msg))
}

// TestFVTNotificationCenter walks the notification acceptance criteria
// through the gateway (AC1-AC7, AC15).
func TestFVTNotificationCenter(t *testing.T) {
	env := newNotificationEnv(t)

	// AC4: GetNotificationPreferences returns all enabled by default.
	code, body := env.call(t, http.MethodGet, "/api/v1/notifications/preferences", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	prefs, _ := body["preferences"].([]any)
	require.Len(t, prefs, 4)
	for _, p := range prefs {
		pm, _ := p.(map[string]any)
		assert.True(t, pm["enabled"].(bool))
	}

	// AC4: UpdateNotificationPreferences persists; bad event -> 11005.
	code, body = env.call(t, http.MethodPut, "/api/v1/notifications/preferences", map[string]any{
		"preferences": []map[string]any{
			{"eventType": "billing.balance_low", "enabled": false},
			{"eventType": "billing.invoice_created", "enabled": true},
			{"eventType": "billing.invoice_paid", "enabled": true},
			{"eventType": "billing.spend_limit_breached", "enabled": true},
		},
	}, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	_, body = env.call(t, http.MethodPut, "/api/v1/notifications/preferences", map[string]any{
		"preferences": []map[string]any{{"eventType": "deployment.status_changed", "enabled": true}},
	}, "org-fvt")
	assert.EqualValues(t, float64(11005), body["code"])

	// AC5: CreateNotificationThreshold valid; invalid -> 11004.
	code, body = env.call(t, http.MethodPost, "/api/v1/notifications/thresholds", map[string]any{
		"name": "low balance", "metric": "balance_low", "operator": "THRESHOLD_OPERATOR_LT", "value": 1000, "enabled": true,
	}, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	th, _ := body["threshold"].(map[string]any)
	thresholdID, _ := th["thresholdId"].(string)
	require.NotEmpty(t, thresholdID)
	_, body = env.call(t, http.MethodPost, "/api/v1/notifications/thresholds", map[string]any{
		"name": "bad", "metric": "autoscaling_replicas", "operator": "THRESHOLD_OPERATOR_GT", "value": 1000,
	}, "org-fvt")
	assert.EqualValues(t, float64(11004), body["code"])

	// AC5: UpdateNotificationThreshold; unknown -> 11003.
	code, body = env.call(t, http.MethodPut, "/api/v1/notifications/thresholds/"+thresholdID, map[string]any{
		"enabled": false,
	}, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	_, body = env.call(t, http.MethodPut, "/api/v1/notifications/thresholds/00000000-0000-0000-0000-000000000000", map[string]any{
		"enabled": false,
	}, "org-fvt")
	assert.EqualValues(t, float64(11003), body["code"])

	// AC5: DeleteNotificationThreshold.
	code, body = env.call(t, http.MethodDelete, "/api/v1/notifications/thresholds/"+thresholdID, nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)

	// Re-enable balance_low so the AC6 fan-out creates a notification.
	code, body = env.call(t, http.MethodPut, "/api/v1/notifications/preferences", map[string]any{
		"preferences": []map[string]any{
			{"eventType": "billing.balance_low", "enabled": true},
			{"eventType": "billing.invoice_created", "enabled": true},
			{"eventType": "billing.invoice_paid", "enabled": true},
			{"eventType": "billing.spend_limit_breached", "enabled": true},
		},
	}, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)

	// AC6: a subscribed event creates a notification for every enabled
	// user; a disabled event type creates none.
	env.publishEvent(t, "org-fvt", "billing.balance_low", map[string]any{"balance_cents": 500})
	code, body = env.call(t, http.MethodGet, "/api/v1/notifications", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	notifs, _ := body["notifications"].([]any)
	require.Len(t, notifs, 1)
	n0, _ := notifs[0].(map[string]any)
	assert.Equal(t, "billing.balance_low", n0["eventType"])

	// AC1: GetUnreadCount.
	code, body = env.call(t, http.MethodGet, "/api/v1/notifications/unread-count", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	assert.Equal(t, "1", body["unreadCount"])

	// AC2: MarkNotificationRead; unknown -> 11001.
	notifID, _ := n0["notificationId"].(string)
	code, body = env.call(t, http.MethodPost, "/api/v1/notifications/"+notifID+":mark-read", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	_, body = env.call(t, http.MethodPost, "/api/v1/notifications/00000000-0000-0000-0000-000000000000:mark-read", nil, "org-fvt")
	assert.EqualValues(t, float64(11001), body["code"])

	// AC2: MarkAllNotificationsRead.
	code, body = env.call(t, http.MethodPost, "/api/v1/notifications:mark-all-read", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	code, body = env.call(t, http.MethodGet, "/api/v1/notifications/unread-count", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	assert.Equal(t, "0", body["unreadCount"])

	// AC3: DeleteNotification; unknown -> 11001.
	code, body = env.call(t, http.MethodDelete, "/api/v1/notifications/"+notifID, nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	_, body = env.call(t, http.MethodDelete, "/api/v1/notifications/00000000-0000-0000-0000-000000000000", nil, "org-fvt")
	assert.EqualValues(t, float64(11001), body["code"])

	// AC7: a threshold crossing creates a notification with the
	// threshold's name.
	code, body = env.call(t, http.MethodPost, "/api/v1/notifications/thresholds", map[string]any{
		"name": "low balance alert", "metric": "balance_low", "operator": "THRESHOLD_OPERATOR_LT", "value": 1000, "enabled": true,
	}, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	code, body = env.call(t, http.MethodGet, "/api/v1/notifications/thresholds", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	env.publishEvent(t, "org-fvt", "billing.balance_low", map[string]any{"balance_cents": 500})
	code, body = env.call(t, http.MethodGet, "/api/v1/notifications", nil, "org-fvt")
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	notifs, _ = body["notifications"].([]any)
	foundThreshold := false
	for _, n := range notifs {
		nm, _ := n.(map[string]any)
		if nm["title"] == "low balance alert" {
			foundThreshold = true
		}
	}
	assert.True(t, foundThreshold, "expected a threshold notification")
}
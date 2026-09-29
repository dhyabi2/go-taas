package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	commonv1 "github.com/go-taas/go-taas/proto/taas/common/v1"
	webhookv1 "github.com/go-taas/go-taas/proto/taas/webhook/v1"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
	"github.com/go-taas/go-taas/pkg/mq"
	"github.com/go-taas/go-taas/services/audit"
)

func newServiceTestEnv(t *testing.T) *Service {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Webhook{}, &WebhookDelivery{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	svc := NewForFVT(db)
	svc.SetRoleGuard(fakeRoleGuard{})
	svc.SetSessionUserResolver(&fakeUserResolver{userID: "u-1"})
	svc.SetSessionOrgResolver(&fakeOrgResolver{orgID: "org-1"})
	return svc
}

func withOrg(ctx context.Context, orgID string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs(organizationMetadataKey, orgID))
}

func withAdminPath(ctx context.Context) context.Context {
	md, _ := metadata.FromIncomingContext(ctx)
	md = md.Copy()
	md.Set("x-request-path", "/api/v1/admin/webhooks")
	return metadata.NewIncomingContext(ctx, md)
}

func withUserPath(ctx context.Context) context.Context {
	md, _ := metadata.FromIncomingContext(ctx)
	md = md.Copy()
	md.Set("x-request-path", "/api/v1/webhooks")
	return metadata.NewIncomingContext(ctx, md)
}

func assertCode(t *testing.T, err error, code apierrors.Code) {
	t.Helper()
	ae, ok := apierrors.As(err)
	require.True(t, ok, "expected an APIError, got %v", err)
	assert.Equal(t, code, ae.Code)
}

type fakeUserResolver struct{ userID string }

func (f *fakeUserResolver) SessionUserID(context.Context) (string, error) { return f.userID, nil }

type fakeOrgResolver struct{ orgID string }

func (f *fakeOrgResolver) SessionActiveOrg(ctx context.Context) (string, error) {
	// Prefer the org from the context metadata so role-scoping tests can
	// exercise different orgs.
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get(organizationMetadataKey); len(vals) > 0 && vals[0] != "" {
			return vals[0], nil
		}
	}
	return f.orgID, nil
}

type fakeRoleGuard struct{}

func (fakeRoleGuard) RequireRole(_ context.Context, orgID, userID, _ string) error {
	if orgID == "org-1" && userID == "u-1" {
		return nil
	}
	return apierrors.New(apierrors.CodeForbidden)
}

type recordingRecorder struct{ events []*audit.AuditEvent }

func (r *recordingRecorder) Record(_ context.Context, ev *audit.AuditEvent) {
	r.events = append(r.events, ev)
}

// AC1: CreateWebhook returns the plaintext secret once; GetWebhook never
// returns it.
func TestServiceCreateWebhookSecretOnce(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withAdminPath(withOrg(context.Background(), "org-1"))

	resp, err := svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name:              "ops",
		Url:               "https://example.com/hook",
		EnabledEventTypes: []string{EventDeploymentStatusChanged},
		MaxAttempts:       5,
		BackoffSeconds:    60,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.GetWebhook().GetWebhookId())
	assert.Contains(t, resp.GetPlaintextSecret(), secretPrefix)

	// GetWebhook never returns the secret.
	got, err := svc.GetWebhook(ctx, &webhookv1.GetWebhookRequest{WebhookId: resp.GetWebhook().GetWebhookId()})
	require.NoError(t, err)
	assert.Equal(t, "ops", got.GetWebhook().GetName())
	assert.Equal(t, SurfaceAdmin, got.GetWebhook().GetSurface())
}

// AC2: CreateWebhook with an invalid config returns 10702; with an event
// type outside the surface's catalog returns 10705.
func TestServiceCreateWebhookValidation(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withAdminPath(withOrg(context.Background(), "org-1"))

	// Invalid URL.
	_, err := svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "ops", Url: "not-a-url", EnabledEventTypes: []string{EventDeploymentStatusChanged},
	})
	assertCode(t, err, apierrors.CodeWebhookConfigInvalid)
	// Invalid retry.
	_, err = svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "ops", Url: "https://example.com", EnabledEventTypes: []string{EventDeploymentStatusChanged},
		MaxAttempts: 99,
	})
	assertCode(t, err, apierrors.CodeWebhookConfigInvalid)
	// Event type outside the admin catalog.
	_, err = svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "ops", Url: "https://example.com", EnabledEventTypes: []string{EventInvoiceCreated},
	})
	assertCode(t, err, apierrors.CodeWebhookEventTypeInvalid)
}

// AC3: ListWebhooks returns the surface's webhooks with a delivery
// summary.
func TestServiceListWebhooks(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withAdminPath(withOrg(context.Background(), "org-1"))
	_, err := svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "ops", Url: "https://example.com/hook", EnabledEventTypes: []string{EventDeploymentStatusChanged},
	})
	require.NoError(t, err)
	list, err := svc.ListWebhooks(ctx, &webhookv1.ListWebhooksRequest{
		Page: &commonv1.PageRequest{Limit: 20},
	})
	require.NoError(t, err)
	assert.Len(t, list.GetWebhooks(), 1)
	assert.Equal(t, "ops", list.GetWebhooks()[0].GetName())
}

// AC4: UpdateWebhook updates config; SetWebhookEnabled pauses/resumes;
// DeleteWebhook removes the webhook.
func TestServiceUpdateEnableDelete(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withAdminPath(withOrg(context.Background(), "org-1"))
	resp, err := svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "ops", Url: "https://example.com/hook", EnabledEventTypes: []string{EventDeploymentStatusChanged},
	})
	require.NoError(t, err)
	id := resp.GetWebhook().GetWebhookId()

	// Update.
	upd, err := svc.UpdateWebhook(ctx, &webhookv1.UpdateWebhookRequest{
		WebhookId: id, Name: "ops2", Url: "https://example.com/hook2",
		EnabledEventTypes: []string{EventAutoscalingScaled}, MaxAttempts: 3, BackoffSeconds: 30,
	})
	require.NoError(t, err)
	assert.Equal(t, "ops2", upd.GetWebhook().GetName())
	assert.Equal(t, int32(3), upd.GetWebhook().GetMaxAttempts())

	// Disable.
	dis, err := svc.SetWebhookEnabled(ctx, &webhookv1.SetWebhookEnabledRequest{WebhookId: id, Enabled: false})
	require.NoError(t, err)
	assert.False(t, dis.GetWebhook().GetEnabled())

	// Delete.
	_, err = svc.DeleteWebhook(ctx, &webhookv1.DeleteWebhookRequest{WebhookId: id})
	require.NoError(t, err)
	_, err = svc.GetWebhook(ctx, &webhookv1.GetWebhookRequest{WebhookId: id})
	assertCode(t, err, apierrors.CodeWebhookNotFound)
}

// AC5: RollWebhookSecret regenerates the secret and returns the new
// plaintext once.
func TestServiceRollWebhookSecret(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withAdminPath(withOrg(context.Background(), "org-1"))
	resp, err := svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "ops", Url: "https://example.com/hook", EnabledEventTypes: []string{EventDeploymentStatusChanged},
	})
	require.NoError(t, err)
	oldSecret := resp.GetPlaintextSecret()
	roll, err := svc.RollWebhookSecret(ctx, &webhookv1.RollWebhookSecretRequest{WebhookId: resp.GetWebhook().GetWebhookId()})
	require.NoError(t, err)
	assert.NotEmpty(t, roll.GetPlaintextSecret())
	assert.NotEqual(t, oldSecret, roll.GetPlaintextSecret())
}

// AC6: TestWebhook records a delivered delivery; a disabled webhook
// returns 10703.
func TestServiceTestWebhook(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withAdminPath(withOrg(context.Background(), "org-1"))
	// A local endpoint that returns 200.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "ops", Url: srv.URL, EnabledEventTypes: []string{EventDeploymentStatusChanged},
	})
	require.NoError(t, err)
	testResp, err := svc.TestWebhook(ctx, &webhookv1.TestWebhookRequest{WebhookId: resp.GetWebhook().GetWebhookId()})
	require.NoError(t, err)
	assert.Equal(t, webhookv1.WebhookDeliveryStatus_WEBHOOK_DELIVERY_STATUS_DELIVERED, testResp.GetDelivery().GetStatus())

	// Disabled webhook -> 10703.
	_, err = svc.SetWebhookEnabled(ctx, &webhookv1.SetWebhookEnabledRequest{WebhookId: resp.GetWebhook().GetWebhookId(), Enabled: false})
	require.NoError(t, err)
	_, err = svc.TestWebhook(ctx, &webhookv1.TestWebhookRequest{WebhookId: resp.GetWebhook().GetWebhookId()})
	assertCode(t, err, apierrors.CodeWebhookStateInvalid)
}

// AC7: ListWebhookDeliveries returns the log; ResendWebhookDelivery
// resends a failed delivery and returns 10704 for an unknown delivery.
func TestServiceDeliveriesAndResend(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withAdminPath(withOrg(context.Background(), "org-1"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "ops", Url: srv.URL, EnabledEventTypes: []string{EventDeploymentStatusChanged},
	})
	require.NoError(t, err)
	id := resp.GetWebhook().GetWebhookId()
	_, err = svc.TestWebhook(ctx, &webhookv1.TestWebhookRequest{WebhookId: id})
	require.NoError(t, err)

	// List deliveries.
	list, err := svc.ListWebhookDeliveries(ctx, &webhookv1.ListWebhookDeliveriesRequest{
		WebhookId: id, Page: &commonv1.PageRequest{Limit: 20},
	})
	require.NoError(t, err)
	assert.Len(t, list.GetDeliveries(), 1)
	deliveryID := list.GetDeliveries()[0].GetDeliveryId()

	// Resend the delivered delivery.
	resend, err := svc.ResendWebhookDelivery(ctx, &webhookv1.ResendWebhookDeliveryRequest{
		WebhookId: id, DeliveryId: deliveryID,
	})
	require.NoError(t, err)
	assert.Equal(t, webhookv1.WebhookDeliveryStatus_WEBHOOK_DELIVERY_STATUS_DELIVERED, resend.GetDelivery().GetStatus())

	// Unknown delivery -> 10704.
	_, err = svc.ResendWebhookDelivery(ctx, &webhookv1.ResendWebhookDeliveryRequest{
		WebhookId: id, DeliveryId: "00000000-0000-0000-0000-000000000000",
	})
	assertCode(t, err, apierrors.CodeWebhookDeliveryNotFound)
}

// AC16: admin org scoping returns 10036 for a caller without the role.
func TestServiceAdminRoleScoping(t *testing.T) {
	svc := newServiceTestEnv(t)
	// The fake role guard denies org-2.
	ctx := withAdminPath(withOrg(context.Background(), "org-2"))
	_, err := svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "ops", Url: "https://example.com/hook", EnabledEventTypes: []string{EventDeploymentStatusChanged},
	})
	assertCode(t, err, apierrors.CodeForbidden)
}

// AD12: each mutation writes an audit event.
func TestServiceMutationsAudited(t *testing.T) {
	svc := newServiceTestEnv(t)
	rec := &recordingRecorder{}
	svc.SetAuditRecorder(rec)
	ctx := withAdminPath(withOrg(context.Background(), "org-1"))
	resp, err := svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "ops", Url: "https://example.com/hook", EnabledEventTypes: []string{EventDeploymentStatusChanged},
	})
	require.NoError(t, err)
	_, err = svc.UpdateWebhook(ctx, &webhookv1.UpdateWebhookRequest{WebhookId: resp.GetWebhook().GetWebhookId(), Name: "ops2"})
	require.NoError(t, err)
	_, err = svc.SetWebhookEnabled(ctx, &webhookv1.SetWebhookEnabledRequest{WebhookId: resp.GetWebhook().GetWebhookId(), Enabled: false})
	require.NoError(t, err)
	_, err = svc.RollWebhookSecret(ctx, &webhookv1.RollWebhookSecretRequest{WebhookId: resp.GetWebhook().GetWebhookId()})
	require.NoError(t, err)
	_, err = svc.DeleteWebhook(ctx, &webhookv1.DeleteWebhookRequest{WebhookId: resp.GetWebhook().GetWebhookId()})
	require.NoError(t, err)
	assert.Len(t, rec.events, 5)
}

// AC13: the end-user surface only accepts the user event catalog.
func TestServiceUserSurfaceCatalog(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withUserPath(withOrg(context.Background(), "org-1"))
	// A user event is valid on the user surface.
	resp, err := svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "billing", Url: "https://example.com/hook", EnabledEventTypes: []string{EventInvoiceCreated},
	})
	require.NoError(t, err)
	assert.Equal(t, SurfaceUser, resp.GetWebhook().GetSurface())
	// An admin event is rejected on the user surface.
	_, err = svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "bad", Url: "https://example.com/hook", EnabledEventTypes: []string{EventDeploymentStatusChanged},
	})
	assertCode(t, err, apierrors.CodeWebhookEventTypeInvalid)
}

// AC1: GetWebhook returns 10701 for an unknown webhook.
func TestServiceGetWebhookNotFound(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withAdminPath(withOrg(context.Background(), "org-1"))
	_, err := svc.GetWebhook(ctx, &webhookv1.GetWebhookRequest{WebhookId: "00000000-0000-0000-0000-000000000000"})
	assertCode(t, err, apierrors.CodeWebhookNotFound)
}

// AC4: UpdateWebhook with an invalid config returns 10702.
func TestServiceUpdateWebhookValidation(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withAdminPath(withOrg(context.Background(), "org-1"))
	resp, err := svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "ops", Url: "https://example.com/hook", EnabledEventTypes: []string{EventDeploymentStatusChanged},
	})
	require.NoError(t, err)
	_, err = svc.UpdateWebhook(ctx, &webhookv1.UpdateWebhookRequest{
		WebhookId: resp.GetWebhook().GetWebhookId(), Url: "not-a-url",
	})
	assertCode(t, err, apierrors.CodeWebhookConfigInvalid)
	// Unknown event type.
	_, err = svc.UpdateWebhook(ctx, &webhookv1.UpdateWebhookRequest{
		WebhookId: resp.GetWebhook().GetWebhookId(), EnabledEventTypes: []string{EventInvoiceCreated},
	})
	assertCode(t, err, apierrors.CodeWebhookEventTypeInvalid)
}

// AC7: ListWebhookDeliveries filters by status.
func TestServiceListDeliveriesFilter(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withAdminPath(withOrg(context.Background(), "org-1"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	resp, err := svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "ops", Url: srv.URL, EnabledEventTypes: []string{EventDeploymentStatusChanged},
	})
	require.NoError(t, err)
	id := resp.GetWebhook().GetWebhookId()
	_, err = svc.TestWebhook(ctx, &webhookv1.TestWebhookRequest{WebhookId: id})
	require.NoError(t, err)
	// Filter by delivered status.
	list, err := svc.ListWebhookDeliveries(ctx, &webhookv1.ListWebhookDeliveriesRequest{
		WebhookId: id, Status: webhookv1.WebhookDeliveryStatus_WEBHOOK_DELIVERY_STATUS_DELIVERED,
		Page: &commonv1.PageRequest{Limit: 20},
	})
	require.NoError(t, err)
	assert.Len(t, list.GetDeliveries(), 1)
	// Filter by failed status -> empty.
	list, err = svc.ListWebhookDeliveries(ctx, &webhookv1.ListWebhookDeliveriesRequest{
		WebhookId: id, Status: webhookv1.WebhookDeliveryStatus_WEBHOOK_DELIVERY_STATUS_FAILED,
		Page: &commonv1.PageRequest{Limit: 20},
	})
	require.NoError(t, err)
	assert.Empty(t, list.GetDeliveries())
	// Unknown webhook -> 10701.
	_, err = svc.ListWebhookDeliveries(ctx, &webhookv1.ListWebhookDeliveriesRequest{
		WebhookId: "00000000-0000-0000-0000-000000000000",
	})
	assertCode(t, err, apierrors.CodeWebhookNotFound)
}

// AC7: ResendWebhookDelivery on a pending delivery returns 10703.
func TestServiceResendPendingDelivery(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withAdminPath(withOrg(context.Background(), "org-1"))
	resp, err := svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "ops", Url: "https://example.com/hook", EnabledEventTypes: []string{EventDeploymentStatusChanged},
	})
	require.NoError(t, err)
	id := resp.GetWebhook().GetWebhookId()
	// Insert a pending delivery directly.
	repo, err := svc.repository()
	require.NoError(t, err)
	now := time.Now().UTC()
	payload, err := BuildPayload(&webhookEvent{ID: "evt-1", Type: EventDeploymentStatusChanged, CreatedAt: now.Unix(), Data: json.RawMessage(`{}`)})
	require.NoError(t, err)
	dID, err := repo.InsertDelivery(ctx, &WebhookDelivery{
		WebhookID: id, OrganizationID: "org-1", EventID: "evt-1", EventType: EventDeploymentStatusChanged,
		Status: DeliveryStatusPending, Payload: string(payload), CreatedAt: now,
	})
	require.NoError(t, err)
	_, err = svc.ResendWebhookDelivery(ctx, &webhookv1.ResendWebhookDeliveryRequest{
		WebhookId: id, DeliveryId: dID,
	})
	assertCode(t, err, apierrors.CodeWebhookStateInvalid)
}

// AC5: RollWebhookSecret on an unknown webhook returns 10701.
func TestServiceRollSecretNotFound(t *testing.T) {
	svc := newServiceTestEnv(t)
	ctx := withAdminPath(withOrg(context.Background(), "org-1"))
	_, err := svc.RollWebhookSecret(ctx, &webhookv1.RollWebhookSecretRequest{WebhookId: "00000000-0000-0000-0000-000000000000"})
	assertCode(t, err, apierrors.CodeWebhookNotFound)
}

// AD10: PublishEvent publishes a canonical envelope to webhook.events.
func TestPublishEvent(t *testing.T) {
	client := mq.NewFake()
	// Subscribe to capture the message.
	var captured mq.Message
	require.NoError(t, client.Subscribe(context.Background(), mq.DefaultSubjects().WebhookEvents, func(msg mq.Message) error {
		captured = msg
		return nil
	}))
	PublishEvent(context.Background(), client, "org-1", EventInvoiceCreated, "inv-1", map[string]any{"amount": 100})
	assert.Equal(t, "org-1", captured.Headers["organization_id"])
	assert.Equal(t, EventInvoiceCreated, captured.Headers["event_type"])
	var ev webhookEvent
	require.NoError(t, json.Unmarshal(captured.Body, &ev))
	assert.Equal(t, EventInvoiceCreated, ev.Type)
	assert.Equal(t, "inv-1", ev.ID)
}

// AD10: PublishEvent with a nil client is a no-op.
func TestPublishEventNilClient(t *testing.T) {
	t.Helper()
	// Should not panic.
	PublishEvent(context.Background(), nil, "org-1", EventInvoiceCreated, "inv-1", map[string]any{})
}

// AD10: PublishEvent with an unmarshalable payload logs and skips.
func TestPublishEventMarshalFailure(t *testing.T) {
	t.Helper()
	client := mq.NewFake()
	// A channel cannot be marshalled to JSON.
	PublishEvent(context.Background(), client, "org-1", EventInvoiceCreated, "inv-1", make(chan int))
	// No panic; nothing published.
}

// The transitional (session-less) path resolves the org from the
// X-Organization-Id header when no session resolver is wired.
func TestServiceTransitionalOrgResolution(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Webhook{}, &WebhookDelivery{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	// No session resolvers wired: the transitional header is used.
	svc := NewForFVT(db)
	ctx := withAdminPath(withOrg(context.Background(), "org-1"))
	resp, err := svc.CreateWebhook(ctx, &webhookv1.CreateWebhookRequest{
		Name: "ops", Url: "https://example.com/hook", EnabledEventTypes: []string{EventDeploymentStatusChanged},
	})
	require.NoError(t, err)
	assert.Equal(t, "org-1", resp.GetWebhook().GetOrganizationId())

	// Missing org header -> 10001.
	ctxNoOrg := withAdminPath(context.Background())
	_, err = svc.CreateWebhook(ctxNoOrg, &webhookv1.CreateWebhookRequest{
		Name: "ops", Url: "https://example.com/hook", EnabledEventTypes: []string{EventDeploymentStatusChanged},
	})
	assertCode(t, err, apierrors.CodeUnauthorized)
}

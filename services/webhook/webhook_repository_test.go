package webhook

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

func newWebhookTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Webhook{}, &WebhookDelivery{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return db
}

func newWebhookTestRepo(t *testing.T) *Repository {
	t.Helper()
	return NewRepository(newWebhookTestDB(t))
}

func mustInsertWebhook(ctx context.Context, t *testing.T, repo *Repository, wh *Webhook) string {
	t.Helper()
	id, err := repo.InsertWebhook(ctx, wh)
	require.NoError(t, err)
	return id
}

// AC1: InsertWebhook round-trips a webhook with its encrypted secret.
func TestRepositoryInsertWebhook(t *testing.T) {
	repo := newWebhookTestRepo(t)
	ctx := context.Background()
	wh := &Webhook{
		OrganizationID:   "org-1",
		Surface:          SurfaceAdmin,
		Name:             "ops",
		URL:              "https://example.com/hook",
		Enabled:          true,
		MaxAttempts:      5,
		BackoffSeconds:   60,
		SecretCiphertext: []byte("cipher"),
	}
	require.NoError(t, wh.SetEventTypes([]string{EventDeploymentStatusChanged}))
	id := mustInsertWebhook(ctx, t, repo, wh)
	assert.NotEmpty(t, id)

	got, err := repo.FindWebhookByID(ctx, "org-1", id)
	require.NoError(t, err)
	assert.Equal(t, "ops", got.Name)
	assert.Equal(t, "https://example.com/hook", got.URL)
	assert.Equal(t, []byte("cipher"), got.SecretCiphertext)
	types, err := got.EventTypes()
	require.NoError(t, err)
	assert.Equal(t, []string{EventDeploymentStatusChanged}, types)
}

// AC1: FindWebhookByID returns 10701 for an unknown or malformed id.
func TestRepositoryFindWebhookNotFound(t *testing.T) {
	repo := newWebhookTestRepo(t)
	ctx := context.Background()
	_, err := repo.FindWebhookByID(ctx, "org-1", "00000000-0000-0000-0000-000000000000")
	assert.Equal(t, apierrors.CodeWebhookNotFound, apierrors.CodeOf(err))
	_, err = repo.FindWebhookByID(ctx, "org-1", "not-a-uuid")
	assert.Equal(t, apierrors.CodeWebhookNotFound, apierrors.CodeOf(err))
}

// AC3: ListWebhooks filters by name and enabled state and paginates.
func TestRepositoryListWebhooks(t *testing.T) {
	repo := newWebhookTestRepo(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		wh := &Webhook{
			OrganizationID:   "org-1",
			Surface:          SurfaceAdmin,
			Name:             fmt.Sprintf("hook-%d", i),
			URL:              "https://example.com/hook",
			Enabled:          i%2 == 0,
			MaxAttempts:      5,
			BackoffSeconds:   60,
			SecretCiphertext: []byte("cipher"),
		}
		require.NoError(t, wh.SetEventTypes([]string{EventDeploymentStatusChanged}))
		mustInsertWebhook(ctx, t, repo, wh)
	}
	// Name search.
	rows, total, err := repo.ListWebhooks(ctx, WebhookFilter{OrganizationID: "org-1", Name: "hook-1", Limit: 20})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, rows, 1)
	assert.Equal(t, "hook-1", rows[0].Name)
	// Enabled filter.
	_, total, err = repo.ListWebhooks(ctx, WebhookFilter{OrganizationID: "org-1", EnabledFilter: true, Enabled: true, Limit: 20})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	// Pagination.
	rows, total, err = repo.ListWebhooks(ctx, WebhookFilter{OrganizationID: "org-1", Offset: 0, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, rows, 2)
}

// AC8: FindWebhooksForEvent returns only enabled webhooks subscribed to
// the event type.
func TestRepositoryFindWebhooksForEvent(t *testing.T) {
	repo := newWebhookTestRepo(t)
	ctx := context.Background()
	wh1 := &Webhook{OrganizationID: "org-1", Surface: SurfaceAdmin, Name: "a", URL: "https://a", Enabled: true, MaxAttempts: 5, BackoffSeconds: 60, SecretCiphertext: []byte("c")}
	require.NoError(t, wh1.SetEventTypes([]string{EventDeploymentStatusChanged}))
	mustInsertWebhook(ctx, t, repo, wh1)
	wh2 := &Webhook{OrganizationID: "org-1", Surface: SurfaceAdmin, Name: "b", URL: "https://b", Enabled: false, MaxAttempts: 5, BackoffSeconds: 60, SecretCiphertext: []byte("c")}
	require.NoError(t, wh2.SetEventTypes([]string{EventDeploymentStatusChanged}))
	mustInsertWebhook(ctx, t, repo, wh2)
	wh3 := &Webhook{OrganizationID: "org-1", Surface: SurfaceAdmin, Name: "c", URL: "https://c", Enabled: true, MaxAttempts: 5, BackoffSeconds: 60, SecretCiphertext: []byte("c")}
	require.NoError(t, wh3.SetEventTypes([]string{EventAutoscalingScaled}))
	mustInsertWebhook(ctx, t, repo, wh3)

	rows, err := repo.FindWebhooksForEvent(ctx, "org-1", EventDeploymentStatusChanged)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, "a", rows[0].Name)
	// Other org sees nothing.
	rows, err = repo.FindWebhooksForEvent(ctx, "org-2", EventDeploymentStatusChanged)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// AC7: InsertDelivery/FindDeliveryByID round-trip; unknown -> 10704.
func TestRepositoryDeliveryRoundTrip(t *testing.T) {
	repo := newWebhookTestRepo(t)
	ctx := context.Background()
	wh := &Webhook{OrganizationID: "org-1", Surface: SurfaceAdmin, Name: "a", URL: "https://a", Enabled: true, MaxAttempts: 5, BackoffSeconds: 60, SecretCiphertext: []byte("c")}
	require.NoError(t, wh.SetEventTypes([]string{EventDeploymentStatusChanged}))
	whID := mustInsertWebhook(ctx, t, repo, wh)
	now := time.Now().UTC()
	d := &WebhookDelivery{
		WebhookID:      whID,
		OrganizationID: "org-1",
		EventID:        "evt-1",
		EventType:      EventDeploymentStatusChanged,
		Status:         DeliveryStatusPending,
		Payload:        `{"id":"evt-1"}`,
		CreatedAt:      now,
	}
	dID, err := repo.InsertDelivery(ctx, d)
	require.NoError(t, err)
	got, err := repo.FindDeliveryByID(ctx, "org-1", whID, dID)
	require.NoError(t, err)
	assert.Equal(t, EventDeploymentStatusChanged, got.EventType)
	// Unknown delivery.
	_, err = repo.FindDeliveryByID(ctx, "org-1", whID, "00000000-0000-0000-0000-000000000000")
	assert.Equal(t, apierrors.CodeWebhookDeliveryNotFound, apierrors.CodeOf(err))
}

// AC7: ListDeliveries filters by status and event type.
func TestRepositoryListDeliveries(t *testing.T) {
	repo := newWebhookTestRepo(t)
	ctx := context.Background()
	wh := &Webhook{OrganizationID: "org-1", Surface: SurfaceAdmin, Name: "a", URL: "https://a", Enabled: true, MaxAttempts: 5, BackoffSeconds: 60, SecretCiphertext: []byte("c")}
	require.NoError(t, wh.SetEventTypes([]string{EventDeploymentStatusChanged}))
	whID := mustInsertWebhook(ctx, t, repo, wh)
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		status := DeliveryStatusDelivered
		if i == 1 {
			status = DeliveryStatusFailed
		}
		_, err := repo.InsertDelivery(ctx, &WebhookDelivery{
			WebhookID: whID, OrganizationID: "org-1", EventID: fmt.Sprintf("evt-%d", i),
			EventType: EventDeploymentStatusChanged, Status: status, Payload: `{}`, CreatedAt: now,
		})
		require.NoError(t, err)
	}
	rows, total, err := repo.ListDeliveries(ctx, DeliveryFilter{OrganizationID: "org-1", WebhookID: whID, Status: DeliveryStatusFailed, Limit: 20})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, rows, 1)
	assert.Equal(t, DeliveryStatusFailed, rows[0].Status)
	// Event type filter.
	_, total, err = repo.ListDeliveries(ctx, DeliveryFilter{OrganizationID: "org-1", WebhookID: whID, EventType: EventDeploymentStatusChanged, Limit: 20})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
}

// AC8: FindDueDeliveries returns pending deliveries whose next attempt
// is due.
func TestRepositoryFindDueDeliveries(t *testing.T) {
	repo := newWebhookTestRepo(t)
	ctx := context.Background()
	wh := &Webhook{OrganizationID: "org-1", Surface: SurfaceAdmin, Name: "a", URL: "https://a", Enabled: true, MaxAttempts: 5, BackoffSeconds: 60, SecretCiphertext: []byte("c")}
	require.NoError(t, wh.SetEventTypes([]string{EventDeploymentStatusChanged}))
	whID := mustInsertWebhook(ctx, t, repo, wh)
	now := time.Now().UTC()
	past := now.Add(-time.Minute)
	future := now.Add(time.Hour)
	_, err := repo.InsertDelivery(ctx, &WebhookDelivery{WebhookID: whID, OrganizationID: "org-1", EventID: "e1", EventType: EventDeploymentStatusChanged, Status: DeliveryStatusPending, Payload: `{}`, CreatedAt: now, NextAttemptAt: &past})
	require.NoError(t, err)
	_, err = repo.InsertDelivery(ctx, &WebhookDelivery{WebhookID: whID, OrganizationID: "org-1", EventID: "e2", EventType: EventDeploymentStatusChanged, Status: DeliveryStatusPending, Payload: `{}`, CreatedAt: now, NextAttemptAt: &future})
	require.NoError(t, err)
	due, err := repo.FindDueDeliveries(ctx, now, 10)
	require.NoError(t, err)
	assert.Len(t, due, 1)
	assert.Equal(t, "e1", due[0].EventID)
}

// AC8: DeleteDeliveriesBefore deletes old deliveries in batches.
func TestRepositoryDeleteDeliveriesBefore(t *testing.T) {
	repo := newWebhookTestRepo(t)
	ctx := context.Background()
	wh := &Webhook{OrganizationID: "org-1", Surface: SurfaceAdmin, Name: "a", URL: "https://a", Enabled: true, MaxAttempts: 5, BackoffSeconds: 60, SecretCiphertext: []byte("c")}
	require.NoError(t, wh.SetEventTypes([]string{EventDeploymentStatusChanged}))
	whID := mustInsertWebhook(ctx, t, repo, wh)
	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	for i := 0; i < 3; i++ {
		_, err := repo.InsertDelivery(ctx, &WebhookDelivery{WebhookID: whID, OrganizationID: "org-1", EventID: fmt.Sprintf("e%d", i), EventType: EventDeploymentStatusChanged, Status: DeliveryStatusDelivered, Payload: `{}`, CreatedAt: old})
		require.NoError(t, err)
	}
	deleted, err := repo.DeleteDeliveriesBefore(ctx, time.Now().UTC(), 2)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)
	deleted, err = repo.DeleteDeliveriesBefore(ctx, time.Now().UTC(), 2)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
}

// AC4: DeleteWebhook cascades to deliveries.
func TestRepositoryDeleteWebhookCascades(t *testing.T) {
	repo := newWebhookTestRepo(t)
	ctx := context.Background()
	wh := &Webhook{OrganizationID: "org-1", Surface: SurfaceAdmin, Name: "a", URL: "https://a", Enabled: true, MaxAttempts: 5, BackoffSeconds: 60, SecretCiphertext: []byte("c")}
	require.NoError(t, wh.SetEventTypes([]string{EventDeploymentStatusChanged}))
	whID := mustInsertWebhook(ctx, t, repo, wh)
	_, err := repo.InsertDelivery(ctx, &WebhookDelivery{WebhookID: whID, OrganizationID: "org-1", EventID: "e1", EventType: EventDeploymentStatusChanged, Status: DeliveryStatusDelivered, Payload: `{}`, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	require.NoError(t, repo.DeleteWebhook(ctx, "org-1", whID))
	_, err = repo.FindWebhookByID(ctx, "org-1", whID)
	assert.Equal(t, apierrors.CodeWebhookNotFound, apierrors.CodeOf(err))
	var count int64
	require.NoError(t, repo.DB(ctx).Model(&WebhookDelivery{}).Where("webhook_id = ?", whID).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

// AC4: SetWebhookEnabled toggles the enabled state.
func TestRepositorySetWebhookEnabled(t *testing.T) {
	repo := newWebhookTestRepo(t)
	ctx := context.Background()
	wh := &Webhook{OrganizationID: "org-1", Surface: SurfaceAdmin, Name: "a", URL: "https://a", Enabled: true, MaxAttempts: 5, BackoffSeconds: 60, SecretCiphertext: []byte("c")}
	require.NoError(t, wh.SetEventTypes([]string{EventDeploymentStatusChanged}))
	whID := mustInsertWebhook(ctx, t, repo, wh)
	got, err := repo.SetWebhookEnabled(ctx, "org-1", whID, false)
	require.NoError(t, err)
	assert.False(t, got.Enabled)
	got, err = repo.SetWebhookEnabled(ctx, "org-1", whID, true)
	require.NoError(t, err)
	assert.True(t, got.Enabled)
}

// AC5: RollWebhookSecret replaces the encrypted secret.
func TestRepositoryRollWebhookSecret(t *testing.T) {
	repo := newWebhookTestRepo(t)
	ctx := context.Background()
	wh := &Webhook{OrganizationID: "org-1", Surface: SurfaceAdmin, Name: "a", URL: "https://a", Enabled: true, MaxAttempts: 5, BackoffSeconds: 60, SecretCiphertext: []byte("old")}
	require.NoError(t, wh.SetEventTypes([]string{EventDeploymentStatusChanged}))
	whID := mustInsertWebhook(ctx, t, repo, wh)
	got, err := repo.RollWebhookSecret(ctx, "org-1", whID, []byte("new"))
	require.NoError(t, err)
	assert.Equal(t, []byte("new"), got.SecretCiphertext)
}

// AC3: DeliverySummary returns total/delivered/failed counts.
func TestRepositoryDeliverySummary(t *testing.T) {
	repo := newWebhookTestRepo(t)
	ctx := context.Background()
	wh := &Webhook{OrganizationID: "org-1", Surface: SurfaceAdmin, Name: "a", URL: "https://a", Enabled: true, MaxAttempts: 5, BackoffSeconds: 60, SecretCiphertext: []byte("c")}
	require.NoError(t, wh.SetEventTypes([]string{EventDeploymentStatusChanged}))
	whID := mustInsertWebhook(ctx, t, repo, wh)
	now := time.Now().UTC()
	for _, s := range []string{DeliveryStatusDelivered, DeliveryStatusDelivered, DeliveryStatusFailed} {
		_, err := repo.InsertDelivery(ctx, &WebhookDelivery{WebhookID: whID, OrganizationID: "org-1", EventID: "e", EventType: EventDeploymentStatusChanged, Status: s, Payload: `{}`, CreatedAt: now})
		require.NoError(t, err)
	}
	total, delivered, failed, err := repo.DeliverySummary(ctx, whID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Equal(t, int64(2), delivered)
	assert.Equal(t, int64(1), failed)
}

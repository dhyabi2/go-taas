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
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/mq"
)

func newRunnerTestDB(t *testing.T) *gorm.DB {
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

// AC8: the delivery runner delivers a due pending delivery and marks it
// delivered on a 2xx response.
func TestDeliveryRunnerDelivers(t *testing.T) {
	db := newRunnerTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh := &Webhook{OrganizationID: "org-1", Surface: SurfaceAdmin, Name: "a", URL: srv.URL, Enabled: true, MaxAttempts: 3, BackoffSeconds: 1, SecretCiphertext: mustEncrypt(t, "whsec_secret")}
	require.NoError(t, wh.SetEventTypes([]string{EventDeploymentStatusChanged}))
	whID, err := repo.InsertWebhook(ctx, wh)
	require.NoError(t, err)

	now := time.Now().UTC()
	past := now.Add(-time.Minute)
	payload, err := BuildPayload(&webhookEvent{ID: "evt-1", Type: EventDeploymentStatusChanged, CreatedAt: now.Unix(), Data: json.RawMessage(`{}`)})
	require.NoError(t, err)
	_, err = repo.InsertDelivery(ctx, &WebhookDelivery{
		WebhookID: whID, OrganizationID: "org-1", EventID: "evt-1", EventType: EventDeploymentStatusChanged,
		Status: DeliveryStatusPending, Payload: string(payload), CreatedAt: now, NextAttemptAt: &past,
	})
	require.NoError(t, err)

	runner := NewDeliveryRunner(repo, NewDeliverer(5*time.Second), "dev-webhook-key-fvt", time.Second, 1)
	runner.DeliverOnce(ctx)

	deliveries, _, err := repo.ListDeliveries(ctx, DeliveryFilter{OrganizationID: "org-1", WebhookID: whID, Limit: 10})
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	assert.Equal(t, DeliveryStatusDelivered, deliveries[0].Status)
	assert.Equal(t, 1, deliveries[0].AttemptCount)
}

// AC8: a non-2xx response retries per the policy and then marks the
// delivery failed.
func TestDeliveryRunnerRetriesThenFails(t *testing.T) {
	db := newRunnerTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	wh := &Webhook{OrganizationID: "org-1", Surface: SurfaceAdmin, Name: "a", URL: srv.URL, Enabled: true, MaxAttempts: 2, BackoffSeconds: 1, SecretCiphertext: mustEncrypt(t, "whsec_secret")}
	require.NoError(t, wh.SetEventTypes([]string{EventDeploymentStatusChanged}))
	whID, err := repo.InsertWebhook(ctx, wh)
	require.NoError(t, err)

	now := time.Now().UTC()
	past := now.Add(-time.Minute)
	payload, err := BuildPayload(&webhookEvent{ID: "evt-1", Type: EventDeploymentStatusChanged, CreatedAt: now.Unix(), Data: json.RawMessage(`{}`)})
	require.NoError(t, err)
	_, err = repo.InsertDelivery(ctx, &WebhookDelivery{
		WebhookID: whID, OrganizationID: "org-1", EventID: "evt-1", EventType: EventDeploymentStatusChanged,
		Status: DeliveryStatusPending, Payload: string(payload), CreatedAt: now, NextAttemptAt: &past,
	})
	require.NoError(t, err)

	runner := NewDeliveryRunner(repo, NewDeliverer(5*time.Second), "dev-webhook-key-fvt", time.Second, 1)
	// First pass: attempt 1, stays pending (attempt < max).
	runner.DeliverOnce(ctx)
	deliveries, _, err := repo.ListDeliveries(ctx, DeliveryFilter{OrganizationID: "org-1", WebhookID: whID, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatusPending, deliveries[0].Status)
	assert.Equal(t, 1, deliveries[0].AttemptCount)

	// Force the next attempt to be due so the second pass picks it up.
	dueNow := time.Now().UTC().Add(-time.Minute)
	deliveries[0].NextAttemptAt = &dueNow
	require.NoError(t, repo.UpdateDelivery(ctx, deliveries[0]))

	// Second pass: attempt 2 = max, marks failed.
	runner.DeliverOnce(ctx)
	deliveries, _, err = repo.ListDeliveries(ctx, DeliveryFilter{OrganizationID: "org-1", WebhookID: whID, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatusFailed, deliveries[0].Status)
	assert.Equal(t, 2, deliveries[0].AttemptCount)
	assert.NotEmpty(t, deliveries[0].FailureReason)
}

// AC8: the event consumer routes a published event to matching webhooks
// by inserting pending deliveries.
func TestEventConsumerRoutesEvent(t *testing.T) {
	db := newRunnerTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	wh := &Webhook{OrganizationID: "org-1", Surface: SurfaceAdmin, Name: "a", URL: "https://a", Enabled: true, MaxAttempts: 5, BackoffSeconds: 60, SecretCiphertext: mustEncrypt(t, "whsec_secret")}
	require.NoError(t, wh.SetEventTypes([]string{EventDeploymentStatusChanged}))
	whID, err := repo.InsertWebhook(ctx, wh)
	require.NoError(t, err)

	consumer := NewEventConsumer(mq.NewFake(), repo, 1)
	ev := webhookEvent{ID: "evt-1", Type: EventDeploymentStatusChanged, CreatedAt: time.Now().Unix(), Data: json.RawMessage(`{}`)}
	body, err := json.Marshal(ev)
	require.NoError(t, err)
	msg := mq.Message{Subject: "webhook.events", Body: body, Headers: map[string]string{"organization_id": "org-1"}}
	require.NoError(t, consumer.handle(ctx, msg))

	deliveries, _, err := repo.ListDeliveries(ctx, DeliveryFilter{OrganizationID: "org-1", WebhookID: whID, Limit: 10})
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	assert.Equal(t, DeliveryStatusPending, deliveries[0].Status)
	assert.Equal(t, EventDeploymentStatusChanged, deliveries[0].EventType)
}

// AD8: the retention runner deletes old deliveries.
func TestRetentionRunnerDeletes(t *testing.T) {
	db := newRunnerTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	wh := &Webhook{OrganizationID: "org-1", Surface: SurfaceAdmin, Name: "a", URL: "https://a", Enabled: true, MaxAttempts: 5, BackoffSeconds: 60, SecretCiphertext: mustEncrypt(t, "whsec_secret")}
	require.NoError(t, wh.SetEventTypes([]string{EventDeploymentStatusChanged}))
	whID, err := repo.InsertWebhook(ctx, wh)
	require.NoError(t, err)

	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	_, err = repo.InsertDelivery(ctx, &WebhookDelivery{WebhookID: whID, OrganizationID: "org-1", EventID: "e1", EventType: EventDeploymentStatusChanged, Status: DeliveryStatusDelivered, Payload: `{}`, CreatedAt: old})
	require.NoError(t, err)

	runner := NewWebhookRetentionRunner(repo, 90*24*time.Hour, 100, time.Hour)
	runner.RetainOnce(ctx)

	var count int64
	require.NoError(t, repo.DB(ctx).Model(&WebhookDelivery{}).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func mustEncrypt(t *testing.T, plaintext string) []byte {
	t.Helper()
	ct, err := EncryptSecret(plaintext, "dev-webhook-key-fvt")
	require.NoError(t, err)
	return ct
}

// The runner Run methods loop until the context is cancelled.
func TestRunnerRunCancels(t *testing.T) {
	db := newRunnerTestDB(t)
	repo := NewRepository(db)
	ctx, cancel := context.WithCancel(context.Background())

	dr := NewDeliveryRunner(repo, NewDeliverer(time.Second), "dev-webhook-key-fvt", time.Millisecond, 1)
	done := make(chan error, 1)
	go func() { done <- dr.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("delivery runner did not stop on cancel")
	}

	rr := NewWebhookRetentionRunner(repo, 90*24*time.Hour, 100, time.Millisecond)
	done = make(chan error, 1)
	go func() { done <- rr.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("retention runner did not stop on cancel")
	}
}

// The runner constructors return nil when the required components are
// unavailable.
func TestRunnerConstructorsNil(t *testing.T) {
	assert.Nil(t, NewDeliveryRunnerRunner(nil))
	assert.Nil(t, NewWebhookRetentionRunnerRunner(nil))
	assert.Nil(t, NewEventConsumerRunner(nil))
}

// The event consumer Run method subscribes and blocks until cancelled.
func TestEventConsumerRun(t *testing.T) {
	db := newRunnerTestDB(t)
	repo := NewRepository(db)
	client := mq.NewFake()
	consumer := NewEventConsumer(client, repo, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- consumer.Run(ctx) }()
	// Give the subscription a moment to register.
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("event consumer did not stop on cancel")
	}
}

// The event consumer skips malformed and invalid events.
func TestEventConsumerSkipsInvalid(t *testing.T) {
	db := newRunnerTestDB(t)
	repo := NewRepository(db)
	consumer := NewEventConsumer(mq.NewFake(), repo, 1)
	ctx := context.Background()
	// Malformed JSON.
	require.NoError(t, consumer.handle(ctx, mq.Message{Body: []byte("not-json")}))
	// Missing id/type.
	require.NoError(t, consumer.handle(ctx, mq.Message{Body: []byte(`{"id":"","type":""}`)}))
	// Missing org header.
	require.NoError(t, consumer.handle(ctx, mq.Message{Body: []byte(`{"id":"e1","type":"t"}`)}))
}

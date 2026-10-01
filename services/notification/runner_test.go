package notification

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/config"
	"github.com/go-taas/go-taas/pkg/mq"
	"github.com/go-taas/go-taas/pkg/server"
)

func newRetentionTestRepo(t *testing.T) *Repository {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Notification{}, &NotificationPreference{}, &NotificationThreshold{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return NewRepository(db)
}

// RetainOnce deletes notifications older than the TTL.
func TestRetentionRunnerRetainOnce(t *testing.T) {
	repo := newRetentionTestRepo(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-200 * 24 * time.Hour)
	_, err := repo.InsertNotification(ctx, &Notification{
		OrganizationID: "org-1", UserID: "u-1", Surface: SurfaceUser,
		EventType: "billing.balance_low", Title: "old", Body: "b", Severity: SeverityInfo,
		Data: `{}`, CreatedAt: old,
	})
	require.NoError(t, err)
	_, err = repo.InsertNotification(ctx, &Notification{
		OrganizationID: "org-1", UserID: "u-1", Surface: SurfaceUser,
		EventType: "billing.balance_low", Title: "new", Body: "b", Severity: SeverityInfo,
		Data: `{}`, CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)

	runner := NewNotificationRetentionRunner(repo, 90*24*time.Hour, 1000, time.Hour)
	runner.RetainOnce(ctx)

	rows, total, err := repo.ListNotifications(ctx, NotificationFilter{
		OrganizationID: "org-1", UserID: "u-1", Offset: 0, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "new", rows[0].Title)
}

// Run loops on the ticker until ctx is cancelled.
func TestRetentionRunnerRun(t *testing.T) {
	repo := newRetentionTestRepo(t)
	runner := NewNotificationRetentionRunner(repo, 90*24*time.Hour, 1000, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("runner did not stop on cancel")
	}
}

// PublishEvent publishes a canonical envelope to notification.events.
func TestPublishEvent(t *testing.T) {
	client := mq.NewFake()
	var got mq.Message
	require.NoError(t, client.Subscribe(context.Background(), mq.DefaultSubjects().NotificationEvents, func(msg mq.Message) error {
		got = msg
		return nil
	}))
	PublishEvent(context.Background(), client, "org-1", "billing.balance_low", "evt-1", map[string]any{"balance_cents": 500})
	assert.Equal(t, "org-1", got.Headers["organization_id"])
	assert.Equal(t, "billing.balance_low", got.Headers["event_type"])
	assert.Contains(t, string(got.Body), "billing.balance_low")
}

// PublishEvent with a nil client is a no-op.
func TestPublishEventNilClient(_ *testing.T) {
	PublishEvent(context.Background(), nil, "org-1", "billing.balance_low", "evt-1", map[string]any{})
}

// titleForEvent / bodyForEvent / severityForEvent cover the catalog.
func TestEventHelpers(t *testing.T) {
	assert.Equal(t, "Deployment status changed", titleForEvent("deployment.status_changed"))
	assert.Equal(t, "Service autoscaled", titleForEvent("autoscaling.scaled"))
	assert.Equal(t, "Service scaled to zero", titleForEvent("autoscaling.scale_to_zero"))
	assert.Equal(t, "Invoice created", titleForEvent("billing.invoice_created"))
	assert.Equal(t, "Invoice paid", titleForEvent("billing.invoice_paid"))
	assert.Equal(t, "Spend limit breached", titleForEvent("billing.spend_limit_breached"))
	assert.Equal(t, "Balance low", titleForEvent("billing.balance_low"))
	assert.Equal(t, "unknown.event", titleForEvent("unknown.event"))

	assert.Equal(t, SeverityWarning, severityForEvent("deployment.status_changed"))
	assert.Equal(t, SeverityWarning, severityForEvent("billing.spend_limit_breached"))
	assert.Equal(t, SeverityInfo, severityForEvent("billing.invoice_created"))

	assert.Equal(t, "/admin/inference-services", linkForEvent("deployment.status_changed"))
	assert.Equal(t, "/admin/autoscaling", linkForEvent("autoscaling.scaled"))
	assert.Equal(t, "/billing", linkForEvent("billing.balance_low"))
	assert.Equal(t, "", linkForEvent("unknown.event"))
}

// metricValueForEvent extracts the metric from the payload.
func TestMetricValueForEvent(t *testing.T) {
	// balance_low from balance_cents.
	v, ok := metricValueForEvent("balance_low", "billing.balance_low", map[string]any{"balance_cents": 500})
	assert.True(t, ok)
	assert.Equal(t, 500.0, v)
	// spend_limit from spent_cents.
	v, ok = metricValueForEvent("spend_limit", "billing.spend_limit_breached", map[string]any{"spent_cents": 1000})
	assert.True(t, ok)
	assert.Equal(t, 1000.0, v)
	// autoscaling_replicas from replicas.
	v, ok = metricValueForEvent("autoscaling_replicas", "autoscaling.scaled", map[string]any{"replicas": 10})
	assert.True(t, ok)
	assert.Equal(t, 10.0, v)
	// deployment_failure on a failed deployment.
	v, ok = metricValueForEvent("deployment_failure", "deployment.status_changed", map[string]any{"state": "failed"})
	assert.True(t, ok)
	assert.Equal(t, 1.0, v)
	// deployment_failure on a running deployment -> not ok.
	_, ok = metricValueForEvent("deployment_failure", "deployment.status_changed", map[string]any{"state": "running"})
	assert.False(t, ok)
	// Missing metric -> not ok.
	_, ok = metricValueForEvent("balance_low", "billing.balance_low", map[string]any{})
	assert.False(t, ok)
}

// toFloat converts JSON numbers.
func TestToFloat(t *testing.T) {
	assert.Equal(t, 1.5, toFloat(1.5))
	assert.Equal(t, 2.0, toFloat(2))
	assert.Equal(t, 3.0, toFloat(int64(3)))
	assert.Equal(t, 0.0, toFloat("not-a-number"))
}

// bodyForEvent covers the catalog bodies.
func TestBodyForEvent(t *testing.T) {
	assert.Equal(t, "An inference service changed state.", bodyForEvent("deployment.status_changed", nil))
	assert.Equal(t, "An autoscaling policy scaled a service.", bodyForEvent("autoscaling.scaled", nil))
	assert.Equal(t, "An autoscaling policy scaled a service to zero.", bodyForEvent("autoscaling.scale_to_zero", nil))
	assert.Equal(t, "An invoice was created.", bodyForEvent("billing.invoice_created", nil))
	assert.Equal(t, "An invoice was paid.", bodyForEvent("billing.invoice_paid", nil))
	assert.Equal(t, "A spend limit was breached.", bodyForEvent("billing.spend_limit_breached", nil))
	assert.Equal(t, "An account balance ran low.", bodyForEvent("billing.balance_low", nil))
	assert.Equal(t, `{"x":1}`, bodyForEvent("unknown.event", []byte(`{"x":1}`)))
}

// eventTypesForSurface and metricsForSurface cover both surfaces.
func TestSurfaceCatalogs(t *testing.T) {
	assert.Equal(t, AdminEventTypes, eventTypesForSurface(SurfaceAdmin))
	assert.Equal(t, UserEventTypes, eventTypesForSurface(SurfaceUser))
	assert.Equal(t, AdminMetrics, metricsForSurface(SurfaceAdmin))
	assert.Equal(t, UserMetrics, metricsForSurface(SurfaceUser))
}

// New constructs a service with the shared components (nil-safe).
func TestServiceNew(t *testing.T) {
	svc := New(nil)
	assert.NotNil(t, svc)
	assert.Equal(t, ServiceName, svc.ServiceName())
}

// The runner constructors return nil when the components are nil.
func TestRunnerConstructorsNil(t *testing.T) {
	assert.Nil(t, NewEventConsumerRunner(nil))
	assert.Nil(t, NewNotificationRetentionRunnerRunner(nil))
}

// The runner constructors build from the shared components.
func TestRunnerConstructorsFromComponents(t *testing.T) {
	db := newRetentionTestDB(t)
	client := mq.NewFake()
	components := &fakeComponents{db: db, client: client}
	assert.NotNil(t, NewEventConsumerRunner(components))

	// The retention runner reads the notification config; initialize it.
	cfg := &config.Configuration{}
	cfg.Notification.Retention.Enabled = true
	config.SetConfigForTest(cfg)
	assert.NotNil(t, NewNotificationRetentionRunnerRunner(components))
}

type fakeComponents struct {
	db     *gorm.DB
	client mq.Client
}

func (f *fakeComponents) DB() server.DBComponent {
	if f.db == nil {
		return nil
	}
	return &fakeDBComponent{db: f.db}
}
func (f *fakeComponents) Redis() server.RedisComponent {
	return nil
}
func (f *fakeComponents) MQ() server.MQComponent {
	if f.client == nil {
		return nil
	}
	return &fakeMQComponent{client: f.client}
}

type fakeDBComponent struct{ db *gorm.DB }

func (f *fakeDBComponent) GormDB() any { return f.db }

type fakeMQComponent struct{ client mq.Client }

func (f *fakeMQComponent) Publish(ctx context.Context, subject string, body []byte, headers map[string]string) error {
	return f.client.Publish(ctx, subject, body, headers)
}
func (f *fakeMQComponent) Client() any { return f.client }

func newRetentionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Notification{}, &NotificationPreference{}, &NotificationThreshold{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return db
}

// Run subscribes to notification.events and blocks until cancelled.
func TestEventConsumerRun(t *testing.T) {
	repo := newRetentionTestRepo(t)
	client := mq.NewFake()
	consumer := NewEventConsumer(client, repo, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- consumer.Run(ctx) }()
	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("consumer did not stop on cancel")
	}
}

// NewEventConsumer falls back to 1 worker when workers <= 0.
func TestNewEventConsumerDefaultWorkers(t *testing.T) {
	repo := newRetentionTestRepo(t)
	consumer := NewEventConsumer(mq.NewFake(), repo, 0)
	assert.NotNil(t, consumer)
	assert.Equal(t, 1, consumer.workers)
}

// NewEventConsumerRunner returns nil when the MQ or DB component is
// unavailable.
func TestNewEventConsumerRunnerMissingComponents(t *testing.T) {
	db := newRetentionTestDB(t)
	client := mq.NewFake()
	// MQ nil.
	assert.Nil(t, NewEventConsumerRunner(&fakeComponents{db: db, client: nil}))
	// DB nil.
	assert.Nil(t, NewEventConsumerRunner(&fakeComponents{db: nil, client: client}))
}

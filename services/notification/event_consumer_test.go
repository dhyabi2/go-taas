package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/mq"
)

func newConsumerTestEnv(t *testing.T) (*Repository, *EventConsumer) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Notification{}, &NotificationPreference{}, &NotificationThreshold{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	repo := NewRepository(db)
	consumer := NewEventConsumer(mq.NewFake(), repo, 1)
	return repo, consumer
}

func buildEventMsg(t *testing.T, orgID, eventType string, data any) mq.Message {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	body, err := json.Marshal(notificationEvent{
		ID:   "evt-1",
		Type: eventType,
		Data: raw,
	})
	require.NoError(t, err)
	return mq.Message{
		Subject: "notification.events",
		Body:    body,
		Headers: map[string]string{"organization_id": orgID},
	}
}

// AC6: a subscribed event creates a notification for every enabled user;
// a disabled event type creates none.
func TestConsumerFanOut(t *testing.T) {
	repo, consumer := newConsumerTestEnv(t)
	ctx := context.Background()
	require.NoError(t, repo.UpsertPreferences(ctx, "org-1", "u-1", []string{"billing.balance_low"}))
	require.NoError(t, repo.UpsertPreferences(ctx, "org-1", "u-2", []string{"billing.invoice_created"}))

	err := consumer.HandleForTest(ctx, buildEventMsg(t, "org-1", "billing.balance_low", map[string]any{"balance_cents": 500}))
	require.NoError(t, err)

	rows, total, err := repo.ListNotifications(ctx, NotificationFilter{
		OrganizationID: "org-1", UserID: "u-1", Offset: 0, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "billing.balance_low", rows[0].EventType)
	// u-2 disabled balance_low -> no notification.
	rows, total, err = repo.ListNotifications(ctx, NotificationFilter{
		OrganizationID: "org-1", UserID: "u-2", Offset: 0, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, rows)
}

// AC7: a threshold crossing creates a notification with the threshold's
// name.
func TestConsumerThreshold(t *testing.T) {
	repo, consumer := newConsumerTestEnv(t)
	ctx := context.Background()
	require.NoError(t, repo.UpsertPreferences(ctx, "org-1", "u-1", []string{"billing.balance_low"}))
	_, err := repo.InsertThreshold(ctx, &NotificationThreshold{
		OrganizationID: "org-1", UserID: "u-1", Surface: SurfaceUser,
		Name: "low balance alert", Metric: "balance_low", Operator: OperatorLT, Value: 1000, Enabled: true,
	})
	require.NoError(t, err)

	// Balance 500 < 1000 -> crosses.
	err = consumer.HandleForTest(ctx, buildEventMsg(t, "org-1", "billing.balance_low", map[string]any{"balance_cents": 500}))
	require.NoError(t, err)

	rows, total, err := repo.ListNotifications(ctx, NotificationFilter{
		OrganizationID: "org-1", UserID: "u-1", Offset: 0, Limit: 20,
	})
	require.NoError(t, err)
	// One from the fan-out + one from the threshold.
	assert.Equal(t, int64(2), total)
	foundThreshold := false
	for _, n := range rows {
		if n.Title == "low balance alert" {
			foundThreshold = true
		}
	}
	assert.True(t, foundThreshold, "expected a threshold notification")
}

// AC7: a threshold that is not crossed creates no threshold
// notification.
func TestConsumerThresholdNotCrossed(t *testing.T) {
	repo, consumer := newConsumerTestEnv(t)
	ctx := context.Background()
	require.NoError(t, repo.UpsertPreferences(ctx, "org-1", "u-1", []string{"billing.balance_low"}))
	_, err := repo.InsertThreshold(ctx, &NotificationThreshold{
		OrganizationID: "org-1", UserID: "u-1", Surface: SurfaceUser,
		Name: "low balance alert", Metric: "balance_low", Operator: OperatorLT, Value: 1000, Enabled: true,
	})
	require.NoError(t, err)

	// Balance 5000 > 1000 -> does not cross.
	err = consumer.HandleForTest(ctx, buildEventMsg(t, "org-1", "billing.balance_low", map[string]any{"balance_cents": 5000}))
	require.NoError(t, err)

	rows, total, err := repo.ListNotifications(ctx, NotificationFilter{
		OrganizationID: "org-1", UserID: "u-1", Offset: 0, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.NotEqual(t, "low balance alert", rows[0].Title)
}

// AC6: an admin-surface event creates a notification for an admin user.
func TestConsumerAdminEvent(t *testing.T) {
	repo, consumer := newConsumerTestEnv(t)
	ctx := context.Background()
	require.NoError(t, repo.UpsertPreferences(ctx, "org-1", "u-admin", []string{"deployment.status_changed"}))

	err := consumer.HandleForTest(ctx, buildEventMsg(t, "org-1", "deployment.status_changed", map[string]any{"state": "failed"}))
	require.NoError(t, err)

	rows, total, err := repo.ListNotifications(ctx, NotificationFilter{
		OrganizationID: "org-1", UserID: "u-admin", Offset: 0, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "deployment.status_changed", rows[0].EventType)
	assert.Equal(t, SurfaceAdmin, rows[0].Surface)
}

// AC7: an autoscaling_replicas threshold crossing creates a threshold
// notification.
func TestConsumerAutoscalingThreshold(t *testing.T) {
	repo, consumer := newConsumerTestEnv(t)
	ctx := context.Background()
	require.NoError(t, repo.UpsertPreferences(ctx, "org-1", "u-admin", []string{"autoscaling.scaled"}))
	_, err := repo.InsertThreshold(ctx, &NotificationThreshold{
		OrganizationID: "org-1", UserID: "u-admin", Surface: SurfaceAdmin,
		Name: "too many replicas", Metric: "autoscaling_replicas", Operator: OperatorGT, Value: 8, Enabled: true,
	})
	require.NoError(t, err)

	err = consumer.HandleForTest(ctx, buildEventMsg(t, "org-1", "autoscaling.scaled", map[string]any{"replicas": 10}))
	require.NoError(t, err)

	rows, total, err := repo.ListNotifications(ctx, NotificationFilter{
		OrganizationID: "org-1", UserID: "u-admin", Offset: 0, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	found := false
	for _, n := range rows {
		if n.Title == "too many replicas" {
			found = true
		}
	}
	assert.True(t, found)
}

// AC6: a malformed event is skipped without error.
func TestConsumerMalformedEvent(t *testing.T) {
	_, consumer := newConsumerTestEnv(t)
	ctx := context.Background()
	err := consumer.HandleForTest(ctx, mq.Message{
		Subject: "notification.events",
		Body:    []byte("not-json"),
		Headers: map[string]string{"organization_id": "org-1"},
	})
	require.NoError(t, err)
}

// AC6: an event type outside the catalog is skipped.
func TestConsumerUnknownEventType(t *testing.T) {
	repo, consumer := newConsumerTestEnv(t)
	ctx := context.Background()
	require.NoError(t, repo.UpsertPreferences(ctx, "org-1", "u-1", []string{"billing.balance_low"}))
	err := consumer.HandleForTest(ctx, buildEventMsg(t, "org-1", "unknown.event", map[string]any{"x": 1}))
	require.NoError(t, err)
	rows, total, err := repo.ListNotifications(ctx, NotificationFilter{
		OrganizationID: "org-1", UserID: "u-1", Offset: 0, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, rows)
}

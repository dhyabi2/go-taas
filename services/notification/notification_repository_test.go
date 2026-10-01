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

	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

func newNotificationTestDB(t *testing.T) *gorm.DB {
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

func newNotificationTestRepo(t *testing.T) *Repository {
	t.Helper()
	return NewRepository(newNotificationTestDB(t))
}

func mustInsertNotification(ctx context.Context, t *testing.T, repo *Repository, n *Notification) string {
	t.Helper()
	id, err := repo.InsertNotification(ctx, n)
	require.NoError(t, err)
	return id
}

// AC1: InsertNotification round-trips a notification.
func TestRepositoryInsertNotification(t *testing.T) {
	repo := newNotificationTestRepo(t)
	ctx := context.Background()
	id := mustInsertNotification(ctx, t, repo, &Notification{
		OrganizationID: "org-1",
		UserID:         "u-1",
		Surface:        SurfaceUser,
		EventType:      "billing.balance_low",
		Title:          "Balance low",
		Body:           "An account balance ran low.",
		Severity:       SeverityWarning,
		Read:           false,
		Data:           `{"balance_cents":500}`,
		Link:           "/billing",
	})
	assert.NotEmpty(t, id)

	got, err := repo.FindNotificationByID(ctx, "org-1", "u-1", id)
	require.NoError(t, err)
	assert.Equal(t, "billing.balance_low", got.EventType)
	assert.Equal(t, "Balance low", got.Title)
}

// AC2: FindNotificationByID returns 11001 on an unknown id.
func TestRepositoryFindNotificationNotFound(t *testing.T) {
	repo := newNotificationTestRepo(t)
	ctx := context.Background()
	_, err := repo.FindNotificationByID(ctx, "org-1", "u-1", "00000000-0000-0000-0000-000000000000")
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeNotificationNotFound, apierrors.CodeOf(err))
}

// AC1: ListNotifications filters by read state and event type and
// paginates.
func TestRepositoryListNotifications(t *testing.T) {
	repo := newNotificationTestRepo(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		mustInsertNotification(ctx, t, repo, &Notification{
			OrganizationID: "org-1",
			UserID:         "u-1",
			Surface:        SurfaceUser,
			EventType:      "billing.balance_low",
			Title:          "Balance low",
			Body:           "body",
			Severity:       SeverityWarning,
			Read:           i%2 == 0,
			Data:           `{}`,
		})
	}
	// Read filter.
	rows, total, err := repo.ListNotifications(ctx, NotificationFilter{
		OrganizationID: "org-1", UserID: "u-1", ReadFilter: true, Read: true, Offset: 0, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, rows, 3)
	// Event filter.
	rows, total, err = repo.ListNotifications(ctx, NotificationFilter{
		OrganizationID: "org-1", UserID: "u-1", EventType: "billing.balance_low", Offset: 0, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, rows, 5)
	// Pagination.
	rows, total, err = repo.ListNotifications(ctx, NotificationFilter{
		OrganizationID: "org-1", UserID: "u-1", Offset: 0, Limit: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, rows, 2)
}

// AC2: MarkNotificationRead and MarkAllNotificationsRead.
func TestRepositoryMarkRead(t *testing.T) {
	repo := newNotificationTestRepo(t)
	ctx := context.Background()
	id1 := mustInsertNotification(ctx, t, repo, &Notification{
		OrganizationID: "org-1", UserID: "u-1", Surface: SurfaceUser,
		EventType: "billing.balance_low", Title: "t", Body: "b", Severity: SeverityInfo, Data: `{}`,
	})
	mustInsertNotification(ctx, t, repo, &Notification{
		OrganizationID: "org-1", UserID: "u-1", Surface: SurfaceUser,
		EventType: "billing.balance_low", Title: "t", Body: "b", Severity: SeverityInfo, Data: `{}`,
	})
	got, err := repo.MarkNotificationRead(ctx, "org-1", "u-1", id1)
	require.NoError(t, err)
	assert.True(t, got.Read)
	count, err := repo.CountUnread(ctx, "org-1", "u-1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	marked, err := repo.MarkAllNotificationsRead(ctx, "org-1", "u-1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), marked)
	count, err = repo.CountUnread(ctx, "org-1", "u-1")
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

// AC3: DeleteNotification deletes one; unknown returns 11001.
func TestRepositoryDeleteNotification(t *testing.T) {
	repo := newNotificationTestRepo(t)
	ctx := context.Background()
	id := mustInsertNotification(ctx, t, repo, &Notification{
		OrganizationID: "org-1", UserID: "u-1", Surface: SurfaceUser,
		EventType: "billing.balance_low", Title: "t", Body: "b", Severity: SeverityInfo, Data: `{}`,
	})
	require.NoError(t, repo.DeleteNotification(ctx, "org-1", "u-1", id))
	_, err := repo.FindNotificationByID(ctx, "org-1", "u-1", id)
	assert.Equal(t, apierrors.CodeNotificationNotFound, apierrors.CodeOf(err))
	err = repo.DeleteNotification(ctx, "org-1", "u-1", "00000000-0000-0000-0000-000000000000")
	assert.Equal(t, apierrors.CodeNotificationNotFound, apierrors.CodeOf(err))
}

// AC4: GetPreferences returns nil when absent; UpsertPreferences
// persists.
func TestRepositoryPreferences(t *testing.T) {
	repo := newNotificationTestRepo(t)
	ctx := context.Background()
	row, err := repo.GetPreferences(ctx, "org-1", "u-1")
	require.NoError(t, err)
	assert.Nil(t, row)
	require.NoError(t, repo.UpsertPreferences(ctx, "org-1", "u-1", []string{"billing.balance_low"}))
	row, err = repo.GetPreferences(ctx, "org-1", "u-1")
	require.NoError(t, err)
	require.NotNil(t, row)
	types, err := row.EventTypes()
	require.NoError(t, err)
	assert.Equal(t, []string{"billing.balance_low"}, types)
	// Upsert updates.
	require.NoError(t, repo.UpsertPreferences(ctx, "org-1", "u-1", []string{"billing.invoice_created"}))
	row, err = repo.GetPreferences(ctx, "org-1", "u-1")
	require.NoError(t, err)
	types, err = row.EventTypes()
	require.NoError(t, err)
	assert.Equal(t, []string{"billing.invoice_created"}, types)
}

// AC5: InsertThreshold / FindThresholdByID / ListThresholds /
// UpdateThreshold / DeleteThreshold.
func TestRepositoryThresholds(t *testing.T) {
	repo := newNotificationTestRepo(t)
	ctx := context.Background()
	id, err := repo.InsertThreshold(ctx, &NotificationThreshold{
		OrganizationID: "org-1", UserID: "u-1", Surface: SurfaceUser,
		Name: "low balance", Metric: "balance_low", Operator: OperatorLT, Value: 1000, Enabled: true,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, id)
	got, err := repo.FindThresholdByID(ctx, "org-1", "u-1", id)
	require.NoError(t, err)
	assert.Equal(t, "low balance", got.Name)
	// Unknown -> 11003.
	_, err = repo.FindThresholdByID(ctx, "org-1", "u-1", "00000000-0000-0000-0000-000000000000")
	assert.Equal(t, apierrors.CodeNotificationThresholdNotFound, apierrors.CodeOf(err))
	// List with enabled filter.
	rows, total, err := repo.ListThresholds(ctx, ThresholdFilter{
		OrganizationID: "org-1", UserID: "u-1", EnabledFilter: true, Enabled: true, Offset: 0, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, rows, 1)
	// Update.
	got.Enabled = false
	require.NoError(t, repo.UpdateThreshold(ctx, got))
	rows, total, err = repo.ListThresholds(ctx, ThresholdFilter{
		OrganizationID: "org-1", UserID: "u-1", EnabledFilter: true, Enabled: false, Offset: 0, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, rows, 1)
	// Delete.
	require.NoError(t, repo.DeleteThreshold(ctx, "org-1", "u-1", id))
	_, err = repo.FindThresholdByID(ctx, "org-1", "u-1", id)
	assert.Equal(t, apierrors.CodeNotificationThresholdNotFound, apierrors.CodeOf(err))
}

// AC6: FindEnabledUsersForEvent returns users whose preferences enable
// the event type; a disabled event type excludes the user.
func TestRepositoryFindEnabledUsersForEvent(t *testing.T) {
	repo := newNotificationTestRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.UpsertPreferences(ctx, "org-1", "u-1", []string{"billing.balance_low"}))
	require.NoError(t, repo.UpsertPreferences(ctx, "org-1", "u-2", []string{"billing.invoice_created"}))
	users, err := repo.FindEnabledUsersForEvent(ctx, "org-1", SurfaceUser, "billing.balance_low")
	require.NoError(t, err)
	assert.Contains(t, users, "u-1")
	assert.NotContains(t, users, "u-2")
}

// AC7: FindEnabledThresholds returns only enabled thresholds.
func TestRepositoryFindEnabledThresholds(t *testing.T) {
	repo := newNotificationTestRepo(t)
	ctx := context.Background()
	_, err := repo.InsertThreshold(ctx, &NotificationThreshold{
		OrganizationID: "org-1", UserID: "u-1", Surface: SurfaceUser,
		Name: "low", Metric: "balance_low", Operator: OperatorLT, Value: 1000, Enabled: true,
	})
	require.NoError(t, err)
	_, err = repo.InsertThreshold(ctx, &NotificationThreshold{
		OrganizationID: "org-1", UserID: "u-1", Surface: SurfaceUser,
		Name: "off", Metric: "spend_limit", Operator: OperatorGT, Value: 1000, Enabled: false,
	})
	require.NoError(t, err)
	rows, err := repo.FindEnabledThresholds(ctx, "org-1", SurfaceUser)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, "low", rows[0].Name)
}

// AC6: DeleteNotificationsBefore deletes old notifications in batches.
func TestRepositoryDeleteNotificationsBefore(t *testing.T) {
	repo := newNotificationTestRepo(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-200 * 24 * time.Hour)
	for i := 0; i < 3; i++ {
		_, err := repo.InsertNotification(ctx, &Notification{
			OrganizationID: "org-1", UserID: "u-1", Surface: SurfaceUser,
			EventType: "billing.balance_low", Title: "t", Body: "b", Severity: SeverityInfo,
			Data: `{}`, CreatedAt: old,
		})
		require.NoError(t, err)
	}
	deleted, err := repo.DeleteNotificationsBefore(ctx, time.Now().UTC().Add(-90*24*time.Hour), 2)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)
	deleted, err = repo.DeleteNotificationsBefore(ctx, time.Now().UTC().Add(-90*24*time.Hour), 2)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
}

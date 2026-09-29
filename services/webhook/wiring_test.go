package webhook

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	commonv1 "github.com/go-taas/go-taas/proto/taas/common/v1"

	"github.com/go-taas/go-taas/pkg/config"
	"github.com/go-taas/go-taas/pkg/server"
)

// fakeComponents provides a DB component for the production wiring path.
type fakeComponents struct {
	db *gorm.DB
}

func (f *fakeComponents) DB() server.DBComponent       { return &fakeDBComponent{db: f.db} }
func (f *fakeComponents) Redis() server.RedisComponent { return nil }
func (f *fakeComponents) MQ() server.MQComponent       { return nil }

type fakeDBComponent struct{ db *gorm.DB }

func (f *fakeDBComponent) GormDB() any { return f.db }

// TestServiceWiring exercises the production constructor, Migrate,
// AttachToServer, ServiceName and gateway registration.
func TestServiceWiring(t *testing.T) {
	cfg := &config.Configuration{}
	cfg.MQ.Namespace = "test"
	cfg.Webhook.Delivery.Timeout = 5 * 1e9
	config.SetConfigForTest(cfg)

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})

	svc := New(&fakeComponents{db: db})
	assert.Equal(t, ServiceName, svc.ServiceName())
	require.NoError(t, svc.Migrate(context.Background()))

	grpcSrv := grpc.NewServer()
	svc.AttachToServer(grpcSrv)
	assert.NotNil(t, svc.GetServiceHandlerRegisterFn())
}

// TestMigrateSchemaForFVT applies the schema.
func TestMigrateSchemaForFVT(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	require.NoError(t, MigrateSchemaForFVT(db))
	assert.True(t, db.Migrator().HasTable(&Webhook{}))
	assert.True(t, db.Migrator().HasTable(&WebhookDelivery{}))
}

// TestSurfaceFromContext derives the surface from the request path.
func TestSurfaceFromContext(t *testing.T) {
	ctx := withAdminPath(withOrg(context.Background(), "org-1"))
	assert.Equal(t, SurfaceAdmin, surfaceFromContext(ctx))
	ctx = withUserPath(withOrg(context.Background(), "org-1"))
	assert.Equal(t, SurfaceUser, surfaceFromContext(ctx))
	assert.Equal(t, SurfaceUser, surfaceFromContext(context.Background()))
}

// TestEventTypesForSurface returns the correct catalog.
func TestEventTypesForSurface(t *testing.T) {
	assert.Equal(t, AdminEventTypes, eventTypesForSurface(SurfaceAdmin))
	assert.Equal(t, UserEventTypes, eventTypesForSurface(SurfaceUser))
}

// TestNormalizePagination clamps the page request.
func TestNormalizePagination(t *testing.T) {
	offset, limit := normalizePagination(nil)
	assert.Equal(t, 0, offset)
	assert.Equal(t, 20, limit)
	offset, limit = normalizePagination(&commonv1.PageRequest{Offset: 10, Limit: 500})
	assert.Equal(t, 10, offset)
	assert.Equal(t, 100, limit)
}

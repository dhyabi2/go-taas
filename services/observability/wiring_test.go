package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"gorm.io/gorm"

	observabilityv1 "github.com/go-taas/go-taas/proto/taas/observability/v1"
	apierrors "github.com/go-taas/go-taas/pkg/errors"
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
	db := newServiceTestDB(t)
	svc := New(&fakeComponents{db: db})
	assert.Equal(t, ServiceName, svc.ServiceName())
	require.NoError(t, svc.Migrate(context.Background()))

	grpcSrv := grpc.NewServer()
	svc.AttachToServer(grpcSrv)
	assert.NotNil(t, svc.GetServiceHandlerRegisterFn())
}

// The service implements the server.Service / server.ServiceWithGateway
// interfaces (AttachToServer, ServiceName, GetServiceHandlerRegisterFn)
// and the Migrator interface (Migrate).
func TestServerInterface(t *testing.T) {
	db := newServiceTestDB(t)
	svc := NewForFVT(db)

	assert.Equal(t, ServiceName, svc.ServiceName())
	assert.NotNil(t, svc.GetServiceHandlerRegisterFn())

	grpcSrv := grpc.NewServer()
	svc.AttachToServer(grpcSrv)
	// The service is registered under the observability service name.
	assert.NotNil(t, grpcSrv.GetServiceInfo()["taas.observability.v1.ObservabilityService"])

	// Migrate creates the request_logs projection table.
	require.NoError(t, svc.Migrate(context.Background()))
}

// MigrateSchemaForFVT applies the schema onto a caller-provided DB.
func TestMigrateSchemaForFVT(t *testing.T) {
	db := newServiceTestDB(t)
	require.NoError(t, MigrateSchemaForFVT(db))
	// The two additive request_logs indexes (Section 4.2) are created.
	assert.True(t, db.Migrator().HasIndex(&requestLogRow{}, "idx_request_logs_created"))
	assert.True(t, db.Migrator().HasIndex(&requestLogRow{}, "idx_request_logs_model_created"))
}

// resolveOrganizationID returns 10001 when the org header is absent or
// empty.
func TestResolveOrganizationID(t *testing.T) {
	ctx := context.Background()
	_, err := resolveOrganizationID(ctx)
	assert.Equal(t, apierrors.CodeUnauthorized, apierrors.CodeOf(err))

	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("x-organization-id", "org-1"))
	org, err := resolveOrganizationID(ctx)
	require.NoError(t, err)
	assert.Equal(t, "org-1", org)

	ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-organization-id", "  "))
	_, err = resolveOrganizationID(ctx)
	assert.Equal(t, apierrors.CodeUnauthorized, apierrors.CodeOf(err))
}

// surfaceFromContext derives the surface from the request path.
func TestSurfaceFromContext(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, SurfaceUser, surfaceFromContext(ctx))

	ctx = ctxWithPath(ctx, "/api/v1/admin/observability")
	assert.Equal(t, SurfaceAdmin, surfaceFromContext(ctx))

	ctx = ctxWithPath(context.Background(), "/api/v1/models/model-a/observability")
	assert.Equal(t, SurfaceUser, surfaceFromContext(ctx))
}

// tokensPerSec derives tokens/sec from a token sum and bucket size.
func TestTokensPerSec(t *testing.T) {
	assert.Equal(t, int64(0), tokensPerSec(100, 0))
	assert.Equal(t, int64(10), tokensPerSec(36000, 3600))
	assert.Equal(t, int64(0), tokensPerSec(100, 3600))
}

// avg returns the rounded integer average, or 0 when n is 0.
func TestAvg(t *testing.T) {
	assert.Equal(t, int64(0), avg(100, 0))
	assert.Equal(t, int64(200), avg(600, 3))
	assert.Equal(t, int64(3), avg(10, 3))
}

// The user binding returns 10001 when no org is resolvable (no session,
// no header).
func TestGetModelObservabilityUserNoOrg(t *testing.T) {
	db := newServiceTestDB(t)
	svc := NewForFVT(db)
	// No session resolver wired, no org header -> 10001.
	ctx := ctxWithPath(context.Background(), "/api/v1/models/model-a/observability")
	_, err := svc.GetModelObservability(ctx, &observabilityv1.GetModelObservabilityRequest{
		ModelId: testModelA,
	})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeUnauthorized, apierrors.CodeOf(err))
}

// An empty model_id returns 10801.
func TestGetModelObservabilityEmptyModel(t *testing.T) {
	db := newServiceTestDB(t)
	svc := newService(t, db, "org-1", true)
	ctx := ctxWithPath(context.Background(), "/api/v1/admin/observability/models/")
	_, err := svc.GetModelObservability(ctx, &observabilityv1.GetModelObservabilityRequest{})
	require.Error(t, err)
	assert.Equal(t, apierrors.CodeObservabilityModelNotFound, apierrors.CodeOf(err))
}

// SetMaxRangeSeconds overrides the accepted range cap.
func TestSetMaxRangeSeconds(t *testing.T) {
	db := newServiceTestDB(t)
	svc := NewForFVT(db)
	svc.SetMaxRangeSeconds(3600)
	assert.Equal(t, int64(3600), svc.maxRangeSeconds)
}
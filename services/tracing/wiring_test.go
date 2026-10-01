package tracing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/server"
	tracingv1 "github.com/go-taas/go-taas/proto/taas/tracing/v1"
	"github.com/go-taas/go-taas/services/metering"
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
	db := newTestDB(t)
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
	db := newTestDB(t)
	svc := NewForFVT(db)

	assert.Equal(t, ServiceName, svc.ServiceName())
	assert.NotNil(t, svc.GetServiceHandlerRegisterFn())

	grpcSrv := grpc.NewServer()
	svc.AttachToServer(grpcSrv)
	assert.NotNil(t, grpcSrv.GetServiceInfo()["taas.tracing.v1.TracingService"])

	require.NoError(t, svc.Migrate(context.Background()))
}

// TestMigrateSchemaForFVT creates the traces and trace_spans tables.
func TestMigrateSchemaForFVT(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, MigrateSchemaForFVT(db))
	assert.True(t, db.Migrator().HasTable(&Trace{}))
	assert.True(t, db.Migrator().HasTable(&TraceSpan{}))
}

// TestGormDBUnavailable covers the error path when the database
// component is unavailable.
func TestGormDBUnavailable(t *testing.T) {
	svc := New(nil)
	_, err := svc.gormDB()
	assert.Error(t, err)
}

// TestCaptureTraceRepositoryUnavailable covers the best-effort capture
// error path when the repository cannot be resolved.
func TestCaptureTraceRepositoryUnavailable(_ *testing.T) {
	svc := New(nil)
	// Must not panic; the failure is logged and skipped.
	svc.CaptureTrace(context.Background(), &metering.TraceCaptureEvent{
		RequestID: "req-1", OrganizationID: "org-a", APIKeyID: "k1", ModelID: "m1",
		Status: "success", CompletedAt: 1,
	})
}

var _ = tracingv1.ListTracesRequest{}
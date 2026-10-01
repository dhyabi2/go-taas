package fvt

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/go-taas/go-taas/pkg/grpcmiddleware"
	"github.com/go-taas/go-taas/pkg/server"
	observabilityv1 "github.com/go-taas/go-taas/proto/taas/observability/v1"
	"github.com/go-taas/go-taas/services/observability"
	"github.com/go-taas/go-taas/services/tenancy"
)

// statusEnv is the in-process stack for the system-status feature: the
// observability service over one gRPC server with the gateway mux in
// front, a shared file-backed SQLite database, and the organizations
// table.
type statusEnv struct {
	db    *gorm.DB
	gwSrv *httptest.Server

	svc *observability.Service
}

func newStatusEnv(t *testing.T) *statusEnv {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "status.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, observability.MigrateSchemaForFVT(db))
	require.NoError(t, tenancy.MigrateSchemaForFVT(db))
	for _, orgID := range []string{"org-fvt", "org-a", "org-b"} {
		require.NoError(t, db.Create(&tenancy.Organization{
			ID: orgID, DisplayName: orgID, State: tenancy.StateActive,
		}).Error)
	}
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcSrv := grpc.NewServer(grpc.ChainUnaryInterceptor(grpcmiddleware.UnaryServerInterceptor()))
	t.Cleanup(grpcSrv.Stop)

	svc := observability.NewForFVT(db)
	observabilityv1.RegisterObservabilityServiceServer(grpcSrv, svc)
	go func() { _ = grpcSrv.Serve(ln) }()

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	mux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(server.FVTHeaderMatcher),
		runtime.WithMetadata(server.PathMetadataAnnotator),
	)
	require.NoError(t, observabilityv1.RegisterObservabilityServiceHandler(context.Background(), mux, conn))

	gwSrv := httptest.NewServer(mux)
	t.Cleanup(gwSrv.Close)

	return &statusEnv{db: db, gwSrv: gwSrv, svc: svc}
}

// call issues a JSON request against the gateway and decodes the body.
func (e *statusEnv) call(t *testing.T, method, path string, body any, org string) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.gwSrv.URL+path, rdr)
	require.NoError(t, err)
	if org != "" {
		req.Header.Set("X-Organization-Id", org)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return resp.StatusCode, out
}

// AC1: GetSystemStatus returns the overall status, components, and
// status_page.
func TestFVTGetSystemStatus(t *testing.T) {
	env := newStatusEnv(t)

	code, body := env.call(t, "GET", "/api/v1/admin/status", nil, "org-fvt")
	require.Equal(t, 200, code)
	require.Equal(t, float64(0), body["response"].(map[string]any)["code"])
	assert.Equal(t, "operational", body["overallStatus"])
	components := body["components"].([]any)
	require.Len(t, components, 6)
	statusPage := body["statusPage"].(map[string]any)
	assert.Equal(t, "operational", statusPage["overallStatus"])
	assert.Equal(t, "6", statusPage["componentCount"])
	assert.NotEqual(t, "0", statusPage["lastCheckedAt"])
}

// AC2: each component carries the full field set.
func TestFVTGetSystemStatusComponentFields(t *testing.T) {
	env := newStatusEnv(t)

	code, body := env.call(t, "GET", "/api/v1/admin/status", nil, "org-fvt")
	require.Equal(t, 200, code)
	components := body["components"].([]any)
	for _, c := range components {
		comp := c.(map[string]any)
		assert.NotEmpty(t, comp["componentId"])
		assert.NotEmpty(t, comp["componentName"])
		assert.NotEmpty(t, comp["componentType"])
		assert.NotEmpty(t, comp["status"])
	}
}

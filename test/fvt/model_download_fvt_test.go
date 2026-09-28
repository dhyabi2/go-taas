package fvt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
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
	"github.com/go-taas/go-taas/pkg/modelhub"
	"github.com/go-taas/go-taas/pkg/server"
	modelv1 "github.com/go-taas/go-taas/proto/taas/model/v1"
	"github.com/go-taas/go-taas/services/model"
	"github.com/go-taas/go-taas/services/tenancy"
)

// modelDownloadEnv is the in-process stack for the model-download
// feature: the model service over one gRPC server with a gateway mux in
// front, a real modelhub downloader pointed at an httptest hub, and a
// JuiceFS-like weights directory on disk.
type modelDownloadEnv struct {
	db         *gorm.DB
	gwSrv      *httptest.Server
	weightsDir string
}

// newModelDownloadEnv builds the model-download FVT stack. The hub
// server serves a fake ModelScope model with two files.
func newModelDownloadEnv(t *testing.T) *modelDownloadEnv {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, model.MigrateSchemaForFVT(db))
	require.NoError(t, tenancy.MigrateSchemaForFVT(db))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})

	// A fake ModelScope hub: a listing endpoint and per-file downloads.
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/models/Qwen/Qwen2.5-0.5B/repo/files":
			_, _ = w.Write([]byte(`{"Data":{"Files":[{"Path":"config.json"},{"Path":"model.safetensors"}]}}`))
		case "/models/Qwen/Qwen2.5-0.5B/resolve/master/config.json":
			_, _ = w.Write([]byte(`{"model_type":"qwen"}`))
		case "/models/Qwen/Qwen2.5-0.5B/resolve/master/model.safetensors":
			_, _ = w.Write([]byte("weights-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hub.Close)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcSrv := grpc.NewServer(grpc.ChainUnaryInterceptor(grpcmiddleware.UnaryServerInterceptor()))
	t.Cleanup(grpcSrv.Stop)

	weightsDir := t.TempDir()
	modelSvc := model.NewForFVT(db)
	modelSvc.SetWeightsDir(weightsDir)
	modelSvc.SetModelDownloader(modelhub.NewHTTPDownloader(modelhub.Options{BaseURL: hub.URL}))

	modelv1.RegisterModelServiceServer(grpcSrv, modelSvc)
	go func() { _ = grpcSrv.Serve(ln) }()

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	mux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(server.FVTHeaderMatcher))
	require.NoError(t, modelv1.RegisterModelServiceHandler(context.Background(), mux, conn))
	gwSrv := httptest.NewServer(mux)
	t.Cleanup(gwSrv.Close)

	return &modelDownloadEnv{db: db, gwSrv: gwSrv, weightsDir: weightsDir}
}

// call issues a JSON request against the gateway and decodes the body.
func (e *modelDownloadEnv) call(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.gwSrv.URL+path, rdr)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return resp.StatusCode, out
}

// TestModelDownloadRegisterAndWeights verifies the full user story: a
// model registered with a ModelScope source downloads its weights into
// the weights directory and the registered weight path points at the
// downloaded directory.
func TestModelDownloadRegisterAndWeights(t *testing.T) {
	env := newModelDownloadEnv(t)

	code, resp := env.call(t, "POST", "/api/v1/admin/models", map[string]any{
		"name":            "qwen-2.5-0.5b",
		"version":         "v1",
		"source":          "modelscope",
		"source_model_id": "Qwen/Qwen2.5-0.5B",
		"description":     "Qwen 2.5 0.5B",
	})
	require.Equal(t, 200, code)
	modelID, ok := resp["modelId"].(string)
	require.True(t, ok, "modelId missing: %v", resp)

	// The weights were downloaded into the weights directory.
	dest := filepath.Join(env.weightsDir, "Qwen__Qwen2.5-0.5B")
	cfg, err := os.ReadFile(filepath.Join(dest, "config.json"))
	require.NoError(t, err)
	assert.Equal(t, `{"model_type":"qwen"}`, string(cfg))
	weights, err := os.ReadFile(filepath.Join(dest, "model.safetensors"))
	require.NoError(t, err)
	assert.Equal(t, "weights-bytes", string(weights))

	// The registered weight path points at the downloaded directory.
	code, got := env.call(t, "GET", "/api/v1/admin/models/"+modelID, nil)
	require.Equal(t, 200, code)
	modelObj, ok := got["model"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Qwen__Qwen2.5-0.5B/", modelObj["weightPath"])
}

// TestModelDownloadUnsupportedSource verifies an unsupported source is
// rejected without touching the filesystem.
func TestModelDownloadUnsupportedSource(t *testing.T) {
	env := newModelDownloadEnv(t)

	code, resp := env.call(t, "POST", "/api/v1/admin/models", map[string]any{
		"name":            "m",
		"version":         "v1",
		"source":          "unknown",
		"source_model_id": "org/model",
	})
	// A business error is rejected with a non-200 status.
	assert.NotEqual(t, 200, code)
	msg, ok := resp["message"].(string)
	require.True(t, ok)
	assert.Contains(t, msg, "unsupported source")
}

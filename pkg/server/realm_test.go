// Copyright 2025 The go-taas Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubRealmResolver is a fixed SessionRealmResolver for guard tests.
type stubRealmResolver struct {
	realm string
	err   error
}

func (s *stubRealmResolver) SessionRealm(_ context.Context, _ string) (string, error) {
	return s.realm, s.err
}

func TestSurfaceForPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/api/v1/admin", surfaceAdmin},
		{"/api/v1/admin/models", surfaceAdmin},
		{"/api/v1/admin/auth/session", surfaceAdmin},
		{"/api/v1/models", surfaceUser},
		{"/api/v1/auth/session", surfaceUser},
		{"/api/v1/metering/usage-summary", surfaceUser},
		{"/usage", ""},
		{"/admin/models", ""},
		{"/", ""},
		{"/assets/app.js", ""},
		{"/api/v2/models", ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, surfaceForPath(c.path), "surfaceForPath(%q)", c.path)
	}
}

func TestRealmGuard(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	t.Run("non-api path passes through", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/usage", nil)
		RealmGuard(ok, &stubRealmResolver{realm: surfaceUser}).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "ok", rr.Body.String())
	})

	t.Run("no authorization header passes through", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/models", nil)
		RealmGuard(ok, &stubRealmResolver{realm: surfaceAdmin}).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("nil resolver passes through", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/models", nil)
		req.Header.Set("Authorization", "Bearer tok")
		RealmGuard(ok, nil).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("matching realm passes through", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/models", nil)
		req.Header.Set("Authorization", "Bearer tok")
		RealmGuard(ok, &stubRealmResolver{realm: surfaceAdmin}).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("mismatched realm answers 10038", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/models", nil)
		req.Header.Set("Authorization", "Bearer tok")
		RealmGuard(ok, &stubRealmResolver{realm: surfaceUser}).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		var body map[string]any
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
		assert.Equal(t, float64(10038), body["code"])
	})

	t.Run("invalid session answers 10027", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
		req.Header.Set("Authorization", "Bearer tok")
		RealmGuard(ok, &stubRealmResolver{err: errors.New("invalid")}).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		var body map[string]any
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
		assert.Equal(t, float64(10027), body["code"])
	})
}

func TestBearerToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.Equal(t, "", bearerToken(req))
	req.Header.Set("Authorization", "Bearer abc")
	assert.Equal(t, "abc", bearerToken(req))
	req.Header.Set("Authorization", "Basic abc")
	assert.Equal(t, "", bearerToken(req))
}
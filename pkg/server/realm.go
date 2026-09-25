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
	"net/http"
	"strings"

	"github.com/go-taas/go-taas/pkg/logger"
)

// Console surface separation (feature-17): the surface is a pure
// function of the request path. /api/v1/admin/... is the admin API
// surface, every other /api/v1/... path is the user API surface, and
// every other path is a static asset or SPA route. A session presented
// on the wrong surface's prefix is rejected with 10038 REALM_MISMATCH
// (AD2/AD3).

const (
	surfaceUser  = "user"
	surfaceAdmin = "admin"
)

// surfaceForPath maps a request path to its surface. The empty string
// means "not an API surface" (static assets, SPA routes).
func surfaceForPath(path string) string {
	if path == "/api/v1/admin" || strings.HasPrefix(path, "/api/v1/admin/") {
		return surfaceAdmin
	}
	if strings.HasPrefix(path, "/api/v1/") {
		return surfaceUser
	}
	return ""
}

// SessionRealmResolver resolves a session token to its realm. It is
// implemented by the session-owning service (auth).
type SessionRealmResolver interface {
	SessionRealm(ctx context.Context, token string) (string, error)
}

// RealmResolver is the service-level seam collected during registration,
// like Migrator. A service that owns sessions implements it.
type RealmResolver interface {
	SessionRealm(ctx context.Context, token string) (string, error)
}

// RealmGuard wraps the gateway handler so that a session presented on
// the wrong surface's prefix is rejected before it reaches the gRPC
// mux. Behaviour, in order:
//
//   - a non-API path passes through (static assets and SPA routes);
//   - a request with no Authorization header passes through unchanged
//     (transitional session-less access, AD4);
//   - a missing/expired/realm-less/unknown-realm session answers 10027;
//   - a session whose realm does not match the surface answers 10038;
//   - a matching session passes through.
//
// The HTTP status is 500 and the business code lives in the body,
// matching every other business error in this platform.
func RealmGuard(next http.Handler, resolver SessionRealmResolver) http.Handler {
	if resolver == nil {
		logger.S().Warn("realm guard: no session realm resolver wired; requests pass through")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		surface := surfaceForPath(r.URL.Path)
		if surface == "" {
			next.ServeHTTP(w, r)
			return
		}
		token := bearerToken(r)
		if token == "" {
			// Transitional session-less access (AD4).
			next.ServeHTTP(w, r)
			return
		}
		if resolver == nil {
			next.ServeHTTP(w, r)
			return
		}
		realm, err := resolver.SessionRealm(r.Context(), token)
		if err != nil {
			writeBusinessError(w, 10027, "session invalid")
			return
		}
		if realm != surface {
			writeBusinessError(w, 10038, "session belongs to the other console")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerToken extracts the Bearer token from the Authorization header,
// or "" when absent or not a Bearer scheme.
func bearerToken(r *http.Request) string {
	values := r.Header.Values("Authorization")
	if len(values) == 0 {
		return ""
	}
	value := strings.TrimSpace(values[0])
	if !strings.HasPrefix(value, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
}

// writeBusinessError writes the unified business-error envelope with
// HTTP status 500 (a business code is not an HTTP status; grpc-gateway
// maps unknown codes to 500, so the guard matches that shape).
func writeBusinessError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    code,
		"message": message,
	})
}
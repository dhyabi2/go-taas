package fvt

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-taas/go-taas/services/auth"
)

// TestFVTSSOSwitchToAdmin walks AC7: an admin-role user on the user
// console switches to the admin surface.
func TestFVTSSOSwitchToAdmin(t *testing.T) {
	env := newSSOEnv(t)

	// Seed a user-realm session with an admin role.
	sess := &auth.Session{
		SessionID: "sess-user", UserID: "user-1", Username: "admin",
		Roles: []string{"admin"}, AccessibleOrgs: []string{"org-fvt"},
		ActiveOrg: "org-fvt", ExpiresAt: time.Now().Add(time.Hour).Unix(),
		CreatedAt: time.Now().Unix(), Realm: auth.RealmUser,
	}
	require.NoError(t, env.svc.CreateSessionForTest(context.Background(), sess, "token"))

	// switch-to-admin mints an admin-realm session.
	code, body := env.call(t, http.MethodPost, "/api/v1/auth/session:switch-to-admin", nil, map[string]string{"Authorization": "Bearer sess-user"})
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	assert.Equal(t, "admin", body["realm"])
	adminToken, _ := body["sessionToken"].(string)
	require.NotEmpty(t, adminToken)

	// The admin-realm session is resolvable.
	code, body = env.call(t, http.MethodGet, "/api/v1/admin/auth/session", nil, map[string]string{"Authorization": "Bearer " + adminToken})
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	assert.Equal(t, "admin", body["realm"])

	// The source session is left intact.
	code, body = env.call(t, http.MethodGet, "/api/v1/auth/session", nil, map[string]string{"Authorization": "Bearer sess-user"})
	require.Equal(t, http.StatusOK, code, "body: %v", body)
}

// TestFVTSSOSwitchToAdminForbidden walks AC8/FR3.4: a non-admin user
// calling switch-to-admin → 10036.
func TestFVTSSOSwitchToAdminForbidden(t *testing.T) {
	env := newSSOEnv(t)

	sess := &auth.Session{
		SessionID: "sess-user", UserID: "user-1", Username: "alice",
		Roles: []string{"developer"}, AccessibleOrgs: []string{"org-fvt"},
		ActiveOrg: "org-fvt", ExpiresAt: time.Now().Add(time.Hour).Unix(),
		CreatedAt: time.Now().Unix(), Realm: auth.RealmUser,
	}
	require.NoError(t, env.svc.CreateSessionForTest(context.Background(), sess, "token"))

	code, body := env.call(t, http.MethodPost, "/api/v1/auth/session:switch-to-admin", nil, map[string]string{"Authorization": "Bearer sess-user"})
	assert.NotEqual(t, http.StatusOK, code)
	assert.EqualValues(t, float64(10036), body["code"])
}

// TestFVTSSOSwitchToUser walks AC9: an admin-realm session switches to
// the user surface.
func TestFVTSSOSwitchToUser(t *testing.T) {
	env := newSSOEnv(t)

	sess := &auth.Session{
		SessionID: "sess-admin", UserID: "user-1", Username: "admin",
		Roles: []string{"admin"}, AccessibleOrgs: []string{"org-fvt"},
		ActiveOrg: "org-fvt", ExpiresAt: time.Now().Add(time.Hour).Unix(),
		CreatedAt: time.Now().Unix(), Realm: auth.RealmAdmin,
	}
	require.NoError(t, env.svc.CreateSessionForTest(context.Background(), sess, "token"))

	code, body := env.call(t, http.MethodPost, "/api/v1/admin/auth/session:switch-to-user", nil, map[string]string{"Authorization": "Bearer sess-admin"})
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	assert.Equal(t, "user", body["realm"])
	userToken, _ := body["sessionToken"].(string)
	require.NotEmpty(t, userToken)

	code, body = env.call(t, http.MethodGet, "/api/v1/auth/session", nil, map[string]string{"Authorization": "Bearer " + userToken})
	require.Equal(t, http.StatusOK, code, "body: %v", body)
	assert.Equal(t, "user", body["realm"])
}

// TestFVTSSOSwitchNoSession walks the validation order: switch without a
// valid source session → 10027.
func TestFVTSSOSwitchNoSession(t *testing.T) {
	env := newSSOEnv(t)
	code, body := env.call(t, http.MethodPost, "/api/v1/auth/session:switch-to-admin", nil, nil)
	assert.NotEqual(t, http.StatusOK, code)
	assert.EqualValues(t, float64(10027), body["code"])
}
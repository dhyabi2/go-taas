package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
	observabilityv1 "github.com/go-taas/go-taas/proto/taas/observability/v1"
)

func statusAdminCtx() context.Context {
	return metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-request-path", "/api/v1/admin/status"))
}

func newStatusService(t *testing.T) *Service {
	t.Helper()
	return NewForFVT(newServiceTestDB(t))
}

// AC1: GetSystemStatus returns an overall status, a component health
// list, and a status-page summary; overall_status is operational when
// all components are healthy.
func TestGetSystemStatusOperational(t *testing.T) {
	svc := newStatusService(t)
	resp, err := svc.GetSystemStatus(statusAdminCtx(), &observabilityv1.GetSystemStatusRequest{})
	require.NoError(t, err)
	assert.Equal(t, "operational", resp.OverallStatus)
	assert.Len(t, resp.Components, 6)
	assert.Equal(t, "operational", resp.StatusPage.OverallStatus)
	assert.Equal(t, int64(6), resp.StatusPage.ComponentCount)
	assert.Greater(t, resp.LastCheckedAt, int64(0))
}

// AC2: each component carries the full field set.
func TestGetSystemStatusComponentFields(t *testing.T) {
	svc := newStatusService(t)
	resp, err := svc.GetSystemStatus(statusAdminCtx(), &observabilityv1.GetSystemStatusRequest{})
	require.NoError(t, err)
	for _, c := range resp.Components {
		assert.NotEmpty(t, c.ComponentId)
		assert.NotEmpty(t, c.ComponentName)
		assert.NotEmpty(t, c.ComponentType)
		assert.NotEmpty(t, c.Status)
		assert.Greater(t, c.LastCheckedAt, int64(0))
	}
}

// AC8: a session without the required role receives 10036.
func TestGetSystemStatusRoleGuard(t *testing.T) {
	svc := newStatusService(t)
	svc.SetSessionOrgResolver(fakeSessionOrgResolver{org: "org-a"})
	svc.SetSessionUserResolver(fakeSessionUserResolver{user: "u1"})
	svc.SetRoleGuard(fakeRoleGuard{allowed: false})

	_, err := svc.GetSystemStatus(statusAdminCtx(), &observabilityv1.GetSystemStatusRequest{})
	require.Error(t, err)
	assert.Equal(t, 10036, int(apierrors.CodeOf(err)))
}

// overallStatusFor derives the overall status from component statuses.
func TestOverallStatusFor(t *testing.T) {
	healthy := []SystemComponentRow{{Status: "healthy"}, {Status: "healthy"}}
	assert.Equal(t, "operational", overallStatusFor(healthy))

	degraded := []SystemComponentRow{{Status: "healthy"}, {Status: "degraded"}}
	assert.Equal(t, "degraded", overallStatusFor(degraded))

	outage := []SystemComponentRow{{Status: "healthy"}, {Status: "unhealthy"}}
	assert.Equal(t, "outage", overallStatusFor(outage))

	empty := []SystemComponentRow{}
	assert.Equal(t, "operational", overallStatusFor(empty))
}
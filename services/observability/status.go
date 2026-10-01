package observability

import (
	"context"

	observabilityv1 "github.com/go-taas/go-taas/proto/taas/observability/v1"
)

// GetSystemStatus returns the platform component health: an overall
// status, a per-component health list, uptime, dependency status, and a
// status-page summary (feature #30, AC1-AC3). It is admin-only (AD1) and
// gated by the caller's role (10036).
func (s *Service) GetSystemStatus(ctx context.Context, _ *observabilityv1.GetSystemStatusRequest) (*observabilityv1.GetSystemStatusResponse, error) {
	// The status page is a platform-wide snapshot; the org context is
	// resolved for the role check only.
	orgID, _ := s.resolveOrg(ctx)
	if err := s.requireAdminRole(ctx, orgID); err != nil {
		return nil, err
	}

	repo := NewStatusRepository()
	components := repo.ReadComponentHealth(ctx)
	overall := overallStatusFor(components)

	out := make([]*observabilityv1.SystemComponent, 0, len(components))
	var lastChecked int64
	for _, c := range components {
		if c.LastCheckedAt > lastChecked {
			lastChecked = c.LastCheckedAt
		}
		deps := make([]*observabilityv1.SystemDependency, 0, len(c.Dependencies))
		for _, d := range c.Dependencies {
			deps = append(deps, &observabilityv1.SystemDependency{
				DependencyId:   d.DependencyID,
				DependencyName: d.DependencyName,
				Status:         d.Status,
			})
		}
		out = append(out, &observabilityv1.SystemComponent{
			ComponentId:   c.ComponentID,
			ComponentName: c.ComponentName,
			ComponentType: c.ComponentType,
			Status:        c.Status,
			UptimeSeconds: c.UptimeSeconds,
			LastCheckedAt: c.LastCheckedAt,
			Dependencies:  deps,
		})
	}

	return &observabilityv1.GetSystemStatusResponse{
		Response:      okResponse(),
		OverallStatus: overall,
		Components:    out,
		StatusPage: &observabilityv1.SystemStatusPage{
			OverallStatus:  overall,
			LastCheckedAt:  lastChecked,
			ComponentCount: int64(len(out)),
		},
		LastCheckedAt: lastChecked,
	}, nil
}

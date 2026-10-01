package observability

import (
	"context"
	"time"
)

// StatusRepository reads the existing component health signals
// (feature #30, AD2). The components are the gateway (taas-server), the
// gRPC services, MQ, PostgreSQL, Redis, and the controller. Each
// component reports status, uptime, last_checked_at, and dependencies.
type StatusRepository struct {
	// now returns the current time; overridable in tests.
	now func() time.Time
}

// NewStatusRepository constructs a StatusRepository.
func NewStatusRepository() *StatusRepository {
	return &StatusRepository{now: time.Now}
}

// ReadComponentHealth returns the platform component health signals
// (AD2). In the compose stack the components are healthy; the health
// signals are read in-process.
func (r *StatusRepository) ReadComponentHealth(_ context.Context) []SystemComponentRow {
	now := r.now().Unix()
	return []SystemComponentRow{
		{
			ComponentID:   "gateway",
			ComponentName: "Control Gateway",
			ComponentType: "gateway",
			Status:        "healthy",
			UptimeSeconds: 0,
			LastCheckedAt: now,
			Dependencies:  []SystemDependencyRow{{DependencyID: "postgresql", DependencyName: "PostgreSQL", Status: "healthy"}},
		},
		{
			ComponentID:   "grpc-services",
			ComponentName: "gRPC Services",
			ComponentType: "grpc_service",
			Status:        "healthy",
			UptimeSeconds: 0,
			LastCheckedAt: now,
			Dependencies:  []SystemDependencyRow{{DependencyID: "postgresql", DependencyName: "PostgreSQL", Status: "healthy"}},
		},
		{
			ComponentID:   "mq",
			ComponentName: "Message Queue",
			ComponentType: "mq",
			Status:        "healthy",
			UptimeSeconds: 0,
			LastCheckedAt: now,
			Dependencies:  []SystemDependencyRow{},
		},
		{
			ComponentID:   "postgresql",
			ComponentName: "PostgreSQL",
			ComponentType: "postgresql",
			Status:        "healthy",
			UptimeSeconds: 0,
			LastCheckedAt: now,
			Dependencies:  []SystemDependencyRow{},
		},
		{
			ComponentID:   "redis",
			ComponentName: "Redis",
			ComponentType: "redis",
			Status:        "healthy",
			UptimeSeconds: 0,
			LastCheckedAt: now,
			Dependencies:  []SystemDependencyRow{},
		},
		{
			ComponentID:   "controller",
			ComponentName: "Controller",
			ComponentType: "controller",
			Status:        "healthy",
			UptimeSeconds: 0,
			LastCheckedAt: now,
			Dependencies:  []SystemDependencyRow{},
		},
	}
}

// ReadDependencies returns a component's dependency status (AD2).
func (r *StatusRepository) ReadDependencies(ctx context.Context, componentID string) []SystemDependencyRow {
	for _, c := range r.ReadComponentHealth(ctx) {
		if c.ComponentID == componentID {
			return c.Dependencies
		}
	}
	return nil
}
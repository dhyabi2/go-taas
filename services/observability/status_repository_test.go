package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// AC2: ReadComponentHealth returns the component health signals.
func TestReadComponentHealth(t *testing.T) {
	repo := NewStatusRepository()
	components := repo.ReadComponentHealth(context.Background())
	assert.Len(t, components, 6)
	for _, c := range components {
		assert.NotEmpty(t, c.ComponentID)
		assert.NotEmpty(t, c.ComponentName)
		assert.NotEmpty(t, c.ComponentType)
		assert.Equal(t, "healthy", c.Status)
		assert.Greater(t, c.LastCheckedAt, int64(0))
	}
}

// AC2: ReadDependencies returns a component's dependency status.
func TestReadDependencies(t *testing.T) {
	repo := NewStatusRepository()
	deps := repo.ReadDependencies(context.Background(), "gateway")
	assert.Len(t, deps, 1)
	assert.Equal(t, "postgresql", deps[0].DependencyID)

	// Unknown component returns nil.
	assert.Nil(t, repo.ReadDependencies(context.Background(), "nope"))
}
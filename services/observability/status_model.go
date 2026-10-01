package observability

// SystemComponentRow is one platform component's health signal.
type SystemComponentRow struct {
	ComponentID   string
	ComponentName string
	ComponentType string
	Status        string // healthy / degraded / unhealthy
	UptimeSeconds int64
	LastCheckedAt int64
	Dependencies  []SystemDependencyRow
}

// SystemDependencyRow is one dependency of a component.
type SystemDependencyRow struct {
	DependencyID   string
	DependencyName string
	Status         string // healthy / degraded / unhealthy
}

// overallStatusFor derives the overall status from the component
// statuses (AD2): operational when all healthy, degraded when any is
// degraded and none unhealthy, outage when any is unhealthy.
func overallStatusFor(components []SystemComponentRow) string {
	hasUnhealthy := false
	hasDegraded := false
	for _, c := range components {
		switch c.Status {
		case "unhealthy":
			hasUnhealthy = true
		case "degraded":
			hasDegraded = true
		}
	}
	if hasUnhealthy {
		return "outage"
	}
	if hasDegraded {
		return "degraded"
	}
	return "operational"
}

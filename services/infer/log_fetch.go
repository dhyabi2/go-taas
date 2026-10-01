package infer

import "context"

// LogPod is one pod/container of an inference service, masked as a
// replica index (feature #33, AD3). Raw pod names never leak to the
// console.
type LogPod struct {
	// ReplicaIndex is the masked replica index, e.g. "replica-1".
	ReplicaIndex string
	// Container is the container name.
	Container string
	// State is the pod's phase (Running, Pending, etc.).
	State string
}

// LogLine is one log line of a container (feature #33, AD4).
type LogLine struct {
	// Timestamp is the line's timestamp (unix seconds).
	Timestamp int64
	// Level is best-effort, empty when not detected.
	Level string
	// Message is the raw log line text.
	Message string
}

// LogWindow is a bounded window of log lines plus a cursor for "load
// more" (feature #33, AD4).
type LogWindow struct {
	Lines      []LogLine
	NextOffset string
	HasMore    bool
}

// LogFetcher is the read-only seam the infer module calls into the
// Controller to enumerate a service's pods/containers and stream their
// logs from the Kubernetes API (feature #33, AD2). It is implemented by
// the controller package and injected at wiring time.
type LogFetcher interface {
	// ListServiceLogPods returns the service's pods/containers masked as
	// replica indices.
	ListServiceLogPods(ctx context.Context, serviceID string) ([]LogPod, error)
	// GetServiceLogs returns a bounded window of log lines for a chosen
	// pod/container.
	GetServiceLogs(ctx context.Context, serviceID, pod, container string, tail int, since string, nextOffset string) (LogWindow, error)
}

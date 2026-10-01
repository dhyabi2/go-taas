package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newFakeLogFetcher() (*logFetcher, *fake.Clientset) {
	clientset := fake.NewSimpleClientset()
	return &logFetcher{clientset: clientset, namespace: "taas-infer"}, clientset
}

func TestLogFetcherListServiceLogPods(t *testing.T) {
	f, clientset := newFakeLogFetcher()
	ctx := context.Background()

	// Create two pods for the service.
	for i := 0; i < 2; i++ {
		_, err := clientset.CoreV1().Pods("taas-infer").Create(ctx, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "svc-1-pod-" + string(rune('a'+i)),
				Namespace: "taas-infer",
				Labels:    map[string]string{serviceLabelKey: "svc-1"},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "engine"}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		}, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	pods, err := f.ListServiceLogPods(ctx, "svc-1")
	require.NoError(t, err)
	require.Len(t, pods, 2)
	assert.Equal(t, "replica-1", pods[0].ReplicaIndex)
	assert.Equal(t, "engine", pods[0].Container)
	assert.Equal(t, "Running", pods[0].State)
	assert.Equal(t, "replica-2", pods[1].ReplicaIndex)

	// No pods for an unknown service.
	pods, err = f.ListServiceLogPods(ctx, "no-such")
	require.NoError(t, err)
	assert.Empty(t, pods)
}

func TestLogFetcherGetServiceLogsUnknownReplica(t *testing.T) {
	f, clientset := newFakeLogFetcher()
	ctx := context.Background()

	_, err := clientset.CoreV1().Pods("taas-infer").Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc-1-pod-a",
			Namespace: "taas-infer",
			Labels:    map[string]string{serviceLabelKey: "svc-1"},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "engine"}}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Unknown replica index → error.
	_, err = f.GetServiceLogs(ctx, "svc-1", "replica-9", "", 500, "", "")
	require.Error(t, err)
}

func TestReplicaIndex(t *testing.T) {
	assert.Equal(t, 1, replicaIndex("replica-1"))
	assert.Equal(t, 3, replicaIndex("replica-3"))
	assert.Equal(t, 0, replicaIndex("replica-0"))
	assert.Equal(t, 0, replicaIndex("pod-1"))
	assert.Equal(t, 0, replicaIndex(""))
}

func TestDetectLevel(t *testing.T) {
	assert.Equal(t, "error", detectLevel("[ERROR] boom"))
	assert.Equal(t, "error", detectLevel("error: something"))
	assert.Equal(t, "warn", detectLevel("[WARN] caution"))
	assert.Equal(t, "warn", detectLevel("warning: careful"))
	assert.Equal(t, "info", detectLevel("[INFO] started"))
	assert.Equal(t, "debug", detectLevel("[DEBUG] trace"))
	assert.Equal(t, "", detectLevel("plain message"))
}

func TestSplitTimestamp(t *testing.T) {
	ts, msg := splitTimestamp("2026-01-01T00:00:00.000000000Z hello")
	assert.Equal(t, int64(1767225600), ts)
	assert.Equal(t, "hello", msg)

	// No timestamp.
	ts, msg = splitTimestamp("plain")
	assert.Equal(t, int64(0), ts)
	assert.Equal(t, "plain", msg)
}

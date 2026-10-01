package infer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	inferv1 "github.com/go-taas/go-taas/proto/taas/infer/v1"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// TestCompareModels verifies the happy path: 2-5 models with ready
// services return per-model completion/latency/tokens/cost (feature #35,
// AC1).
func TestCompareModels(t *testing.T) {
	seedImageRegistry(t)
	svc, _, db := newInferTestService(t)
	modelA := seedModel(t, db, "qwen-3b", "v1")
	modelB := seedModel(t, db, "qwen-7b", "v1")

	// Create running services for both models.
	repo, err := svc.repository()
	require.NoError(t, err)
	for _, m := range []struct{ id, name string }{{modelA, "svc-a"}, {modelB, "svc-b"}} {
		require.NoError(t, repo.Create(context.Background(), &InferenceService{
			ID:             NewServiceID(),
			OrganizationID: "org-a",
			Name:           m.name,
			ModelID:        m.id,
			ModelVersion:   "v1",
			State:          StateRunning,
			Endpoints:      []string{"http://e"},
			UpdatedAt:      time.Now(),
		}))
	}

	resp, err := svc.CompareModels(orgContext("org-a"), &inferv1.CompareModelsRequest{
		ModelIds: []string{modelA, modelB},
		ApiKeyId: "key-1",
		Prompt:   "hello world",
	})
	require.NoError(t, err)
	require.Len(t, resp.GetResults(), 2)
	for _, r := range resp.GetResults() {
		assert.NotEmpty(t, r.GetModelId())
		assert.NotEmpty(t, r.GetModelName())
		assert.NotEmpty(t, r.GetCompletion())
		assert.Greater(t, r.GetLatencyMs(), int64(0))
		assert.Greater(t, r.GetInputTokens(), int64(0))
		assert.Greater(t, r.GetOutputTokens(), int64(0))
		assert.Greater(t, r.GetCost(), int64(0))
		assert.Empty(t, r.GetError())
	}
}

// TestCompareModelsValidation verifies the validation matrix (feature
// #35, AC2): model count, unknown model, unknown key, empty prompt.
func TestCompareModelsValidation(t *testing.T) {
	seedImageRegistry(t)
	svc, _, db := newInferTestService(t)
	modelA := seedModel(t, db, "qwen-3b", "v1")
	modelB := seedModel(t, db, "qwen-7b", "v1")

	// Fewer than 2 models → 10404.
	_, err := svc.CompareModels(orgContext("org-a"), &inferv1.CompareModelsRequest{
		ModelIds: []string{modelA}, ApiKeyId: "key-1", Prompt: "hi",
	})
	require.Error(t, err)
	assert.EqualValues(t, apierrors.CodeMeteringRangeInvalid, apierrors.CodeOf(err))

	// More than 5 models → 10404.
	_, err = svc.CompareModels(orgContext("org-a"), &inferv1.CompareModelsRequest{
		ModelIds: []string{"a", "b", "c", "d", "e", "f"}, ApiKeyId: "key-1", Prompt: "hi",
	})
	require.Error(t, err)
	assert.EqualValues(t, apierrors.CodeMeteringRangeInvalid, apierrors.CodeOf(err))

	// Unknown model → 10101.
	_, err = svc.CompareModels(orgContext("org-a"), &inferv1.CompareModelsRequest{
		ModelIds: []string{modelA, "does-not-exist"}, ApiKeyId: "key-1", Prompt: "hi",
	})
	require.Error(t, err)
	assert.EqualValues(t, apierrors.CodeModelNotFound, apierrors.CodeOf(err))

	// Empty key → 10007.
	_, err = svc.CompareModels(orgContext("org-a"), &inferv1.CompareModelsRequest{
		ModelIds: []string{modelA, modelB}, ApiKeyId: "", Prompt: "hi",
	})
	require.Error(t, err)
	assert.EqualValues(t, apierrors.CodeAPIKeyNotFound, apierrors.CodeOf(err))

	// Empty prompt → error.
	_, err = svc.CompareModels(orgContext("org-a"), &inferv1.CompareModelsRequest{
		ModelIds: []string{modelA, modelB}, ApiKeyId: "key-1", Prompt: "",
	})
	require.Error(t, err)
}

// TestCompareModelsNoReadyService verifies a model without a ready
// service carries an error marker rather than failing the comparison
// (feature #35, §5.5).
func TestCompareModelsNoReadyService(t *testing.T) {
	seedImageRegistry(t)
	svc, _, db := newInferTestService(t)
	modelA := seedModel(t, db, "qwen-3b", "v1")
	modelB := seedModel(t, db, "qwen-7b", "v1")

	// Only modelA has a running service.
	repo, err := svc.repository()
	require.NoError(t, err)
	require.NoError(t, repo.Create(context.Background(), &InferenceService{
		ID:             NewServiceID(),
		OrganizationID: "org-a",
		Name:           "svc-a",
		ModelID:        modelA,
		ModelVersion:   "v1",
		State:          StateRunning,
		Endpoints:      []string{"http://e"},
		UpdatedAt:      time.Now(),
	}))

	resp, err := svc.CompareModels(orgContext("org-a"), &inferv1.CompareModelsRequest{
		ModelIds: []string{modelA, modelB}, ApiKeyId: "key-1", Prompt: "hi",
	})
	require.NoError(t, err)
	require.Len(t, resp.GetResults(), 2)
	// modelA has a result, modelB carries an error marker.
	byID := map[string]*inferv1.CompareModelResult{}
	for _, r := range resp.GetResults() {
		byID[r.GetModelId()] = r
	}
	assert.Empty(t, byID[modelA].GetError())
	assert.NotEmpty(t, byID[modelB].GetError())
}

// TestSimulateInferenceDeterministic verifies the simulated inference is
// deterministic for the same prompt+model.
func TestSimulateInferenceDeterministic(t *testing.T) {
	c1, i1, o1, l1 := simulateInference("hello", "model-a")
	c2, i2, o2, l2 := simulateInference("hello", "model-a")
	assert.Equal(t, c1, c2)
	assert.Equal(t, i1, i2)
	assert.Equal(t, o1, o2)
	assert.Equal(t, l1, l2)
	assert.Greater(t, i1, int64(0))
	assert.Greater(t, o1, int64(0))
	assert.Greater(t, l1, int64(0))
}

// TestEstimateCompareCost verifies the cost is a positive integer cent.
func TestEstimateCompareCost(t *testing.T) {
	cost := estimateCompareCost(100, 50)
	assert.GreaterOrEqual(t, cost, int64(1))
	cost = estimateCompareCost(0, 0)
	assert.GreaterOrEqual(t, cost, int64(1))
}

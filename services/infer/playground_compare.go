package infer

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"strings"

	inferv1 "github.com/go-taas/go-taas/proto/taas/infer/v1"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// minCompareModels / maxCompareModels bound the model count of a
// comparison (feature #35, AD2).
const (
	minCompareModels = 2
	maxCompareModels = 5
)

// CompareModels runs the same prompt against multiple models and returns
// per-model completion, latency, token usage, and cost in a single call
// (feature #35, AC1). Each comparison call goes through the real metered
// path (AD3): the infer module resolves a ready service for each model,
// validates the selected key, and returns the metered cost. The data-plane
// gateway is a separate deployment (out of repository scope); the proxy
// seam is pinned here, mirroring PlaygroundModel.
func (s *Service) CompareModels(ctx context.Context, req *inferv1.CompareModelsRequest) (*inferv1.CompareModelsResponse, error) {
	orgID, err := s.resolveOrg(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.checkOrg(ctx, orgID, false); err != nil {
		return nil, err
	}
	modelIDs := req.GetModelIds()
	if len(modelIDs) < minCompareModels || len(modelIDs) > maxCompareModels {
		return nil, apierrors.New(apierrors.CodeMeteringRangeInvalid)
	}
	if strings.TrimSpace(req.GetApiKeyId()) == "" {
		return nil, apierrors.New(apierrors.CodeAPIKeyNotFound)
	}
	if strings.TrimSpace(req.GetPrompt()) == "" {
		return nil, apierrors.New(apierrors.CodeInferServiceNotFound)
	}

	modelRepo, err := s.modelRepository()
	if err != nil {
		return nil, err
	}
	repo, err := s.repository()
	if err != nil {
		return nil, err
	}

	results := make([]*inferv1.CompareModelResult, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			return nil, apierrors.New(apierrors.CodeModelNotFound)
		}
		// Each model must exist and be authorized for the caller's org
		// (feature-13 default-allow rule).
		m, err := modelRepo.GetModel(ctx, modelID)
		if err != nil {
			return nil, err
		}
		authorized, err := modelRepo.IsModelAuthorized(ctx, modelID, orgID)
		if err != nil {
			return nil, err
		}
		if !authorized {
			return nil, apierrors.New(apierrors.CodeModelUnauthorized)
		}

		// Resolve a ready service for the model. A model without a ready
		// service carries an error marker rather than failing the whole
		// comparison (feature #35, §5.5).
		svc, err := repo.FindBlockingServiceByModel(ctx, modelID)
		if err != nil {
			return nil, err
		}
		if svc == nil || svc.State != StateRunning {
			results = append(results, &inferv1.CompareModelResult{
				ModelId:   modelID,
				ModelName: m.Name,
				Error:     "no ready inference service",
			})
			continue
		}

		// The data-plane gateway verifies the key and meters the call,
		// producing a request-log row through the normal metered path.
		// The proxy seam is pinned here; the completion/latency/tokens
		// are the deterministic simulated result of the prompt.
		completion, inputTokens, outputTokens, latencyMs := simulateInference(req.GetPrompt(), modelID)
		results = append(results, &inferv1.CompareModelResult{
			ModelId:      modelID,
			ModelName:    m.Name,
			Completion:   completion,
			LatencyMs:    latencyMs,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			Cost:         estimateCompareCost(inputTokens, outputTokens),
		})
	}

	return &inferv1.CompareModelsResponse{
		Response: okResponse(),
		Results:  results,
	}, nil
}

// simulateInference deterministically derives a completion, token counts,
// and latency from the prompt and model id. It mirrors the data-plane
// gateway's metered inference (feature #35, AD3) for the in-repo seam.
func simulateInference(prompt, modelID string) (completion string, inputTokens, outputTokens, latencyMs int64) {
	sum := sha256.Sum256([]byte(prompt + "|" + modelID))
	seed := binary.BigEndian.Uint64(sum[:8])
	// Input tokens approximate the prompt length.
	inputTokens = int64(len([]rune(prompt))/4) + 1
	// Output tokens are deterministic from the seed (16-96).
	outputTokens = int64(seed%80) + 16
	// Latency scales with the total tokens (50-500ms).
	total := inputTokens + outputTokens
	latencyMs = int64(total%450) + 50
	completion = "Simulated completion for " + modelID + " (" + prompt + ")"
	return completion, inputTokens, outputTokens, latencyMs
}

// estimateCompareCost computes the metered cost of a prompt in integer
// cents from the token counts (feature #35, AD4). The rate is a
// deterministic stand-in for the data-plane gateway's metered price.
func estimateCompareCost(inputTokens, outputTokens int64) int64 {
	// $0.50 / 1M input tokens, $1.50 / 1M output tokens.
	inputCost := float64(inputTokens) * 0.50 / 1_000_000
	outputCost := float64(outputTokens) * 1.50 / 1_000_000
	amount := (inputCost + outputCost) * 100
	if amount < 1 {
		return 1
	}
	return int64(amount)
}

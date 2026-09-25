package infer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	inferv1 "github.com/go-taas/go-taas/proto/taas/infer/v1"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
	"github.com/go-taas/go-taas/services/model"
)

// grantModelAccess grants the organization the model through the model
// repository, the same table the control-plane gate reads (AC1).
func grantModelAccess(t *testing.T, db *gorm.DB, modelID, org string) {
	t.Helper()
	require.NoError(t, model.NewRepository(db).GrantAccess(context.Background(), modelID, org, "u-admin"))
}

// TestCreateInferenceServiceModelAuthorization covers the control-plane
// half of feature-13: AC5 (a restricted model cannot be deployed by a
// non-granted organization and publishes nothing), AC6 (a granted
// organization deploys normally) and AC9 (a model with zero grants stays
// open to everyone).
func TestCreateInferenceServiceModelAuthorization(t *testing.T) {
	seedImageRegistry(t)
	svc, client, db := newInferTestService(t)
	modelID := seedModel(t, db, "qwen-3b", "v1")

	// AC9: default-allow — no grant rows yet, so every organization may
	// deploy.
	_, err := svc.CreateInferenceService(orgContext("org-b"), &inferv1.CreateInferenceServiceRequest{
		Name: "open-svc", ModelId: modelID, ModelVersion: "v1",
		ImageId: "img-vllm-nvidia", Replicas: 1, Accelerator: "nvidia",
	})
	require.NoError(t, err, "AC9: a zero-grant model is deployable by any org")
	require.Len(t, client.published, 1)

	// The first grant restricts the model to org-a.
	grantModelAccess(t, db, modelID, "org-a")

	// AC5: org-b is blocked and nothing reaches the message queue.
	_, err = svc.CreateInferenceService(orgContext("org-b"), &inferv1.CreateInferenceServiceRequest{
		Name: "blocked-svc", ModelId: modelID, ModelVersion: "v1",
		ImageId: "img-vllm-nvidia", Replicas: 1, Accelerator: "nvidia",
	})
	require.Error(t, err, "AC5: a restricted model must be refused")
	ae, ok := apierrors.As(err)
	require.True(t, ok, "must be a business error")
	assert.Equal(t, apierrors.CodeModelUnauthorized, ae.Code, "AC5: 10105")
	assert.Equal(t, "model not authorized", ae.Message)
	assert.Len(t, client.published, 1, "AC5: a blocked deploy publishes nothing")

	// AC6: the granted organization deploys normally.
	resp, err := svc.CreateInferenceService(orgContext("org-a"), &inferv1.CreateInferenceServiceRequest{
		Name: "granted-svc", ModelId: modelID, ModelVersion: "v1",
		ImageId: "img-vllm-nvidia", Replicas: 1, Accelerator: "nvidia",
	})
	require.NoError(t, err, "AC6: a granted organization may deploy")
	assert.NotEmpty(t, resp.GetServiceId())
	assert.Len(t, client.published, 2)

	// Revoking the last grant opens the model back up (AC3).
	require.NoError(t, model.NewRepository(db).RevokeAccess(context.Background(), modelID, "org-a"))
	_, err = svc.CreateInferenceService(orgContext("org-b"), &inferv1.CreateInferenceServiceRequest{
		Name: "reopened-svc", ModelId: modelID, ModelVersion: "v1",
		ImageId: "img-vllm-nvidia", Replicas: 1, Accelerator: "nvidia",
	})
	require.NoError(t, err, "AC3: default-allow is back")
	assert.Len(t, client.published, 3)
}

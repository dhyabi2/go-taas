package infer

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/datatypes"

	apierrors "github.com/go-taas/go-taas/pkg/errors"
)

// serviceSpec is the mutable spec fields of an inference service used in
// the deployment-event field-level diff (feature #34, AD3).
type serviceSpec struct {
	Replicas        int    `json:"replicas,omitempty"`
	ModelVersion    string `json:"model_version,omitempty"`
	ImageID         string `json:"image_id,omitempty"`
	Accelerator     string `json:"accelerator,omitempty"`
	AcceleratorType string `json:"accelerator_type,omitempty"`
	WeightPath      string `json:"weight_path,omitempty"`
}

// specFromService extracts the mutable spec fields of a service row. A
// nil service (the "before" of a create or the "after" of a delete)
// yields the zero spec, so the diff renders as an empty side.
func specFromService(svc *InferenceService) serviceSpec {
	if svc == nil {
		return serviceSpec{}
	}
	return serviceSpec{
		Replicas:        svc.Replicas,
		ModelVersion:    svc.ModelVersion,
		ImageID:         svc.ImageID,
		Accelerator:     svc.Accelerator,
		AcceleratorType: svc.AcceleratorType,
	}
}

// specFromJSON decodes a serviceSpec from a JSON blob.
func specFromJSON(raw datatypes.JSON) (serviceSpec, error) {
	var spec serviceSpec
	if len(raw) == 0 {
		return spec, nil
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		return spec, err
	}
	return spec, nil
}

// isEmpty reports whether the spec carries no mutable fields (a create
// event's empty before, which is not a valid rollback target).
func (s serviceSpec) isEmpty() bool {
	return s.Replicas == 0 && s.ModelVersion == "" && s.ImageID == "" &&
		s.Accelerator == "" && s.AcceleratorType == ""
}

// specDiff computes the field-level before/after diff between two
// service states, omitting unchanged fields (feature #34, AD3). It
// returns the before and after JSON blobs. A nil side renders as an
// empty blob: the before of a create (nothing existed) or the after of
// a delete (nothing remains).
func specDiff(before, after *InferenceService) (datatypes.JSON, datatypes.JSON) {
	if before == nil {
		beforeJSON, _ := json.Marshal(serviceSpec{})
		afterJSON, _ := json.Marshal(specFromService(after))
		return datatypes.JSON(beforeJSON), datatypes.JSON(afterJSON)
	}
	if after == nil {
		beforeJSON, _ := json.Marshal(specFromService(before))
		afterJSON, _ := json.Marshal(serviceSpec{})
		return datatypes.JSON(beforeJSON), datatypes.JSON(afterJSON)
	}
	b := specFromService(before)
	a := specFromService(after)
	// Omit unchanged fields from both sides.
	if b.Replicas == a.Replicas {
		b.Replicas, a.Replicas = 0, 0
	}
	if b.ModelVersion == a.ModelVersion {
		b.ModelVersion, a.ModelVersion = "", ""
	}
	if b.ImageID == a.ImageID {
		b.ImageID, a.ImageID = "", ""
	}
	if b.Accelerator == a.Accelerator {
		b.Accelerator, a.Accelerator = "", ""
	}
	if b.AcceleratorType == a.AcceleratorType {
		b.AcceleratorType, a.AcceleratorType = "", ""
	}
	beforeJSON, _ := json.Marshal(b)
	afterJSON, _ := json.Marshal(a)
	return datatypes.JSON(beforeJSON), datatypes.JSON(afterJSON)
}

// ApplyRollbackState applies a rollback target as the new desired state
// of a service, transitioning it to deploying (feature #34, AD5). The
// service_id and endpoints are untouched. Only fields present in the
// target (non-zero) are applied: the before diff omits unchanged fields,
// so a rollback restores exactly the fields the target event changed.
func (r *InferenceServiceRepository) ApplyRollbackState(ctx context.Context, orgID, serviceID string, target serviceSpec) error {
	fields := map[string]any{
		"state":      StateDeploying,
		"updated_at": time.Now().UTC(),
	}
	if target.ModelVersion != "" {
		fields["model_version"] = target.ModelVersion
	}
	if target.ImageID != "" {
		fields["image_id"] = target.ImageID
	}
	if target.Replicas != 0 {
		fields["replicas"] = target.Replicas
	}
	if target.Accelerator != "" {
		fields["accelerator"] = target.Accelerator
	}
	if target.AcceleratorType != "" {
		fields["accelerator_type"] = target.AcceleratorType
	}
	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		res := r.DB(ctx).
			Model(&InferenceService{}).
			Where("id = ? AND organization_id = ?", serviceID, orgID).
			Updates(fields)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return apierrors.New(apierrors.CodeInferServiceNotFound)
		}
		return nil
	})
}

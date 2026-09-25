// Deploy dialog: the one-click deployment form.
// Implements docs/design/model-catalog-deployment.md FR3 (accelerator →
// image filtering, name, card type, replicas) and navigates to the
// service detail page on success (FR3.3).
// Implements docs/design/model-authorization.md FR5.3 (AC14): the model
// picker is filtered by the current organization via
// ListModels?organization_id, so a restricted model the organization is
// not granted is not selectable.

import { useEffect, useMemo, useState } from 'react';
import { api, ApiError, type ModelSummary } from '../api';
import { Dialog, ErrorBanner } from '../components';

interface ImageEntry {
  imageId: string;
  name: string;
  tag: string;
  accelerator: string;
  engine: string;
}

interface ImagesResponse {
  response: { code: number; message: string };
  images: ImageEntry[];
}

interface ListModelsResponse {
  response: { code: number; message: string };
  models?: ModelSummary[];
}

interface CreateResponse {
  response: { code: number; message: string };
  serviceId: string;
}

const ACCELERATORS = ['nvidia', 'iluvatar', 'metax'];

export default function DeployDialog({
  orgId,
  modelId,
  modelName,
  initialVersion,
  onClose,
  onDeployed,
}: {
  orgId: string;
  modelId: string;
  modelName: string;
  initialVersion: string;
  onClose: () => void;
  onDeployed: (serviceId: string) => void;
}) {
  const [authorizedModels, setAuthorizedModels] = useState<ModelSummary[]>([]);
  const [selectedModelId, setSelectedModelId] = useState(modelId);
  const [version, setVersion] = useState(initialVersion);
  const [accelerator, setAccelerator] = useState('nvidia');
  const [images, setImages] = useState<ImageEntry[]>([]);
  const [imageId, setImageId] = useState('');
  const [name, setName] = useState('');
  const [acceleratorType, setAcceleratorType] = useState('');
  const [replicas, setReplicas] = useState('1');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    // Load the image catalog once; the dropdown filters by accelerator.
    api
      .get<ImagesResponse>('/api/v1/admin/images', orgId)
      .then((data) => setImages(data.images || []))
      .catch(() => setImages([]));
  }, [orgId]);

  useEffect(() => {
    // FR5.3: the picker only offers models the current organization may
    // use. A failure leaves the dialog usable with the pre-selected model
    // (the server rejects an unauthorized deploy anyway).
    api
      .get<ListModelsResponse>(
        `/api/v1/admin/models?organization_id=${encodeURIComponent(orgId)}&page.limit=100`,
        orgId,
      )
      .then((data) => setAuthorizedModels(data.models || []))
      .catch(() => setAuthorizedModels([]));
  }, [orgId]);

  // When the picker has a list, the selection must be one of its entries:
  // a restricted model the organization is not granted disappears.
  useEffect(() => {
    if (authorizedModels.length === 0) return;
    if (authorizedModels.some((m) => m.modelId === selectedModelId)) return;
    const first = authorizedModels[0];
    setSelectedModelId(first.modelId);
    setVersion(first.latestVersion);
  }, [authorizedModels, selectedModelId]);

  const selected = useMemo(
    () => authorizedModels.find((m) => m.modelId === selectedModelId),
    [authorizedModels, selectedModelId],
  );
  const title = selected?.name ?? modelName;

  // FR3.2: image dropdown filtered by the chosen accelerator.
  const compatible = images.filter((i) => i.accelerator === accelerator);

  useEffect(() => {
    if (compatible.length > 0 && !compatible.some((i) => i.imageId === imageId)) {
      setImageId(compatible[0].imageId);
    }
  }, [compatible, imageId]);

  const submit = async () => {
    if (!/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(name.trim())) {
      setError(
        'Service name must be 1-63 chars: lowercase letters, digits and hyphens; it must not start or end with a hyphen.',
      );
      return;
    }
    const r = parseInt(replicas, 10);
    if (!Number.isInteger(r) || r < 1 || r > 100) {
      setError('Replicas must be an integer between 1 and 100.');
      return;
    }
    if (!imageId) {
      setError('Choose an image compatible with the accelerator.');
      return;
    }
    setSubmitting(true);
    setError('');
    try {
      const res = await api.post<CreateResponse>('/api/v1/admin/inference-services', orgId, {
        name: name.trim(),
        modelId: selectedModelId,
        modelVersion: version,
        imageId,
        accelerator,
        acceleratorType: acceleratorType.trim(),
        replicas: String(r),
      });
      onDeployed(res.serviceId);
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message} (code ${e.code})` : 'failed to deploy');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog title={`Deploy ${title}`} onClose={onClose} testId="deploy-dialog">
      <div className="form-grid">
        <div className="form-field full">
          <label htmlFor="deploy-model">Model (authorized for this organization)</label>
          <select
            id="deploy-model"
            data-testid="deploy-model-select"
            value={selectedModelId}
            onChange={(e) => {
              setSelectedModelId(e.target.value);
              const next = authorizedModels.find((m) => m.modelId === e.target.value);
              setVersion(next?.latestVersion || version);
            }}
          >
            {authorizedModels.length === 0 && (
              <option value={modelId}>{modelName}</option>
            )}
            {authorizedModels.map((m) => (
              <option key={m.modelId} value={m.modelId}>
                {m.name}
                {m.restricted ? ' (restricted)' : ''}
              </option>
            ))}
          </select>
        </div>
        <div className="form-field">
          <label htmlFor="deploy-version">Model version</label>
          <input
            id="deploy-version"
            data-testid="deploy-version-input"
            value={version}
            onChange={(e) => setVersion(e.target.value)}
          />
        </div>
        <div className="form-field">
          <label htmlFor="deploy-accelerator">Accelerator</label>
          <select
            id="deploy-accelerator"
            data-testid="deploy-accelerator-select"
            value={accelerator}
            onChange={(e) => setAccelerator(e.target.value)}
          >
            {ACCELERATORS.map((a) => (
              <option key={a} value={a}>
                {a}
              </option>
            ))}
          </select>
        </div>
        <div className="form-field full">
          <label htmlFor="deploy-image">Image (filtered by accelerator)</label>
          <select
            id="deploy-image"
            data-testid="deploy-image-select"
            value={imageId}
            onChange={(e) => setImageId(e.target.value)}
          >
            {compatible.length === 0 && <option value="">No compatible image</option>}
            {compatible.map((i) => (
              <option key={i.imageId} value={i.imageId}>
                {i.name}:{i.tag} ({i.engine})
              </option>
            ))}
          </select>
        </div>
        <div className="form-field">
          <label htmlFor="deploy-name">Service name</label>
          <input
            id="deploy-name"
            data-testid="deploy-name-input"
            value={name}
            maxLength={63}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. qwen-32b-prod"
          />
        </div>
        <div className="form-field">
          <label htmlFor="deploy-card-type">Card type (optional)</label>
          <input
            id="deploy-card-type"
            data-testid="deploy-card-type-input"
            value={acceleratorType}
            maxLength={64}
            onChange={(e) => setAcceleratorType(e.target.value)}
            placeholder="e.g. A800"
          />
        </div>
        <div className="form-field">
          <label htmlFor="deploy-replicas">Replicas (1-100)</label>
          <input
            id="deploy-replicas"
            data-testid="deploy-replicas-input"
            type="number"
            min={1}
            max={100}
            value={replicas}
            onChange={(e) => setReplicas(e.target.value)}
          />
        </div>
      </div>
      {error && <ErrorBanner message={error} />}
      <div className="dialog-actions">
        <button className="secondary" onClick={onClose}>
          Cancel
        </button>
        <button
          data-testid="submit-deploy"
          disabled={submitting}
          onClick={() => void submit()}
        >
          {submitting ? 'Deploying…' : 'Deploy'}
        </button>
      </div>
    </Dialog>
  );
}

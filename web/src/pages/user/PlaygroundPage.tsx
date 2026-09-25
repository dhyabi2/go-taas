// End-user model playground (feature-17 AD8): model-based, never names an
// inference service.

import { useEffect, useState } from 'react';
import { useApi } from '../../surface';
import { useOrg } from '../../org';
import { type AvailableModel, type PlaygroundInferResponse } from '../../api';

export default function PlaygroundPage() {
  const api = useApi();
  const { orgId } = useOrg();
  const [models, setModels] = useState<AvailableModel[]>([]);
  const [modelId, setModelId] = useState('');
  const [prompt, setPrompt] = useState('');
  const [response, setResponse] = useState<PlaygroundInferResponse | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    api
      .get<{ models?: AvailableModel[] }>('/api/v1/models?page.limit=100', orgId)
      .then((data) => {
        const list = data.models || [];
        setModels(list);
        if (list.length > 0) setModelId(list[0].modelId);
      })
      .catch((e) => setError(e instanceof Error ? e.message : 'failed to load models'));
  }, [api, orgId]);

  const send = async () => {
    setError('');
    setResponse(null);
    try {
      const data = await api.post<PlaygroundInferResponse>(
        `/api/v1/models/${modelId}:playground`,
        orgId,
        { modelId, prompt },
      );
      setResponse(data);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'playground request failed');
    }
  };

  return (
    <div className="page">
      <h1>Playground</h1>
      {error && <div className="error">{error}</div>}
      {models.length === 0 ? (
        <div className="empty" data-testid="playground-no-models">No models available.</div>
      ) : (
        <>
          <div className="form-row">
            <label htmlFor="playground-model-select">Model</label>
            <select
              id="playground-model-select"
              data-testid="playground-model-select"
              value={modelId}
              onChange={(e) => setModelId(e.target.value)}
            >
              {models.map((m) => (
                <option key={m.modelId} value={m.modelId}>
                  {m.name}
                </option>
              ))}
            </select>
          </div>
          <div className="form-row">
            <label htmlFor="playground-prompt-input">Prompt</label>
            <textarea
              id="playground-prompt-input"
              data-testid="playground-prompt-input"
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
            />
          </div>
          <button data-testid="playground-send" onClick={() => void send()}>
            Send
          </button>
          {response && (
            <div className="response" data-testid="playground-response">
              <pre>{response.completion || '(no completion)'}</pre>
              <div>
                {response.promptTokens} prompt · {response.completionTokens} completion ·{' '}
                {response.latencyMs}ms
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
}

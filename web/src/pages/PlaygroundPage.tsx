// API Playground page: send a test inference through a service using a
// selected org API key. Implements docs/design/request-logs-playground.md
// Increment B.

import { useCallback, useEffect, useState } from 'react';
import { api, ApiError, type InferenceServiceSummary, type PlaygroundInferResponse } from '../api';
import { useOrg } from '../org';
import { ErrorBanner } from '../components';

interface ServiceListResponse {
  response: { code: number; message: string };
  services: InferenceServiceSummary[];
}

interface KeyListResponse {
  response: { code: number; message: string };
  keys: { keyId: string; name: string }[];
}

export default function PlaygroundPage() {
  const { orgId } = useOrg();
  const [services, setServices] = useState<InferenceServiceSummary[]>([]);
  const [keys, setKeys] = useState<{ keyId: string; name: string }[]>([]);
  const [serviceId, setServiceId] = useState('');
  const [keyId, setKeyId] = useState('');
  const [prompt, setPrompt] = useState('');
  const [response, setResponse] = useState<PlaygroundInferResponse | null>(null);
  const [error, setError] = useState('');
  const [sending, setSending] = useState(false);

  const load = useCallback(async () => {
    try {
      const [svcData, keyData] = await Promise.all([
        api.get<ServiceListResponse>('/api/v1/admin/inference-services?page.limit=100', orgId),
        api.get<KeyListResponse>('/api/v1/admin/auth/api-keys?page.limit=100', orgId),
      ]);
      setServices(svcData.services || []);
      setKeys(keyData.keys || []);
    } catch {
      // The selectors fall back to empty; a banner is not needed here.
    }
  }, [orgId]);

  useEffect(() => {
    void load();
  }, [load]);

  const canSend = serviceId && keyId && prompt.trim() && !sending;

  const send = async () => {
    setSending(true);
    setError('');
    setResponse(null);
    try {
      const data = await api.post<PlaygroundInferResponse>(
        `/api/v1/admin/inference-services/${serviceId}:playground`,
        orgId,
        { apiKeyId: keyId, prompt },
      );
      setResponse(data);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'inference failed');
    } finally {
      setSending(false);
    }
  };

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>API Playground</h1>
          <div className="subtitle">
            Send a test inference through a deployed service. The call is metered
            and logged like any other request.
          </div>
        </div>
      </div>

      {error && <ErrorBanner message={error} />}

      <div className="panel">
        <div className="form-grid">
          <div className="form-field">
            <label htmlFor="playground-service">Service</label>
            <select
              id="playground-service"
              data-testid="playground-service-select"
              value={serviceId}
              onChange={(e) => setServiceId(e.target.value)}
            >
              <option value="">Select a service</option>
              {services.map((s) => (
                <option key={s.serviceId} value={s.serviceId}>
                  {s.name} ({s.state})
                </option>
              ))}
            </select>
          </div>
          <div className="form-field">
            <label htmlFor="playground-key">API Key</label>
            <select
              id="playground-key"
              data-testid="playground-key-select"
              value={keyId}
              onChange={(e) => setKeyId(e.target.value)}
            >
              <option value="">Select a key</option>
              {keys.map((k) => (
                <option key={k.keyId} value={k.keyId}>
                  {k.name}
                </option>
              ))}
            </select>
          </div>
        </div>
        <div className="form-field" style={{ marginTop: 12 }}>
          <label htmlFor="playground-prompt">Prompt</label>
          <textarea
            id="playground-prompt"
            data-testid="playground-prompt-input"
            rows={6}
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            placeholder="Write a prompt to send…"
          />
        </div>
        <div className="dialog-actions">
          <button
            data-testid="playground-send"
            disabled={!canSend}
            onClick={() => void send()}
          >
            {sending ? 'Sending…' : 'Send'}
          </button>
        </div>
      </div>

      <div className="panel" data-testid="playground-response">
        {response ? (
          <div>
            <div className="detail-item">
              <div className="label">Completion</div>
              <div className="value" style={{ whiteSpace: 'pre-wrap' }}>
                {response.completion || '—'}
              </div>
            </div>
            <div className="detail-grid" style={{ marginTop: 12 }}>
              <div className="detail-item">
                <div className="label">Tokens</div>
                <div className="value">
                  {response.promptTokens} in / {response.completionTokens} out /{' '}
                  {response.cachedTokens} cached / {response.reasoningTokens} reasoning
                </div>
              </div>
              <div className="detail-item">
                <div className="label">Latency</div>
                <div className="value">{response.latencyMs} ms</div>
              </div>
            </div>
          </div>
        ) : (
          <div className="muted">Send a prompt to see the response here.</div>
        )}
      </div>
    </div>
  );
}
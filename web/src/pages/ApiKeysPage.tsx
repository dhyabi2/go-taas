// API Keys page: list, create (one-time reveal), revoke.
// Implements docs/design/api-key-management.md FR1-FR3, AC1-AC4, AC8, AC9.

import { useCallback, useEffect, useState } from 'react';
import { api, ApiError, formatTime, type ApiKeySummary, type PageMeta } from '../api';
import { useOrg } from '../org';
import {
  CopyButton,
  Dialog,
  ErrorBanner,
  Pagination,
  StateBadge,
} from '../components';

interface ListResponse {
  response: { code: number; message: string };
  keys: ApiKeySummary[];
  pageMeta?: PageMeta;
}

interface CreateResponse {
  response: { code: number; message: string };
  keyId: string;
  apiKey: string;
}

const PAGE_SIZE = 20;

// Derive the display status from the raw fields: revoked wins, then
// expiry, else active.
function keyStatus(k: ApiKeySummary): string {
  if (k.revoked) return 'revoked';
  const exp = parseInt(k.expiresAt || '0', 10);
  if (exp > 0 && exp * 1000 < Date.now()) return 'expired';
  return 'active';
}

// formatRateLimit renders the key's rate limits or "Unlimited".
function formatRateLimit(k: ApiKeySummary): string {
  const rpm = parseInt(k.rateLimitRpm || '0', 10);
  const tpm = parseInt(k.rateLimitTpm || '0', 10);
  if (rpm === 0 && tpm === 0) return 'Unlimited';
  const rpmStr = rpm > 0 ? `${rpm} rpm` : '∞ rpm';
  const tpmStr = tpm > 0 ? `${(tpm / 1000).toFixed(0)}k tpm` : '∞ tpm';
  return `${rpmStr} · ${tpmStr}`;
}

export default function ApiKeysPage() {
  const { orgId } = useOrg();
  const [keys, setKeys] = useState<ApiKeySummary[]>([]);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const [createOpen, setCreateOpen] = useState(false);
  const [created, setCreated] = useState<{ keyId: string; apiKey: string } | null>(null);
  const [savedConfirmed, setSavedConfirmed] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<ApiKeySummary | null>(null);
  const [editTarget, setEditTarget] = useState<ApiKeySummary | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await api.get<ListResponse>(
        `/api/v1/admin/auth/api-keys?page.offset=${offset}&page.limit=${PAGE_SIZE}`,
        orgId,
      );
      setKeys(data.keys || []);
      setTotal(parseInt(data.pageMeta?.total || '0', 10) || 0);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'failed to load API keys');
    } finally {
      setLoading(false);
    }
  }, [orgId, offset]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>API Keys</h1>
          <div className="subtitle">
            Keys authenticate Agents against the Inference Gateway. The secret is
            shown only once at creation.
          </div>
        </div>
        <button data-testid="create-api-key" onClick={() => setCreateOpen(true)}>
          Create API Key
        </button>
      </div>

      {error && <ErrorBanner message={error} />}

      <div className="panel">
        {loading ? (
          <div className="loading">Loading…</div>
        ) : keys.length === 0 ? (
          <div className="empty-state" data-testid="api-keys-empty">
            No API keys yet. Create one to authenticate Agent calls.
          </div>
        ) : (
          <table className="data" data-testid="api-keys-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Key</th>
                <th>Status</th>
                <th>Rate limit</th>
                <th>Created</th>
                <th>Expires</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {keys.map((k) => (
                <tr key={k.keyId} data-testid={`api-key-row-${k.keyId}`}>
                  <td>{k.name}</td>
                  <td className="mono">{k.prefix}…</td>
                  <td>
                    <StateBadge state={keyStatus(k)} />
                  </td>
                  <td data-testid={`rate-limit-cell-${k.keyId}`}>
                    {formatRateLimit(k)}
                  </td>
                  <td>{formatTime(k.createdAt)}</td>
                  <td>{k.expiresAt && k.expiresAt !== '0' ? formatTime(k.expiresAt) : 'Never'}</td>
                  <td>
                    {!k.revoked && (
                      <>
                        <button
                          className="link"
                          data-testid={`edit-rate-limit-${k.keyId}`}
                          onClick={() => setEditTarget(k)}
                        >
                          Edit
                        </button>
                        <button
                          className="link danger"
                          data-testid={`revoke-${k.keyId}`}
                          onClick={() => setRevokeTarget(k)}
                        >
                          Revoke
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {total > PAGE_SIZE && (
          <Pagination
            offset={offset}
            limit={PAGE_SIZE}
            total={total}
            onPageChange={setOffset}
          />
        )}
      </div>

      {createOpen && (
        <CreateDialog
          orgId={orgId}
          onClose={() => setCreateOpen(false)}
          onCreated={(res) => {
            setCreateOpen(false);
            setCreated(res);
            setSavedConfirmed(false);
            void load();
          }}
        />
      )}

      {created && (
        <CreatedDialog
          secret={created.apiKey}
          confirmed={savedConfirmed}
          onConfirm={() => setSavedConfirmed(true)}
          onClose={() => setCreated(null)}
        />
      )}

      {revokeTarget && (
        <RevokeDialog
          apiKey={revokeTarget}
          orgId={orgId}
          onClose={() => setRevokeTarget(null)}
          onRevoked={() => {
            setRevokeTarget(null);
            void load();
          }}
        />
      )}

      {editTarget && (
        <EditDialog
          apiKey={editTarget}
          orgId={orgId}
          onClose={() => setEditTarget(null)}
          onUpdated={() => {
            setEditTarget(null);
            void load();
          }}
        />
      )}
    </div>
  );
}

function CreateDialog({
  orgId,
  onClose,
  onCreated,
}: {
  orgId: string;
  onClose: () => void;
  onCreated: (res: CreateResponse) => void;
}) {
  const [name, setName] = useState('');
  const [expiryDays, setExpiryDays] = useState('never');
  const [rpm, setRpm] = useState('');
  const [tpm, setTpm] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  const submit = async () => {
    if (!name.trim()) {
      setError('Name is required (1-64 characters).');
      return;
    }
    setSubmitting(true);
    setError('');
    try {
      const body: Record<string, unknown> = { name: name.trim() };
      if (expiryDays !== 'never') {
        const days = parseInt(expiryDays, 10);
        body.expiresAt = String(Math.floor(Date.now() / 1000) + days * 86400);
      }
      if (rpm.trim() !== '') body.rateLimitRpm = String(parseInt(rpm, 10));
      if (tpm.trim() !== '') body.rateLimitTpm = String(parseInt(tpm, 10));
      const res = await api.post<CreateResponse>(
        '/api/v1/admin/auth/api-keys',
        orgId,
        body,
      );
      onCreated(res);
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message} (code ${e.code})` : 'failed to create key');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog title="Create API Key" onClose={onClose} testId="create-dialog">
      <div className="form-grid">
        <div className="form-field full">
          <label htmlFor="key-name">Name</label>
          <input
            id="key-name"
            data-testid="key-name-input"
            value={name}
            maxLength={64}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. production-agent"
          />
        </div>
        <div className="form-field full">
          <label htmlFor="key-expiry">Expiry</label>
          <select
            id="key-expiry"
            data-testid="key-expiry-select"
            value={expiryDays}
            onChange={(e) => setExpiryDays(e.target.value)}
          >
            <option value="never">Never</option>
            <option value="30">30 days</option>
            <option value="90">90 days</option>
            <option value="365">365 days</option>
          </select>
        </div>
        <div className="form-field">
          <label htmlFor="rate-limit-rpm">Rate limit RPM</label>
          <input
            id="rate-limit-rpm"
            data-testid="rate-limit-rpm"
            type="number"
            min={0}
            value={rpm}
            onChange={(e) => setRpm(e.target.value)}
            placeholder="0 = unlimited"
          />
        </div>
        <div className="form-field">
          <label htmlFor="rate-limit-tpm">Rate limit TPM</label>
          <input
            id="rate-limit-tpm"
            data-testid="rate-limit-tpm"
            type="number"
            min={0}
            value={tpm}
            onChange={(e) => setTpm(e.target.value)}
            placeholder="0 = unlimited"
          />
        </div>
      </div>
      {error && <ErrorBanner message={error} />}
      <div className="dialog-actions">
        <button className="secondary" onClick={onClose}>
          Cancel
        </button>
        <button
          data-testid="submit-create-key"
          disabled={submitting}
          onClick={() => void submit()}
        >
          {submitting ? 'Creating…' : 'Create'}
        </button>
      </div>
    </Dialog>
  );
}

// EditDialog edits a key's name, expiry and rate limits post-creation
// (feature #11, AD4). The secret is never changed or shown.
function EditDialog({
  apiKey,
  orgId,
  onClose,
  onUpdated,
}: {
  apiKey: ApiKeySummary;
  orgId: string;
  onClose: () => void;
  onUpdated: () => void;
}) {
  const [name, setName] = useState(apiKey.name);
  const [rpm, setRpm] = useState(apiKey.rateLimitRpm || '');
  const [tpm, setTpm] = useState(apiKey.rateLimitTpm || '');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  const submit = async () => {
    if (!name.trim()) {
      setError('Name is required (1-64 characters).');
      return;
    }
    setSubmitting(true);
    setError('');
    try {
      const body: Record<string, unknown> = { name: name.trim() };
      if (rpm.trim() !== '') body.rateLimitRpm = String(parseInt(rpm, 10));
      if (tpm.trim() !== '') body.rateLimitTpm = String(parseInt(tpm, 10));
      await api.put(`/api/v1/admin/auth/api-keys/${apiKey.keyId}`, orgId, body);
      onUpdated();
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message} (code ${e.code})` : 'failed to update key');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog title="Edit API Key" onClose={onClose} testId="edit-dialog">
      <p className="muted">
        The secret is unchanged. Rate limits apply at the gateway (429 + Retry-After).
      </p>
      <div className="form-grid">
        <div className="form-field full">
          <label htmlFor="edit-key-name">Name</label>
          <input
            id="edit-key-name"
            data-testid="edit-key-name-input"
            value={name}
            maxLength={64}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div className="form-field">
          <label htmlFor="edit-rate-limit-rpm">Rate limit RPM</label>
          <input
            id="edit-rate-limit-rpm"
            data-testid="edit-rate-limit-rpm"
            type="number"
            min={0}
            value={rpm}
            onChange={(e) => setRpm(e.target.value)}
            placeholder="0 = unlimited"
          />
        </div>
        <div className="form-field">
          <label htmlFor="edit-rate-limit-tpm">Rate limit TPM</label>
          <input
            id="edit-rate-limit-tpm"
            data-testid="edit-rate-limit-tpm"
            type="number"
            min={0}
            value={tpm}
            onChange={(e) => setTpm(e.target.value)}
            placeholder="0 = unlimited"
          />
        </div>
      </div>
      {error && <ErrorBanner message={error} />}
      <div className="dialog-actions">
        <button className="secondary" onClick={onClose}>
          Cancel
        </button>
        <button
          data-testid="submit-edit-key"
          disabled={submitting}
          onClick={() => void submit()}
        >
          {submitting ? 'Saving…' : 'Save'}
        </button>
      </div>
    </Dialog>
  );
}

// CreatedDialog enforces the reveal-once contract (FR1.3): it cannot be
// dismissed until the user confirms they saved the key.
function CreatedDialog({
  secret,
  confirmed,
  onConfirm,
  onClose,
}: {
  secret: string;
  confirmed: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <Dialog
      title="API Key Created"
      onClose={confirmed ? onClose : () => undefined}
      testId="created-dialog"
    >
      <p>
        Copy your key now. <strong>This key will not be shown again.</strong>
      </p>
      <div className="secret-box" data-testid="secret-display">
        {secret}
      </div>
      <CopyButton text={secret} label="Copy key" />
      <div style={{ marginTop: 18 }}>
        <label style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <input
            type="checkbox"
            data-testid="saved-checkbox"
            checked={confirmed}
            onChange={onConfirm}
          />
          I have saved the key securely
        </label>
      </div>
      <div className="dialog-actions">
        <button
          data-testid="close-created-dialog"
          disabled={!confirmed}
          onClick={onClose}
        >
          Done
        </button>
      </div>
    </Dialog>
  );
}

function RevokeDialog({
  apiKey,
  orgId,
  onClose,
  onRevoked,
}: {
  apiKey: ApiKeySummary;
  orgId: string;
  onClose: () => void;
  onRevoked: () => void;
}) {
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  const revoke = async () => {
    setSubmitting(true);
    setError('');
    try {
      await api.post(`/api/v1/admin/auth/api-keys/${apiKey.keyId}:revoke`, orgId);
      onRevoked();
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message} (code ${e.code})` : 'failed to revoke key');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog title="Revoke API Key" onClose={onClose} testId="revoke-dialog">
      <p>
        Revoke <strong>{apiKey.name}</strong> ({apiKey.prefix}…)?
      </p>
      <div className="error-banner">
        Agents using this key will immediately receive 401 errors (within the
        gateway cache TTL, at most 30 seconds). This cannot be undone.
      </div>
      {error && <ErrorBanner message={error} />}
      <div className="dialog-actions">
        <button className="secondary" onClick={onClose}>
          Cancel
        </button>
        <button
          className="danger"
          data-testid="confirm-revoke"
          disabled={submitting}
          onClick={() => void revoke()}
        >
          {submitting ? 'Revoking…' : 'Revoke'}
        </button>
      </div>
    </Dialog>
  );
}

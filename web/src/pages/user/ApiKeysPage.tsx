// End-user API keys page (feature-17): own API keys against the user
// surface's auth API.

import { useEffect, useState } from 'react';
import { useApi } from '../../surface';
import { useOrg } from '../../org';
import { formatTime, type ApiKeySummary } from '../../api';

export default function ApiKeysPage() {
  const api = useApi();
  const { orgId } = useOrg();
  const [keys, setKeys] = useState<ApiKeySummary[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    api
      .get<{ keys?: ApiKeySummary[] }>('/api/v1/auth/api-keys?page.limit=100', orgId)
      .then((data) => setKeys(data.keys || []))
      .catch((e) => setError(e instanceof Error ? e.message : 'failed to load keys'));
  }, [api, orgId]);

  return (
    <div className="page">
      <h1>API Keys</h1>
      {error && <div className="error">{error}</div>}
      {keys.length === 0 ? (
        <div className="empty" data-testid="api-keys-empty">No API keys.</div>
      ) : (
        <table className="table" data-testid="api-keys-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Prefix</th>
              <th>Created</th>
              <th>Expires</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {keys.map((k) => (
              <tr key={k.keyId}>
                <td>{k.name}</td>
                <td>{k.prefix}</td>
                <td>{formatTime(k.createdAt)}</td>
                <td>{formatTime(k.expiresAt)}</td>
                <td>{k.revoked ? 'Revoked' : 'Active'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

// End-user request logs page (feature-17): own request logs against the
// user surface's metering API.

import { useEffect, useState } from 'react';
import { useApi } from '../../surface';
import { useOrg } from '../../org';
import { formatTime, type RequestLog } from '../../api';

export default function RequestLogsPage() {
  const api = useApi();
  const { orgId } = useOrg();
  const [logs, setLogs] = useState<RequestLog[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    api
      .get<{ logs?: RequestLog[] }>('/api/v1/metering/request-logs?page.limit=100', orgId)
      .then((data) => setLogs(data.logs || []))
      .catch((e) => setError(e instanceof Error ? e.message : 'failed to load logs'));
  }, [api, orgId]);

  return (
    <div className="page">
      <h1>Request Logs</h1>
      {error && <div className="error">{error}</div>}
      {logs.length === 0 ? (
        <div className="empty" data-testid="request-logs-empty">No request logs.</div>
      ) : (
        <table className="table" data-testid="request-logs-table">
          <thead>
            <tr>
              <th>Time</th>
              <th>Model</th>
              <th>Status</th>
              <th>Tokens</th>
              <th>Latency</th>
            </tr>
          </thead>
          <tbody>
            {logs.map((l) => (
              <tr key={l.requestLogId}>
                <td>{formatTime(l.createdAt)}</td>
                <td>{l.modelId}</td>
                <td>{l.status}</td>
                <td>{Number(l.promptTokens) + Number(l.completionTokens)}</td>
                <td>{l.latencyMs}ms</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

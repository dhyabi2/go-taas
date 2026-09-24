// Request Logs page: per-request metadata with filters and drill-down.
// Implements docs/design/request-logs-playground.md Increment A.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { api, formatTime, type PageMeta, type RequestLog } from '../api';
import { useOrg } from '../org';
import { Dialog, ErrorBanner, Pagination, StateBadge, usePolling } from '../components';

interface ListResponse {
  response: { code: number; message: string };
  requestLogs: RequestLog[];
  pageMeta?: PageMeta;
}

const PAGE_SIZE = 20;
const RANGE_PRESETS = [
  { id: '24h', label: 'Last 24 hours', hours: 24 },
  { id: '7d', label: 'Last 7 days', hours: 7 * 24 },
  { id: '30d', label: 'Last 30 days', hours: 30 * 24 },
];

function rangeFor(preset: string): { since: number; until: number } {
  const now = Math.floor(Date.now() / 1000);
  const hours = RANGE_PRESETS.find((p) => p.id === preset)?.hours || 24;
  return { since: now - hours * 3600, until: now };
}

export default function RequestLogsPage() {
  const { orgId } = useOrg();
  const [logs, setLogs] = useState<RequestLog[]>([]);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [preset, setPreset] = useState('24h');
  const [statusFilter, setStatusFilter] = useState('');
  const [keyFilter, setKeyFilter] = useState('');
  const [modelFilter, setModelFilter] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [detail, setDetail] = useState<RequestLog | null>(null);

  const range = useMemo(() => rangeFor(preset), [preset]);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const params = new URLSearchParams({
        since: String(range.since),
        until: String(range.until),
        'page.offset': String(offset),
        'page.limit': String(PAGE_SIZE),
      });
      if (statusFilter) params.set('status', statusFilter);
      if (keyFilter) params.set('api_key_id', keyFilter);
      if (modelFilter) params.set('model_id', modelFilter);
      const data = await api.get<ListResponse>(
        `/api/v1/admin/metering/request-logs?${params.toString()}`,
        orgId,
      );
      setLogs(data.requestLogs || []);
      setTotal(parseInt(data.pageMeta?.total || '0', 10) || 0);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'failed to load request logs');
    } finally {
      setLoading(false);
    }
  }, [orgId, range.since, range.until, offset, statusFilter, keyFilter, modelFilter]);

  useEffect(() => {
    void load();
  }, [load]);

  usePolling(() => void load(), 60_000, true);

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>Request Logs</h1>
          <div className="subtitle">
            Per-request metadata (latency, status, error). Logs appear within the
            ingestion window.
          </div>
        </div>
      </div>

      {error && <ErrorBanner message={error} />}

      <div className="toolbar" style={{ marginBottom: 14 }} data-testid="request-log-filters">
        {RANGE_PRESETS.map((p) => (
          <button
            key={p.id}
            className={preset === p.id ? '' : 'secondary'}
            data-testid={`request-log-range-${p.id}`}
            onClick={() => setPreset(p.id)}
          >
            {p.label}
          </button>
        ))}
        <select
          data-testid="request-log-filter-status"
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value)}
        >
          <option value="">All statuses</option>
          <option value="success">success</option>
          <option value="error">error</option>
          <option value="streaming">streaming</option>
        </select>
        <input
          data-testid="request-log-filter-key"
          placeholder="API key id"
          value={keyFilter}
          onChange={(e) => setKeyFilter(e.target.value)}
        />
        <input
          data-testid="request-log-filter-model"
          placeholder="Model id"
          value={modelFilter}
          onChange={(e) => setModelFilter(e.target.value)}
        />
      </div>

      <div className="panel">
        {loading ? (
          <div className="loading">Loading…</div>
        ) : logs.length === 0 ? (
          <div className="empty-state" data-testid="request-logs-empty">
            No request logs in this range. Logs appear after the first inference
            calls.
          </div>
        ) : (
          <table className="data" data-testid="request-logs-table">
            <thead>
              <tr>
                <th>Time</th>
                <th>Request</th>
                <th>Key</th>
                <th>Model</th>
                <th>Tokens</th>
                <th>Latency</th>
                <th>Status</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {logs.map((log) => (
                <tr key={log.requestLogId} data-testid={`request-log-row-${log.requestLogId}`}>
                  <td>{formatTime(log.createdAt)}</td>
                  <td className="mono">{log.requestId}</td>
                  <td className="mono">{log.apiKeyId}</td>
                  <td className="mono">{log.modelId}</td>
                  <td>
                    {log.promptTokens} / {log.completionTokens}
                  </td>
                  <td>{log.latencyMs} ms</td>
                  <td>
                    <StateBadge state={log.status} />
                  </td>
                  <td>
                    <button
                      className="link"
                      data-testid={`request-log-detail-${log.requestLogId}`}
                      onClick={() => setDetail(log)}
                    >
                      Detail
                    </button>
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

      {detail && (
        <Dialog title="Request Log Detail" onClose={() => setDetail(null)}>
          <div className="detail-grid">
            <div className="detail-item">
              <div className="label">Request ID</div>
              <div className="value mono">{detail.requestId}</div>
            </div>
            <div className="detail-item">
              <div className="label">Status</div>
              <div className="value">
                <StateBadge state={detail.status} />
              </div>
            </div>
            <div className="detail-item">
              <div className="label">Model</div>
              <div className="value mono">{detail.modelId}</div>
            </div>
            <div className="detail-item">
              <div className="label">Service</div>
              <div className="value mono">{detail.serviceId || '—'}</div>
            </div>
            <div className="detail-item">
              <div className="label">API Key</div>
              <div className="value mono">{detail.apiKeyId}</div>
            </div>
            <div className="detail-item">
              <div className="label">Latency</div>
              <div className="value">{detail.latencyMs} ms</div>
            </div>
            <div className="detail-item">
              <div className="label">Tokens</div>
              <div className="value">
                {detail.promptTokens} in / {detail.completionTokens} out /{' '}
                {detail.cachedTokens} cached / {detail.reasoningTokens} reasoning
              </div>
            </div>
            <div className="detail-item">
              <div className="label">Created</div>
              <div className="value">{formatTime(detail.createdAt)}</div>
            </div>
            {detail.error && (
              <div className="detail-item">
                <div className="label">Error</div>
                <div className="value">{detail.error}</div>
              </div>
            )}
          </div>
        </Dialog>
      )}
    </div>
  );
}
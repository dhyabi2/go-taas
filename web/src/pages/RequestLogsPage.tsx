// Request Logs page: per-request metadata with filters and drill-down.
// Implements docs/design/request-logs-playground.md Increment A.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { api, formatTime, type PageMeta, type RequestLog } from '../api';
import { useOrg } from '../org';
import { useI18n } from '../i18n';
import { Dialog, ErrorBanner, Pagination, StateBadge, usePolling } from '../components';

interface ListResponse {
  response: { code: number; message: string };
  requestLogs: RequestLog[];
  pageMeta?: PageMeta;
}

const PAGE_SIZE = 20;
const RANGE_PRESETS = [
  { id: '24h', labelKey: 'reqlogs.range24h', hours: 24 },
  { id: '7d', labelKey: 'reqlogs.range7d', hours: 7 * 24 },
  { id: '30d', labelKey: 'reqlogs.range30d', hours: 30 * 24 },
];

function rangeFor(preset: string): { since: number; until: number } {
  const now = Math.floor(Date.now() / 1000);
  const hours = RANGE_PRESETS.find((p) => p.id === preset)?.hours || 24;
  return { since: now - hours * 3600, until: now };
}

export default function RequestLogsPage() {
  const { orgId } = useOrg();
  const { t } = useI18n();
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
      setError(e instanceof Error ? e.message : t('reqlogs.loadFailed'));
    } finally {
      setLoading(false);
    }
  }, [orgId, range.since, range.until, offset, statusFilter, keyFilter, modelFilter, t]);

  useEffect(() => {
    void load();
  }, [load]);

  usePolling(() => void load(), 60_000, true);

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>{t('reqlogs.title')}</h1>
          <div className="subtitle">{t('reqlogs.subtitle')}</div>
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
            {t(p.labelKey)}
          </button>
        ))}
        <select
          data-testid="request-log-filter-status"
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value)}
        >
          <option value="">{t('common.allStatuses')}</option>
          <option value="success">{t('reqlogs.filterSuccess')}</option>
          <option value="error">{t('reqlogs.filterError')}</option>
          <option value="streaming">{t('reqlogs.filterStreaming')}</option>
        </select>
        <input
          data-testid="request-log-filter-key"
          placeholder={t('reqlogs.placeholderApiKey')}
          value={keyFilter}
          onChange={(e) => setKeyFilter(e.target.value)}
        />
        <input
          data-testid="request-log-filter-model"
          placeholder={t('reqlogs.placeholderModel')}
          value={modelFilter}
          onChange={(e) => setModelFilter(e.target.value)}
        />
      </div>

      <div className="panel">
        {loading ? (
          <div className="loading">{t('common.loading')}</div>
        ) : logs.length === 0 ? (
          <div className="empty-state" data-testid="request-logs-empty">
            {t('reqlogs.empty')}
          </div>
        ) : (
          <table className="data" data-testid="request-logs-table">
            <thead>
              <tr>
                <th>{t('reqlogs.colTime')}</th>
                <th>{t('reqlogs.colRequest')}</th>
                <th>{t('reqlogs.colKey')}</th>
                <th>{t('reqlogs.colModel')}</th>
                <th>{t('reqlogs.colTokens')}</th>
                <th>{t('reqlogs.colLatency')}</th>
                <th>{t('reqlogs.colStatus')}</th>
                <th>{t('common.actions')}</th>
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
                  <td>{t('reqlogs.latencyMs', { latencyMs: log.latencyMs })}</td>
                  <td>
                    <StateBadge state={log.status} />
                  </td>
                  <td>
                    <button
                      className="link"
                      data-testid={`request-log-detail-${log.requestLogId}`}
                      onClick={() => setDetail(log)}
                    >
                      {t('common.detail')}
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
        <Dialog title={t('reqlogs.detailTitle')} onClose={() => setDetail(null)}>
          <div className="detail-grid">
            <div className="detail-item">
              <div className="label">{t('reqlogs.fieldRequestId')}</div>
              <div className="value mono">{detail.requestId}</div>
            </div>
            <div className="detail-item">
              <div className="label">{t('reqlogs.fieldStatus')}</div>
              <div className="value">
                <StateBadge state={detail.status} />
              </div>
            </div>
            <div className="detail-item">
              <div className="label">{t('reqlogs.fieldModel')}</div>
              <div className="value mono">{detail.modelId}</div>
            </div>
            <div className="detail-item">
              <div className="label">{t('reqlogs.fieldService')}</div>
              <div className="value mono">{detail.serviceId || '—'}</div>
            </div>
            <div className="detail-item">
              <div className="label">{t('reqlogs.fieldApiKey')}</div>
              <div className="value mono">{detail.apiKeyId}</div>
            </div>
            <div className="detail-item">
              <div className="label">{t('reqlogs.fieldLatency')}</div>
              <div className="value">{t('reqlogs.latencyMs', { latencyMs: detail.latencyMs })}</div>
            </div>
            <div className="detail-item">
              <div className="label">{t('reqlogs.fieldTokens')}</div>
              <div className="value">
                {t('reqlogs.tokensText', {
                  in: detail.promptTokens,
                  out: detail.completionTokens,
                  cached: detail.cachedTokens,
                  reasoning: detail.reasoningTokens,
                })}
              </div>
            </div>
            <div className="detail-item">
              <div className="label">{t('reqlogs.fieldCreated')}</div>
              <div className="value">{formatTime(detail.createdAt)}</div>
            </div>
            {detail.error && (
              <div className="detail-item">
                <div className="label">{t('reqlogs.fieldError')}</div>
                <div className="value">{detail.error}</div>
              </div>
            )}
          </div>
        </Dialog>
      )}
    </div>
  );
}

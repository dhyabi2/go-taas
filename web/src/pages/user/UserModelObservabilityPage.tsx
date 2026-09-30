// End-user Model Observability page (feature #24): the tenant's own
// single-model performance — summary cards, an inline-SVG time-series
// chart with a metric switcher, and a per-API-key breakdown scoped to
// the caller's organization. End-user surface: route
// /models/:modelId/observability, API /api/v1/models/{model_id}/observability.
// Exposes no service ids or operator internals (D7).

import { useCallback, useEffect, useMemo, useState } from 'react';
import { useApi } from '../../surface';
import { useOrg } from '../../org';
import { useI18n } from '../../i18n';
import { BackLink, ErrorBanner, Pagination } from '../../components';
import ObservabilityChart, { type ObservabilityMetric } from '../../components/ObservabilityChart';
import ObservabilityCards from '../../components/ObservabilityCards';
import {
  type GetModelObservabilityResponse,
  type ObservabilityKeyRow,
} from '../../api';

const PAGE_SIZE = 20;
const RANGE_PRESETS = [
  { id: '24h', labelKey: 'observability.range24h', hours: 24 },
  { id: '7d', labelKey: 'observability.range7d', hours: 7 * 24 },
  { id: '30d', labelKey: 'observability.range30d', hours: 30 * 24 },
  { id: 'custom', labelKey: 'observability.customRange', hours: 0 },
];

function rangeFor(preset: string, customSince: string, customUntil: string): { since: number; until: number } {
  const now = Math.floor(Date.now() / 1000);
  if (preset === 'custom') {
    const until = customUntil ? Math.floor(new Date(customUntil).getTime() / 1000) : now;
    const since = customSince
      ? Math.floor(new Date(customSince).getTime() / 1000)
      : until - 24 * 3600;
    return { since, until };
  }
  const hours = RANGE_PRESETS.find((p) => p.id === preset)?.hours || 24;
  return { since: now - hours * 3600, until: now };
}

function errorRatePercent(row: ObservabilityKeyRow): string {
  const req = parseInt(row.requestCount || '0', 10);
  if (req === 0) return '0.0%';
  return `${((parseInt(row.errorCount || '0', 10) / req) * 100).toFixed(1)}%`;
}

export default function UserModelObservabilityPage() {
  const api = useApi();
  const { orgId } = useOrg();
  const { t } = useI18n();
  const modelId = window.location.pathname.split('/').pop() || '';
  const [preset, setPreset] = useState('24h');
  const [customSince, setCustomSince] = useState('');
  const [customUntil, setCustomUntil] = useState('');
  const [metric, setMetric] = useState<ObservabilityMetric>('latency');
  const [data, setData] = useState<GetModelObservabilityResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [notFound, setNotFound] = useState(false);
  const [offset, setOffset] = useState(0);
  const [sortKey, setSortKey] = useState('requestCount');
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('desc');

  const range = useMemo(
    () => rangeFor(preset, customSince, customUntil),
    [preset, customSince, customUntil],
  );

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    setNotFound(false);
    try {
      const params = new URLSearchParams({
        since: String(range.since),
        until: String(range.until),
      });
      const next = await api.get<GetModelObservabilityResponse>(
        `/api/v1/models/${modelId}/observability?${params.toString()}`,
        orgId,
      );
      setData(next);
    } catch (e) {
      const code = e && (e as { code?: number }).code;
      if (code === 10801) {
        setNotFound(true);
      } else {
        setError(e instanceof Error ? e.message : t('observability.loadFailed'));
      }
    } finally {
      setLoading(false);
    }
  }, [api, orgId, modelId, range.since, range.until, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const cards = data?.cards || null;
  const series = data?.series || [];
  const keys = data?.keys || [];

  const sortedKeys = useMemo(() => {
    const arr = [...keys];
    arr.sort((a, b) => {
      const av = parseInt(a[sortKey as keyof ObservabilityKeyRow] as string || '0', 10);
      const bv = parseInt(b[sortKey as keyof ObservabilityKeyRow] as string || '0', 10);
      const cmp = av - bv;
      return sortDir === 'asc' ? cmp : -cmp;
    });
    return arr;
  }, [keys, sortKey, sortDir]);

  const pageRows = sortedKeys.slice(offset, offset + PAGE_SIZE);

  const toggleSort = (key: string) => {
    if (sortKey === key) {
      setSortDir(sortDir === 'asc' ? 'desc' : 'asc');
    } else {
      setSortKey(key);
      setSortDir('desc');
    }
    setOffset(0);
  };

  const dataThrough = cards ? parseInt(cards.dataThrough || '0', 10) : 0;
  const pending = dataThrough > 0 && range.until > dataThrough + 3600;

  if (notFound) {
    return (
      <div data-testid="user-observability-model-notfound">
        <BackLink to={`/models/${modelId}`} label={t('observability.backModel')} />
        <div className="error" data-testid="user-observability-model-notfound-message">
          {t('observability.modelNotFound')}
        </div>
      </div>
    );
  }

  return (
    <div data-testid="user-observability-page">
      <BackLink to={`/models/${modelId}`} label={t('observability.backModel')} />
      <div className="page-header">
        <div>
          <h1 data-testid="user-observability-model-name">{modelId}</h1>
          <div className="subtitle mono">{modelId}</div>
        </div>
      </div>

      {error && <ErrorBanner message={error} />}
      {pending && (
        <div className="pending-badge" data-testid="user-observability-pending-badge">
          {t('observability.pending')}
        </div>
      )}

      <div className="toolbar" style={{ marginBottom: 14 }} data-testid="observability-filter-range">
        {RANGE_PRESETS.map((p) => (
          <button
            key={p.id}
            className={preset === p.id ? '' : 'secondary'}
            data-testid={`observability-range-${p.id}`}
            onClick={() => setPreset(p.id)}
          >
            {t(p.labelKey)}
          </button>
        ))}
        {preset === 'custom' && (
          <>
            <input
              type="datetime-local"
              data-testid="observability-custom-since"
              value={customSince}
              onChange={(e) => setCustomSince(e.target.value)}
            />
            <input
              type="datetime-local"
              data-testid="observability-custom-until"
              value={customUntil}
              onChange={(e) => setCustomUntil(e.target.value)}
            />
          </>
        )}
      </div>

      <ObservabilityCards
        cards={cards}
        dataThroughLabel={t('observability.dataThrough')}
      />

      <div className="panel" style={{ marginTop: 16 }}>
        <div className="toolbar" style={{ marginBottom: 8 }}>
          <span className="muted">{t('observability.chartTitle')}</span>
          <span className="toolbar-spacer" />
          <div data-testid="observability-metric-toggle">
            {(['latency', 'throughput', 'errorRate', 'tokens'] as ObservabilityMetric[]).map((m) => (
              <button
                key={m}
                className={metric === m ? '' : 'secondary'}
                data-testid={`observability-metric-${m}`}
                disabled={loading}
                onClick={() => setMetric(m)}
              >
                {t(`observability.metric.${m}`)}
              </button>
            ))}
          </div>
        </div>
        <ObservabilityChart series={series} metric={metric} />
      </div>

      <div className="panel" style={{ marginTop: 16 }}>
        <h3 style={{ marginTop: 0 }}>{t('observability.keysTitle')}</h3>
        {keys.length === 0 ? (
          <p className="muted" data-testid="observability-key-empty">
            {t('observability.emptyModel')}
          </p>
        ) : (
          <>
            <table className="data" data-testid="observability-key-table">
              <thead>
                <tr>
                  <th>{t('observability.colApiKey')}</th>
                  <th>
                    <button className="sortable" data-testid="observability-key-sort-requests" onClick={() => toggleSort('requestCount')}>
                      {t('observability.colRequests')}
                    </button>
                  </th>
                  <th>
                    <button className="sortable" data-testid="observability-key-sort-error" onClick={() => toggleSort('errorCount')}>
                      {t('observability.colErrorRate')}
                    </button>
                  </th>
                  <th>
                    <button className="sortable" data-testid="observability-key-sort-avg" onClick={() => toggleSort('avgLatencyMs')}>
                      {t('observability.colAvgLatency')}
                    </button>
                  </th>
                  <th>
                    <button className="sortable" data-testid="observability-key-sort-p95" onClick={() => toggleSort('p95LatencyMs')}>
                      {t('observability.colP95')}
                    </button>
                  </th>
                  <th>
                    <button className="sortable" data-testid="observability-key-sort-tokens" onClick={() => toggleSort('outputTokensPerSec')}>
                      {t('observability.colTokensSec')}
                    </button>
                  </th>
                </tr>
              </thead>
              <tbody>
                {pageRows.map((k) => (
                  <tr key={k.apiKeyId} data-testid={`observability-key-row-${k.apiKeyId}`}>
                    <td>{k.apiKeyName || k.apiKeyId}</td>
                    <td>{parseInt(k.requestCount || '0', 10).toLocaleString()}</td>
                    <td>{errorRatePercent(k)}</td>
                    <td>{k.avgLatencyMs || '0'} ms</td>
                    <td>{k.p95LatencyMs || '0'} ms</td>
                    <td>{(k.outputTokensPerSec || '0').toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <Pagination
              offset={offset}
              limit={PAGE_SIZE}
              total={sortedKeys.length}
              onPageChange={setOffset}
            />
          </>
        )}
      </div>
    </div>
  );
}
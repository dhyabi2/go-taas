// Admin Error Analysis page (feature #31): the fleet-wide error analysis
// — summary cards, an error-rate trend chart with a metric switcher, and
// a top-causes table. Admin surface: route /admin/errors, API
// /api/v1/admin/errors.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { useApi } from '../surface';
import { useOrg } from '../org';
import { useI18n } from '../i18n';
import { navigate } from '../router';
import { ErrorBanner, Pagination } from '../components';
import ErrorChart, { type ErrorMetric } from '../components/ErrorChart';
import ErrorCards from '../components/ErrorCards';
import ErrorCausesTable from '../components/ErrorCausesTable';
import { type GetErrorAnalysisOverviewResponse } from '../api';

const PAGE_SIZE = 20;
const RANGE_PRESETS = [
  { id: '24h', labelKey: 'errors.range24h', hours: 24 },
  { id: '7d', labelKey: 'errors.range7d', hours: 7 * 24 },
  { id: '30d', labelKey: 'errors.range30d', hours: 30 * 24 },
  { id: 'custom', labelKey: 'errors.customRange', hours: 0 },
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

export default function ErrorAnalysisPage() {
  const api = useApi();
  const { orgId } = useOrg();
  const { t } = useI18n();
  const [preset, setPreset] = useState('24h');
  const [customSince, setCustomSince] = useState('');
  const [customUntil, setCustomUntil] = useState('');
  const [modelFilter, setModelFilter] = useState('');
  const [metric, setMetric] = useState<ErrorMetric>('errorCount');
  const [data, setData] = useState<GetErrorAnalysisOverviewResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [stale, setStale] = useState(false);
  const [offset, setOffset] = useState(0);

  const range = useMemo(
    () => rangeFor(preset, customSince, customUntil),
    [preset, customSince, customUntil],
  );

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const params = new URLSearchParams({
        since: String(range.since),
        until: String(range.until),
      });
      if (modelFilter) params.set('model_id', modelFilter);
      const next = await api.get<GetErrorAnalysisOverviewResponse>(
        `/api/v1/admin/errors?${params.toString()}`,
        orgId,
      );
      setData(next);
      setStale(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : t('errors.loadFailed'));
      setStale(true);
    } finally {
      setLoading(false);
    }
  }, [api, orgId, range.since, range.until, modelFilter, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const cards = data?.cards || null;
  const series = data?.series || [];
  const causes = data?.causes || [];

  const pageRows = causes.slice(offset, offset + PAGE_SIZE);

  const dataThrough = cards ? parseInt(cards.dataThrough || '0', 10) : 0;
  const pending = dataThrough > 0 && range.until > dataThrough + 3600;

  return (
    <div data-testid="errors-page">
      <div className="page-header">
        <div>
          <h1>{t('errors.title')}</h1>
          <div className="subtitle">{t('errors.subtitle')}</div>
        </div>
        <button
          className="secondary"
          data-testid="errors-refresh"
          disabled={loading}
          onClick={() => void load()}
        >
          {t('errors.refresh')}
        </button>
      </div>

      {error && <ErrorBanner message={error} />}
      {stale && data && (
        <div className="error-banner" data-testid="errors-stale-banner">
          {t('errors.staleData')}
        </div>
      )}
      {pending && (
        <div className="pending-badge" data-testid="errors-pending-badge">
          {t('errors.pending')}
        </div>
      )}

      <div className="toolbar" style={{ marginBottom: 14 }} data-testid="errors-filter-range">
        {RANGE_PRESETS.map((p) => (
          <button
            key={p.id}
            className={preset === p.id ? '' : 'secondary'}
            data-testid={`errors-range-${p.id}`}
            onClick={() => setPreset(p.id)}
          >
            {t(p.labelKey)}
          </button>
        ))}
        {preset === 'custom' && (
          <>
            <input
              type="datetime-local"
              data-testid="errors-custom-since"
              value={customSince}
              onChange={(e) => setCustomSince(e.target.value)}
            />
            <input
              type="datetime-local"
              data-testid="errors-custom-until"
              value={customUntil}
              onChange={(e) => setCustomUntil(e.target.value)}
            />
          </>
        )}
        <span className="toolbar-spacer" />
        <label className="muted" htmlFor="errors-model-filter">
          {t('errors.modelFilter')}
        </label>
        <select
          id="errors-model-filter"
          data-testid="errors-filter-model"
          value={modelFilter}
          onChange={(e) => {
            setModelFilter(e.target.value);
            setOffset(0);
          }}
        >
          <option value="">{t('errors.allModels')}</option>
          {causes.map((c) => (
            <option key={c.errorCode} value={c.errorCode}>
              {c.errorCode}
            </option>
          ))}
        </select>
      </div>

      {cards && <ErrorCards cards={cards} />}

      <div className="panel" style={{ marginTop: 16 }}>
        <div className="toolbar" style={{ marginBottom: 8 }}>
          <span className="muted">{t('errors.chartTitle')}</span>
          <span className="toolbar-spacer" />
          <div data-testid="errors-metric-toggle">
            {(['errorCount', 'errorRate'] as ErrorMetric[]).map((m) => (
              <button
                key={m}
                className={metric === m ? '' : 'secondary'}
                data-testid={`errors-metric-${m}`}
                disabled={loading}
                onClick={() => setMetric(m)}
              >
                {t(`errors.metric.${m}`)}
              </button>
            ))}
          </div>
        </div>
        <ErrorChart series={series} metric={metric} />
      </div>

      <div className="panel" style={{ marginTop: 16 }}>
        <h3 style={{ marginTop: 0 }}>{t('errors.causesTitle')}</h3>
        {causes.length === 0 ? (
          <p className="muted" data-testid="errors-empty">
            {t('errors.empty')}
          </p>
        ) : (
          <>
            <ErrorCausesTable
              rows={pageRows}
              onSelect={(code) => navigate(`/admin/errors/${code}`)}
            />
            <Pagination
              offset={offset}
              limit={PAGE_SIZE}
              total={causes.length}
              onPageChange={setOffset}
            />
          </>
        )}
      </div>
    </div>
  );
}
// Admin Cost Detail page (feature #29): a single dimension value's cost
// — summary cards and an inline-SVG cost trend chart with a metric
// switcher. Admin surface: route /admin/cost/:dimension/:value, API
// /api/v1/admin/cost/{dimension}/{value}.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { useApi } from '../surface';
import { useOrg } from '../org';
import { useI18n } from '../i18n';
import { BackLink, ErrorBanner } from '../components';
import CostChart, { type CostMetric } from '../components/CostChart';
import CostCards from '../components/CostCards';
import { type GetCostAnalyticsResponse } from '../api';

const RANGE_PRESETS = [
  { id: '24h', labelKey: 'cost.range24h', hours: 24 },
  { id: '7d', labelKey: 'cost.range7d', hours: 7 * 24 },
  { id: '30d', labelKey: 'cost.range30d', hours: 30 * 24 },
  { id: 'custom', labelKey: 'cost.customRange', hours: 0 },
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

export default function CostDetailPage() {
  const api = useApi();
  const { orgId } = useOrg();
  const { t } = useI18n();
  const parts = window.location.pathname.split('/').filter(Boolean);
  const dimension = parts[2] || '';
  const value = parts[3] || '';
  const [preset, setPreset] = useState('24h');
  const [customSince, setCustomSince] = useState('');
  const [customUntil, setCustomUntil] = useState('');
  const [metric, setMetric] = useState<CostMetric>('cost');
  const [data, setData] = useState<GetCostAnalyticsResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [notFound, setNotFound] = useState(false);

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
      const next = await api.get<GetCostAnalyticsResponse>(
        `/api/v1/admin/cost/${dimension}/${value}?${params.toString()}`,
        orgId,
      );
      setData(next);
    } catch (e) {
      const code = e && (e as { code?: number }).code;
      if (code === 11302) {
        setNotFound(true);
      } else {
        setError(e instanceof Error ? e.message : t('cost.loadFailed'));
      }
    } finally {
      setLoading(false);
    }
  }, [api, orgId, dimension, value, range.since, range.until, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const cards = data?.cards || null;
  const series = data?.series || [];

  const dataThrough = cards ? parseInt(cards.dataThrough || '0', 10) : 0;
  const pending = dataThrough > 0 && range.until > dataThrough + 3600;

  if (notFound) {
    return (
      <div data-testid="cost-detail-notfound">
        <BackLink to="/admin/cost" label={t('cost.backOverview')} />
        <div className="empty-state">{t('cost.notFound')}</div>
      </div>
    );
  }

  return (
    <div data-testid="cost-detail-page">
      <BackLink to="/admin/cost" label={t('cost.backOverview')} />
      <div className="page-header">
        <div>
          <h1>{t('cost.detailTitle')}</h1>
          <div className="subtitle mono">{value}</div>
        </div>
        <button
          className="secondary"
          data-testid="cost-detail-refresh"
          disabled={loading}
          onClick={() => void load()}
        >
          {t('cost.refresh')}
        </button>
      </div>

      {error && <ErrorBanner message={error} />}
      {pending && (
        <div className="pending-badge" data-testid="cost-detail-pending-badge">
          {t('cost.pending')}
        </div>
      )}

      <div className="toolbar" style={{ marginBottom: 14 }} data-testid="cost-detail-filter-range">
        {RANGE_PRESETS.map((p) => (
          <button
            key={p.id}
            className={preset === p.id ? '' : 'secondary'}
            data-testid={`cost-detail-range-${p.id}`}
            onClick={() => setPreset(p.id)}
          >
            {t(p.labelKey)}
          </button>
        ))}
        {preset === 'custom' && (
          <>
            <input
              type="datetime-local"
              data-testid="cost-detail-custom-since"
              value={customSince}
              onChange={(e) => setCustomSince(e.target.value)}
            />
            <input
              type="datetime-local"
              data-testid="cost-detail-custom-until"
              value={customUntil}
              onChange={(e) => setCustomUntil(e.target.value)}
            />
          </>
        )}
      </div>

      {cards && <CostCards cards={cards} />}

      <div className="panel" style={{ marginTop: 16 }}>
        <div className="toolbar" style={{ marginBottom: 8 }}>
          <span className="muted">{t('cost.chartTitle')}</span>
          <span className="toolbar-spacer" />
          <div data-testid="cost-detail-metric-toggle">
            {(['cost', 'tokens', 'costPerToken'] as CostMetric[]).map((m) => (
              <button
                key={m}
                className={metric === m ? '' : 'secondary'}
                data-testid={`cost-detail-metric-${m}`}
                disabled={loading}
                onClick={() => setMetric(m)}
              >
                {t(`cost.metric.${m}`)}
              </button>
            ))}
          </div>
        </div>
        <CostChart series={series} metric={metric} />
      </div>
    </div>
  );
}
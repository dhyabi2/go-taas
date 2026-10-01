// Admin Usage Key Detail page (feature #28): a single API key's usage —
// summary cards and an inline-SVG trend chart with a metric switcher.
// Admin surface: route /admin/usage/keys/:apiKeyId, API
// /api/v1/admin/usage/keys/{api_key_id}.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { useApi } from '../surface';
import { useOrg } from '../org';
import { useI18n } from '../i18n';
import { BackLink, ErrorBanner } from '../components';
import UsageKeysChart, { type UsageKeysMetric } from '../components/UsageKeysChart';
import UsageKeysCards from '../components/UsageKeysCards';
import { type GetUsageKeysResponse } from '../api';

const RANGE_PRESETS = [
  { id: '24h', labelKey: 'usageKeys.range24h', hours: 24 },
  { id: '7d', labelKey: 'usageKeys.range7d', hours: 7 * 24 },
  { id: '30d', labelKey: 'usageKeys.range30d', hours: 30 * 24 },
  { id: 'custom', labelKey: 'usageKeys.customRange', hours: 0 },
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

export default function UsageKeyDetailPage() {
  const api = useApi();
  const { orgId } = useOrg();
  const { t } = useI18n();
  const apiKeyId = window.location.pathname.split('/').pop() || '';
  const [preset, setPreset] = useState('24h');
  const [customSince, setCustomSince] = useState('');
  const [customUntil, setCustomUntil] = useState('');
  const [metric, setMetric] = useState<UsageKeysMetric>('requests');
  const [data, setData] = useState<GetUsageKeysResponse | null>(null);
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
      const next = await api.get<GetUsageKeysResponse>(
        `/api/v1/admin/usage/keys/${apiKeyId}?${params.toString()}`,
        orgId,
      );
      setData(next);
    } catch (e) {
      const code = e && (e as { code?: number }).code;
      if (code === 11201) {
        setNotFound(true);
      } else {
        setError(e instanceof Error ? e.message : t('usageKeys.loadFailed'));
      }
    } finally {
      setLoading(false);
    }
  }, [api, orgId, apiKeyId, range.since, range.until, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const cards = data?.cards || null;
  const series = data?.series || [];

  const dataThrough = cards ? parseInt(cards.dataThrough || '0', 10) : 0;
  const pending = dataThrough > 0 && range.until > dataThrough + 3600;

  if (notFound) {
    return (
      <div data-testid="usage-key-detail-notfound">
        <BackLink to="/admin/usage/keys" label={t('usageKeys.backOverview')} />
        <div className="empty-state">{t('usageKeys.notFound')}</div>
      </div>
    );
  }

  return (
    <div data-testid="usage-key-detail-page">
      <BackLink to="/admin/usage/keys" label={t('usageKeys.backOverview')} />
      <div className="page-header">
        <div>
          <h1>{t('usageKeys.detailTitle')}</h1>
          <div className="subtitle mono">{apiKeyId}</div>
        </div>
        <button
          className="secondary"
          data-testid="usage-key-detail-refresh"
          disabled={loading}
          onClick={() => void load()}
        >
          {t('usageKeys.refresh')}
        </button>
      </div>

      {error && <ErrorBanner message={error} />}
      {pending && (
        <div className="pending-badge" data-testid="usage-key-detail-pending-badge">
          {t('usageKeys.pending')}
        </div>
      )}

      <div className="toolbar" style={{ marginBottom: 14 }} data-testid="usage-key-detail-filter-range">
        {RANGE_PRESETS.map((p) => (
          <button
            key={p.id}
            className={preset === p.id ? '' : 'secondary'}
            data-testid={`usage-key-detail-range-${p.id}`}
            onClick={() => setPreset(p.id)}
          >
            {t(p.labelKey)}
          </button>
        ))}
        {preset === 'custom' && (
          <>
            <input
              type="datetime-local"
              data-testid="usage-key-detail-custom-since"
              value={customSince}
              onChange={(e) => setCustomSince(e.target.value)}
            />
            <input
              type="datetime-local"
              data-testid="usage-key-detail-custom-until"
              value={customUntil}
              onChange={(e) => setCustomUntil(e.target.value)}
            />
          </>
        )}
      </div>

      {cards && <UsageKeysCards cards={cards} />}

      <div className="panel" style={{ marginTop: 16 }}>
        <div className="toolbar" style={{ marginBottom: 8 }}>
          <span className="muted">{t('usageKeys.chartTitle')}</span>
          <span className="toolbar-spacer" />
          <div data-testid="usage-key-detail-metric-toggle">
            {(['requests', 'tokens', 'cost', 'errorRate'] as UsageKeysMetric[]).map((m) => (
              <button
                key={m}
                className={metric === m ? '' : 'secondary'}
                data-testid={`usage-key-detail-metric-${m}`}
                disabled={loading}
                onClick={() => setMetric(m)}
              >
                {t(`usageKeys.metric.${m}`)}
              </button>
            ))}
          </div>
        </div>
        <UsageKeysChart series={series} metric={metric} />
      </div>
    </div>
  );
}
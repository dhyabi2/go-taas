// End-user Error Detail page (feature #31): a single error code's
// analysis — summary cards and an inline-SVG error-rate trend chart with
// a metric switcher. User surface: route /errors/:errorCode, API
// /api/v1/errors/{error_code}. Tenant-scoped and masked (AD8).

import { useCallback, useEffect, useMemo, useState } from 'react';
import { useApi } from '../../surface';
import { useOrg } from '../../org';
import { useI18n } from '../../i18n';
import { BackLink, ErrorBanner } from '../../components';
import ErrorChart, { type ErrorMetric } from '../../components/ErrorChart';
import ErrorCards from '../../components/ErrorCards';
import { type GetErrorAnalysisResponse } from '../../api';

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

export default function UserErrorDetailPage() {
  const api = useApi();
  const { orgId } = useOrg();
  const { t } = useI18n();
  const errorCode = window.location.pathname.split('/').pop() || '';
  const [preset, setPreset] = useState('24h');
  const [customSince, setCustomSince] = useState('');
  const [customUntil, setCustomUntil] = useState('');
  const [metric, setMetric] = useState<ErrorMetric>('errorCount');
  const [data, setData] = useState<GetErrorAnalysisResponse | null>(null);
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
      const next = await api.get<GetErrorAnalysisResponse>(
        `/api/v1/errors/${errorCode}?${params.toString()}`,
        orgId,
      );
      setData(next);
    } catch (e) {
      const code = e && (e as { code?: number }).code;
      if (code === 11501) {
        setNotFound(true);
      } else {
        setError(e instanceof Error ? e.message : t('errors.loadFailed'));
      }
    } finally {
      setLoading(false);
    }
  }, [api, orgId, errorCode, range.since, range.until, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const cards = data?.cards || null;
  const series = data?.series || [];

  const dataThrough = cards ? parseInt(cards.dataThrough || '0', 10) : 0;
  const pending = dataThrough > 0 && range.until > dataThrough + 3600;

  if (notFound) {
    return (
      <div data-testid="user-error-detail-notfound">
        <BackLink to="/errors" label={t('errors.backOverview')} />
        <div className="empty-state">{t('errors.notFound')}</div>
      </div>
    );
  }

  return (
    <div data-testid="user-error-detail-page">
      <BackLink to="/errors" label={t('errors.backOverview')} />
      <div className="page-header">
        <div>
          <h1>{t('errors.detailTitle')}</h1>
          <div className="subtitle mono">{errorCode}</div>
        </div>
        <button
          className="secondary"
          data-testid="user-error-detail-refresh"
          disabled={loading}
          onClick={() => void load()}
        >
          {t('errors.refresh')}
        </button>
      </div>

      {error && <ErrorBanner message={error} />}
      {pending && (
        <div className="pending-badge" data-testid="user-error-detail-pending-badge">
          {t('errors.pending')}
        </div>
      )}

      <div className="toolbar" style={{ marginBottom: 14 }} data-testid="user-error-detail-filter-range">
        {RANGE_PRESETS.map((p) => (
          <button
            key={p.id}
            className={preset === p.id ? '' : 'secondary'}
            data-testid={`user-error-detail-range-${p.id}`}
            onClick={() => setPreset(p.id)}
          >
            {t(p.labelKey)}
          </button>
        ))}
        {preset === 'custom' && (
          <>
            <input
              type="datetime-local"
              data-testid="user-error-detail-custom-since"
              value={customSince}
              onChange={(e) => setCustomSince(e.target.value)}
            />
            <input
              type="datetime-local"
              data-testid="user-error-detail-custom-until"
              value={customUntil}
              onChange={(e) => setCustomUntil(e.target.value)}
            />
          </>
        )}
      </div>

      {cards && <ErrorCards cards={cards} />}

      <div className="panel" style={{ marginTop: 16 }}>
        <div className="toolbar" style={{ marginBottom: 8 }}>
          <span className="muted">{t('errors.chartTitle')}</span>
          <span className="toolbar-spacer" />
          <div data-testid="user-error-detail-metric-toggle">
            {(['errorCount', 'errorRate'] as ErrorMetric[]).map((m) => (
              <button
                key={m}
                className={metric === m ? '' : 'secondary'}
                data-testid={`user-error-detail-metric-${m}`}
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
    </div>
  );
}
// Admin Webhook detail page (feature #23): config, signing secret
// (reveal/roll), and delivery log with manual resend. Route
// /admin/webhooks/:webhookId, API /api/v1/admin/webhooks/*.

import { useCallback, useEffect, useState } from 'react';
import { useApi } from '../surface';
import { useOrg } from '../org';
import { useI18n } from '../i18n';
import { navigate, useParams } from '../router';
import { ApiError, formatTime, type PageMeta } from '../api';
import { ErrorBanner, Pagination, StateBadge } from '../components';
import SecretRevealDialog from '../components/SecretRevealDialog';
import type { Webhook } from './WebhooksPage';

export interface WebhookDelivery {
  deliveryId: string;
  webhookId: string;
  eventId: string;
  eventType: string;
  status: string;
  httpStatusCode: number;
  attemptCount: number;
  failureReason: string;
  createdAt: string;
  lastAttemptAt: string;
}

interface GetResponse {
  response: { code: number; message: string };
  webhook: Webhook;
}

interface DeliveriesResponse {
  response: { code: number; message: string };
  deliveries: WebhookDelivery[];
  pageMeta?: PageMeta;
}

const PAGE_SIZE = 20;

function deliveryStatusLabel(status: string): string {
  switch (status) {
    case 'WEBHOOK_DELIVERY_STATUS_DELIVERED':
      return 'delivered';
    case 'WEBHOOK_DELIVERY_STATUS_FAILED':
      return 'failed';
    case 'WEBHOOK_DELIVERY_STATUS_PENDING':
      return 'pending';
    default:
      return 'pending';
  }
}

export default function WebhookDetailPage() {
  const api = useApi();
  const { orgId } = useOrg();
  const { t } = useI18n();
  const params = useParams();
  const webhookId = params.webhookId || '';
  const [webhook, setWebhook] = useState<Webhook | null>(null);
  const [deliveries, setDeliveries] = useState<WebhookDelivery[]>([]);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [statusFilter, setStatusFilter] = useState('all');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [notFound, setNotFound] = useState(false);
  const [revealedSecret, setRevealedSecret] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await api.get<GetResponse>(`/api/v1/admin/webhooks/${webhookId}`, orgId);
      setWebhook(data.webhook);
      setNotFound(false);
    } catch (e) {
      if (e instanceof ApiError && e.code === 10701) {
        setNotFound(true);
      } else {
        setError(e instanceof Error ? e.message : t('webhooks.loadFailed'));
      }
    } finally {
      setLoading(false);
    }
  }, [api, orgId, webhookId, t]);

  const loadDeliveries = useCallback(async () => {
    try {
      const params = new URLSearchParams();
      params.set('page.offset', String(offset));
      params.set('page.limit', String(PAGE_SIZE));
      if (statusFilter !== 'all') params.set('status', statusFilter);
      const data = await api.get<DeliveriesResponse>(
        `/api/v1/admin/webhooks/${webhookId}/deliveries?${params.toString()}`,
        orgId,
      );
      setDeliveries(data.deliveries || []);
      setTotal(parseInt(data.pageMeta?.total || '0', 10) || 0);
    } catch (e) {
      setError(e instanceof Error ? e.message : t('webhooks.loadFailed'));
    }
  }, [api, orgId, webhookId, offset, statusFilter, t]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (webhookId) void loadDeliveries();
  }, [loadDeliveries, webhookId]);

  const toggleEnabled = async () => {
    if (!webhook) return;
    setBusy(true);
    try {
      await api.post(`/api/v1/admin/webhooks/${webhookId}:set-enabled`, orgId, {
        enabled: !webhook.enabled,
      });
      void load();
    } catch (e) {
      setError(e instanceof Error ? e.message : t('webhooks.updateFailed'));
    } finally {
      setBusy(false);
    }
  };

  const rollSecret = async () => {
    setBusy(true);
    try {
      const data = await api.post<{ plaintextSecret: string }>(
        `/api/v1/admin/webhooks/${webhookId}:roll-secret`,
        orgId,
        {},
      );
      setRevealedSecret(data.plaintextSecret);
    } catch (e) {
      setError(e instanceof Error ? e.message : t('webhooks.rollFailed'));
    } finally {
      setBusy(false);
    }
  };

  const test = async () => {
    setBusy(true);
    try {
      await api.post(`/api/v1/admin/webhooks/${webhookId}:test`, orgId, {});
      void loadDeliveries();
    } catch (e) {
      setError(e instanceof Error ? e.message : t('webhooks.testFailed'));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!webhook) return;
    if (!window.confirm(t('webhooks.deleteConfirm', { name: webhook.name }))) return;
    setBusy(true);
    try {
      await api.del(`/api/v1/admin/webhooks/${webhookId}`, orgId);
      navigate('/admin/webhooks');
    } catch (e) {
      setError(e instanceof Error ? e.message : t('webhooks.deleteFailed'));
    } finally {
      setBusy(false);
    }
  };

  const resend = async (deliveryId: string) => {
    setBusy(true);
    try {
      await api.post(
        `/api/v1/admin/webhooks/${webhookId}/deliveries/${deliveryId}:resend`,
        orgId,
        {},
      );
      void loadDeliveries();
    } catch (e) {
      setError(e instanceof Error ? e.message : t('webhooks.resendFailed'));
    } finally {
      setBusy(false);
    }
  };

  if (notFound) {
    return (
      <div className="not-found" data-testid="webhook-not-found">
        <h1>{t('webhooks.notFound')}</h1>
        <button onClick={() => navigate('/admin/webhooks')}>{t('webhooks.backToList')}</button>
      </div>
    );
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <button className="link" onClick={() => navigate('/admin/webhooks')}>
            ← {t('webhooks.backToList')}
          </button>
          <h1>{webhook?.name || t('webhooks.detailTitle')}</h1>
          <div className="subtitle mono">{webhook?.url}</div>
        </div>
        {webhook && <StateBadge state={webhook.enabled ? 'enabled' : 'disabled'} />}
      </div>

      {error && <ErrorBanner message={error} />}

      {loading ? (
        <div className="loading">{t('common.loading')}</div>
      ) : (
        webhook && (
          <>
            <div className="panel" data-testid="webhook-detail-config">
              <h3>{t('webhooks.configTitle')}</h3>
              <dl className="detail-grid">
                <dt>{t('webhooks.fieldName')}</dt>
                <dd>{webhook.name}</dd>
                <dt>{t('webhooks.fieldUrl')}</dt>
                <dd className="mono">{webhook.url}</dd>
                <dt>{t('webhooks.fieldEvents')}</dt>
                <dd>
                  {webhook.enabledEventTypes.map((ev) => (
                    <span key={ev} className="chip">
                      {ev}
                    </span>
                  ))}
                </dd>
                <dt>{t('webhooks.fieldMaxAttempts')}</dt>
                <dd>{webhook.maxAttempts}</dd>
                <dt>{t('webhooks.fieldBackoff')}</dt>
                <dd>{webhook.backoffSeconds}s</dd>
                <dt>{t('common.created')}</dt>
                <dd>{formatTime(webhook.createdAt)}</dd>
              </dl>
              <div className="dialog-actions">
                <button className="secondary" disabled={busy} onClick={() => void toggleEnabled()}>
                  {webhook.enabled ? t('common.disable') : t('common.enable')}
                </button>
                <button className="secondary" disabled={busy} onClick={() => void rollSecret()}>
                  {t('webhooks.rollSecret')}
                </button>
                <button className="secondary" disabled={busy} onClick={() => void test()}>
                  {t('webhooks.test')}
                </button>
                <button className="danger" disabled={busy} onClick={() => void remove()}>
                  {t('common.delete')}
                </button>
              </div>
            </div>

            <div className="panel" data-testid="webhook-secret-masked">
              <h3>{t('webhooks.secretTitle')}</h3>
              <code className="mono">whsec_••••••••</code>
              <div className="dialog-actions">
                <button className="secondary" disabled={busy} onClick={() => void rollSecret()}>
                  {t('webhooks.rollSecret')}
                </button>
              </div>
            </div>

            <div className="panel">
              <h3>{t('webhooks.deliveryLogTitle')}</h3>
              <div className="filter-bar">
                <select
                  data-testid="webhook-delivery-filter-status"
                  value={statusFilter}
                  onChange={(e) => {
                    setStatusFilter(e.target.value);
                    setOffset(0);
                  }}
                >
                  <option value="all">{t('common.allStatuses')}</option>
                  <option value="WEBHOOK_DELIVERY_STATUS_DELIVERED">{t('webhooks.delivered')}</option>
                  <option value="WEBHOOK_DELIVERY_STATUS_FAILED">{t('webhooks.failed')}</option>
                  <option value="WEBHOOK_DELIVERY_STATUS_PENDING">{t('webhooks.pending')}</option>
                </select>
              </div>
              {deliveries.length === 0 ? (
                <div className="empty-state" data-testid="webhook-delivery-empty">
                  {t('webhooks.deliveryEmpty')}
                </div>
              ) : (
                <table className="data" data-testid="webhook-delivery-table">
                  <thead>
                    <tr>
                      <th>{t('webhooks.colEventType')}</th>
                      <th>{t('common.status')}</th>
                      <th>{t('webhooks.colHttpStatus')}</th>
                      <th>{t('webhooks.colAttempts')}</th>
                      <th>{t('common.created')}</th>
                      <th>{t('common.actions')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {deliveries.map((d) => (
                      <tr key={d.deliveryId} data-testid={`webhook-delivery-row-${d.deliveryId}`}>
                        <td><code>{d.eventType}</code></td>
                        <td><StateBadge state={deliveryStatusLabel(d.status)} /></td>
                        <td>{d.httpStatusCode || '—'}</td>
                        <td>{d.attemptCount}</td>
                        <td>{formatTime(d.createdAt)}</td>
                        <td>
                          {d.status !== 'WEBHOOK_DELIVERY_STATUS_PENDING' && (
                            <button
                              className="link"
                              disabled={busy}
                              data-testid={`webhook-resend-${d.deliveryId}`}
                              onClick={() => void resend(d.deliveryId)}
                            >
                              {t('webhooks.resend')}
                            </button>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              {total > PAGE_SIZE && (
                <Pagination offset={offset} limit={PAGE_SIZE} total={total} onPageChange={setOffset} />
              )}
            </div>
          </>
        )
      )}

      {revealedSecret && (
        <SecretRevealDialog secret={revealedSecret} onDone={() => setRevealedSecret(null)} />
      )}
    </div>
  );
}
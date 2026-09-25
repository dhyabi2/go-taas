// Admin Payments page (feature-14): payment channels and payment intents.

import { useEffect, useState } from 'react';
import { useApi } from '../surface';
import { useOrg } from '../org';
import { formatTime } from '../api';

interface PaymentChannel {
  channelId: string;
  type: string;
  displayName: string;
  enabled: boolean;
  createdAt: string;
}

interface PaymentIntent {
  intentId: string;
  accountId: string;
  amountCents: string;
  channel: string;
  status: string;
  idempotencyKey: string;
  reference: string;
  createdAt: string;
  paidAt: string;
  payUrl: string;
}

export default function BillingPaymentsPage() {
  const api = useApi();
  const { orgId } = useOrg();
  const [channels, setChannels] = useState<PaymentChannel[]>([]);
  const [intents, setIntents] = useState<PaymentIntent[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    api
      .get<{ channels?: PaymentChannel[] }>('/api/v1/admin/billing/payment-channels?page.limit=100', orgId)
      .then((data) => setChannels(data.channels || []))
      .catch((e) => setError(e instanceof Error ? e.message : 'failed to load channels'));
    api
      .get<{ intents?: PaymentIntent[] }>('/api/v1/admin/billing/payment-intents?page.limit=100', orgId)
      .then((data) => setIntents(data.intents || []))
      .catch(() => {
        // Intents may be empty; not fatal.
      });
  }, [api, orgId]);

  const payIntent = async (intentId: string) => {
    try {
      await api.post(`/api/v1/admin/billing/payment-intents/${intentId}:pay`, orgId, {});
      setIntents((prev) =>
        prev.map((i) => (i.intentId === intentId ? { ...i, status: 'paid' } : i)),
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : 'payment failed');
    }
  };

  return (
    <div className="page">
      <h1>Payments</h1>
      {error && <div className="error">{error}</div>}
      <h2>Channels</h2>
      <table className="table" data-testid="payments-table">
        <thead>
          <tr>
            <th>Channel</th>
            <th>Type</th>
            <th>Status</th>
          </tr>
        </thead>
        <tbody>
          {channels.map((c) => (
            <tr key={c.channelId}>
              <td>{c.displayName}</td>
              <td>{c.type}</td>
              <td>{c.enabled ? 'Enabled' : 'Disabled'}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <h2>Payment Intents</h2>
      {intents.length === 0 ? (
        <div className="empty">No payment intents yet.</div>
      ) : (
        <table className="table">
          <thead>
            <tr>
              <th>Amount</th>
              <th>Channel</th>
              <th>Status</th>
              <th>Created</th>
              <th>Action</th>
            </tr>
          </thead>
          <tbody>
            {intents.map((i) => (
              <tr key={i.intentId} data-testid={`payment-intent-row-${i.intentId}`}>
                <td>{i.amountCents}</td>
                <td>{i.channel}</td>
                <td data-testid={`payment-intent-status-${i.intentId}`}>{i.status}</td>
                <td>{formatTime(i.createdAt)}</td>
                <td>
                  {i.status === 'pending' && (
                    <button
                      data-testid={`payment-intent-pay-${i.intentId}`}
                      onClick={() => void payIntent(i.intentId)}
                    >
                      Simulate payment
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

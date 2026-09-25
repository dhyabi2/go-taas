// Admin Invoices page (feature-14): invoice list, generate, download.

import { useEffect, useState } from 'react';
import { useApi } from '../surface';
import { useOrg } from '../org';
import { formatTime } from '../api';

interface Invoice {
  invoiceId: string;
  billId: string;
  organizationId: string;
  periodStart: string;
  periodEnd: string;
  totalCents: string;
  currency: string;
  status: string;
  issuedAt: string;
  paidAt: string;
}

export default function BillingInvoicesPage() {
  const api = useApi();
  const { orgId } = useOrg();
  const [invoices, setInvoices] = useState<Invoice[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    api
      .get<{ invoices?: Invoice[] }>('/api/v1/admin/billing/invoices?page.limit=100', orgId)
      .then((data) => setInvoices(data.invoices || []))
      .catch((e) => setError(e instanceof Error ? e.message : 'failed to load invoices'));
  }, [api, orgId]);

  const download = async (invoiceId: string) => {
    try {
      const data = await api.get<{ content?: string }>(
        `/api/v1/admin/billing/invoices/${invoiceId}:download`,
        orgId,
      );
      window.alert(data.content || 'no content');
    } catch (e) {
      setError(e instanceof Error ? e.message : 'download failed');
    }
  };

  return (
    <div className="page">
      <h1>Invoices</h1>
      {error && <div className="error">{error}</div>}
      {invoices.length === 0 ? (
        <div className="empty" data-testid="invoices-empty">No invoices yet.</div>
      ) : (
        <table className="table" data-testid="invoices-table">
          <thead>
            <tr>
              <th>Period</th>
              <th>Total</th>
              <th>Status</th>
              <th>Issued</th>
              <th>Action</th>
            </tr>
          </thead>
          <tbody>
            {invoices.map((inv) => (
              <tr key={inv.invoiceId} data-testid={`invoice-row-${inv.invoiceId}`}>
                <td>{formatTime(inv.periodStart)} – {formatTime(inv.periodEnd)}</td>
                <td>{inv.totalCents} {inv.currency}</td>
                <td>{inv.status}</td>
                <td>{formatTime(inv.issuedAt)}</td>
                <td>
                  <button data-testid={`invoice-download-${inv.invoiceId}`} onClick={() => void download(inv.invoiceId)}>
                    Download
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

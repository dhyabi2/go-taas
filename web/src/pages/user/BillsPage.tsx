// End-user bills page (feature-17): own bills (read-only) against the
// user surface's billing API.

import { useEffect, useState } from 'react';
import { useApi } from '../../surface';
import { useOrg } from '../../org';
import { formatTime, type BillSummary } from '../../api';

export default function BillsPage() {
  const api = useApi();
  const { orgId } = useOrg();
  const [bills, setBills] = useState<BillSummary[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    api
      .get<{ bills?: BillSummary[] }>('/api/v1/billing/bills?page.limit=100', orgId)
      .then((data) => setBills(data.bills || []))
      .catch((e) => setError(e instanceof Error ? e.message : 'failed to load bills'));
  }, [api, orgId]);

  return (
    <div className="page">
      <h1>Bills</h1>
      {error && <div className="error">{error}</div>}
      {bills.length === 0 ? (
        <div className="empty" data-testid="bills-empty">No bills.</div>
      ) : (
        <table className="table" data-testid="bills-table">
          <thead>
            <tr>
              <th>Period</th>
              <th>Amount</th>
              <th>Charges</th>
            </tr>
          </thead>
          <tbody>
            {bills.map((b) => (
              <tr key={b.billId}>
                <td>{formatTime(b.periodStart)} – {formatTime(b.periodEnd)}</td>
                <td>{b.amount} {b.currency}</td>
                <td>{b.chargeCount}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

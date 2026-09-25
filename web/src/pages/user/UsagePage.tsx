// End-user usage page (feature-17): own usage, cost and balance against
// the user surface's metering/billing APIs.

import { useEffect, useState } from 'react';
import { useApi } from '../../surface';
import { useOrg } from '../../org';
import { formatTime, type BalanceResponse, type UsageDashboardResponse } from '../../api';

export default function UsagePage() {
  const api = useApi();
  const { orgId } = useOrg();
  const [dashboard, setDashboard] = useState<UsageDashboardResponse | null>(null);
  const [balance, setBalance] = useState<BalanceResponse | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    api
      .get<UsageDashboardResponse>('/api/v1/metering/usage-dashboard', orgId)
      .then(setDashboard)
      .catch((e) => setError(e instanceof Error ? e.message : 'failed to load usage'));
    api
      .get<BalanceResponse>('/api/v1/billing/balance', orgId)
      .then(setBalance)
      .catch(() => {
        // Balance may be absent (no account); not fatal.
      });
  }, [api, orgId]);

  const cards = dashboard?.cards;

  return (
    <div className="page" data-testid="usage-dashboard-cards">
      <h1>Usage</h1>
      {error && <div className="error">{error}</div>}
      {balance && (
        <div className="balance-widget" data-testid="usage-balance-widget">
          {balance.mode === 'prepaid'
            ? `Balance: ${balance.balance} ${balance.currency}`
            : `Quota: ${balance.monthlyQuotaCents} (used ${balance.usedThisCycleCents})`}
        </div>
      )}
      {cards && (
        <div className="cards">
          <div className="card" data-testid="usage-metric-cost">
            Cost: {cards.totalCostCents}
          </div>
          <div className="card" data-testid="usage-metric-tokens">
            Tokens: {Number(cards.promptTokens) + Number(cards.completionTokens)}
          </div>
          <div className="card" data-testid="usage-metric-requests">
            Requests: {cards.requestCount}
          </div>
        </div>
      )}
      {dashboard?.dailyBuckets && dashboard.dailyBuckets.length > 0 ? (
        <table className="table" data-testid="usage-table">
          <thead>
            <tr>
              <th>Date</th>
              <th>Group</th>
              <th>Cost</th>
              <th>Tokens</th>
            </tr>
          </thead>
          <tbody>
            {dashboard.dailyBuckets.map((bucket) =>
              (bucket.groups || []).map((g) => (
                <tr key={`${bucket.date}-${g.groupKey}`}>
                  <td>{formatTime(bucket.date)}</td>
                  <td>{g.groupKey}</td>
                  <td>{g.costCents}</td>
                  <td>{Number(g.promptTokens) + Number(g.completionTokens)}</td>
                </tr>
              )),
            )}
          </tbody>
        </table>
      ) : (
        <div className="empty" data-testid="usage-empty">No usage yet.</div>
      )}
    </div>
  );
}

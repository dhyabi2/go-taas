// UsageKeysCards renders the per-key usage analytics summary cards
// (feature #28, AD7): Requests, Error rate, Total tokens, Total cost,
// Avg latency, p95 latency, each with a "data through <time>" freshness
// note. The wire carries integer counts, integer milliseconds and integer
// cents only; error rate and cost are derived client-side.

import type { UsageKeysCard } from '../api';
import { formatTime } from '../api';

// errorRatePercent derives the error rate as a percentage.
function errorRatePercent(card: UsageKeysCard): string {
  const req = parseInt(card.requestCount || '0', 10);
  if (req === 0) return '0.0%';
  return `${((parseInt(card.errorCount || '0', 10) / req) * 100).toFixed(1)}%`;
}

// costDollars formats integer cents as dollars.
function costDollars(cents: string): string {
  return `$${(parseInt(cents || '0', 10) / 100).toFixed(2)}`;
}

export default function UsageKeysCards({ cards }: { cards: UsageKeysCard }) {
  const dataThrough = cards.dataThrough ? formatTime(cards.dataThrough) : '—';
  return (
    <div className="card-row" data-testid="usage-keys-cards">
      <div className="card" data-testid="usage-keys-card-requests">
        <div className="card-value">{parseInt(cards.requestCount || '0', 10).toLocaleString()}</div>
        <div className="card-label">Requests</div>
        <div className="card-note">data through {dataThrough}</div>
      </div>
      <div className="card" data-testid="usage-keys-card-error-rate">
        <div className="card-value">{errorRatePercent(cards)}</div>
        <div className="card-label">Error rate</div>
        <div className="card-note">data through {dataThrough}</div>
      </div>
      <div className="card" data-testid="usage-keys-card-tokens">
        <div className="card-value">{parseInt(cards.totalTokens || '0', 10).toLocaleString()}</div>
        <div className="card-label">Total tokens</div>
        <div className="card-note">data through {dataThrough}</div>
      </div>
      <div className="card" data-testid="usage-keys-card-cost">
        <div className="card-value">{costDollars(cards.totalCostCents)}</div>
        <div className="card-label">Total cost</div>
        <div className="card-note">data through {dataThrough}</div>
      </div>
      <div className="card" data-testid="usage-keys-card-avg-latency">
        <div className="card-value">{parseInt(cards.avgLatencyMs || '0', 10).toLocaleString()} ms</div>
        <div className="card-label">Avg latency</div>
        <div className="card-note">data through {dataThrough}</div>
      </div>
      <div className="card" data-testid="usage-keys-card-p95">
        <div className="card-value">{parseInt(cards.p95LatencyMs || '0', 10).toLocaleString()} ms</div>
        <div className="card-label">p95 latency</div>
        <div className="card-note">data through {dataThrough}</div>
      </div>
    </div>
  );
}
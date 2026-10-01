// ObservabilityCards renders the model observability summary card row
// (feature #24, AD8): Requests, Error rate, Avg latency, p95 latency,
// Output tokens/sec, Input tokens/sec, each with a "data through <time>"
// freshness note. error_rate is derived client-side as error ÷ requests.

import type { ObservabilityCard } from '../api';
import { formatTime } from '../api';

// errorRatePercent derives the error rate as a percentage.
function errorRatePercent(cards: ObservabilityCard): string {
  const req = parseInt(cards.requestCount || '0', 10);
  if (req === 0) return '0.0%';
  return `${((parseInt(cards.errorCount || '0', 10) / req) * 100).toFixed(1)}%`;
}

export default function ObservabilityCards({
  cards,
  dataThroughLabel,
}: {
  cards: ObservabilityCard | null;
  dataThroughLabel: string;
}) {
  if (!cards) {
    return (
      <div className="dashboard-cards" data-testid="observability-cards">
        {['Requests', 'Error rate', 'Avg latency', 'p95 latency', 'Output tokens/sec', 'Input tokens/sec'].map(
          (label) => (
            <div className="dashboard-card" key={label}>
              <div className="label">{label}</div>
              <div className="value">—</div>
            </div>
          ),
        )}
      </div>
    );
  }

  const dataThrough = parseInt(cards.dataThrough || '0', 10);
  const freshness = dataThrough > 0 ? `${dataThroughLabel} ${formatTime(String(dataThrough))}` : '';

  const items = [
    { label: 'Requests', value: parseInt(cards.requestCount || '0', 10).toLocaleString() },
    { label: 'Error rate', value: errorRatePercent(cards) },
    { label: 'Avg latency', value: `${cards.avgLatencyMs || '0'} ms` },
    { label: 'p95 latency', value: `${cards.p95LatencyMs || '0'} ms` },
    { label: 'Output tokens/sec', value: (cards.outputTokensPerSec || '0').toLocaleString() },
    { label: 'Input tokens/sec', value: (cards.inputTokensPerSec || '0').toLocaleString() },
  ];

  return (
    <div className="dashboard-cards" data-testid="observability-cards">
      {items.map((item) => (
        <div className="dashboard-card" key={item.label}>
          <div className="label">{item.label}</div>
          <div className="value">{item.value}</div>
          {freshness && <div className="sub">{freshness}</div>}
        </div>
      ))}
    </div>
  );
}
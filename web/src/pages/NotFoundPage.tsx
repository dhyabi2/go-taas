import { navigate } from '../router';

export default function NotFoundPage({
  homePath,
  homeLabel,
}: {
  homePath?: string;
  homeLabel?: string;
}) {
  return (
    <div className="panel" style={{ marginTop: 40, textAlign: 'center' }}>
      <h2>Page not found</h2>
      <p className="muted">The page you requested does not exist.</p>
      {homePath && (
        <button className="link" data-testid="not-found-home" onClick={() => navigate(homePath)}>
          Go to {homeLabel || 'home'}
        </button>
      )}
    </div>
  );
}

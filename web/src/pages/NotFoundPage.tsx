import { navigate } from '../router';
import { useI18n } from '../i18n';

export default function NotFoundPage({
  homePath,
  homeLabel,
}: {
  homePath?: string;
  homeLabel?: string;
}) {
  const { t } = useI18n();
  return (
    <div className="panel" style={{ marginTop: 40, textAlign: 'center' }}>
      <h2>{t('notFound.title')}</h2>
      <p className="muted">{t('notFound.body')}</p>
      {homePath && (
        <button className="link" data-testid="not-found-home" onClick={() => navigate(homePath)}>
          {t('notFound.home', { home: homeLabel || 'home' })}
        </button>
      )}
    </div>
  );
}

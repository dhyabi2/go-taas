// SecretRevealDialog shows a plaintext secret once with a Copy action
// (feature #23, §6.3). Reused by the webhook create flow and the
// roll-secret flow.

import { CopyButton } from '../components';
import { useI18n } from '../i18n';

export default function SecretRevealDialog({
  secret,
  onDone,
}: {
  secret: string;
  onDone: () => void;
}) {
  const { t } = useI18n();
  return (
    <div className="dialog-backdrop">
      <div className="dialog" data-testid="webhook-secret-reveal">
        <h2>{t('webhooks.secretTitle')}</h2>
        <p>{t('webhooks.secretBody')}</p>
        <code className="secret-value" data-testid="webhook-secret-value">
          {secret}
        </code>
        <div className="dialog-actions">
          <CopyButton text={secret} label={t('common.copy')} />
          <button data-testid="webhook-secret-done" onClick={onDone}>
            {t('common.done')}
          </button>
        </div>
      </div>
    </div>
  );
}
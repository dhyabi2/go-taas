// End-user My Activity page (feature-15): the caller's own audit events.

import { useEffect, useState } from 'react';
import { useApi } from '../../surface';
import { useOrg } from '../../org';
import { useI18n } from '../../i18n';
import { formatTime } from '../../api';

interface AuditEvent {
  auditEventId: string;
  organizationId: string;
  actorUserId: string;
  actorType: string;
  action: string;
  resourceType: string;
  resourceId: string;
  result: string;
  ipAddress: string;
  userAgent: string;
  metadata: string;
  createdAt: string;
}

export default function ActivityPage() {
  const api = useApi();
  const { orgId } = useOrg();
  const { t } = useI18n();
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    api
      .get<{ auditEvents?: AuditEvent[] }>('/api/v1/audit/activity?page.limit=100', orgId)
      .then((data) => setEvents(data.auditEvents || []))
      .catch((e) => setError(e instanceof Error ? e.message : t('uactivity.loadFailed')));
  }, [api, orgId, t]);

  return (
    <div className="page">
      <h1>{t('uactivity.title')}</h1>
      {error && <div className="error">{error}</div>}
      {events.length === 0 ? (
        <div className="empty" data-testid="activity-empty">{t('uactivity.empty')}</div>
      ) : (
        <table className="table" data-testid="activity-table">
          <thead>
            <tr>
              <th>{t('uactivity.colTime')}</th>
              <th>{t('uactivity.colAction')}</th>
              <th>{t('uactivity.colResource')}</th>
              <th>{t('uactivity.colResult')}</th>
            </tr>
          </thead>
          <tbody>
            {events.map((e) => (
              <tr key={e.auditEventId}>
                <td>{formatTime(e.createdAt)}</td>
                <td>{e.action}</td>
                <td>{e.resourceType}:{e.resourceId}</td>
                <td>{e.result}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

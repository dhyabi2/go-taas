// Invitations page: the active organization's invitation pipeline with
// invite, resend and revoke. Implements docs/design/org-members-rbac.md
// FR2, AC4-AC7.

import { useCallback, useEffect, useState } from 'react';
import { api, ApiError, type Invitation } from '../api';
import { useOrg } from '../org';
import { Dialog, ErrorBanner, StateBadge } from '../components';

const ASSIGNABLE_ROLES = ['admin', 'member', 'viewer'];

export default function InvitationsPage() {
  const { orgId } = useOrg();
  const [invitations, setInvitations] = useState<Invitation[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [inviteOpen, setInviteOpen] = useState(false);
  const [inviteEmail, setInviteEmail] = useState('');
  const [inviteRole, setInviteRole] = useState('member');
  const [newToken, setNewToken] = useState('');
  const [revokeTarget, setRevokeTarget] = useState<Invitation | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await api.get<{ response: { code: number; message: string }; invitations: Invitation[] }>(
        `/api/v1/admin/tenancy/organizations/${orgId}/invitations?page.limit=100`,
        orgId,
      );
      setInvitations(data.invitations || []);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'failed to load invitations');
    } finally {
      setLoading(false);
    }
  }, [orgId]);

  useEffect(() => {
    void load();
  }, [load]);

  const createInvite = async () => {
    setError('');
    setNewToken('');
    try {
      const data = await api.post<{ response: { code: number; message: string }; token: string }>(
        `/api/v1/admin/tenancy/organizations/${orgId}/invitations`,
        orgId,
        { email: inviteEmail, role: inviteRole },
      );
      setNewToken(data.token);
      setInviteOpen(false);
      setInviteEmail('');
      setInviteRole('member');
      await load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'failed to create invitation');
    }
  };

  const resend = async (invitationId: string) => {
    setError('');
    setNewToken('');
    try {
      const data = await api.post<{ response: { code: number; message: string }; token: string }>(
        `/api/v1/admin/tenancy/invitations/${invitationId}:resend`,
        orgId,
        {},
      );
      setNewToken(data.token);
      await load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'failed to resend invitation');
    }
  };

  const revoke = async () => {
    if (!revokeTarget) return;
    setError('');
    try {
      await api.post(
        `/api/v1/admin/tenancy/invitations/${revokeTarget.invitationId}:revoke`,
        orgId,
        {},
      );
      setRevokeTarget(null);
      await load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'failed to revoke invitation');
    }
  };

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>Invitations</h1>
          <div className="subtitle">
            Invitation pipeline for <strong className="mono">{orgId}</strong>.
          </div>
        </div>
        <button data-testid="invite-button" onClick={() => setInviteOpen(true)}>
          Invite member
        </button>
      </div>

      {error && <ErrorBanner message={error} />}
      {newToken && (
        <div className="notice" data-testid="invite-token">
          Invite link token: <strong className="mono">{newToken}</strong>
        </div>
      )}

      <div className="panel">
        {loading ? (
          <div className="loading">Loading…</div>
        ) : invitations.length === 0 ? (
          <div className="empty-state" data-testid="invitations-empty">
            No invitations yet. Invite the first member.
          </div>
        ) : (
          <table className="data" data-testid="invitations-table">
            <thead>
              <tr>
                <th>Email</th>
                <th>Role</th>
                <th>Status</th>
                <th>Expires</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {invitations.map((inv) => (
                <tr key={inv.invitationId} data-testid={`invitation-row-${inv.invitationId}`}>
                  <td>{inv.email}</td>
                  <td>
                    <StateBadge state={inv.role} />
                  </td>
                  <td>
                    <StateBadge state={inv.status} />
                  </td>
                  <td>{new Date(parseInt(inv.expiresAt, 10) * 1000).toLocaleString()}</td>
                  <td>
                    {inv.status === 'pending' && (
                      <>
                        <button
                          className="link"
                          data-testid={`invite-resend-${inv.invitationId}`}
                          onClick={() => void resend(inv.invitationId)}
                        >
                          Resend
                        </button>
                        <button
                          className="link danger"
                          data-testid={`invite-revoke-${inv.invitationId}`}
                          onClick={() => setRevokeTarget(inv)}
                        >
                          Revoke
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {inviteOpen && (
        <Dialog title="Invite member" onClose={() => setInviteOpen(false)}>
          <div className="form">
            <label>
              Email
              <input
                data-testid="invite-email-input"
                value={inviteEmail}
                onChange={(e) => setInviteEmail(e.target.value)}
                placeholder="person@example.com"
              />
            </label>
            <label>
              Role
              <select
                data-testid="invite-role-select"
                value={inviteRole}
                onChange={(e) => setInviteRole(e.target.value)}
              >
                {ASSIGNABLE_ROLES.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </select>
            </label>
            <button data-testid="invite-save" onClick={() => void createInvite()}>
              Invite
            </button>
          </div>
        </Dialog>
      )}

      {revokeTarget && (
        <Dialog title="Revoke invitation" onClose={() => setRevokeTarget(null)}>
          <p>
            Revoke the invitation for <strong>{revokeTarget.email}</strong>?
          </p>
          <button
            className="danger"
            data-testid="invite-revoke-confirm"
            onClick={() => void revoke()}
          >
            Revoke
          </button>
        </Dialog>
      )}
    </div>
  );
}
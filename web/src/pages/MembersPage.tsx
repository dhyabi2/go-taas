// Members page: the active organization's roster with add, role change
// and remove. Implements docs/design/org-members-rbac.md FR1, AC1-AC3.

import { useCallback, useEffect, useState } from 'react';
import { api, ApiError, type OrgMember } from '../api';
import { useOrg } from '../org';
import { Dialog, ErrorBanner, StateBadge } from '../components';

const ASSIGNABLE_ROLES = ['admin', 'member', 'viewer'];

export default function MembersPage() {
  const { orgId } = useOrg();
  const [members, setMembers] = useState<OrgMember[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [addOpen, setAddOpen] = useState(false);
  const [addUserId, setAddUserId] = useState('');
  const [addRole, setAddRole] = useState('member');
  const [removeTarget, setRemoveTarget] = useState<OrgMember | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await api.get<{ response: { code: number; message: string }; members: OrgMember[] }>(
        `/api/v1/admin/tenancy/organizations/${orgId}/members?page.limit=100`,
        orgId,
      );
      setMembers(data.members || []);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'failed to load members');
    } finally {
      setLoading(false);
    }
  }, [orgId]);

  useEffect(() => {
    void load();
  }, [load]);

  const addMember = async () => {
    setError('');
    try {
      await api.post(
        `/api/v1/admin/tenancy/organizations/${orgId}/members`,
        orgId,
        { userId: addUserId, role: addRole },
      );
      setAddOpen(false);
      setAddUserId('');
      setAddRole('member');
      await load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'failed to add member');
    }
  };

  const changeRole = async (userId: string, role: string) => {
    setError('');
    try {
      await api.patch(
        `/api/v1/admin/tenancy/organizations/${orgId}/members/${userId}`,
        orgId,
        { role },
      );
      await load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'failed to change role');
    }
  };

  const removeMember = async () => {
    if (!removeTarget) return;
    setError('');
    try {
      await api.del(
        `/api/v1/admin/tenancy/organizations/${orgId}/members/${removeTarget.userId}`,
        orgId,
      );
      setRemoveTarget(null);
      await load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'failed to remove member');
    }
  };

  return (
    <div>
      <div className="page-header">
        <div>
          <h1>Members</h1>
          <div className="subtitle">
            Organization roster for <strong className="mono">{orgId}</strong>.
            Roles: owner (protected), admin, member, viewer.
          </div>
        </div>
        <button data-testid="add-member-button" onClick={() => setAddOpen(true)}>
          Add member
        </button>
      </div>

      {error && <ErrorBanner message={error} />}

      <div className="panel">
        {loading ? (
          <div className="loading">Loading…</div>
        ) : members.length === 0 ? (
          <div className="empty-state" data-testid="members-empty">
            No members yet. Add the first member.
          </div>
        ) : (
          <table className="data" data-testid="org-members-table">
            <thead>
              <tr>
                <th>User</th>
                <th>Role</th>
                <th>Joined</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {members.map((m) => (
                <tr key={m.userId} data-testid={`org-member-row-${m.userId}`}>
                  <td>
                    <strong className="mono">{m.userId}</strong>
                  </td>
                  <td>
                    <StateBadge state={m.role} />
                    {m.role === 'owner' && (
                      <select
                        className="member-role-select"
                        data-testid={`member-role-select-${m.userId}`}
                        value={m.role}
                        disabled
                      >
                        <option value="owner">owner</option>
                      </select>
                    )}
                    {m.role !== 'owner' && (
                      <select
                        className="member-role-select"
                        data-testid={`member-role-select-${m.userId}`}
                        value={m.role}
                        onChange={(e) => changeRole(m.userId, e.target.value)}
                      >
                        {ASSIGNABLE_ROLES.map((r) => (
                          <option key={r} value={r}>
                            {r}
                          </option>
                        ))}
                      </select>
                    )}
                  </td>
                  <td>{new Date(parseInt(m.joinedAt, 10) * 1000).toLocaleString()}</td>
                  <td>
                    {m.role !== 'owner' && (
                      <button
                        className="link danger"
                        data-testid={`member-remove-${m.userId}`}
                        onClick={() => setRemoveTarget(m)}
                      >
                        Remove
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {addOpen && (
        <Dialog title="Add member" onClose={() => setAddOpen(false)}>
          <div className="form">
            <label>
              User ID
              <input
                data-testid="add-member-user-id"
                value={addUserId}
                onChange={(e) => setAddUserId(e.target.value)}
                placeholder="user id"
              />
            </label>
            <label>
              Role
              <select
                data-testid="add-member-role"
                value={addRole}
                onChange={(e) => setAddRole(e.target.value)}
              >
                {ASSIGNABLE_ROLES.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </select>
            </label>
            <button data-testid="add-member-save" onClick={() => void addMember()}>
              Add
            </button>
          </div>
        </Dialog>
      )}

      {removeTarget && (
        <Dialog title="Remove member" onClose={() => setRemoveTarget(null)}>
          <p>
            Remove <strong className="mono">{removeTarget.userId}</strong> from{' '}
            <strong className="mono">{orgId}</strong>?
          </p>
          <button
            className="danger"
            data-testid="member-remove-confirm"
            onClick={() => void removeMember()}
          >
            Remove
          </button>
        </Dialog>
      )}
    </div>
  );
}
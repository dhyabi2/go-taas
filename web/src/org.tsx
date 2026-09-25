// Realm-scoped organization context (feature-17). Each surface keeps its
// own organization key and resolves the org from its own session (or the
// transitional header). The user-realm selector is disabled in
// transitional mode (no operator API call).

import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { getOrgId, getSessionToken, setOrgId, type OrganizationSummary, type SessionInfo } from './api';
import { useApi, useRealm } from './surface';

const DEFAULT_ORG = 'org-default';

const OrgContext = createContext<{
  orgId: string;
  setOrgId: (id: string) => void;
}>({ orgId: DEFAULT_ORG, setOrgId: () => {} });

export function OrgProvider({ children }: { children: ReactNode }) {
  const realm = useRealm();
  const [orgId, setOrgIdState] = useState(() => getOrgId(realm) || DEFAULT_ORG);

  useEffect(() => {
    document.documentElement.dataset.org = orgId;
  }, [orgId]);

  const setOrg = (id: string) => {
    setOrgId(realm, id);
    setOrgIdState(id);
  };

  return (
    <OrgContext.Provider value={{ orgId, setOrgId: setOrg }}>{children}</OrgContext.Provider>
  );
}

export function useOrg() {
  return useContext(OrgContext);
}

// OrgSwitcher is the organization selector rendered in the sidebar. When
// a session is present it lists the session's accessible orgs and
// switching calls UpdateSessionOrg; when no session exists it falls back
// to the tenancy API list (transitional mode).
export function OrgSwitcher() {
  const realm = useRealm();
  const api = useApi();
  const { orgId, setOrgId } = useOrg();
  const [orgs, setOrgs] = useState<OrganizationSummary[]>([]);
  const [notice, setNotice] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [session, setSession] = useState<SessionInfo | null>(null);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        if (getSessionToken(realm)) {
          const sess = await api.get<SessionInfo>(`/api/v1${realm === 'admin' ? '/admin' : ''}/auth/session`, '');
          if (cancelled) return;
          setSession(sess);
          const list = (sess.accessibleOrgs || []).map((id) => ({
            organizationId: id,
            displayName: id,
            state: 'active',
          } as OrganizationSummary));
          setOrgs(list);
          if (sess.activeOrg) {
            setOrgId(sess.activeOrg);
          } else if (list.length > 0) {
            setOrgId(list[0].organizationId);
          }
          return;
        }
        // No session: transitional tenancy API list (admin surface only).
        if (realm === 'admin') {
          const data = await api.get<{ organizations: OrganizationSummary[] }>(
            '/api/v1/admin/tenancy/organizations?page.limit=100',
            orgId,
          );
          if (cancelled) return;
          const list = data.organizations || [];
          setOrgs(list);
          const stored = getOrgId(realm);
          if (list.length > 0) {
            const match = list.find((o) => o.organizationId === stored);
            if (!match) {
              setOrgId(list[0].organizationId);
              setNotice(
                `Previous organization "${stored || DEFAULT_ORG}" no longer exists; switched to the first available one.`,
              );
            }
          }
        }
      } catch {
        // The tenancy/session API is unavailable: keep the fallback below.
      } finally {
        if (!cancelled) setLoaded(true);
      }
    };
    void load();
    return () => {
      cancelled = true;
    };
    // orgId is intentionally not a dependency: load once on mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const switchOrg = async (id: string) => {
    setOrgId(id);
    if (session) {
      try {
        await api.post(`/api/v1${realm === 'admin' ? '/admin' : ''}/auth/session/org`, '', { organizationId: id });
      } catch {
        // The switch failed server-side; the local org id is still set.
      }
    }
  };

  if (!loaded && orgs.length === 0) {
    return (
      <div className="org-switcher">
        <label htmlFor="org-switcher-select">Organization</label>
        <select id="org-switcher-select" data-testid="org-switcher-select" value={orgId} disabled>
          <option value={orgId}>{orgId}</option>
        </select>
      </div>
    );
  }

  return (
    <div className="org-switcher">
      <label htmlFor="org-switcher-select">Organization</label>
      <select
        id="org-switcher-select"
        data-testid="org-switcher-select"
        value={orgId}
        onChange={(e) => void switchOrg(e.target.value)}
      >
        {orgs.length === 0 ? (
          <option value={orgId}>{orgId}</option>
        ) : (
          orgs.map((o) => (
            <option key={o.organizationId} value={o.organizationId}>
              {o.displayName || o.organizationId}
            </option>
          ))
        )}
      </select>
      {notice && (
        <div className="org-switcher-notice" data-testid="org-switcher-notice">
          {notice}
        </div>
      )}
    </div>
  );
}

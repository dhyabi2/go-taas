// End-user custom login page (feature-22): a TaaS-branded
// username/password form that authenticates against a selected OIDC or
// LDAP provider via SSOPasswordLogin. The realm is derived from the
// authenticated role; the console stores the token under the
// realm-matching key and routes by the returned realm.

import { useState } from 'react';
import { useApi } from '../../surface';
import { setSessionToken, type Realm } from '../../api';
import { navigate } from '../../router';
import { realmCustomLoginPath, realmHome } from '../../surface-routes';
import brandLogo from '../../assets/brand/logo.svg';

interface PasswordLoginResponse {
  sessionToken?: string;
  realm?: string;
  expiresAt?: string;
}

// errorCopy maps business codes to sentences (feature-22 FR6.3).
function errorCopy(code: number): string {
  switch (code) {
    case 10022:
      return 'This identity provider is disabled. Ask your platform operator.';
    case 10024:
      return 'The username or password is incorrect.';
    case 10025:
      return 'This identity is not linked to a go-taas account. Ask your organization administrator for an invitation.';
    case 10027:
      return 'Sign-in did not complete. Try again.';
    default:
      return 'Sign-in failed. Try again.';
  }
}

export default function UserCustomLoginPage({ providerId }: { providerId: string }) {
  const api = useApi();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const canSubmit = username.trim().length > 0 && password.length > 0 && !submitting;

  const submit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    setError('');
    try {
      const data = await api.post<PasswordLoginResponse>(
        `/api/v1/auth/sso/${providerId}/login`,
        '',
        { username: username.trim(), password },
      );
      const token = data.sessionToken;
      const realm = (data.realm as Realm) || 'user';
      if (!token) {
        setError('Sign-in did not complete. Try again.');
        return;
      }
      // Store the token under the realm-matching key (feature-22 FR2.3).
      setSessionToken(realm, token);
      // Route by the returned realm (feature-22 FR2.2).
      navigate(realmHome(realm));
    } catch (e) {
      const code = e && (e as { code?: number }).code;
      setError(errorCopy(typeof code === 'number' ? code : 0));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="login" data-testid="custom-login-form">
      <div className="login-brand" aria-hidden="true">
        <img src={brandLogo} alt="" className="login-brand-logo" />
      </div>
      <a
        href={realmCustomLoginPath('user', providerId)}
        className="back-link"
        data-testid="custom-login-back"
        onClick={(e) => {
          e.preventDefault();
          navigate('/login');
        }}
      >
        ← All sign-in options
      </a>
      <h1>Sign in to {providerId}</h1>
      <p className="login-subtitle">
        Your credentials are verified by {providerId} and are not stored by go-taas.
      </p>
      {error && (
        <div className="error" data-testid="custom-login-error" role="alert">
          {error}
        </div>
      )}
      <form
        className="custom-login-form"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <label className="field">
          <span>Username</span>
          <input
            type="text"
            autoComplete="username"
            data-testid="custom-login-username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            disabled={submitting}
          />
        </label>
        <label className="field">
          <span>Password</span>
          <input
            type="password"
            autoComplete="current-password"
            data-testid="custom-login-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={submitting}
          />
        </label>
        <button
          type="submit"
          className="primary"
          data-testid="custom-login-submit"
          disabled={!canSubmit}
        >
          {submitting ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  );
}
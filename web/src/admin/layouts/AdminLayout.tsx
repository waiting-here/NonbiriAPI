import { useEffect, useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { NavLink, Outlet } from 'react-router';
import { useTranslation } from 'react-i18next';
import { LanguageSwitcher } from '@shared/components/LanguageSwitcher';
import { usePublicConfig } from '@shared/query/publicConfig';
import { ErrorState, LoadingState } from '@shared/components/States';
import { apiFetch, isApiError, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { ThemeToggle } from '@shared/theme/ThemeToggle';
import { useAdminSession } from '../data';
import { clearManagementSession } from '@shared/charityManagement';

interface NavItem {
  to: string;
  key: string;
  end?: boolean;
}

const NAV_ITEMS: NavItem[] = [
  { to: '/', end: true, key: 'dashboard' },
  { to: '/users', key: 'users' },
  { to: '/logs', key: 'logs' },
  { to: '/endpoints', key: 'endpoints' },
  { to: '/alerts', key: 'alerts' },
  { to: '/settings', key: 'settings' },
  { to: '/charity', key: 'charity' },
];

function AdminLogin({ onSignedIn, notice }: { onSignedIn: () => Promise<void>; notice?: unknown }) {
  const { t } = useTranslation();
  const config = usePublicConfig(true, '/admin/api/config');
  const siteName = config.data?.siteName || t('app.name');
  const siteLogoURL = config.data?.siteLogoURL;
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [validationError, setValidationError] = useState('');
  const [error, setError] = useState<unknown>(null);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(null);
    setValidationError('');
    const submittedPassword = password;
    setPassword('');
    if (!username.trim() || !submittedPassword) {
      setValidationError(t('common.formInvalid'));
      return;
    }
    setBusy(true);
    try {
      await apiFetch<unknown>('/admin/api/login', {
        method: 'POST',
        json: { username: username.trim(), password: submittedPassword },
      });
      await onSignedIn();
    } catch (requestError) {
      setError(requestError);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="auth-shell">
      <header className="site-header">
        <span className="brand">
          {siteLogoURL ? (
            <img className="brand-logo" src={siteLogoURL} alt="" aria-hidden="true" />
          ) : null}
          {siteName}
          <span className="brand-suffix">{t('admin.shell.brandSuffix')}</span>
        </span>
        <div className="site-actions">
          <LanguageSwitcher />
          <ThemeToggle />
        </div>
      </header>
      <main className="auth-main">
        <section className="card login-card" aria-labelledby="admin-login-title">
          <p className="eyebrow">{siteName}</p>
          <h1 id="admin-login-title">{t('admin.shell.loginTitle')}</h1>
          <p className="page-description">{t('admin.shell.loginBody')}</p>
          <form className="login-form" onSubmit={submit} noValidate>
            <label>
              <span>{t('admin.shell.username')}</span>
              <input
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                autoComplete="username"
                maxLength={128}
                required
              />
            </label>
            <label>
              <span>{t('admin.shell.password')}</span>
              <input
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                autoComplete="current-password"
                maxLength={512}
                required
              />
            </label>
            {validationError ? <p className="field-error" role="alert">{validationError}</p> : null}
            {notice ? (
              isApiError(notice) ? <p className="field-error" role="alert">{notice.message}</p> : <ErrorState error={notice} />
            ) : null}
            {error ? <ErrorState error={error} /> : null}
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {busy ? t('common.working') : t('admin.shell.login')}
            </button>
          </form>
        </section>
      </main>
    </div>
  );
}

export function AdminLayout() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [logoutRequested, setLogoutRequested] = useState(false);
  const session = useAdminSession(!logoutRequested);
  const config = usePublicConfig(true, '/admin/api/config');
  const siteName = config.data?.siteName || t('app.name');
  const siteLogoURL = config.data?.siteLogoURL;
  const [menuOpen, setMenuOpen] = useState(false);
  const [logoutError, setLogoutError] = useState<unknown>(null);
  const [logoutBusy, setLogoutBusy] = useState(false);

  useEffect(() => {
    document.title = `${siteName} · ${t('admin.shell.title')}`;
  }, [t, siteName]);

  // Sync the browser tab icon with the configured site logo. The default
  // mark is baked into index.html; capture it on mount so clearing the logo
  // restores it instead of leaving a stale icon.
  const [defaultIcon] = useState(
    () => document.querySelector<HTMLLinkElement>('link[rel="icon"]')?.href ?? '',
  );
  useEffect(() => {
    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    if (!link) return;
    link.href = siteLogoURL || defaultIcon;
  }, [siteLogoURL, defaultIcon]);

  if (logoutRequested) {
    if (logoutBusy) return <LoadingState />;
    return (
      <AdminLogin
        notice={logoutError}
        onSignedIn={async () => {
          const result = await session.refetch();
          if (result.error) throw result.error;
          setLogoutRequested(false);
        }}
      />
    );
  }
  if (session.isPending) return <LoadingState />;
  if (session.error && !isUnauthorized(session.error) && !isNotFoundError(session.error)) {
    return <ErrorState error={session.error} onRetry={() => void session.refetch()} />;
  }
  if (session.error || !session.data) {
    return (
      <AdminLogin
        onSignedIn={async () => {
          const result = await session.refetch();
          if (result.error) throw result.error;
        }}
      />
    );
  }

  const logout = async () => {
    setLogoutError(null);
    // Close the station at user intent, before the network request. A failed
    // logout must not retain the old admin's projections or shell.
    clearManagementSession(queryClient, 'admin');
    setLogoutRequested(true);
    setMenuOpen(false);
    setLogoutBusy(true);
    try {
      await apiFetch<void>('/admin/api/logout', { method: 'POST' });
      clearManagementSession(queryClient, 'admin');
      await session.refetch();
      setMenuOpen(false);
    } catch (error) {
      clearManagementSession(queryClient, 'admin');
      setLogoutError(error);
    } finally {
      setLogoutBusy(false);
    }
  };

  return (
    <>
      <a className="skip-link" href="#main">
        {t('shell.skipToContent')}
      </a>
      <header className="site-header">
        <span className="brand">
          {siteLogoURL ? (
            <img className="brand-logo" src={siteLogoURL} alt="" aria-hidden="true" />
          ) : null}
          {siteName}
          <span className="brand-suffix">{t('admin.shell.brandSuffix')}</span>
        </span>
        <button
          type="button"
          className="btn btn-secondary mobile-menu-btn admin-menu-btn"
          aria-expanded={menuOpen}
          aria-controls="admin-navigation"
          aria-label={t(menuOpen ? 'shell.closeMenu' : 'shell.openMenu')}
          onClick={() => setMenuOpen((open) => !open)}
        >
          {menuOpen ? '×' : '☰'}
        </button>
        <div className="site-actions">
          <span className="user-chip">{t('admin.shell.sessionUser', { name: session.data.admin.username })}</span>
          <LanguageSwitcher />
          <ThemeToggle />
          <button type="button" className="btn btn-secondary" onClick={() => void logout()} disabled={logoutBusy}>
            {logoutBusy ? t('common.working') : t('common.signOut')}
          </button>
        </div>
      </header>
      {menuOpen ? (
        <button
          type="button"
          className="shell-overlay"
          aria-label={t('shell.closeMenu')}
          onClick={() => setMenuOpen(false)}
        />
      ) : null}
      <div className="admin-shell">
        <aside
          id="admin-navigation"
          className={`admin-sidebar ${menuOpen ? 'is-open' : ''}`}
          aria-label={t('admin.shell.navLabel')}
        >
          <nav>
            <ul>
              {NAV_ITEMS.map((item) => (
                <li key={item.key}>
                  <NavLink to={item.to} end={item.end} onClick={() => setMenuOpen(false)}>
                    {t(`admin.${item.key}.nav`)}
                  </NavLink>
                </li>
              ))}
            </ul>
          </nav>
        </aside>
        <main id="main" className="admin-content" tabIndex={-1}>
          {logoutError ? <ErrorState error={logoutError} /> : null}
          {/* Keep route-local mutation and form state scoped to one admin. */}
          <Outlet key={session.data.admin.username} />
        </main>
      </div>
    </>
  );
}

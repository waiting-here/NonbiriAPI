import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { NavLink, Outlet } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Brand, useBrandFavicon } from '@shared/components/Brand';
import { AccountMenu } from '@shared/components/AccountMenu';
import { LanguageSwitcher } from '@shared/components/LanguageSwitcher';
import { PublicShell } from '@shared/components/PublicShell';
import { usePublicConfig } from '@shared/query/publicConfig';
import { ErrorState, LoadingState, PageFooter } from '@shared/components/States';
import { Icon } from '@shared/components/Icon';
import { useOptionalToast } from '@shared/components/Toast';
import { apiFetch, isApiError, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { ThemeToggle } from '@shared/theme/ThemeToggle';
import { SHELL_DRAWER_MEDIA_QUERY } from '@shared/styles/tokens';
import { useAdminSession } from '../data';
import { clearManagementSession } from '@shared/charityManagement';
import { ADMIN_NAV_GROUPS, ADMIN_PRIMARY_NAV } from '../navigation';

const ADMIN_GROUP_LABEL_KEYS: Record<(typeof ADMIN_NAV_GROUPS)[number], string> = {
  overview: 'admin.navigation.overview',
  users: 'admin.navigation.users',
  operations: 'admin.navigation.operations',
  content: 'admin.navigation.content',
};

function AdminLogin({ onSignedIn, notice }: { onSignedIn: () => Promise<void>; notice?: unknown }) {
  const { t } = useTranslation();
  // Anonymous admin bootstrap must remain local: /admin/api/config is an ADM
  // route and a 401 response must never prevent this login surface from
  // rendering. Authenticated AdminLayout owns the protected config query.
  const siteName = t('app.name');
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
      <header className="site-header nb-admin-header">
        <Brand siteName={siteName} className="brand nb-admin-header__brand" suffix={<span className="brand-suffix nb-admin-header__suffix">{t('admin.shell.brandSuffix')}</span>} />
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
  const toastContext = useOptionalToast();
  const clearToasts = toastContext?.clear;
  const [logoutRequested, setLogoutRequested] = useState(false);
  const session = useAdminSession(!logoutRequested);
  const config = usePublicConfig(Boolean(session.data) && !logoutRequested, '/admin/api/config');
  const siteName = config.data?.siteName || t('app.name');
  const siteLogoURL = config.data?.siteLogoURL;
  const [menuOpen, setMenuOpen] = useState(false);
  const menuButtonRef = useRef<HTMLButtonElement>(null);
  const sidebarRef = useRef<HTMLElement>(null);
  const [logoutError, setLogoutError] = useState<unknown>(null);
  const [logoutBusy, setLogoutBusy] = useState(false);
  const toastIdentity = logoutRequested
    ? 'logout'
    : session.data?.admin.username
      ? `admin:${session.data.admin.username}`
      : 'anonymous';
  const previousToastIdentityRef = useRef<string | undefined>(undefined);

  const restoreMenuFocus = useCallback(() => {
    // The menu button is hidden on desktop; only restore focus when the
    // responsive drawer is available.
    if (typeof window !== 'undefined'
      && typeof window.matchMedia === 'function'
      && window.matchMedia(SHELL_DRAWER_MEDIA_QUERY.admin).matches) {
      menuButtonRef.current?.focus();
    }
  }, []);
  const closeMenu = useCallback(() => {
    setMenuOpen(false);
    restoreMenuFocus();
  }, [restoreMenuFocus]);

  useEffect(() => {
    document.title = `${siteName} · ${t('admin.shell.title')}`;
  }, [t, siteName]);

  useEffect(() => {
    if (previousToastIdentityRef.current && previousToastIdentityRef.current !== toastIdentity) clearToasts?.();
    previousToastIdentityRef.current = toastIdentity;
  }, [clearToasts, toastIdentity]);

  useBrandFavicon(siteLogoURL);

  useEffect(() => {
    if (!menuOpen) return;
    const firstLink = sidebarRef.current?.querySelector<HTMLElement>('a[href], button:not([disabled])');
    firstLink?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        closeMenu();
        return;
      }
      if (event.key !== 'Tab') return;
      const focusable = Array.from(sidebarRef.current?.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), select:not([disabled])',
      ) ?? []);
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [closeMenu, menuOpen]);

  useEffect(() => {
    if (!menuOpen || typeof window === 'undefined' || typeof window.matchMedia !== 'function') return;
    const media = window.matchMedia(SHELL_DRAWER_MEDIA_QUERY.admin);
    const closeForDesktop = (event?: MediaQueryListEvent) => {
      if (event && event.matches) return;
      if (!media.matches) setMenuOpen(false);
    };
    closeForDesktop();
    media.addEventListener('change', closeForDesktop);
    return () => media.removeEventListener('change', closeForDesktop);
  }, [menuOpen]);

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

  // Only an authenticated admin may reach the protected bootstrap. Do not
  // mount the normal sidebar/content until its closed DTO has resolved.
  if (config.isPending && !config.data) {
    return <PublicShell station="admin" siteName={siteName}><div className="nb-page nb-page--readable"><LoadingState /></div></PublicShell>;
  }
  if (config.error && !config.data) {
    return <PublicShell station="admin" siteName={siteName}><div className="nb-page nb-page--readable"><ErrorState error={config.error} onRetry={() => void config.refetch()} /></div></PublicShell>;
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
      <header className="site-header nb-admin-header">
        <Brand siteName={siteName} siteLogoURL={siteLogoURL} className="brand nb-admin-header__brand" suffix={<span className="brand-suffix nb-admin-header__suffix">{t('admin.shell.brandSuffix')}</span>} />
        <button
          type="button"
          ref={menuButtonRef}
          className="btn btn-secondary mobile-menu-btn admin-menu-btn nb-menu-button"
          aria-expanded={menuOpen}
          aria-controls="admin-navigation"
          aria-label={t(menuOpen ? 'shell.closeMenu' : 'shell.openMenu')}
          onClick={() => setMenuOpen((open) => !open)}
        >
          <Icon name={menuOpen ? 'close' : 'menu'} />
        </button>
        <div className="site-actions nb-admin-header__actions">
          <AccountMenu
            displayName={session.data.admin.username}
            signOutLabel={t('common.signOut')}
            working={logoutBusy}
            station="admin"
            languageControl={<LanguageSwitcher />}
            onSignOut={() => void logout()}
          />
        </div>
      </header>
      {menuOpen ? (
        <button
          type="button"
          className="shell-overlay"
          aria-label={t('shell.closeMenu')}
          onClick={closeMenu}
        />
      ) : null}
      <div className="admin-shell nb-admin-shell">
        <aside
          ref={sidebarRef}
          id="admin-navigation"
          className={`admin-sidebar nb-admin-sidebar ${menuOpen ? 'is-open' : ''}`}
          aria-label={t('admin.shell.navLabel')}
        >
          <nav className="nb-admin-sidebar__nav">
            {ADMIN_NAV_GROUPS.map((group) => {
              const items = ADMIN_PRIMARY_NAV.filter((item) => item.group === group);
              if (items.length === 0) return null;
              const groupTitle = t(ADMIN_GROUP_LABEL_KEYS[group], { defaultValue: t('admin.settings.nav') });
              return (
                <section className="nb-admin-sidebar__group" key={group}>
                  <h2 className="nb-admin-sidebar__group-title">{groupTitle}</h2>
                  {items.map((item) => {
                    const label = item.labelKey
                      ? t(item.labelKey, { defaultValue: item.fallbackLabelKey ? t(item.fallbackLabelKey) : item.labelKey })
                      : t(`admin.${item.key}.nav`);
                    return (
                      <NavLink className="nb-admin-sidebar__link" key={item.key} to={item.to} end={item.end} onClick={closeMenu}>
                        {item.icon ? <Icon name={item.icon} /> : null}
                        <span className="nb-admin-sidebar__label">{label}</span>
                      </NavLink>
                    );
                  })}
                </section>
              );
            })}
          </nav>
        </aside>
        <main id="main" className="admin-content nb-admin-content" tabIndex={-1}>
          {logoutError ? <ErrorState error={logoutError} /> : null}
          {/* Keep route-local mutation and form state scoped to one admin. */}
          <Outlet key={session.data.admin.username} />
          <PageFooter copyright={t('common.copyright', { year: new Date().getFullYear() })} />
        </main>
      </div>
    </>
  );
}

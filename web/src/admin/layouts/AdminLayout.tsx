import { DraftLeaveScope } from '@shared/components/charity/DraftLeaveScope';
import { FocusScope } from 'react-aria';
import { useDrawerOverlay } from '@shared/components/OverlaySurface';
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Outlet } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Brand, useBrandFavicon } from '@shared/components/Brand';
import { AccountMenu } from '@shared/components/AccountMenu';
import { LanguageSwitcher } from '@shared/components/LanguageSwitcher';
import { PublicShell } from '@shared/components/PublicShell';
import { TimeContextProvider } from '@shared/components/TimeContext';
import { usePublicConfig } from '@shared/query/publicConfig';
import { ErrorState, LoadingState, PageFooter } from '@shared/components/States';
import { Icon } from '@shared/components/Icon';
import { useOptionalToast } from '@shared/components/Toast';
import { apiFetch, isApiError, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { ThemeToggle, DisplayPreferences } from '@shared/theme/ThemeToggle';
import { SHELL_DRAWER_MEDIA_QUERY } from '@shared/styles/tokens';
import { useAdminSession } from '../data';
import { useAdminBranding } from '../branding';
import { useStationTitle } from '@shared/routing/useStationTitle';
import { clearManagementSession } from '@shared/charityManagement';
import { AdminNavigation } from './AdminNavigation';

function AdminLogin({
  onSignedIn,
  notice,
  siteName,
  siteLogoURL,
}: {
  onSignedIn: () => Promise<void>;
  notice?: unknown;
  siteName: string;
  siteLogoURL?: string;
}) {
  const { t } = useTranslation();
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
        <Brand
          siteName={siteName}
          siteLogoURL={siteLogoURL}
          className="brand nb-admin-header__brand"
          suffix={
            <span className="brand-suffix nb-admin-header__suffix">
              {t('admin.shell.brandSuffix')}
            </span>
          }
        />
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
            {validationError ? (
              <p className="field-error" role="alert">
                {validationError}
              </p>
            ) : null}
            {notice ? (
              isApiError(notice) ? (
                <p className="field-error" role="alert">
                  {notice.message}
                </p>
              ) : (
                <ErrorState error={notice} />
              )
            ) : null}
            {error ? <ErrorState error={error} /> : null}
            <button type="submit" className="nb-btn nb-btn--primary" disabled={busy}>
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
  const branding = useAdminBranding();
  const config = usePublicConfig(Boolean(session.data) && !logoutRequested, '/admin/api/config');
  const siteName = config.data?.siteName || branding.data?.siteName || t('app.name');
  const siteLogoURL = config.data?.siteLogoURL ?? branding.data?.siteLogoURL;
  const [menuOpen, setMenuOpen] = useState(false);
  const menuButtonRef = useRef<HTMLButtonElement>(null);
  const sidebarRef = useRef<HTMLElement>(null);
  const headerRef = useRef<HTMLElement>(null);
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
    if (
      typeof window !== 'undefined' &&
      typeof window.matchMedia === 'function' &&
      window.matchMedia(SHELL_DRAWER_MEDIA_QUERY.admin).matches
    ) {
      menuButtonRef.current?.focus();
    }
  }, []);
  const closeMenu = useCallback(() => {
    setMenuOpen(false);
    requestAnimationFrame(restoreMenuFocus);
  }, [restoreMenuFocus]);

  useStationTitle('admin', siteName, Boolean(session.data) && !logoutRequested);
  const shellReady = Boolean(session.data && config.data) && !logoutRequested;
  useEffect(() => {
    const header = headerRef.current;
    if (!shellReady || !header || typeof ResizeObserver === 'undefined') return;
    const root = document.documentElement.style;
    const measure = () => root.setProperty('--nb-admin-header-height', `${header.offsetHeight}px`);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(header);
    return () => {
      observer.disconnect();
      root.removeProperty('--nb-admin-header-height');
    };
  }, [shellReady]);

  useEffect(() => {
    if (previousToastIdentityRef.current && previousToastIdentityRef.current !== toastIdentity)
      clearToasts?.();
    previousToastIdentityRef.current = toastIdentity;
  }, [clearToasts, toastIdentity]);

  useBrandFavicon(siteLogoURL);

  const drawerProps = useDrawerOverlay(menuOpen, closeMenu, sidebarRef);

  useEffect(() => {
    if (!menuOpen || typeof window === 'undefined' || typeof window.matchMedia !== 'function')
      return;
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
        siteName={siteName}
        siteLogoURL={siteLogoURL}
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
  if (
    session.error &&
    !session.data &&
    !isUnauthorized(session.error) &&
    !isNotFoundError(session.error)
  ) {
    return <ErrorState error={session.error} onRetry={() => void session.refetch()} />;
  }
  if (!session.data) {
    return (
      <AdminLogin
        siteName={siteName}
        siteLogoURL={siteLogoURL}
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
    return (
      <PublicShell station="admin" siteName={siteName}>
        <div className="nb-page nb-page--readable">
          <LoadingState />
        </div>
      </PublicShell>
    );
  }
  if (config.error && !config.data) {
    return (
      <PublicShell station="admin" siteName={siteName}>
        <div className="nb-page nb-page--readable">
          <ErrorState error={config.error} onRetry={() => void config.refetch()} />
        </div>
      </PublicShell>
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
      <header className="site-header nb-admin-header" ref={headerRef}>
        <Brand
          siteName={siteName}
          siteLogoURL={siteLogoURL}
          className="brand nb-admin-header__brand"
          suffix={
            <span className="brand-suffix nb-admin-header__suffix">
              {t('admin.shell.brandSuffix')}
            </span>
          }
        />
        <button
          type="button"
          ref={menuButtonRef}
          className="nb-btn nb-btn--secondary mobile-menu-btn admin-menu-btn nb-menu-button"
          style={menuOpen ? { visibility: 'hidden' } : undefined}
          aria-expanded={menuOpen}
          aria-controls="admin-navigation"
          aria-label={t(menuOpen ? 'shell.closeMenu' : 'shell.openMenu')}
          onClick={() => setMenuOpen((open) => !open)}
        >
          <Icon name={menuOpen ? 'close' : 'menu'} />
        </button>
        <div className="site-actions nb-admin-header__actions">
          <ThemeToggle />
          <AccountMenu
            displayName={session.data.admin.username}
            signOutLabel={t('common.signOut')}
            working={logoutBusy}
            station="admin"
            languageControl={
              <>
                <LanguageSwitcher />
                <DisplayPreferences />
              </>
            }
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
      <div className="nb-admin-shell">
        <FocusScope contain={menuOpen}>
          <aside
            {...drawerProps}
            role={menuOpen ? 'dialog' : undefined}
            aria-modal={menuOpen || undefined}
            ref={sidebarRef}
            id="admin-navigation"
            className={`nb-admin-sidebar${menuOpen ? ' is-open' : ''}`}
            aria-label={t('admin.shell.navLabel')}
          >
            <AdminNavigation key={toastIdentity} accountId={toastIdentity} onNavigate={closeMenu} />
            {menuOpen && (
              <button
                type="button"
                className="nb-btn nb-btn--secondary nb-drawer-close"
                aria-label={t('shell.closeMenu')}
                onClick={closeMenu}
              >
                <Icon name="close" />
              </button>
            )}
          </aside>
        </FocusScope>
        <main id="main" className="nb-admin-content" tabIndex={-1}>
          {logoutError ? <ErrorState error={logoutError} /> : null}
          {/* Keep route-local mutation and form state scoped to one admin. */}
          <TimeContextProvider station="admin">
            <DraftLeaveScope>
              <Outlet key={session.data.admin.username} />
            </DraftLeaveScope>
          </TimeContextProvider>
          <PageFooter copyright={t('common.copyright', { year: new Date().getFullYear() })} />
        </main>
      </div>
    </>
  );
}

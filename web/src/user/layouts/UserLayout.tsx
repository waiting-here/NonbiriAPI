import { useCallback, useEffect, useRef, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Link, NavLink, Outlet, useLocation, useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { AccountMenu } from '@shared/components/AccountMenu';
import { Brand, useBrandFavicon } from '@shared/components/Brand';
import { LanguageSwitcher } from '@shared/components/LanguageSwitcher';
import { PublicShell } from '@shared/components/PublicShell';
import { ErrorState, LoadingState, NoticePage, PageFooter } from '@shared/components/States';
import { Icon } from '@shared/components/Icon';
import { useOptionalToast } from '@shared/components/Toast';
import { apiFetch, isApiError, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { ThemeToggle } from '@shared/theme/ThemeToggle';
import { usePublicConfig } from '@shared/query/publicConfig';
import { routePath } from '@shared/routing/routeRegistry';
import { SHELL_DRAWER_MEDIA_QUERY } from '@shared/styles/tokens';
import { useUserSession } from '../data';
import { clearManagementSession } from '@shared/charityManagement';
import { USER_PRIMARY_NAV } from '../navigation';

const USER_ACCOUNT_PATH = routePath('user', 'account');
const USER_STEWARD_PATH = routePath('user', 'steward');
const USER_PRIVACY_PATH = routePath('user', 'privacy');
const USER_TERMS_PATH = routePath('user', 'terms');
const USER_REPORT_PATH = routePath('user', 'credential-report');
const USER_ANNOUNCEMENTS_PATH = routePath('user', 'announcements');
const USER_MAINTENANCE_PATH = routePath('user', 'maintenance');
const USER_REGISTRATION_CLOSED_PATH = routePath('user', 'registration-closed');
const USER_PUBLIC_SHELL_PATHS = new Set([
  USER_PRIVACY_PATH,
  USER_TERMS_PATH,
  USER_MAINTENANCE_PATH,
  USER_REGISTRATION_CLOSED_PATH,
  USER_REPORT_PATH,
]);

export function UserLayout() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const toastContext = useOptionalToast();
  const clearToasts = toastContext?.clear;
  const navigate = useNavigate();
  const [logoutRequested, setLogoutRequested] = useState(false);
  const session = useUserSession(!logoutRequested);
  const config = usePublicConfig();
  const siteName = config.data?.siteName || t('app.name');
  const siteLogoURL = config.data?.siteLogoURL;
  const [menuOpen, setMenuOpen] = useState(false);
  const menuButtonRef = useRef<HTMLButtonElement>(null);
  const navRef = useRef<HTMLElement>(null);
  // Logout is a local station boundary. Keep the shell closed even if the
  // server response is delayed or the request fails, so stale session data
  // cannot briefly re-open the old account.
  const logout = useMutation({
    mutationFn: () => apiFetch<void>('/api/auth/logout', { method: 'POST' }),
    onMutate: () => {
      setLogoutRequested(true);
      clearManagementSession(queryClient, 'steward');
      setMenuOpen(false);
    },
    onSuccess: () => {
      setMenuOpen(false);
      navigate('/');
    },
  });

  useEffect(() => {
    document.title = `${siteName} · ${t('user.shell.title')}`;
  }, [t, siteName]);

  useBrandFavicon(siteLogoURL);

  const restoreMenuFocus = useCallback(() => {
    // The menu button is intentionally hidden on desktop. Only restore focus
    // when the drawer can actually be opened, otherwise a desktop navigation
    // click would move focus to an invisible control.
    if (typeof window !== 'undefined'
      && typeof window.matchMedia === 'function'
      && window.matchMedia(SHELL_DRAWER_MEDIA_QUERY.user).matches) {
      menuButtonRef.current?.focus();
    }
  }, []);
  const closeMenu = useCallback(() => {
    setMenuOpen(false);
    restoreMenuFocus();
  }, [restoreMenuFocus]);

  const location = useLocation();
  // The OAuth re-authorization callback always returns to the configured
  // redirect path (default "/"), not to the account page that requested it.
  // If an account action parked a pending intent in sessionStorage and the
  // elevated capability is now present, resume on /account where the
  // AccountPage picks the intent back up.
  useEffect(() => {
    let pending: string;
    try {
      pending = window.sessionStorage.getItem('nb.pending.elevation') ?? '';
    } catch {
      return;
    }
    if (pending !== 'export' && pending !== 'delete') return;
    if (location.pathname === USER_ACCOUNT_PATH) return;
    const hasElevated = document.cookie
      .split(';')
      .map((part) => part.trim())
      .some((part) => part.startsWith('nb_elevated='));
    if (hasElevated) navigate(USER_ACCOUNT_PATH, { replace: true });
  }, [location.pathname, navigate]);

  useEffect(() => {
    if (!menuOpen) return;
    const firstLink = navRef.current?.querySelector<HTMLElement>('a[href], button:not([disabled])');
    firstLink?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        closeMenu();
        return;
      }
      if (event.key !== 'Tab') return;
      const focusable = Array.from(navRef.current?.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), select:not([disabled])') ?? []);
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
    const media = window.matchMedia(SHELL_DRAWER_MEDIA_QUERY.user);
    const closeForDesktop = (event?: MediaQueryListEvent) => {
      if (event && event.matches) return;
      if (!media.matches) setMenuOpen(false);
    };
    closeForDesktop();
    media.addEventListener('change', closeForDesktop);
    return () => media.removeEventListener('change', closeForDesktop);
  }, [menuOpen]);

  const signedIn = !logoutRequested && !session.error && Boolean(session.data?.user);
  const profile = signedIn ? session.data?.user : undefined;
  const showStewardEntry = profile?.effective_level === 5;
  // The fixed primary navigation never grows with capabilities. Level-5
  // co-management is exposed through the account menu (and its mobile drawer
  // equivalent) exactly once, so the same destination cannot appear in both
  // primary navigation and account actions.
  const navItems = signedIn ? USER_PRIMARY_NAV : [];
  // Server nickname / avatar win over the global Discord profile; either may
  // be absent, in which case the global value (or nothing) is shown.
  const displayName = profile ? profile.guild_nick || profile.username : '';
  const profileAvatar = profile ? profile.guild_avatar_url || profile.avatar_url || '' : '';
  const showSignIn = !logout.isPending
    && (logoutRequested || (!signedIn && (isUnauthorized(session.error) || isNotFoundError(session.error))));
  const toastIdentity = logoutRequested
    ? 'logout'
    : signedIn && profile
      ? `user:${profile.id}`
      : 'anonymous';
  const previousToastIdentityRef = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (previousToastIdentityRef.current && previousToastIdentityRef.current !== toastIdentity) clearToasts?.();
    previousToastIdentityRef.current = toastIdentity;
  }, [clearToasts, toastIdentity]);

  // Maintenance mode replaces the whole user station with a notice page so no
  // site feature is reachable; the admin station (a separate host) is
  // unaffected and can toggle this off. The public-config query is already in
  // flight above, so this only takes effect once it resolves.
  const inMaintenance = config.data?.maintenanceMode === true;
  const isStandalonePublicRoute = USER_PUBLIC_SHELL_PATHS.has(location.pathname);
  if (isStandalonePublicRoute) {
    // Legal and explicit public status routes keep a compact shell even when
    // the bootstrap is pending/failed. Their pages provide built-in legal or
    // status copy and never need the signed-in navigation.
    return (
      <PublicShell siteName={siteName} siteLogoURL={siteLogoURL}>
        <Outlet key={profile?.id ?? 'signed-out'} />
      </PublicShell>
    );
  }
  if (config.isPending && !config.data) {
    return <PublicShell siteName={siteName}><div className="nb-page nb-page--readable"><LoadingState /></div></PublicShell>;
  }
  if (config.error && !config.data) {
    return <PublicShell siteName={siteName}><div className="nb-page nb-page--readable"><ErrorState error={config.error} onRetry={() => void config.refetch()} /></div></PublicShell>;
  }
  if (inMaintenance) {
    return (
      <PublicShell siteName={siteName} siteLogoURL={siteLogoURL}>
        <div className="nb-page nb-page--readable"><NoticePage titleKey="common.maintenanceTitle" bodyKey="common.maintenanceBody" icon="maintenance" /></div>
      </PublicShell>
    );
  }

  return (
    <div className="nb-user-shell">
      <a className="skip-link" href="#main">
        {t('shell.skipToContent')}
      </a>
      <header className="site-header user-header nb-user-header">
        <Brand href={routePath('user', 'home')} siteName={siteName} siteLogoURL={siteLogoURL} className="brand" />
        {signedIn ? (
          <button
            ref={menuButtonRef}
            type="button"
            className="btn btn-secondary mobile-menu-btn nb-menu-button"
            aria-expanded={menuOpen}
            aria-controls="user-navigation"
            aria-label={t(menuOpen ? 'shell.closeMenu' : 'shell.openMenu')}
            onClick={() => setMenuOpen((open) => !open)}
          >
            <Icon name={menuOpen ? 'close' : 'menu'} />
          </button>
        ) : null}
        <nav
          ref={navRef}
          id="user-navigation"
          className={`site-nav nb-user-header__nav ${menuOpen ? 'is-open' : ''}`}
          aria-label={t('user.shell.navLabel')}
        >
          <ul className="nb-user-header__nav-list">
            {navItems.map((item) => (
              <li key={item.key}>
                <NavLink className="nb-user-header__nav-link" to={item.to} end={item.end} onClick={closeMenu}>
                  {item.icon ? <Icon name={item.icon} /> : null}
                  {item.labelKey ? t(item.labelKey, { defaultValue: item.fallbackLabelKey ? t(item.fallbackLabelKey) : item.labelKey }) : t(`user.${item.key}.nav`)}
                </NavLink>
              </li>
            ))}
          </ul>
          {signedIn && menuOpen ? (
            <div className="nb-user-drawer-actions">
              <div className="nb-user-drawer-actions__identity">{displayName}</div>
              <Link className="nb-button nb-button--ghost nb-button--small" to={USER_ACCOUNT_PATH} onClick={closeMenu}>
                <Icon name="account" />
                {t('user.account.nav')}
              </Link>
              {showStewardEntry ? (
                  <Link className="nb-button nb-button--ghost nb-button--small" to={USER_STEWARD_PATH} onClick={closeMenu}>
                  <Icon name="steward" />
                  {t('user.steward.nav')}
                </Link>
              ) : null}
              <ThemeToggle />
              <button type="button" className="nb-button nb-button--secondary" onClick={() => { closeMenu(); logout.mutate(); }} disabled={logout.isPending}>
                <Icon name="logout" />
                {logout.isPending ? t('common.working') : t('common.signOut')}
              </button>
            </div>
          ) : null}
        </nav>
        {signedIn && menuOpen ? (
          <button
            type="button"
            className="nb-user-drawer-overlay"
            aria-label={t('shell.closeMenu')}
            onClick={closeMenu}
          />
        ) : null}
        <div className={`site-actions nb-user-header__actions ${signedIn ? 'nb-user-header__actions--signed-in' : 'nb-user-header__actions--anonymous'}`}>
          {signedIn ? (
            <AccountMenu displayName={displayName} avatarURL={profileAvatar} signOutLabel={t('common.signOut')} working={logout.isPending} steward={showStewardEntry} onSignOut={() => logout.mutate()} />
          ) : null}
          {!signedIn ? <LanguageSwitcher /> : null}
          {!signedIn ? <ThemeToggle /> : null}
          {!signedIn && showSignIn ? (
            <a className="btn btn-primary" href="/api/auth/discord/start">
              {t('common.signIn')}
            </a>
          ) : null}
        </div>
      </header>
      {logout.error ? (
        <div className="shell-error nb-shell-status">
          {isApiError(logout.error) ? (
            <p className="field-error" role="alert">{logout.error.message}</p>
          ) : (
            <ErrorState error={logout.error} />
          )}
        </div>
      ) : null}
      <main id="main" className="user-main nb-user-shell__main" tabIndex={-1}>
        {/* A same-tab account switch must remount every route-local form,
            mutation observer, and transient secret/result state. */}
        <Outlet key={profile?.id ?? 'signed-out'} />
      </main>
      <div className="site-footer nb-user-shell__footer">
        <PageFooter
          copyright={t('common.copyright', { year: new Date().getFullYear() })}
          links={<>{signedIn ? <Link to={USER_ANNOUNCEMENTS_PATH}>{t('user.announcements.nav')}</Link> : null}<Link to={USER_REPORT_PATH}>{t('user.report.nav')}</Link><Link to={USER_PRIVACY_PATH}>{t('user.legal.privacy.nav')}</Link><Link to={USER_TERMS_PATH}>{t('user.legal.terms.nav')}</Link></>}
        />
      </div>
    </div>
  );
}

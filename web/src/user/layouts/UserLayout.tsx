import { useEffect, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Link, NavLink, Outlet } from 'react-router';
import { useTranslation } from 'react-i18next';
import { LanguageSwitcher } from '@shared/components/LanguageSwitcher';
import { ErrorState } from '@shared/components/States';
import { apiFetch, isNotFoundError, isUnauthorized } from '@shared/query/http';
import { ThemeToggle } from '@shared/theme/ThemeToggle';
import { userKeys, useUserSession } from '../data';

interface NavItem {
  to: string;
  key: string;
  end?: boolean;
}

const NAV_ITEMS: NavItem[] = [
  { to: '/', end: true, key: 'home' },
  { to: '/endpoints', key: 'endpoints' },
  { to: '/models', key: 'models' },
  { to: '/keys', key: 'keys' },
  { to: '/issues', key: 'issues' },
  { to: '/account', key: 'account' },
];

export function UserLayout() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const session = useUserSession();
  const [menuOpen, setMenuOpen] = useState(false);
  const logout = useMutation({
    mutationFn: () => apiFetch<void>('/api/auth/logout', { method: 'POST' }),
    onSuccess: () => {
      // Remove every account-owned query immediately after session logout.
      queryClient.removeQueries({ queryKey: userKeys.all });
      setMenuOpen(false);
    },
  });

  useEffect(() => {
    document.title = `${t('app.name')} · ${t('user.shell.title')}`;
  }, [t]);

  const signedIn = Boolean(session.data?.user);
  const showSignIn = !signedIn && (isUnauthorized(session.error) || isNotFoundError(session.error));

  return (
    <>
      <a className="skip-link" href="#main">
        {t('shell.skipToContent')}
      </a>
      <header className="site-header user-header">
        <Link className="brand" to="/" aria-label={t('app.name')}>
          <span className="brand-mark" aria-hidden="true">
            N
          </span>
          <span>{t('app.name')}</span>
          <span className="brand-suffix">{t('user.shell.brandSuffix')}</span>
        </Link>
        <button
          type="button"
          className="btn btn-secondary mobile-menu-btn"
          aria-expanded={menuOpen}
          aria-controls="user-navigation"
          aria-label={t(menuOpen ? 'shell.closeMenu' : 'shell.openMenu')}
          onClick={() => setMenuOpen((open) => !open)}
        >
          {menuOpen ? '×' : '☰'}
        </button>
        <nav
          id="user-navigation"
          className={`site-nav ${menuOpen ? 'is-open' : ''}`}
          aria-label={t('user.shell.navLabel')}
        >
          <ul>
            {NAV_ITEMS.map((item) => (
              <li key={item.key}>
                <NavLink to={item.to} end={item.end} onClick={() => setMenuOpen(false)}>
                  {t(`user.${item.key}.nav`)}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <div className="site-actions">
          {signedIn ? <span className="user-chip">{session.data?.user.username}</span> : null}
          <LanguageSwitcher />
          <ThemeToggle />
          {signedIn ? (
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => logout.mutate()}
              disabled={logout.isPending}
            >
              {logout.isPending ? t('common.working') : t('common.signOut')}
            </button>
          ) : showSignIn ? (
            <a className="btn btn-primary" href="/api/auth/discord/start">
              {t('common.signIn')}
            </a>
          ) : null}
        </div>
      </header>
      {logout.error ? <div className="shell-error"><ErrorState error={logout.error} /></div> : null}
      <main id="main" className="user-main">
        <Outlet />
      </main>
      <footer className="site-footer">
        <span>{t('common.copyright', { year: new Date().getFullYear() })}</span>
        <Link to="/privacy">{t('user.legal.privacy.nav')}</Link>
        <Link to="/terms">{t('user.legal.terms.nav')}</Link>
      </footer>
    </>
  );
}

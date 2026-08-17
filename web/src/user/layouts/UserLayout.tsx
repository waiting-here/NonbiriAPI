import { useEffect } from 'react';
import { Link, NavLink, Outlet } from 'react-router';
import { useTranslation } from 'react-i18next';
import { LanguageSwitcher } from '@shared/components/LanguageSwitcher';
import { ThemeToggle } from '@shared/theme/ThemeToggle';

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

  useEffect(() => {
    document.title = t('app.name');
  }, [t]);

  return (
    <>
      <a className="skip-link" href="#main">
        {t('shell.skipToContent')}
      </a>
      <header className="site-header">
        <span className="brand">{t('app.name')}</span>
        <nav className="site-nav" aria-label={t('user.shell.navLabel')}>
          <ul>
            {NAV_ITEMS.map((item) => (
              <li key={item.key}>
                <NavLink to={item.to} end={item.end}>
                  {t(`user.${item.key}.nav`)}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <div className="site-actions">
          <LanguageSwitcher />
          <ThemeToggle />
        </div>
      </header>
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

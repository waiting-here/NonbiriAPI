import { useEffect } from 'react';
import { NavLink, Outlet } from 'react-router';
import { useTranslation } from 'react-i18next';
import { LanguageSwitcher } from '@shared/components/LanguageSwitcher';
import { ThemeToggle } from '@shared/theme/ThemeToggle';

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
  { to: '/models', key: 'models' },
  { to: '/alerts', key: 'alerts' },
  { to: '/settings', key: 'settings' },
];

export function AdminLayout() {
  const { t } = useTranslation();

  useEffect(() => {
    document.title = `${t('app.name')} · ${t('admin.shell.title')}`;
  }, [t]);

  return (
    <>
      <a className="skip-link" href="#main">
        {t('shell.skipToContent')}
      </a>
      <header className="site-header">
        <span className="brand">
          {t('app.name')}
          <span className="brand-suffix">{t('admin.shell.brandSuffix')}</span>
        </span>
        <div className="site-actions">
          <LanguageSwitcher />
          <ThemeToggle />
        </div>
      </header>
      <div className="admin-shell">
        <aside className="admin-sidebar" aria-label={t('admin.shell.navLabel')}>
          <nav>
            <ul>
              {NAV_ITEMS.map((item) => (
                <li key={item.key}>
                  <NavLink to={item.to} end={item.end}>
                    {t(`admin.${item.key}.nav`)}
                  </NavLink>
                </li>
              ))}
            </ul>
          </nav>
        </aside>
        <main id="main" className="admin-content">
          <Outlet />
        </main>
      </div>
    </>
  );
}

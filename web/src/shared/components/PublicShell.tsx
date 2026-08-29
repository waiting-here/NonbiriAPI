import type { ReactNode } from 'react';
import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Brand } from './Brand';
import { LanguageSwitcher } from './LanguageSwitcher';
import { PageFooter } from './States';
import { ThemeToggle } from '@shared/theme/ThemeToggle';
import { routePath } from '@shared/routing/routeRegistry';

interface PublicShellProps {
  siteName: string;
  siteLogoURL?: string;
  children: ReactNode;
  station?: 'user' | 'admin';
  footerLinks?: ReactNode;
  className?: string;
}

/** Public, anonymous-safe shell shared by maintenance, legal and auth states. */
export function PublicShell({ siteName, siteLogoURL, children, station = 'user', footerLinks, className = '' }: PublicShellProps) {
  const { t } = useTranslation();
  const links = footerLinks ?? (station === 'user' ? <><Link to={routePath('user', 'privacy')}>{t('user.legal.privacy.nav')}</Link><Link to={routePath('user', 'terms')}>{t('user.legal.terms.nav')}</Link></> : null);
  return (
    <div className={`nb-public-shell${className ? ` ${className}` : ''}`}>
      <a className="skip-link" href="#main">{t('shell.skipToContent')}</a>
      <header className="nb-public-shell__header">
        <Brand href={station === 'user' ? routePath('user', 'home') : undefined} siteName={siteName} siteLogoURL={siteLogoURL} />
        <div className="nb-public-shell__actions">
          <LanguageSwitcher />
          <ThemeToggle />
        </div>
      </header>
      <main id="main" className="nb-public-shell__main" tabIndex={-1}>{children}</main>
      <div className="nb-public-shell__footer"><PageFooter copyright={t('common.copyright', { year: new Date().getFullYear() })} links={links} /></div>
    </div>
  );
}

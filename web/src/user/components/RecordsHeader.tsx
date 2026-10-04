import type { ReactNode } from 'react';
import { Link, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import { PageHeader } from '@shared/components/States';
import './records.css';

const routes = [
  ['/logs', 'user.records.calls'],
  ['/credits', 'user.records.credits'],
  ['/issues', 'user.records.issues'],
  ['/debug', 'user.debug.nav'],
] as const;

export function RecordsHeader({
  description,
  actions,
  issueCount,
}: {
  description?: string;
  actions?: ReactNode;
  issueCount?: string;
}) {
  const { t } = useTranslation();
  const { pathname } = useLocation();
  return (
    <div className="records-header">
      <PageHeader title={t('user.records.title')} description={description} actions={actions} />
      <nav className="nb-tabs records-tabs" aria-label={t('user.records.title')}>
        {routes.map(([to, key]) => (
          <Link key={to} to={to} aria-current={pathname === to ? 'page' : undefined}>
            {t(key)}
            {to === '/issues' && issueCount !== undefined ? (
              <span className="nb-badge">{issueCount}</span>
            ) : null}
          </Link>
        ))}
      </nav>
    </div>
  );
}

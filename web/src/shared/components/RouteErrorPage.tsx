import { useTranslation } from 'react-i18next';
import { Link, useRouteError } from 'react-router';
import { Icon, type IconName } from './Icon';
import { isApiError, isForbidden, isNotFoundError } from '@shared/query/http';
import { usePublicConfig } from '@shared/query/publicConfig';
import { routePath } from '@shared/routing/routeRegistry';
import errorStateURL from '@shared/assets/state-error.svg';
import emptyStateURL from '@shared/assets/state-empty.svg';
import maintenanceStateURL from '@shared/assets/state-maintenance.svg';
import { PublicShell } from './PublicShell';

export type RouteErrorKind = '403' | '404' | '500' | 'network' | 'render' | 'maintenance';

export function classifyRouteError(error: unknown): RouteErrorKind {
  if (isForbidden(error)) return '403';
  if (isNotFoundError(error)) return '404';
  if (isApiError(error) && error.code === 'network_error') return 'network';
  // `maintenance` is an explicit frozen error code.  A transport-level 503
  // (`service_unavailable`) still represents an ordinary recoverable server
  // failure and must not hide behind the maintenance state.
  if (isApiError(error) && error.code === 'maintenance') return 'maintenance';
  if (isApiError(error) && error.status >= 500) return '500';
  return 'render';
}

/**
 * Router errors are intentionally not echoed: route errors can contain raw
 * upstream or implementation text. The user gets a bounded recovery action.
 */
export function RouteErrorPage({ station = 'user', authenticated = false }: { station?: 'user' | 'admin'; authenticated?: boolean }) {
  const { t } = useTranslation();
  const error = useRouteError();
  const config = usePublicConfig(station === 'user' || (station === 'admin' && authenticated), station === 'admin' ? '/admin/api/config' : '/api/config');
  const siteName = config.data?.siteName || t('app.name');
  const kind = classifyRouteError(error);
  const titleKey = kind === '404'
    ? 'common.notFoundTitle'
    : kind === '403'
      ? 'common.forbiddenTitle'
      : kind === '500'
        ? 'common.serverErrorTitle'
        : kind === 'maintenance'
          ? 'common.maintenanceTitle'
          : kind === 'network'
          ? 'common.networkError'
          : 'common.routeErrorTitle';
  const bodyKey = kind === '404'
    ? 'common.notFoundBody'
    : kind === '403'
      ? 'common.forbiddenBody'
      : kind === '500'
        ? 'common.serverErrorBody'
        : kind === 'maintenance'
          ? 'common.maintenanceBody'
          : kind === 'network'
          ? 'common.networkHint'
          : 'common.routeErrorBody';
  const icon: IconName = kind === '404'
    ? 'empty'
    : kind === 'network'
      ? 'info'
      : kind === 'maintenance'
        ? 'maintenance'
        : kind === '403'
          ? 'warning'
          : 'error';
  const recovery = kind === 'maintenance' ? null : kind === '403' || kind === '404' ? (
    <Link className="nb-button nb-button--secondary" to={routePath(station, station === 'admin' ? 'admin-home' : 'home')}>
      {t('common.backHome')}
    </Link>
  ) : (
    <button type="button" className="btn btn-primary nb-button nb-button--primary" onClick={() => window.location.reload()}>
      {t('common.reload')}
    </button>
  );
  const body = (
    <section className="page nb-standalone-state" data-error-kind={kind}>
      <div className="nb-state nb-state--error">
        <span className="nb-state__icon">
          <img className="nb-state__illustration" src={kind === '404' ? emptyStateURL : kind === 'maintenance' ? maintenanceStateURL : errorStateURL} alt="" />
          <Icon name={icon} />
        </span>
        <div>
          <h1>{t(titleKey)}</h1>
          <p>{t(bodyKey)}</p>
          {recovery}
        </div>
      </div>
    </section>
  );
  return (
    <PublicShell station={station} siteName={siteName} siteLogoURL={config.data?.siteLogoURL}>
      {body}
    </PublicShell>
  );
}

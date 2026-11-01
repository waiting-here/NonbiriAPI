import { Button } from '@shared/components/ui/Button';
import { useTranslation } from 'react-i18next';
import { Link, useRouteError } from 'react-router';
import { isApiError, isForbidden, isNotFoundError } from '@shared/query/http';
import { usePublicConfig } from '@shared/query/publicConfig';
import { routePath } from '@shared/routing/routeRegistry';
import { Illustration } from './Illustration';
import { PublicShell } from './PublicShell';
import { LoginRestrictions } from './AutomaticRestrictions';

export type RouteErrorKind =
  '403' | '404' | '500' | 'network' | 'render' | 'maintenance' | 'resource';

export function classifyRouteError(error: unknown): RouteErrorKind {
  if (
    error instanceof Error &&
    /Failed to fetch dynamically imported module|Importing a module script failed|Loading chunk .* failed|Unable to preload CSS/i.test(
      error.message,
    )
  )
    return 'resource';
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
export function RouteErrorPage({
  station = 'user',
  authenticated = false,
}: {
  station?: 'user' | 'admin';
  authenticated?: boolean;
}) {
  const { t } = useTranslation();
  const error = useRouteError();
  const config = usePublicConfig(
    station === 'user' || (station === 'admin' && authenticated),
    station === 'admin' ? '/admin/api/config' : '/api/config',
  );
  const siteName = config.data?.siteName || t('app.name');
  const kind = classifyRouteError(error);
  const titleKey =
    kind === 'resource'
      ? 'common.resourceErrorTitle'
      : kind === '404'
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
  const bodyKey =
    kind === 'resource'
      ? 'common.resourceErrorBody'
      : kind === '404'
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
  const recovery =
    kind === 'maintenance' ? null : kind === '403' || kind === '404' ? (
      <Link
        className="nb-btn nb-btn--secondary"
        to={routePath(station, station === 'admin' ? 'admin-home' : 'home')}
      >
        {t('common.backHome')}
      </Link>
    ) : (
      <Button type="button" variant="primary" onClick={() => window.location.reload()}>
        {t('common.reload')}
      </Button>
    );
  const body = (
    <section className="page nb-standalone-state" data-error-kind={kind}>
      <div className="nb-state nb-state--error">
        <span className="nb-state__icon nb-state__art" aria-hidden="true">
          <Illustration
            name={kind === '404' ? 'search' : kind === 'maintenance' ? 'maintenance' : 'error'}
          />
        </span>
        <div>
          <h1>{t(titleKey)}</h1>
          <p>{t(bodyKey)}</p>
          {kind === '403' && station === 'user' && <LoginRestrictions />}
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

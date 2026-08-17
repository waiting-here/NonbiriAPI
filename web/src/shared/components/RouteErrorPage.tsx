import { useTranslation } from 'react-i18next';
import { useRouteError } from 'react-router';

/**
 * Router errors are intentionally not echoed: route errors can contain raw
 * upstream or implementation text. The user gets a bounded recovery action.
 */
export function RouteErrorPage() {
  const { t } = useTranslation();
  useRouteError();
  return (
    <section className="page standalone-state">
      <h1>{t('common.routeErrorTitle')}</h1>
      <p>{t('common.routeErrorBody')}</p>
      <button type="button" className="btn btn-primary" onClick={() => window.location.reload()}>
        {t('common.reload')}
      </button>
    </section>
  );
}

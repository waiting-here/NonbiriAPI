import { useTranslation } from 'react-i18next';
import { useRouteError } from 'react-router';

/**
 * Error boundary rendered by the router when a route throws. Raw error details
 * are never surfaced to the user; they are logged for diagnosis only.
 */
export function RouteErrorPage() {
  const { t } = useTranslation();
  const error = useRouteError();
  console.error(error);
  return (
    <section className="page">
      <h1>{t('common.routeErrorTitle')}</h1>
      <p>{t('common.routeErrorBody')}</p>
      <button type="button" onClick={() => window.location.reload()}>
        {t('common.reload')}
      </button>
    </section>
  );
}

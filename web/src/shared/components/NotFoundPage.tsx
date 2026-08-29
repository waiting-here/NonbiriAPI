import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import emptyStateURL from '@shared/assets/state-empty.svg';
import { routePath } from '@shared/routing/routeRegistry';

export function NotFoundPage({ station = 'user' }: { station?: 'user' | 'admin' }) {
  const { t } = useTranslation();
  // This component is mounted beneath a station shell's wildcard route. The
  // shell already owns branding/config; keeping this leaf pure prevents a
  // second Brand and an unnecessary config request on nested 404s.
  return (
    <section className="page nb-standalone-state" data-error-kind="404">
      <div className="nb-state nb-state--warning">
        <span className="nb-state__icon"><img className="nb-state__illustration" src={emptyStateURL} alt="" /></span>
        <div>
          <h1>{t('common.notFoundTitle')}</h1>
          <p>{t('common.notFoundBody')}</p>
          <Link className="nb-button nb-button--secondary" to={routePath(station, station === 'admin' ? 'admin-home' : 'home')}><span>{t('common.backHome')}</span></Link>
        </div>
      </div>
    </section>
  );
}

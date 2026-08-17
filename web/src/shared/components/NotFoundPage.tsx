import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';

export function NotFoundPage() {
  const { t } = useTranslation();
  return (
    <section className="page">
      <h1>{t('common.notFoundTitle')}</h1>
      <p>{t('common.notFoundBody')}</p>
      <Link to="/">{t('common.backHome')}</Link>
    </section>
  );
}

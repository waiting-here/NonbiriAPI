import { useTranslation } from 'react-i18next';

export function TermsPage() {
  const { t } = useTranslation();
  return (
    <section className="page">
      <h1>{t('user.legal.terms.title')}</h1>
      <p className="page-muted">{t('user.legal.terms.body')}</p>
    </section>
  );
}

import { useTranslation } from 'react-i18next';

export function TermsPage() {
  const { t } = useTranslation();
  return (
    <article className="page legal-copy">
      <header className="page-header">
        <div>
          <p className="eyebrow">{t('app.name')}</p>
          <h1>{t('user.legal.terms.title')}</h1>
          <p className="page-description">{t('user.legal.terms.intro')}</p>
        </div>
      </header>
      <section>
        <h2>{t('user.legal.terms.useTitle')}</h2>
        <p>{t('user.legal.terms.useBody')}</p>
      </section>
      <section>
        <h2>{t('user.legal.terms.credentialsTitle')}</h2>
        <p>{t('user.legal.terms.credentialsBody')}</p>
      </section>
      <section>
        <h2>{t('user.legal.terms.availabilityTitle')}</h2>
        <p>{t('user.legal.terms.availabilityBody')}</p>
      </section>
      <section>
        <h2>{t('user.legal.terms.dataTitle')}</h2>
        <p>{t('user.legal.terms.dataBody')}</p>
      </section>
      <section>
        <h2>{t('user.legal.terms.changesTitle')}</h2>
        <p>{t('user.legal.terms.changesBody')}</p>
      </section>
    </article>
  );
}

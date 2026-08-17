import { useTranslation } from 'react-i18next';

export function PrivacyPage() {
  const { t } = useTranslation();
  return (
    <article className="page legal-copy">
      <header className="page-header">
        <div>
          <p className="eyebrow">{t('app.name')}</p>
          <h1>{t('user.legal.privacy.title')}</h1>
          <p className="page-description">{t('user.legal.privacy.intro')}</p>
          <p className="legal-notice">{t('user.legal.privacy.notice')}</p>
        </div>
      </header>
      <section>
        <h2>{t('user.legal.privacy.operatorTitle')}</h2>
        <p>{t('user.legal.privacy.operatorBody')}</p>
      </section>
      <section>
        <h2>{t('user.legal.privacy.identityTitle')}</h2>
        <p>{t('user.legal.privacy.identityBody')}</p>
      </section>
      <section>
        <h2>{t('user.legal.privacy.dataTitle')}</h2>
        <p>{t('user.legal.privacy.dataBody')}</p>
      </section>
      <section>
        <h2>{t('user.legal.privacy.credentialsTitle')}</h2>
        <p>{t('user.legal.privacy.credentialsBody')}</p>
      </section>
      <section>
        <h2>{t('user.legal.privacy.trafficTitle')}</h2>
        <p>{t('user.legal.privacy.trafficBody')}</p>
      </section>
      <section>
        <h2>{t('user.legal.privacy.sharingTitle')}</h2>
        <p>{t('user.legal.privacy.sharingBody')}</p>
      </section>
      <section>
        <h2>{t('user.legal.privacy.retentionTitle')}</h2>
        <p>{t('user.legal.privacy.retentionBody')}</p>
      </section>
      <section>
        <h2>{t('user.legal.privacy.rightsTitle')}</h2>
        <p>{t('user.legal.privacy.rightsBody')}</p>
      </section>
      <section>
        <h2>{t('user.legal.privacy.securityTitle')}</h2>
        <p>{t('user.legal.privacy.securityBody')}</p>
      </section>
      <section>
        <h2>{t('user.legal.privacy.contactTitle')}</h2>
        <p>{t('user.legal.privacy.contactBody')}</p>
      </section>
    </article>
  );
}

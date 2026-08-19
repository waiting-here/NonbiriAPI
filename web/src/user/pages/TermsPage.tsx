import { useTranslation } from 'react-i18next';
import { usePublicConfig } from '@shared/query/publicConfig';
import { LegalSections } from '../components/LegalSections';

export function TermsPage() {
  const { t, i18n } = useTranslation();
  const config = usePublicConfig();
  const locale = i18n.language.startsWith('en') ? 'en' : 'zh';
  const override = locale === 'en' ? config.data?.legalTermsOverrideEn : config.data?.legalTermsOverrideZh;
  const hasOverride = Boolean(override);
  const authoritative = config.data?.legalAuthoritativeLocale ?? '';
  const authoritativeName = authoritative ? t(`user.legal.localeName.${authoritative}`) : '';

  return (
    <article className="page legal-copy">
      <header className="page-header">
        <div>
          <p className="eyebrow">{t('app.name')}</p>
          <h1>{t('user.legal.terms.title')}</h1>
          <p className="page-description">{t('user.legal.terms.intro')}</p>
          {!hasOverride ? <p className="legal-notice">{t('user.legal.terms.notice')}</p> : null}
          {authoritative ? (
            <p className="legal-notice" role="note">
              {t('user.legal.authoritativeNotice', { locale: authoritativeName })}
            </p>
          ) : null}
        </div>
      </header>
      {hasOverride && override ? (
        <LegalSections override={override} />
      ) : (
        <>
          <section>
            <h2>{t('user.legal.terms.operatorTitle')}</h2>
            <p>{t('user.legal.terms.operatorBody')}</p>
          </section>
          <section>
            <h2>{t('user.legal.terms.accessTitle')}</h2>
            <p>{t('user.legal.terms.accessBody')}</p>
          </section>
          <section>
            <h2>{t('user.legal.terms.useTitle')}</h2>
            <p>{t('user.legal.terms.useBody')}</p>
          </section>
          <section>
            <h2>{t('user.legal.terms.upstreamTitle')}</h2>
            <p>{t('user.legal.terms.upstreamBody')}</p>
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
            <h2>{t('user.legal.terms.suspensionTitle')}</h2>
            <p>{t('user.legal.terms.suspensionBody')}</p>
          </section>
          <section>
            <h2>{t('user.legal.terms.liabilityTitle')}</h2>
            <p>{t('user.legal.terms.liabilityBody')}</p>
          </section>
          <section>
            <h2>{t('user.legal.terms.ipTitle')}</h2>
            <p>{t('user.legal.terms.ipBody')}</p>
          </section>
          <section>
            <h2>{t('user.legal.terms.changesTitle')}</h2>
            <p>{t('user.legal.terms.changesBody')}</p>
          </section>
        </>
      )}
    </article>
  );
}

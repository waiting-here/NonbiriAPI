import { useTranslation } from 'react-i18next';
import { PageHeader } from '@shared/components/States';
import { Note } from '@shared/components/ui';
import { usePublicConfig } from '@shared/query/publicConfig';
import { LegalLayout } from '../components/LegalLayout';
import { LegalSections } from '../components/LegalSections';

export function PrivacyPage() {
  const { t, i18n } = useTranslation();
  const config = usePublicConfig();
  const locale = i18n.language.startsWith('en') ? 'en' : 'zh';
  const override =
    locale === 'en' ? config.data?.legalPrivacyOverrideEn : config.data?.legalPrivacyOverrideZh;
  const hasOverride = Boolean(override);
  const authoritative = config.data?.legalAuthoritativeLocale ?? '';
  const authoritativeName = authoritative ? t(`user.legal.localeName.${authoritative}`) : '';

  return (
    <article className="page legal-copy nb-stack">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('user.legal.privacy.title')}
        description={t('user.legal.privacy.intro')}
      />
      {!hasOverride ? <Note>{t('user.legal.privacy.notice')}</Note> : null}
      {authoritative ? (
        <Note>{t('user.legal.authoritativeNotice', { locale: authoritativeName })}</Note>
      ) : null}
      <LegalLayout documentKey="privacy">
        {hasOverride && override ? (
          <LegalSections override={override} />
        ) : (
          <>
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
              <h2>{t('user.legal.privacy.economyTitle')}</h2>
              <p>{t('user.legal.privacy.economyBody')}</p>
              <p>{t('user.legal.privacy.creditHistoryBody')}</p>
            </section>
            <section>
              <h2>{t('user.legal.privacy.credentialsTitle')}</h2>
              <p>{t('user.legal.privacy.credentialsBody')}</p>
            </section>
            <section>
              <h2>{t('user.legal.privacy.trafficTitle')}</h2>
              <p>{t('user.legal.privacy.trafficBody')}</p>
              <p>{t('user.legal.privacy.routingBody')}</p>
              <p>{t('user.legal.privacy.activityCoverBody')}</p>
            </section>
            <section>
              <h2>{t('user.legal.privacy.diagnosticsTitle')}</h2>
              <p>{t('user.legal.privacy.diagnosticsBody')}</p>
              <p>{t('user.legal.privacy.sourcesBody')}</p>
            </section>
            <section>
              <h2>{t('user.legal.privacy.auditTitle')}</h2>
              <p>{t('user.legal.privacy.auditBody')}</p>
              <p>{t('user.legal.privacy.scanBody')}</p>
              <p>{t('user.legal.privacy.browserAuditBody')}</p>
              <p>{t('user.legal.privacy.returnAlertBody')}</p>
            </section>
            <section>
              <h2>{t('user.legal.privacy.imageTitle')}</h2>
              <p>{t('user.legal.privacy.imageBody')}</p>
            </section>
            <section>
              <h2>{t('user.legal.privacy.fatFishTitle')}</h2>
              <p>{t('user.legal.privacy.fatFishBody')}</p>
            </section>
            <section>
              <h2>{t('user.legal.privacy.gachaTitle')}</h2>
              <p>{t('user.legal.privacy.gachaBody')}</p>
            </section>
            <section>
              <h2>{t('user.legal.privacy.aiPlayersTitle')}</h2>
              <p>{t('user.legal.privacy.aiPlayersBody')}</p>
            </section>
            <section>
              <h2>{t('user.legal.privacy.sharingTitle')}</h2>
              <p>{t('user.legal.privacy.sharingBody')}</p>
              <p>{t('user.legal.privacy.keyLimitsBody')}</p>
              <p>{t('user.legal.privacy.charityManagementBody')}</p>
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
          </>
        )}
      </LegalLayout>
    </article>
  );
}

import { useTranslation } from 'react-i18next';

export function PrivacyPage() {
  const { t } = useTranslation();
  return (
    <section className="page">
      <h1>{t('user.legal.privacy.title')}</h1>
      <p className="page-muted">{t('user.legal.privacy.body')}</p>
    </section>
  );
}

import { useTranslation } from 'react-i18next';

interface PlaceholderPageProps {
  title: string;
  description: string;
}

/**
 * Generic placeholder rendered for routes whose business pages arrive in a
 * later milestone. Keeps every shell route navigable and keyboard-accessible.
 */
export function PlaceholderPage({ title, description }: PlaceholderPageProps) {
  const { t } = useTranslation();
  return (
    <section className="page">
      <h1>{title}</h1>
      <p>{description}</p>
      <p className="page-muted">{t('common.comingSoon')}</p>
    </section>
  );
}

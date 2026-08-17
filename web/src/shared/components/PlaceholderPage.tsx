import { useTranslation } from 'react-i18next';

interface PlaceholderPageProps {
  title: string;
  description: string;
}

/**
 * Generic fallback for a route that intentionally has no data view yet. Keeps
 * every shell route navigable and keyboard-accessible.
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

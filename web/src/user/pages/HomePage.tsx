import { useTranslation } from 'react-i18next';
import { PlaceholderPage } from '@shared/components/PlaceholderPage';

export function HomePage() {
  const { t } = useTranslation();
  return <PlaceholderPage title={t('user.home.title')} description={t('user.home.description')} />;
}

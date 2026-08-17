import { useTranslation } from 'react-i18next';
import { PlaceholderPage } from '@shared/components/PlaceholderPage';

export function KeysPage() {
  const { t } = useTranslation();
  return <PlaceholderPage title={t('user.keys.title')} description={t('user.keys.description')} />;
}

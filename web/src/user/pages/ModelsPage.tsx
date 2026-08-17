import { useTranslation } from 'react-i18next';
import { PlaceholderPage } from '@shared/components/PlaceholderPage';

export function ModelsPage() {
  const { t } = useTranslation();
  return (
    <PlaceholderPage title={t('user.models.title')} description={t('user.models.description')} />
  );
}

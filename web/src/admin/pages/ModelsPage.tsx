import { useTranslation } from 'react-i18next';
import { PlaceholderPage } from '@shared/components/PlaceholderPage';

export function ModelsPage() {
  const { t } = useTranslation();
  return (
    <PlaceholderPage title={t('admin.models.title')} description={t('admin.models.description')} />
  );
}

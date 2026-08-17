import { useTranslation } from 'react-i18next';
import { PlaceholderPage } from '@shared/components/PlaceholderPage';

export function EndpointsPage() {
  const { t } = useTranslation();
  return (
    <PlaceholderPage
      title={t('admin.endpoints.title')}
      description={t('admin.endpoints.description')}
    />
  );
}

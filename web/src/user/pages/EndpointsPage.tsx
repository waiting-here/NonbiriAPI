import { useTranslation } from 'react-i18next';
import { PlaceholderPage } from '@shared/components/PlaceholderPage';

export function EndpointsPage() {
  const { t } = useTranslation();
  return (
    <PlaceholderPage
      title={t('user.endpoints.title')}
      description={t('user.endpoints.description')}
    />
  );
}

import { useTranslation } from 'react-i18next';
import { PlaceholderPage } from '@shared/components/PlaceholderPage';

export function DashboardPage() {
  const { t } = useTranslation();
  return (
    <PlaceholderPage
      title={t('admin.dashboard.title')}
      description={t('admin.dashboard.description')}
    />
  );
}

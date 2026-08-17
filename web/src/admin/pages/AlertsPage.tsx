import { useTranslation } from 'react-i18next';
import { PlaceholderPage } from '@shared/components/PlaceholderPage';

export function AlertsPage() {
  const { t } = useTranslation();
  return (
    <PlaceholderPage title={t('admin.alerts.title')} description={t('admin.alerts.description')} />
  );
}

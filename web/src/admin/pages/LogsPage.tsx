import { useTranslation } from 'react-i18next';
import { PlaceholderPage } from '@shared/components/PlaceholderPage';

export function LogsPage() {
  const { t } = useTranslation();
  return (
    <PlaceholderPage title={t('admin.logs.title')} description={t('admin.logs.description')} />
  );
}

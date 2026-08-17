import { useTranslation } from 'react-i18next';
import { PlaceholderPage } from '@shared/components/PlaceholderPage';

export function UsersPage() {
  const { t } = useTranslation();
  return (
    <PlaceholderPage title={t('admin.users.title')} description={t('admin.users.description')} />
  );
}

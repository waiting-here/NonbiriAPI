import { useTranslation } from 'react-i18next';
import { PlaceholderPage } from '@shared/components/PlaceholderPage';

export function AccountPage() {
  const { t } = useTranslation();
  return (
    <PlaceholderPage title={t('user.account.title')} description={t('user.account.description')} />
  );
}

import { useTranslation } from 'react-i18next';
import { PlaceholderPage } from '@shared/components/PlaceholderPage';

export function IssuesPage() {
  const { t } = useTranslation();
  return (
    <PlaceholderPage title={t('user.issues.title')} description={t('user.issues.description')} />
  );
}

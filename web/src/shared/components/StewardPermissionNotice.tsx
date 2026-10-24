import { useTranslation } from 'react-i18next';
import { Note } from './ui';

export function StewardPermissionNotice() {
  const { t } = useTranslation();
  return <Note tone="warn">{t('management.permissions.denied')}</Note>;
}

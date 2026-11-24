import { useTranslation } from 'react-i18next';
import { Note } from './ui';

export function StewardPermissionNotice({ permission }: { permission?: string }) {
  const { t } = useTranslation();
  return (
    <Note tone="warn">
      {t('management.permissions.denied')}
      {permission && (
        <>
          {' '}
          · {t('common.governance.needed')} ·{' '}
          {t(`management.permissions.labels.${permission.replaceAll('.', '_')}`)}
        </>
      )}
    </Note>
  );
}

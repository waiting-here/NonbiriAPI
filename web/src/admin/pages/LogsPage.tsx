import { useTranslation } from 'react-i18next';
import { PageHeader } from '@shared/components/States';
import { RoleLogPanel } from '@shared/components/log';
import '@shared/operations/operations.css';

export function LogsPage() {
  const { t, i18n } = useTranslation();
  return (
    <div className="page ops-stack">
      <PageHeader
        eyebrow={t('admin.navigation.operations')}
        title={t('admin.logs.logsTitle')}
        description={t('admin.logs.description')}
      />
      <RoleLogPanel role="admin" language={i18n.resolvedLanguage} />
    </div>
  );
}

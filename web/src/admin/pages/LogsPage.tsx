import { useTranslation } from 'react-i18next';
import { PageHeader } from '@shared/components/States';
import { OperatorLogWorkspace } from '@shared/observability/OperatorLogWorkspace';
import '@shared/operations/operations.css';
import { useAdminSession } from '../data';

export function LogsPage() {
  const { t } = useTranslation();
  const session = useAdminSession();
  return (
    <div className="page ops-stack">
      <PageHeader
        eyebrow={t('admin.navigation.records')}
        title={t('admin.logs.logsTitle')}
        description={t('admin.logs.description')}
      />
      <OperatorLogWorkspace
        role="admin"
        accountId={session.data?.admin.username}
        scopeReady={!session.isPending && !session.error && Boolean(session.data?.admin.username)}
        canReadRequests
        canReadActivity
      />
    </div>
  );
}

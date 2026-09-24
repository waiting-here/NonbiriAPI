import { useTranslation } from 'react-i18next';
import { PageHeader } from '@shared/components/States';
import { RoleLogPanel } from '@shared/components/log';
import { IndependentDiagnostics } from '@shared/observability/IndependentDiagnostics';
import '@shared/operations/operations.css';
import { useAdminSession } from '../data';

export function LogsPage() {
  const { t, i18n } = useTranslation();
  const session = useAdminSession();
  return (
    <div className="page ops-stack">
      <PageHeader
        eyebrow={t('admin.navigation.operations')}
        title={t('admin.logs.logsTitle')}
        description={t('admin.logs.description')}
      />
      <RoleLogPanel
        role="admin"
        language={i18n.resolvedLanguage}
        accountId={session.data?.admin.username}
        scopeReady={!session.isPending && !session.error && Boolean(session.data?.admin.username)}
        enabled={!session.isPending && !session.error}
      />
      <IndependentDiagnostics
        role="admin"
        accountId={session.data?.admin.username}
        scopeReady={!session.isPending && !session.error && Boolean(session.data?.admin.username)}
        enabled={!session.isPending && !session.error}
      />
    </div>
  );
}

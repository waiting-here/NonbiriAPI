import { ErrorState, LoadingState } from '@shared/components/States';
import { RiskAuditPanel } from '@shared/riskAudit/Panel';
import { useAdminSession } from '../data';

export function RiskAuditPage() {
  const session = useAdminSession();
  if (session.error)
    return <ErrorState error={session.error} onRetry={() => void session.refetch()} />;
  if (!session.data?.admin.username) return <LoadingState />;
  return <RiskAuditPanel role="admin" scopeKey={session.data.admin.username} />;
}

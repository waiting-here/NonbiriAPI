import { useQueryClient } from '@tanstack/react-query';
import { EndpointPolicies } from '@shared/components/EndpointPolicies';
import { ErrorState, LoadingState } from '@shared/components/States';
import { useAdminSession } from '../data';
import '@shared/operations/operations.css';
export function EndpointPoliciesPage() {
  const session = useAdminSession();
  const client = useQueryClient();
  if (session.isPending) return <LoadingState />;
  if (session.error)
    return <ErrorState error={session.error} onRetry={() => void session.refetch()} />;
  if (!session.data) return <LoadingState />;
  return (
    <EndpointPolicies
      key={session.data.admin.username}
      role="admin"
      accountId={`admin:${session.data.admin.username}`}
      onCapabilityLoss={() => {
        void client.invalidateQueries({ queryKey: ['admin', 'session'] });
      }}
    />
  );
}

import { useAdminSession } from '../data';
import { ErrorState, LoadingState } from '@shared/components/States';
import { AgentWorkspace } from '@shared/agent/AgentWorkspace';
import { TimeContextProvider } from '@shared/components/TimeContext';
export function AgentPage() {
  const session = useAdminSession();
  if (session.isPending) return <LoadingState />;
  if (session.error)
    return <ErrorState error={session.error} onRetry={() => void session.refetch()} />;
  const account = session.data?.admin.username ?? '';
  return (
    <TimeContextProvider station="admin">
      <AgentWorkspace key={account} role="admin" account={account} />
    </TimeContextProvider>
  );
}

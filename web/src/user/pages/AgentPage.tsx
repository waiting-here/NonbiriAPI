import { AgentWorkspace } from '@shared/agent/AgentWorkspace';
import { ErrorState, LoadingState } from '@shared/components/States';
import { StewardPermissionNotice } from '@shared/components/StewardPermissionNotice';
import { TimeContextProvider } from '@shared/components/TimeContext';
import { UserPageGate } from '../components/UserPageGate';
import { useUserSession } from '../data';
export function AgentPage() {
  const session = useUserSession();
  if (session.isPending) return <LoadingState />;
  if (session.error)
    return <ErrorState error={session.error} onRetry={() => void session.refetch()} />;
  const user = session.data?.user;
  if (!user || ![5, 6].includes(user.effective_level) || user.is_banned)
    return <StewardPermissionNotice />;
  const account = [user.id, user.effective_level, user.steward_permissions?.revision ?? '0'].join(
    ':',
  );
  return (
    <UserPageGate>
      <TimeContextProvider station="steward">
        <AgentWorkspace key={account} role="steward" account={account} />
      </TimeContextProvider>
    </UserPageGate>
  );
}

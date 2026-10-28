import { useAdminSession } from '../data';
import { ErrorState, LoadingState } from '@shared/components/States';
import { TimeContextProvider } from '@shared/components/TimeContext';
import { ManagementHistory } from '../features/managementHistory/ManagementHistory';

export function ManagementOperationsPage() {
  const session = useAdminSession();
  if (session.isPending) return <LoadingState />;
  if (session.error)
    return <ErrorState error={session.error} onRetry={() => void session.refetch()} />;
  const account = session.data?.admin.username ?? '';
  return (
    <TimeContextProvider station="admin">
      <ManagementHistory key={account} account={account} />
    </TimeContextProvider>
  );
}

import { useQueryClient } from '@tanstack/react-query';
import { hasStewardPermission } from '@shared/operations/stewardPermissions';
import { StewardPermissionNotice } from '@shared/components/StewardPermissionNotice';
import { ChannelBindingPresets } from '@shared/components/charity/ChannelBindingPresets';
import { ErrorState, LoadingState } from '@shared/components/States';
import { useUserSession } from '../data';
import { UserPageGate } from '../components/UserPageGate';
import '@shared/operations/operations.css';
export function ChannelBindingPresetsPage() {
  const session = useUserSession();
  const client = useQueryClient();
  if (session.isPending) return <LoadingState />;
  if (session.error)
    return <ErrorState error={session.error} onRetry={() => void session.refetch()} />;
  return (
    <UserPageGate>
      {session.data && hasStewardPermission(session.data.user, 'charity.presets') ? (
        <ChannelBindingPresets
          key={session.data.user.id}
          role="steward"
          accountId={`steward:${session.data.user.id}`}
          onCapabilityLoss={() => {
            void client.invalidateQueries({ queryKey: ['user', 'session'] });
          }}
        />
      ) : (
        <div className="page">
          <StewardPermissionNotice />
        </div>
      )}
    </UserPageGate>
  );
}

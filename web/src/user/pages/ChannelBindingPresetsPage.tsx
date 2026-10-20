import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ChannelBindingPresets } from '@shared/components/charity/ChannelBindingPresets';
import { ErrorState, LoadingState } from '@shared/components/States';
import { useUserSession } from '../data';
import { UserPageGate } from '../components/UserPageGate';
import '@shared/operations/operations.css';
export function ChannelBindingPresetsPage() {
  const { t } = useTranslation();
  const session = useUserSession();
  const client = useQueryClient();
  if (session.isPending) return <LoadingState />;
  if (session.error)
    return <ErrorState error={session.error} onRetry={() => void session.refetch()} />;
  return (
    <UserPageGate>
      {session.data?.user.effective_level === 6 ? (
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
          <p role="alert">{t('common.bindingPresets.accessDenied')}</p>
        </div>
      )}
    </UserPageGate>
  );
}

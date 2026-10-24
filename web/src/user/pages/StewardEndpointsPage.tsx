import { Link, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import { EndpointManagement } from '@shared/components/EndpointManagement';
import { ErrorState, LoadingState } from '@shared/components/States';
import { hasStewardPermission } from '@shared/operations/stewardPermissions';
import { listReturnPath } from '@shared/operations/listReturn';
import { useUserSession } from '../data';
import { UserPageGate } from '../components/UserPageGate';

export function StewardEndpointsPage() {
  const session = useUserSession();
  const { t } = useTranslation();
  const location = useLocation();
  if (session.isPending) return <LoadingState />;
  if (session.error)
    return <ErrorState error={session.error} onRetry={() => void session.refetch()} />;
  const user = session.data?.user;
  const account = user
    ? `steward:${user.id}:${user.effective_level}:${user.steward_permissions?.revision ?? '0'}`
    : '';
  return (
    <UserPageGate>
      <EndpointManagement
        key={account}
        role="steward"
        account={account}
        scopeReady={Boolean(user)}
        canRead={!user?.is_banned && hasStewardPermission(user, 'endpoints.read')}
        canTag={!user?.is_banned && hasStewardPermission(user, 'endpoints.tags')}
        back={
          <Link to={listReturnPath(location.state, '/steward')}>
            {t('management.endpoints.backManagement')}
          </Link>
        }
      />
    </UserPageGate>
  );
}

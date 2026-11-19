import { Link, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import { logManagementReturnPath } from '@shared/components/log/managementNavigation';
import { UserMfaReset } from './UserMfaReset';
import { useEffect, useRef } from 'react';
import { useSearchState } from '@shared/operations/useSearchState';
import { UserManagement } from '@shared/components/UserManagement';
import { useAdminSession } from '../data';
import { UserDeletion } from './UserDeletion';

export function UsersPage() {
  const [, setSearchParams] = useSearchState();
  const session = useAdminSession();
  const { t } = useTranslation();
  const logReturnTo = logManagementReturnPath(useLocation().state, 'admin');
  const account = session.data?.admin.username;
  const scopeReady = Boolean(account) && !session.error;
  const previousAccount = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (previousAccount.current !== undefined && previousAccount.current !== account) {
      setSearchParams(
        (previous) => {
          const next = new URLSearchParams(previous);
          next.delete('user');
          next.delete('deleted');
          return next;
        },
        { replace: true },
      );
    }
    previousAccount.current = account;
  }, [account, setSearchParams]);
  return (
    <>
      {logReturnTo && <Link to={logReturnTo}>{t('common.operations.logs.returnToLogs')}</Link>}
      <UserManagement
        role="admin"
        renderSecurity={(user) =>
          user.is_admin ? null : <UserMfaReset key={user.id} user={user} />
        }
        renderDeletion={(user, refresh) => <UserDeletion user={user} refresh={refresh} />}
        key={account ?? 'anonymous'}
        account={account ?? ''}
        scopeReady={scopeReady}
        sessionError={session.error}
      />
    </>
  );
}

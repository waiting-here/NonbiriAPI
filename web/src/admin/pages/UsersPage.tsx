import { useEffect, useRef } from 'react';
import { useSearchState } from '@shared/operations/useSearchState';
import { UserManagement } from '@shared/components/UserManagement';
import { useAdminSession } from '../data';
import { UserDeletion } from './UserDeletion';

export function UsersPage() {
  const [, setSearchParams] = useSearchState();
  const session = useAdminSession();
  const account = session.data?.admin.username;
  const scopeReady = Boolean(account) && !session.error;
  const previousAccount = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (previousAccount.current !== undefined && previousAccount.current !== account) {
      setSearchParams(
        (previous) => {
          const next = new URLSearchParams(previous);
          next.delete('user');
          return next;
        },
        { replace: true },
      );
    }
    previousAccount.current = account;
  }, [account, setSearchParams]);
  return (
    <UserManagement
      role="admin"
      renderDeletion={(user, refresh) => <UserDeletion user={user} refresh={refresh} />}
      key={account ?? 'anonymous'}
      account={account ?? ''}
      scopeReady={scopeReady}
      sessionError={session.error}
    />
  );
}

import { useEffect, useRef } from 'react';
import { EndpointManagement } from '@shared/components/EndpointManagement';
import { useSearchState } from '@shared/operations/useSearchState';
import { useAdminSession } from '../data';

export function EndpointsPage() {
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
          next.delete('expanded_base_url');
          next.delete('endpoint_user_page');
          return next;
        },
        { replace: true },
      );
    }
    previousAccount.current = account;
  }, [account, setSearchParams]);
  return (
    <EndpointManagement
      role="admin"
      key={account ?? 'anonymous'}
      account={account ?? ''}
      scopeReady={scopeReady}
      sessionError={session.error}
    />
  );
}

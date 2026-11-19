import { AccountMfaPanel } from '../features/mfa/AccountMfaPanel';
import { AccountWorkspace } from '../features/core/AccountWorkspace';
import { productionAccountLifecycleAdapter } from '../features/core/adapters';
import { CoreUserGate } from '../features/core/components';
import '../features/core/core.css';
import { InactivityStatus } from '@shared/inactivity/InactivityStatus';

export function AccountPage() {
  return (
    <CoreUserGate>
      {(user) => (
        <AccountWorkspace
          key={user.id}
          user={user}
          security={<AccountMfaPanel key={`${user.id}-mfa`} />}
          lifecycleAdapter={productionAccountLifecycleAdapter}
          activity={<InactivityStatus key={`${user.id}-activity`} accountId={user.id} />}
        />
      )}
    </CoreUserGate>
  );
}

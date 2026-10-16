import { AccountMfaPanel } from '../features/mfa/AccountMfaPanel';
import { AccountWorkspace } from '../features/core/AccountWorkspace';
import { productionAccountLifecycleAdapter } from '../features/core/adapters';
import { CoreUserGate } from '../features/core/components';
import '../features/core/core.css';
import { AutomaticRestrictions } from '@shared/components/AutomaticRestrictions';
import { InactivityStatus } from '@shared/inactivity/InactivityStatus';

export function AccountPage() {
  return (
    <CoreUserGate>
      {(user) => (
        <>
          <AutomaticRestrictions
            key={user.id + '-restrictions'}
            restrictions={user.automatic_restrictions}
          />
          <AccountWorkspace
            key={user.id}
            user={user}
            security={<AccountMfaPanel key={`${user.id}-mfa`} />}
            lifecycleAdapter={productionAccountLifecycleAdapter}
            activity={<InactivityStatus key={`${user.id}-activity`} accountId={user.id} />}
          />
        </>
      )}
    </CoreUserGate>
  );
}

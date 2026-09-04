import { AccountWorkspace } from '../features/core/AccountWorkspace';
import { productionAccountLifecycleAdapter } from '../features/core/adapters';
import { CoreUserGate } from '../features/core/components';
import '../features/core/core.css';

export function AccountPage() {
  return (
    <CoreUserGate>
      {(user) => (
        <AccountWorkspace
          key={user.id}
          user={user}
          lifecycleAdapter={productionAccountLifecycleAdapter}
        />
      )}
    </CoreUserGate>
  );
}

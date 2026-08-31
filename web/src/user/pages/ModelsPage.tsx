import { CoreUserGate } from '../features/core/components';
import { ModelsWorkspace } from '../features/core/ModelsWorkspace';
import '../features/core/core.css';

export function ModelsPage() {
  return <CoreUserGate>{(user) => <ModelsWorkspace key={user.id} user={user} />}</CoreUserGate>;
}

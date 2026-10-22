import { useParams } from 'react-router';
import { CoreUserGate } from '../features/core/components';
import { EndpointDetail } from '../features/core/EndpointDetail';
import { ConnectionsWorkspace } from '../features/core/connections/ConnectionsWorkspace';
import '../features/core/core.css';

export function EndpointsPage() {
  const { endpointId } = useParams<{ endpointId?: string }>();
  return (
    <CoreUserGate>
      {(user) =>
        endpointId ? (
          <EndpointDetail
            key={user.id + ':' + endpointId}
            accountId={user.id}
            endpointId={endpointId}
          />
        ) : (
          <ConnectionsWorkspace key={user.id} user={user} />
        )
      }
    </CoreUserGate>
  );
}

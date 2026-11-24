import { notifyManager, useQueryClient } from '@tanstack/react-query';
import { useCallback, useSyncExternalStore } from 'react';

const keys = { admin: ['admin', 'session'], user: ['user', 'session'] } as const;

/** Observe the station's authoritative cache entry without changing how it is fetched. */
export function useStationSessionSnapshot<T = unknown>(
  station: keyof typeof keys,
  batchNotifications = false,
) {
  const client = useQueryClient();
  const subscribe = useCallback(
    (notify: () => void) => {
      const onChange = batchNotifications ? notifyManager.batchCalls(notify) : notify;
      return client.getQueryCache().subscribe(({ query }) => {
        const key = query.queryKey;
        if (key.length === 2 && key[0] === station && key[1] === 'session') onChange();
      });
    },
    [batchNotifications, client, station],
  );
  const snapshot = useCallback(() => client.getQueryState<T>(keys[station]), [client, station]);
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}

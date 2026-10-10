import type { QueryClient } from '@tanstack/react-query';
import {
  captureStationSession,
  stationSessionMatches,
  StationSessionChangedError,
} from '@shared/charityManagement';
import { apiFetch, ApiError } from '@shared/query/http';
import { idempotentOptions, queryPath } from '@shared/operations/api';
import type {
  Action,
  Configuration,
  AdminInput,
  GachaPort,
  HistoryPage,
  Outcome,
  Snapshot,
} from './types';

export class GachaRejection extends Error {
  constructor(readonly code: string) {
    super(code);
  }
}
export function createGachaPort(client: QueryClient, account: string): GachaPort {
  async function bound<T>(request: () => Promise<T>): Promise<T> {
    const owner = client.getQueryData<{ user: { id: string } }>(['user', 'session']);
    if (owner?.user.id !== account) throw new StationSessionChangedError();
    const session = captureStationSession(client, 'steward');
    if (!stationSessionMatches(client, 'steward', session)) throw new StationSessionChangedError();
    const result = await request();
    if (!stationSessionMatches(client, 'steward', session)) throw new StationSessionChangedError();
    return result;
  }
  return {
    scope: 'user:' + account,
    load: () => bound(() => apiFetch<Snapshot>('/api/gacha/state')),
    history: (cursor) =>
      bound(() => apiFetch<HistoryPage>(queryPath('/api/gacha/history', { cursor }))),
    act: async (input: Action, key: string) => {
      const confirmed = await bound(async () => {
        const result = await apiFetch<Outcome | { rejection: string }>(
          '/api/gacha/actions',
          idempotentOptions(key, { method: 'POST', json: input }),
        );
        if ('rejection' in result) throw new GachaRejection(result.rejection);
        if (!result.snapshot)
          throw new ApiError('invalid_response', 'The activity result was incomplete.', 0);
        return result;
      });
      void client.invalidateQueries({ queryKey: ['user', 'session'] });
      void client.invalidateQueries({ queryKey: ['user', 'credits'] });
      void client.invalidateQueries({ queryKey: ['user', 'gacha-entry'] });
      return confirmed;
    },
  };
}
export function getGachaConfiguration(signal?: AbortSignal) {
  return apiFetch<Configuration>('/admin/api/gacha', { signal });
}
export function saveGachaConfiguration(input: AdminInput, key: string, signal?: AbortSignal) {
  return apiFetch<Configuration>(
    '/admin/api/gacha',
    idempotentOptions(key, { method: 'POST', json: input, signal }),
  );
}

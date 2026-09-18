import { useCallback, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  blackjackDetail,
  blackjackHistory,
  blackjackState,
  type BlackjackAction,
  type BlackjackEmote,
} from '@shared/games/blackjack';
import {
  integer,
  oneOf,
  opaqueID,
  record,
  decimal,
  unixSecond,
  invalidResponse,
} from '@shared/operations/wire';
import { createIdempotencyKey, gameRequest, isResponseUnknown } from '../common/request';
import { useGameVisibility } from '../common/visibility';
import { gameKeys } from '../common/snapshot';

export type BlackjackIntent =
  | { kind: 'queue'; stake: string; config_hash: string }
  | { kind: 'leave'; id: string }
  | { kind: 'action'; id: string; hand: number; revision: string; action: BlackjackAction }
  | { kind: 'emote'; id: string; emote: BlackjackEmote };
const base = '/api/games/blackjack';
export const blackjackKeys = {
  root: ['user', 'games', 'blackjack'] as const,
  state: ['user', 'games', 'blackjack', 'state'] as const,
};
export async function sendBlackjack(intent: BlackjackIntent, key: string) {
  const path =
    intent.kind === 'queue'
      ? '/queue'
      : intent.kind === 'leave'
        ? `/queue/${intent.id}`
        : `/sessions/${intent.id}/${intent.kind === 'action' ? 'actions' : 'emotes'}`;
  const json =
    intent.kind === 'queue'
      ? { stake: intent.stake, config_hash: intent.config_hash }
      : intent.kind === 'action'
        ? { hand: intent.hand, revision: intent.revision, action: intent.action }
        : intent.kind === 'emote'
          ? { emote: intent.emote }
          : undefined;
  const r = await gameRequest<unknown>(base + path, {
    method: intent.kind === 'leave' ? 'DELETE' : 'POST',
    json,
    idempotencyKey: key,
    expectedStatuses:
      intent.kind === 'queue' ? [200, 201] : intent.kind === 'action' ? [202] : [204],
  });
  if (intent.kind === 'queue') {
    const p = record(r.data, ['id', 'position', 'state'], 'queue receipt');
    opaqueID(p.id, 'bjq_', 'queue id');
    decimal(p.position, 'queue position');
    oneOf(p.state, ['waiting', 'seated', 'playing'] as const, 'queue state');
  } else if (intent.kind === 'action') {
    const p = record(r.data, ['session_id', 'batch_at', 'hand', 'revision'], 'action receipt');
    if (p.session_id !== intent.id || p.revision !== intent.revision || p.hand !== intent.hand)
      invalidResponse('action receipt identity');
    unixSecond(p.batch_at, 'action time');
    integer(p.hand, 'hand', 0, 1);
  } else if (r.data !== undefined) invalidResponse('empty receipt');
}
export function useBlackjack() {
  const visible = useGameVisibility();
  const client = useQueryClient();
  const query = useQuery({
    queryKey: blackjackKeys.state,
    queryFn: async ({ signal }) =>
      blackjackState(
        (await gameRequest<unknown>(base + '/state', { signal, expectedStatuses: [200] })).data,
      ),
    retry: false,
    refetchInterval: visible ? 700 : 5000,
    refetchIntervalInBackground: true,
    refetchOnWindowFocus: 'always',
    refetchOnReconnect: 'always',
    staleTime: 0,
  });
  const { refetch } = query;
  const refresh = useCallback(() => {
    void refetch();
  }, [refetch]);
  const operation = useRef<{ intent: BlackjackIntent; key: string } | null>(null);
  const busy = useRef(false);
  const [pending, setPending] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const execute = async (intent: BlackjackIntent, retry = false) => {
    if (busy.current || (operation.current && !retry)) return;
    busy.current = true;
    setPending(true);
    setError(null);
    operation.current ??= { intent, key: createIdempotencyKey() };
    try {
      await sendBlackjack(operation.current.intent, operation.current.key);
      operation.current = null;
      setUncertain(false);
      void client.invalidateQueries({ queryKey: [...blackjackKeys.root, 'history'] });
    } catch (failure) {
      setError(failure);
      const unknown = isResponseUnknown(failure);
      setUncertain(unknown);
      if (!unknown) operation.current = null;
    } finally {
      await refetch();
      void client.invalidateQueries({ queryKey: gameKeys.snapshot });
      busy.current = false;
      setPending(false);
    }
  };
  return {
    query,
    refresh,
    pending,
    uncertain,
    error,
    blocked: pending || uncertain || query.isError || !query.data,
    run: (intent: BlackjackIntent) => {
      void execute(intent);
    },
    retry: () => {
      if (operation.current) void execute(operation.current.intent, true);
    },
  };
}
export async function readBlackjackHistory(cursor: string | null, signal: AbortSignal) {
  const search = new URLSearchParams({ limit: '20' });
  if (cursor) search.set('cursor', cursor);
  return blackjackHistory(
    (await gameRequest<unknown>(`${base}/history?${search}`, { signal })).data,
  );
}
export async function readBlackjackDetail(id: string, signal: AbortSignal) {
  opaqueID(id, 'bjt_', 'history id');
  return blackjackDetail((await gameRequest<unknown>(`${base}/history/${id}`, { signal })).data);
}

import { useCallback, useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { createIdempotencyKey, gameRequest, isResponseUnknown, secureBytes } from '../request';
import {
  booleanValue,
  decodeBase64URL,
  encodeBase64URL,
  exactRecord,
  invalidResponse,
  opaqueID,
  unixTime,
} from '../strict';
import { useGameVisibility } from '../visibility';
import {
  cursorValue,
  detailValue,
  homeValue,
  list,
  prefix,
  resultValue,
  revisionValue,
  roundValue,
} from './normalize';
import type { DuelCodec, DuelGame, Page } from './types';

export type DuelIntent =
  | {
      readonly kind: 'queue';
      readonly mode: string;
      readonly termsHash: string;
      readonly loadout?: unknown;
    }
  | { readonly kind: 'cancel'; readonly id: string; readonly revision: string }
  | {
      readonly kind: 'action';
      readonly id: string;
      readonly phaseSeq: string;
      readonly action: unknown;
    }
  | { readonly kind: 'surrender'; readonly id: string; readonly phaseSeq: string };

const deviceCache: Partial<Record<DuelGame, string>> = {};
export function duelDeviceToken(game: DuelGame): string {
  if (deviceCache[game]) return deviceCache[game];
  const key = `nonbiri.game.${game}.device.v1`;
  try {
    const saved = localStorage.getItem(key);
    if (saved && decodeBase64URL(saved, 32)) {
      deviceCache[game] = saved;
      return saved;
    }
  } catch {
    /* Storage may be unavailable; retain this page's random token. */
  }
  const token = encodeBase64URL(secureBytes(32));
  deviceCache[game] = token;
  try {
    localStorage.setItem(key, token);
  } catch {
    /* The in-memory identity remains stable. */
  }
  return token;
}
export const duelKeys = {
  root: (game: DuelGame) => ['user', 'games', game] as const,
  state: (game: DuelGame) => [...duelKeys.root(game), 'state'] as const,
};
export async function sendDuelIntent(game: DuelGame, intent: DuelIntent, key: string) {
  const base = `/api/games/${game}`;
  const path =
    intent.kind === 'queue'
      ? `${base}/queue`
      : intent.kind === 'cancel'
        ? `${base}/queue/${intent.id}`
        : `${base}/sessions/${intent.id}/${intent.kind === 'action' ? 'actions' : 'surrender'}`;
  const body =
    intent.kind === 'queue'
      ? {
          mode: intent.mode,
          expected_terms_hash: intent.termsHash,
          device_token: duelDeviceToken(game),
          ...(intent.loadout === undefined ? {} : { loadout: intent.loadout }),
        }
      : intent.kind === 'cancel'
        ? { expected_revision: intent.revision }
        : intent.kind === 'action'
          ? { phase_seq: intent.phaseSeq, action: intent.action }
          : { phase_seq: intent.phaseSeq };
  const response = await gameRequest<unknown>(path, {
    method: intent.kind === 'cancel' ? 'DELETE' : 'POST',
    json: body,
    idempotencyKey: key,
    expectedStatuses: [intent.kind === 'queue' ? 202 : intent.kind === 'cancel' ? 204 : 200],
  });
  if (intent.kind === 'cancel') {
    if (response.data !== undefined) invalidResponse('cancel receipt');
    return;
  }
  if (intent.kind === 'queue') {
    const r = exactRecord(response.data, ['queue_id', 'revision', 'deadline']);
    opaqueID(r.queue_id, prefix(game, true), 'queue receipt');
    revisionValue(r.revision);
    unixTime(r.deadline, 'queue receipt');
  } else {
    const r = exactRecord(response.data, ['session_id', 'revision', 'phase_seq', 'locked']);
    if (opaqueID(r.session_id, prefix(game), 'session receipt') !== intent.id)
      invalidResponse('receipt identity');
    revisionValue(r.revision);
    revisionValue(r.phase_seq);
    booleanValue(r.locked, 'receipt lock');
  }
}

export function useDuel<V, F, P, S, L, A>(
  codec: DuelCodec<V, F, P, S, L, A>,
  refreshWallets?: () => void | Promise<unknown>,
) {
  const visible = useGameVisibility();
  const client = useQueryClient();
  const query = useQuery({
    queryKey: duelKeys.state(codec.game),
    queryFn: async ({ signal }) =>
      homeValue(
        (
          await gameRequest<unknown>(`/api/games/${codec.game}/state`, {
            signal,
            expectedStatuses: [200],
          })
        ).data,
        codec,
      ),
    retry: false,
    refetchInterval: visible ? 1000 : 5000,
    refetchIntervalInBackground: true,
    refetchOnWindowFocus: 'always',
    refetchOnReconnect: 'always',
    staleTime: 0,
  });
  const { refetch } = query;
  const refresh = useCallback(() => {
    void refetch();
    void refreshWallets?.();
  }, [refetch, refreshWallets]);
  useEffect(() => {
    if (visible) refresh();
  }, [visible, refresh]);
  const inFlight = useRef(false);
  const retained = useRef<{ intent: DuelIntent; key: string } | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [uncertain, setUncertain] = useState(false);
  const [intentKind, setIntentKind] = useState<DuelIntent['kind'] | null>(null);
  const execute = async (intent: DuelIntent, retry = false) => {
    if (inFlight.current || (retained.current && !retry)) return;
    const operation = retained.current ?? { intent, key: createIdempotencyKey() };
    retained.current = operation;
    inFlight.current = true;
    setPending(true);
    setError(null);
    setIntentKind(operation.intent.kind);
    try {
      await sendDuelIntent(codec.game, operation.intent, operation.key);
      retained.current = null;
      setUncertain(false);
      void client.invalidateQueries({ queryKey: [...duelKeys.root(codec.game), 'history'] });
    } catch (failure) {
      setError(failure);
      const unknown = isResponseUnknown(failure);
      setUncertain(unknown);
      if (!unknown) retained.current = null;
    } finally {
      await Promise.allSettled([query.refetch(), refreshWallets?.()]);
      inFlight.current = false;
      setPending(false);
    }
  };
  return {
    query,
    refresh,
    pending,
    error,
    uncertain,
    intentKind,
    blocked: pending || uncertain || query.isError || !query.data,
    run: (intent: DuelIntent) => {
      void execute(intent);
    },
    retry: () => {
      if (retained.current) void execute(retained.current.intent, true);
    },
  };
}

export async function readHistory<V, F, P, S, L, A>(
  codec: DuelCodec<V, F, P, S, L, A>,
  cursor: string | null,
  signal: AbortSignal,
) {
  const search = new URLSearchParams({ limit: '20' });
  if (cursor) search.set('cursor', cursor);
  const r = exactRecord(
    (
      await gameRequest<unknown>(`/api/games/${codec.game}/history?${search}`, {
        signal,
        expectedStatuses: [200],
      })
    ).data,
    ['items', 'next_cursor'],
  );
  return {
    items: list(r.items, 20, (v) => resultValue(v, codec)),
    nextCursor: cursorValue(r.next_cursor),
  };
}
export async function readDetail<V, F, P, S, L, A>(
  codec: DuelCodec<V, F, P, S, L, A>,
  id: string,
  signal: AbortSignal,
) {
  opaqueID(id, prefix(codec.game), 'history id');
  return detailValue(
    (
      await gameRequest<unknown>(`/api/games/${codec.game}/history/${id}`, {
        signal,
        expectedStatuses: [200],
      })
    ).data,
    codec,
  );
}
export async function readRounds<V, F, P, S, L, A>(
  codec: DuelCodec<V, F, P, S, L, A>,
  id: string,
  active: boolean,
  cursor: string | null,
  signal: AbortSignal,
): Promise<Page<ReturnType<typeof roundValue<V, F, P, S, L, A>>>> {
  opaqueID(id, prefix(codec.game), 'round session');
  const search = new URLSearchParams({ limit: '5' });
  if (cursor) search.set('cursor', cursor);
  const r = exactRecord(
    (
      await gameRequest<unknown>(
        `/api/games/${codec.game}/${active ? 'sessions' : 'history'}/${id}/rounds?${search}`,
        { signal, expectedStatuses: [200], maxResponseBytes: 8 * 1024 * 1024 },
      )
    ).data,
    ['items', 'next_cursor', 'server_now'],
  );
  unixTime(r.server_now, 'round server time');
  return {
    items: list(r.items, 5, (v) => roundValue(v, codec)),
    nextCursor: cursorValue(r.next_cursor),
  };
}

import { useQuery } from '@tanstack/react-query';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { companionPath, type CompanionGame } from '@shared/companion';
import { gameRequest } from '../common/request';
import type { Pair, Seat } from '../common/duel/types';

export interface Preferences {
  memory: 'none' | 'opponent' | 'shared';
  contribute: boolean;
}
export interface MatchSummary {
  id: string;
  game: CompanionGame;
  mode: 'standard' | 'quick';
  program: string;
  version: number;
  difficulty: number;
  status: 'active' | 'completed' | 'abandoned' | 'cancelled' | 'technical';
  reason: string;
  seat: Seat;
  winner: number | null;
  scores: Pair<number>;
  created_at: number;
  ended_at: number;
}
export interface Match extends MatchSummary {
  phase: string;
  round: number;
  ai_state: 'queued' | 'thinking' | 'locked' | 'idle';
  decision_id: string;
  view: Record<string, unknown>;
  space: { kind: string; examples: unknown[] };
}
export interface CompanionHome {
  preferences: Preferences;
  active: Match | null;
  latest?: Match | null;
}
export type StartOptions = {
  mode: Match['mode'];
  program: string;
  difficulty: number;
  constraints: Record<string, unknown>;
  setup?: unknown;
};
export function useCompanionRead<T>(
  game: CompanionGame,
  path: string,
  options: { poll?: (data: T | undefined) => boolean; enabled?: boolean; admin?: boolean } = {},
) {
  const base = companionPath(game, options.admin);
  return useQuery({
    queryKey: [options.admin ? 'admin' : 'user', 'games', game, 'companion', path],
    queryFn: async ({ signal }) =>
      (await gameRequest<T>(base + path, { signal, maxResponseBytes: 2 * 1024 * 1024 })).data,
    refetchInterval: (query) => (options.poll?.(query.state.data) ? 1300 : false),
    enabled: options.enabled,
    retry: false,
  });
}
export function useCompanionWrite<T>(
  game: CompanionGame,
  reconcile: () => Promise<unknown>,
  admin = false,
) {
  return useRetainedOperation<{ path: string; body: unknown; method?: 'PUT' | 'POST' }, T>(
    async ({ path, body, method }, key, context) =>
      (
        await gameRequest<T>(companionPath(game, admin) + path, {
          method: method ?? 'POST',
          json: body,
          idempotencyKey: key,
          signal: context.signal,
          maxResponseBytes: 2 * 1024 * 1024,
        })
      ).data,
    reconcile,
    [admin ? 'admin' : 'user', 'games', game, 'companion'],
  );
}

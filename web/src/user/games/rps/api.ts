import { useQuery } from '@tanstack/react-query';
import { ApiError } from '@shared/query/http';
import { gameRequest } from '../common/request';
import { exactRecord, unixTime } from '../common/strict';
import type { RPSMode } from '../common/types';
import {
  normalizeRPSHome,
  normalizeRPSLeaderboard,
  normalizeRPSQueue,
  shouldApplyRPSReplacement,
} from './normalize';
import type { RPSActionIntent, RPSHomeState, RPSLeaderboard, RPSQueue } from './types';

export const rpsKeys = {
  state: ['user', 'games', 'rps', 'state', 'beta1'] as const,
  leaderboard: (mode: RPSMode, board: 'profit_rate' | 'net_profit') =>
    ['user', 'games', 'rps', 'leaderboard', mode, board, 'beta1'] as const,
};

export async function readRPSState(signal?: AbortSignal): Promise<RPSHomeState> {
  return normalizeRPSHome(
    (await gameRequest<unknown>('/api/games/rps/state', { signal, expectedStatuses: [200] })).data,
  );
}
export async function enqueueRPS(
  mode: RPSMode,
  deviceToken: string,
  deathmatchConfirmed: boolean,
  idempotencyKey: string,
): Promise<RPSQueue> {
  const response = await gameRequest<unknown>('/api/games/rps/queue', {
    method: 'POST',
    idempotencyKey,
    expectedStatuses: [202],
    json: { mode, device_token: deviceToken, deathmatch_confirmed: deathmatchConfirmed },
  });
  const queue = normalizeRPSQueue(response.data);
  if (queue.mode !== mode)
    throw new ApiError(
      'invalid_response',
      'The server queued a different RPS mode.',
      response.status,
    );
  return queue;
}
export async function cancelRPSQueue(
  queueID: string,
  revision: string,
  idempotencyKey: string,
): Promise<void> {
  const response = await gameRequest<undefined>(
    `/api/games/rps/queue/${encodeURIComponent(queueID)}`,
    {
      method: 'DELETE',
      idempotencyKey,
      expectedStatuses: [204],
      json: { expected_revision: revision },
    },
  );
  if (response.data !== undefined)
    throw new ApiError(
      'invalid_response',
      'RPS queue cancellation returned a body.',
      response.status,
    );
}
export async function sendRPSAction(intent: RPSActionIntent): Promise<RPSHomeState> {
  const result = normalizeRPSHome(
    (
      await gameRequest<unknown>(
        `/api/games/rps/sessions/${encodeURIComponent(intent.sessionID)}/actions`,
        {
          method: 'POST',
          idempotencyKey: intent.idempotencyKey,
          expectedStatuses: [200],
          json: {
            phase_seq: intent.phaseSeq,
            expected_revision: intent.expectedRevision,
            action: intent.value.action,
            payload: intent.value.payload,
          },
        },
      )
    ).data,
  );
  if (
    (result.kind === 'session' && result.session.sessionID !== intent.sessionID) ||
    (result.kind === 'pending_result' && result.result.sessionID !== intent.sessionID)
  )
    throw new ApiError('invalid_response', 'The server returned a different RPS session.', 200);
  return result;
}
export async function renewRPSLease(sessionID: string, leaseID: string): Promise<number> {
  const value = (
    await gameRequest<unknown>(`/api/games/rps/sessions/${encodeURIComponent(sessionID)}/lease`, {
      method: 'POST',
      json: { lease_id: leaseID },
      expectedStatuses: [200],
    })
  ).data;
  const record = exactRecord(value, ['expires_at'], [], 'RPS lease');
  return unixTime(record.expires_at, 'RPS lease expiry');
}
export async function acknowledgeRPSResult(sessionID: string): Promise<void> {
  const response = await gameRequest<undefined>('/api/games/rps/pending-result/ack', {
    method: 'POST',
    json: { session_id: sessionID },
    expectedStatuses: [204],
  });
  if (response.data !== undefined)
    throw new ApiError('invalid_response', 'RPS result ACK returned a body.', response.status);
}
export async function markRPSTutorialSeen(): Promise<void> {
  const response = await gameRequest<undefined>('/api/games/rps/tutorial/seen', {
    method: 'POST',
    expectedStatuses: [204],
  });
  if (response.data !== undefined)
    throw new ApiError('invalid_response', 'RPS tutorial marker returned a body.', response.status);
}
export async function readRPSLeaderboard(
  mode: RPSMode,
  board: 'profit_rate' | 'net_profit',
  signal?: AbortSignal,
): Promise<RPSLeaderboard> {
  return normalizeRPSLeaderboard(
    (
      await gameRequest<unknown>(`/api/games/rps/leaderboard?mode=${mode}&board=${board}`, {
        signal,
        expectedStatuses: [200],
      })
    ).data,
    mode,
    board,
  );
}
export function useRPSState(enabled: boolean) {
  return useQuery<RPSHomeState>({
    queryKey: rpsKeys.state,
    queryFn: ({ signal }) => readRPSState(signal),
    enabled,
    staleTime: 0,
    retry: false,
    structuralSharing: (current, next) => {
      const cached = current as RPSHomeState | undefined;
      const replacement = next as RPSHomeState;
      return shouldApplyRPSReplacement(cached, replacement) ? replacement : cached;
    },
  });
}
export function useRPSLeaderboard(
  mode: RPSMode,
  board: 'profit_rate' | 'net_profit',
  enabled: boolean,
) {
  return useQuery({
    queryKey: rpsKeys.leaderboard(mode, board),
    queryFn: ({ signal }) => readRPSLeaderboard(mode, board, signal),
    enabled,
    staleTime: 30_000,
    retry: false,
  });
}

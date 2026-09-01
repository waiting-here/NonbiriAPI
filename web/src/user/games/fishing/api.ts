import { useQuery } from '@tanstack/react-query';
import { ApiError } from '@shared/query/http';
import { gameRequest } from '../common/request';
import {
  normalizeFishingLeaderboard,
  normalizeFishingStart,
  normalizeFishingState,
} from './normalize';
import type {
  FishingBatchResult,
  FishingLeaderboard,
  FishingStartIntent,
  FishingStartResult,
  FishingState,
} from './types';

export const fishingKeys = {
  state: ['user', 'games', 'fishing', 'state', 'beta1'] as const,
  leaderboard: (board: 'single' | 'total') =>
    ['user', 'games', 'fishing', 'leaderboard', board, 'beta1'] as const,
};

export async function readFishingState(signal?: AbortSignal): Promise<FishingState> {
  return normalizeFishingState(
    (
      await gameRequest<unknown>('/api/games/fishing/state', {
        signal,
        expectedStatuses: [200],
      })
    ).data,
  );
}

export async function startFishing(
  intent: FishingStartIntent,
  signal?: AbortSignal,
): Promise<FishingStartResult> {
  const response = await gameRequest<unknown>('/api/games/fishing/batches', {
    method: 'POST',
    json: { bait: intent.bait, count: intent.count },
    idempotencyKey: intent.idempotencyKey,
    signal,
    expectedStatuses: [200, 202],
  });
  const result = normalizeFishingStart(response.data);
  if (result.bait !== intent.bait || result.count !== intent.count) {
    throw new ApiError(
      'invalid_response',
      'The server returned a different fishing intent.',
      response.status,
    );
  }
  if (
    (response.status === 200 && !('outcomes' in result)) ||
    (response.status === 202 && 'outcomes' in result)
  ) {
    throw new ApiError(
      'invalid_response',
      'The fishing response status did not match its state.',
      response.status,
    );
  }
  return result;
}

export async function recoverFishing(
  batchID: string,
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<FishingStartResult> {
  const response = await gameRequest<unknown>(
    `/api/games/fishing/batches/${encodeURIComponent(batchID)}/recover`,
    {
      method: 'POST',
      idempotencyKey,
      signal,
      expectedStatuses: [200, 202],
    },
  );
  const result = normalizeFishingStart(response.data);
  if (result.batchID !== batchID) {
    throw new ApiError(
      'invalid_response',
      'The server returned a different fishing batch.',
      response.status,
    );
  }
  if (
    (response.status === 200 && !('outcomes' in result)) ||
    (response.status === 202 && 'outcomes' in result)
  ) {
    throw new ApiError(
      'invalid_response',
      'The fishing recovery status did not match its state.',
      response.status,
    );
  }
  return result;
}

export async function acknowledgeFishing(batchID: string, signal?: AbortSignal): Promise<void> {
  const response = await gameRequest<undefined>(
    `/api/games/fishing/batches/${encodeURIComponent(batchID)}/ack`,
    {
      method: 'POST',
      signal,
      expectedStatuses: [204],
    },
  );
  if (response.data !== undefined)
    throw new ApiError('invalid_response', 'Fishing ACK returned a body.', response.status);
}

export async function readFishingLeaderboard(
  board: 'single' | 'total',
  signal?: AbortSignal,
): Promise<FishingLeaderboard> {
  return normalizeFishingLeaderboard(
    (
      await gameRequest<unknown>(`/api/games/fishing/leaderboard?board=${board}`, {
        signal,
        expectedStatuses: [200],
      })
    ).data,
    board,
  );
}

export function useFishingState(enabled: boolean) {
  return useQuery({
    queryKey: fishingKeys.state,
    queryFn: ({ signal }) => readFishingState(signal),
    enabled,
    staleTime: 0,
    refetchInterval: (query) =>
      query.state.data?.settlementPending?.state === 'settlement_pending' ? 1_500 : false,
    retry: false,
  });
}

export function useFishingLeaderboard(board: 'single' | 'total', enabled: boolean) {
  return useQuery({
    queryKey: fishingKeys.leaderboard(board),
    queryFn: ({ signal }) => readFishingLeaderboard(board, signal),
    enabled,
    staleTime: 30_000,
    retry: false,
  });
}

export function isFishingResult(value: FishingStartResult): value is FishingBatchResult {
  return 'outcomes' in value;
}

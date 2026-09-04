import { useQuery } from '@tanstack/react-query';
import { ApiError } from '@shared/query/http';
import { gameRequest } from '../common/request';
import {
  normalizeLinkLinkCurrent,
  normalizeLinkLinkLease,
  normalizeLinkLinkState,
  normalizeLinkLinkSummary,
  shouldApplyLinkLinkReplacement,
} from './normalize';
import type { LinkLinkCurrent, LinkLinkMatchIntent, LinkLinkState, LinkLinkSummary } from './types';
import type { LinkLinkSpec } from '../common/types';

export const linkLinkKeys = { current: ['user', 'games', 'linklink', 'current', 'beta1'] as const };

export async function readLinkLinkCurrent(signal?: AbortSignal): Promise<LinkLinkCurrent> {
  return normalizeLinkLinkCurrent(
    (await gameRequest<unknown>('/api/games/linklink/session', { signal, expectedStatuses: [200] }))
      .data,
  );
}
export async function startLinkLink(
  spec: LinkLinkSpec,
  idempotencyKey: string,
): Promise<LinkLinkState> {
  const response = await gameRequest<unknown>('/api/games/linklink/sessions', {
    method: 'POST',
    json: { spec },
    idempotencyKey,
    expectedStatuses: [200, 201],
  });
  const state = normalizeLinkLinkState(response.data);
  if (response.status === 201 && state.spec !== spec)
    throw new ApiError(
      'invalid_response',
      'The server started a different LinkLink board.',
      response.status,
    );
  return state;
}
export async function matchLinkLink(
  intent: LinkLinkMatchIntent,
): Promise<LinkLinkState | LinkLinkSummary> {
  const response = await gameRequest<unknown>(
    `/api/games/linklink/sessions/${encodeURIComponent(intent.sessionID)}/matches`,
    {
      method: 'POST',
      idempotencyKey: intent.idempotencyKey,
      expectedStatuses: [200],
      json: {
        expected_revision: intent.expectedRevision,
        first: intent.first,
        second: intent.second,
      },
    },
  );
  const root = response.data as Record<string, unknown> | null;
  const result =
    root && Object.prototype.hasOwnProperty.call(root, 'terminal_reason')
      ? normalizeLinkLinkSummary(response.data)
      : normalizeLinkLinkState(response.data);
  if (result.sessionID !== intent.sessionID)
    throw new ApiError(
      'invalid_response',
      'The server returned a different LinkLink session.',
      response.status,
    );
  return result;
}
export async function abandonLinkLink(
  sessionID: string,
  revision: string,
  idempotencyKey: string,
): Promise<LinkLinkSummary> {
  const result = normalizeLinkLinkSummary(
    (
      await gameRequest<unknown>(
        `/api/games/linklink/sessions/${encodeURIComponent(sessionID)}/abandon`,
        {
          method: 'POST',
          idempotencyKey,
          expectedStatuses: [200],
          json: { expected_revision: revision, confirmation: true },
        },
      )
    ).data,
  );
  if (result.sessionID !== sessionID || result.terminalReason !== 'abandoned')
    throw new ApiError(
      'invalid_response',
      'The server returned a different LinkLink terminal state.',
      200,
    );
  return result;
}
export async function renewLinkLinkLease(sessionID: string, leaseID: string): Promise<number> {
  return normalizeLinkLinkLease(
    (
      await gameRequest<unknown>(
        `/api/games/linklink/sessions/${encodeURIComponent(sessionID)}/lease`,
        {
          method: 'POST',
          json: { lease_id: leaseID },
          expectedStatuses: [200],
        },
      )
    ).data,
  );
}
export function useLinkLinkCurrent(enabled: boolean) {
  return useQuery<LinkLinkCurrent>({
    queryKey: linkLinkKeys.current,
    queryFn: ({ signal }) => readLinkLinkCurrent(signal),
    enabled,
    staleTime: 0,
    retry: false,
    structuralSharing: (current, next) => {
      const cached = current as LinkLinkCurrent | undefined;
      const replacement = next as LinkLinkCurrent;
      return shouldApplyLinkLinkReplacement(cached, replacement) ? replacement : cached;
    },
  });
}

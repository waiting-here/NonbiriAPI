import { useEffect, useEffectEvent, useRef } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { captureStationSession, stationSessionMatches } from '@shared/charityManagement';
import {
  consumeElevationReturn,
  clearElevatedCapabilityCookie,
  clearPendingElevation,
  type ElevationIntent,
} from './elevation';
import type { OperationContext } from './useRetainedOperation';

/** Resume export automatically; deletion still receives its single consequence confirmation. */
export function useElevationReturn(
  accountId: string,
  resume: (
    intent: ElevationIntent,
    token: string,
    context: OperationContext,
  ) => Promise<void> | void,
  onError?: (error: unknown) => void,
) {
  const client = useQueryClient();
  const resumeCurrent = useEffectEvent(resume);
  const reportError = useEffectEvent((error: unknown) => onError?.(error));
  const consumed = useRef(false);
  useEffect(() => {
    if (consumed.current) return;
    const controller = new AbortController();
    const snapshot =
      client.getQueryData<{ user?: { id?: string } }>(['user', 'session'])?.user?.id === accountId
        ? captureStationSession(client, 'steward')
        : undefined;
    const isCurrent = () => {
      const session = client.getQueryData<{ user?: { id?: string } }>(['user', 'session']);
      return (
        !controller.signal.aborted &&
        session?.user?.id === accountId &&
        Boolean(snapshot && stationSessionMatches(client, 'steward', snapshot))
      );
    };
    const assertCurrent = () => {
      if (!isCurrent()) throw new Error('The account session changed.');
    };
    const context: OperationContext = {
      signal: controller.signal,
      isCurrent,
      assertCurrent,
      commit: (effect) => {
        assertCurrent();
        return effect();
      },
    };
    const unsubscribe = client.getQueryCache().subscribe(() => {
      if (!isCurrent()) {
        controller.abort();
        clearPendingElevation();
        clearElevatedCapabilityCookie();
      }
    });
    void Promise.resolve()
      .then(() => {
        // Consume after mount rehearsal, so StrictMode cannot discard a valid return.
        if (controller.signal.aborted) return;
        consumed.current = true;
        const returned = consumeElevationReturn(accountId);
        if (!returned) return;
        assertCurrent();
        return resumeCurrent(returned.intent, returned.token, context);
      })
      .catch((error: unknown) => {
        if (isCurrent()) reportError(error);
      });
    return () => {
      controller.abort();
      unsubscribe();
      if (consumed.current) {
        clearPendingElevation();
        clearElevatedCapabilityCookie();
      }
    };
  }, [accountId, client]);
}

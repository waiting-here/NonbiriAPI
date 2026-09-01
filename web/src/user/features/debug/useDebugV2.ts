import { useCallback, useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { clearStationSession } from '@shared/charityManagement';
import { ApiError, isForbidden, isUnauthorized } from '@shared/query/http';
import { conflictOrUnknown, operationKey } from '@shared/operations/api';
import { applyDebugEvent, initialDebugState, type DebugViewState } from './v2state';
import { changeDebugMode, getDebugSession, replaceDebugSession, startDebugSession, stopDebugSession } from './v2api';
import { openDebugStream, type DebugObserverStatus, type DebugStream } from './v2stream';
import type { DebugMode, DebugSessionMetadata } from './v2types';

export const debugV2Keys = { session: ['user', 'operations', 'debug', 'session'] as const };

type MutationIntent =
  | { kind: 'start' }
  | { kind: 'mode'; mode: DebugMode; revision: string }
  | { kind: 'stop'; revision: string; confirm: boolean }
  | { kind: 'replace'; revision: string; confirm: boolean };

function signature(intent: MutationIntent): string {
  return JSON.stringify(intent);
}

export function useDebugV2() {
  const client = useQueryClient();
  const authority = useQuery({ queryKey: debugV2Keys.session, queryFn: getDebugSession, retry: false });
  const viewRef = useRef<DebugViewState>(initialDebugState());
  const [view, setView] = useState<DebugViewState>(() => initialDebugState());
  const [observer, setObserver] = useState<DebugObserverStatus>('disconnected');
  const [streamError, setStreamError] = useState<unknown>(null);
  const [mutationError, setMutationError] = useState<unknown>(null);
  const [mutating, setMutating] = useState(false);
  const appliedAuthorityRef = useRef('');
  const streamRef = useRef<DebugStream | null>(null);
  const streamGenerationRef = useRef(0);
  const intentRef = useRef<{ signature: string; key: string } | null>(null);
  const mountedRef = useRef(true);

  const disconnect = useCallback(() => {
    streamGenerationRef.current += 1;
    streamRef.current?.close();
    streamRef.current = null;
    setObserver('disconnected');
  }, []);

  const commitView = useCallback((next: DebugViewState) => {
    viewRef.current = next;
    setView(next);
  }, []);

  const clearSensitive = useCallback(() => {
    disconnect();
    commitView(initialDebugState());
    setStreamError(null);
    setMutationError(null);
    intentRef.current = null;
  }, [commitView, disconnect]);

  const connect = useCallback(() => {
    if (streamRef.current || !view.session.active) return;
    setStreamError(null);
    const generation = streamGenerationRef.current + 1;
    streamGenerationRef.current = generation;
    let terminal = false;
    const stream = openDebugStream(
      (event) => {
        if (!mountedRef.current || generation !== streamGenerationRef.current) return;
        try {
          commitView(applyDebugEvent(viewRef.current, event));
          if (event.kind === 'session_end') {
            streamRef.current?.close();
            streamRef.current = null;
            streamGenerationRef.current += 1;
            void client.invalidateQueries({ queryKey: debugV2Keys.session, exact: true });
          }
        } catch (error) {
          setStreamError(error);
          disconnect();
        }
      },
      (status) => {
        if (mountedRef.current && generation === streamGenerationRef.current) setObserver(status);
      },
      (error) => {
        if (!mountedRef.current || generation !== streamGenerationRef.current) return;
        terminal = true;
        streamRef.current?.close();
        streamRef.current = null;
        setObserver('disconnected');
        setStreamError(error);
      },
    );
    if (terminal || generation !== streamGenerationRef.current) stream.close();
    else streamRef.current = stream;
  }, [client, commitView, disconnect, view.session.active]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      streamGenerationRef.current += 1;
      streamRef.current?.close();
      streamRef.current = null;
    };
  }, []);

  useEffect(() => {
    if (!authority.data) return;
    const nextAuthority = authority.data;
    const authoritySignature = JSON.stringify(nextAuthority);
    if (authoritySignature === appliedAuthorityRef.current) return;
    let cancelled = false;
    queueMicrotask(() => {
      if (cancelled || authoritySignature === appliedAuthorityRef.current) return;
      appliedAuthorityRef.current = authoritySignature;
      const current = viewRef.current;
      if (!nextAuthority.active) {
        disconnect();
        commitView(initialDebugState());
      } else if (current.session.active && current.session.id === nextAuthority.id
        && current.session.generation === nextAuthority.generation) {
        commitView({ ...current, session: nextAuthority });
      } else {
        disconnect();
        commitView(initialDebugState(nextAuthority));
      }
    });
    return () => {
      cancelled = true;
    };
  }, [authority.data, commitView, disconnect]);

  useEffect(() => {
    if (authority.data?.active && observer === 'disconnected' && !streamError && !streamRef.current) connect();
  }, [authority.data, connect, observer, streamError]);

  /* eslint-disable react-hooks/set-state-in-effect -- Final authentication loss must erase captured request bodies immediately. */
  useEffect(() => {
    // Authentication loss must immediately close the live observer and erase captured request bodies.
    if (isUnauthorized(authority.error) || isForbidden(authority.error)) {
      clearSensitive();
      clearStationSession(client, 'steward');
    }
  }, [authority.error, clearSensitive, client]);
  /* eslint-enable react-hooks/set-state-in-effect */

  const reconcile = useCallback(async () => {
    try {
      const result = await authority.refetch();
      return result.data;
    } catch {
      return undefined;
    }
  }, [authority]);

  const run = useCallback(async (intent: MutationIntent) => {
    if (mutating) return;
    setMutating(true);
    setMutationError(null);
    const intentSignature = signature(intent);
    const key = intentRef.current?.signature === intentSignature ? intentRef.current.key : operationKey();
    intentRef.current = { signature: intentSignature, key };
    try {
      let result: DebugSessionMetadata = { active: false };
      if (intent.kind === 'start') result = await startDebugSession(key);
      else if (intent.kind === 'mode') result = await changeDebugMode(intent.mode, intent.revision, key);
      else if (intent.kind === 'replace') result = await replaceDebugSession(intent.revision, intent.confirm, key);
      else await stopDebugSession(intent.revision, intent.confirm, key);
      if (!mountedRef.current) return;
      intentRef.current = null;
      disconnect();
      setStreamError(null);
      commitView(initialDebugState(result));
      client.setQueryData(debugV2Keys.session, result);
    } catch (error) {
      if (!mountedRef.current) return;
      setMutationError(error);
      if (isUnauthorized(error) || isForbidden(error)) {
        clearSensitive();
        clearStationSession(client, 'steward');
      } else if (conflictOrUnknown(error)) {
        await reconcile();
      }
    } finally {
      if (mountedRef.current) setMutating(false);
    }
  }, [clearSensitive, client, commitView, disconnect, mutating, reconcile]);

  return {
    authority,
    view,
    observer,
    streamError,
    mutationError,
    mutating,
    connect,
    disconnect,
    start: () => run({ kind: 'start' }),
    setMode: (mode: DebugMode, revision: string) => run({ kind: 'mode', mode, revision }),
    stop: (revision: string, confirm: boolean) => run({ kind: 'stop', revision, confirm }),
    replace: (revision: string, confirm: boolean) => run({ kind: 'replace', revision, confirm }),
  };
}

export function debugMutationMessage(error: unknown): string {
  if (error instanceof ApiError && error.status === 409) return 'The session changed. Refresh authoritative state and review it before confirming again.';
  if (error instanceof ApiError && error.status === 0) return 'The result is unknown. The same request identity is retained; refresh authority before an explicit retry.';
  return error instanceof Error ? error.message : 'The Debug operation failed.';
}

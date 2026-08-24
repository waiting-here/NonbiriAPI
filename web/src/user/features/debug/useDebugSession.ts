import { useCallback, useEffect, useReducer, useRef, useState } from 'react';
import { isApiError } from '@shared/query/http';
import {
  getDebugSession,
  issueLiveChallenge,
  setDebugMode,
  startDebugSession,
  stopDebugSession,
} from './api';
import { DebugEventStore } from './eventStore';
import { connectDebugEvents } from './sseClient';
import {
  debugSessionReducer,
  effectiveDebugMode,
  INITIAL_DEBUG_SESSION_STATE,
  type DebugConnectionState,
} from './sessionReducer';
import type { DebugSessionMetadata, DebugSessionSnapshot, SessionEndReason } from './types';

export interface UseDebugSessionResult {
  metadata: DebugSessionMetadata | null;
  connection: DebugConnectionState;
  mode: 'dry' | 'live';
  traces: readonly DebugSessionSnapshot['traces'][number][];
  lastEventId: number;
  dropped: number;
  gapReason: string | null;
  endReason: SessionEndReason | null;
  error: string | null;
  busy: boolean;
  start: () => Promise<void>;
  stop: () => Promise<void>;
  setDry: () => Promise<void>;
  enableLive: () => Promise<void>;
  clearView: () => void;
}

function errorMessage(error: unknown): string {
  if (error instanceof Error && error.message.length > 0) return error.message.slice(0, 512);
  return 'The debug operation could not be completed.';
}

function isControlSessionLost(error: unknown): boolean {
  return (
    isApiError(error) &&
    (error.status === 401 ||
      error.status === 404 ||
      error.code === 'unauthorized' ||
      error.code === 'not_found')
  );
}

const RECOVERY_WINDOW_MS = 60_000;
const MAX_RECOVERY_ATTEMPTS = 5;
const RECOVERY_DELAYS_MS = [100, 250, 500, 1_000, 2_000] as const;

export function useDebugSession(): UseDebugSessionResult {
  const [view, dispatch] = useReducer(debugSessionReducer, INITIAL_DEBUG_SESSION_STATE);
  const [store] = useState(() => new DebugEventStore());
  const storeRef = useRef(store);
  const [snapshot, setSnapshot] = useState<DebugSessionSnapshot>(() => store.snapshot());
  const [busy, setBusy] = useState(false);
  const mountedRef = useRef(true);
  const streamRef = useRef<AbortController | null>(null);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const stableSnapshotTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const recoveryWindowRef = useRef({ startedAt: 0, count: 0 });
  const scheduleRecoveryRef = useRef<(cause?: string) => void>(() => undefined);
  const sessionRef = useRef<DebugSessionMetadata | null>(null);
  const manuallyStoppedRef = useRef(true);
  const streamAttemptRef = useRef(0);
  const terminalEventRef = useRef(false);
  const recoveryInFlightRef = useRef(false);
  const recoverRef = useRef<(cause?: string) => Promise<void>>(async () => undefined);
  const operationRef = useRef(0);
  const streamConnectedRef = useRef(false);

  const clearReconnectTimer = useCallback(() => {
    if (reconnectTimerRef.current !== null) {
      clearTimeout(reconnectTimerRef.current);
      reconnectTimerRef.current = null;
    }
  }, []);

  const closeStream = useCallback(() => {
    streamAttemptRef.current += 1;
    streamConnectedRef.current = false;
    streamRef.current?.abort();
    streamRef.current = null;
    if (stableSnapshotTimerRef.current !== null) {
      clearTimeout(stableSnapshotTimerRef.current);
      stableSnapshotTimerRef.current = null;
    }
  }, []);

  const terminateLocalSession = useCallback(
    (reason: SessionEndReason = 'session_invalid') => {
      manuallyStoppedRef.current = true;
      terminalEventRef.current = true;
      recoveryInFlightRef.current = false;
      clearReconnectTimer();
      closeStream();
      sessionRef.current = null;
      setSnapshot(storeRef.current.clearSession());
      dispatch({ type: 'session_end', reason });
    },
    [clearReconnectTimer, closeStream],
  );

  const openStream = useCallback(
    (metadata: DebugSessionMetadata, reconnect: boolean, freshSnapshot = false) => {
      if (
        !mountedRef.current ||
        manuallyStoppedRef.current ||
        sessionRef.current === null ||
        sessionRef.current.id !== metadata.id
      )
        return;
      clearReconnectTimer();
      closeStream();
      terminalEventRef.current = false;
      const attempt = streamAttemptRef.current;
      const controller = new AbortController();
      streamRef.current = controller;
      dispatch({ type: 'connect_requested', reconnect });
      void connectDebugEvents({
        signal: controller.signal,
        lastEventId: freshSnapshot
          ? Number.MAX_SAFE_INTEGER
          : storeRef.current.snapshot().lastEventId,
        onInvalid: (reason, consumed) => {
          if (!mountedRef.current || attempt !== streamAttemptRef.current) return;
          streamConnectedRef.current = false;
          const next = storeRef.current.markInvalidRecovery(reason, consumed);
          setSnapshot(next);
          dispatch({ type: 'safe_dry' });
          if (!consumed || next.lastEventId !== consumed.seq) terminateLocalSession();
        },
        onEvent: async (event) => {
          if (!mountedRef.current || attempt !== streamAttemptRef.current) return;
          const next = await storeRef.current.apply(event);
          if (!mountedRef.current || attempt !== streamAttemptRef.current) return;
          setSnapshot(next);
          if (event.type === 'session_end' && next.endReason) {
            terminateLocalSession(next.endReason);
            return;
          }
          if (next.recoveryRequired) {
            dispatch({ type: 'safe_dry' });
            if (next.recoveryFatal || (event.type !== 'gap' && event.type !== 'session_snapshot'))
              throw new Error('The debug stream requires snapshot recovery.');
            return;
          }
          if (next.metadata) {
            sessionRef.current = next.metadata;
            streamConnectedRef.current = next.metadata.connected;
            dispatch({ type: 'metadata_event', metadata: next.metadata });
            if (next.metadata.connected) dispatch({ type: 'connected' });
          }
          if (event.type === 'session_snapshot' && !next.recoveryRequired) {
            if (stableSnapshotTimerRef.current !== null)
              clearTimeout(stableSnapshotTimerRef.current);
            const stableAttempt = attempt;
            stableSnapshotTimerRef.current = setTimeout(() => {
              if (mountedRef.current && stableAttempt === streamAttemptRef.current)
                recoveryWindowRef.current = { startedAt: 0, count: 0 };
              stableSnapshotTimerRef.current = null;
            }, 2_000);
          }
        },
      })
        .then(() => {
          if (attempt === streamAttemptRef.current) streamConnectedRef.current = false;
          if (
            !mountedRef.current ||
            attempt !== streamAttemptRef.current ||
            controller.signal.aborted
          )
            return;
          if (terminalEventRef.current || manuallyStoppedRef.current) return;
          scheduleRecoveryRef.current('The debug stream closed unexpectedly.');
        })
        .catch((error: unknown) => {
          if (attempt === streamAttemptRef.current) streamConnectedRef.current = false;
          if (
            !mountedRef.current ||
            attempt !== streamAttemptRef.current ||
            controller.signal.aborted
          )
            return;
          if (terminalEventRef.current || manuallyStoppedRef.current) return;
          if (isControlSessionLost(error)) {
            terminateLocalSession();
            return;
          }
          scheduleRecoveryRef.current(errorMessage(error));
        });
    },
    [clearReconnectTimer, closeStream, terminateLocalSession],
  );

  const receiveSession = useCallback(
    (metadata: DebugSessionMetadata) => {
      sessionRef.current = metadata;
      storeRef.current.bindSession(metadata.id);
      const next = storeRef.current.seedMetadata(metadata);
      setSnapshot(next);
      dispatch({ type: 'session_received', metadata });
      openStream(metadata, false);
    },
    [openStream],
  );

  const recoverAndReconnect = useCallback(
    async (cause?: string) => {
      const current = sessionRef.current;
      const operation = operationRef.current;
      if (!current || manuallyStoppedRef.current || recoveryInFlightRef.current) return;
      recoveryInFlightRef.current = true;
      // A lost mode/stream response leaves the server-authoritative grant
      // unknown. First detach, then explicitly write dry and read it back from
      // the control plane. No SSE reconnect is attempted until that authority
      // confirms the same generation is dry.
      sessionRef.current = { ...current, mode: 'dry', connected: false };
      dispatch({ type: 'safe_dry' });
      clearReconnectTimer();
      closeStream();
      dispatch({ type: 'connect_requested', reconnect: true });
      try {
        const dry = await setDebugMode('dry');
        if (
          !mountedRef.current ||
          manuallyStoppedRef.current ||
          operation !== operationRef.current
        ) {
          throw new Error('The debug session could not confirm dry mode.');
        }
        if (dry.id !== current.id || dry.generation !== current.generation) {
          terminateLocalSession();
          return;
        }
        if (dry.mode !== 'dry') throw new Error('The debug session could not confirm dry mode.');
        const confirmed = await getDebugSession();
        if ('active' in confirmed) {
          terminateLocalSession();
          return;
        }
        if (confirmed.id !== current.id || confirmed.generation !== current.generation) {
          terminateLocalSession();
          return;
        }
        if (confirmed.mode !== 'dry') {
          throw new Error('The debug session could not confirm dry mode.');
        }
        if (operation === operationRef.current) {
          sessionRef.current = confirmed;
          setSnapshot(storeRef.current.seedMetadata(confirmed));
          dispatch({ type: 'metadata_event', metadata: confirmed });
          openStream(confirmed, true, true);
        }
      } catch (error) {
        if (
          mountedRef.current &&
          !manuallyStoppedRef.current &&
          operation === operationRef.current
        ) {
          if (isControlSessionLost(error)) {
            terminateLocalSession();
            return;
          }
          dispatch({
            type: 'recovery_blocked',
            message: cause
              ? `${cause} Dry mode could not be confirmed; reconnect manually.`
              : errorMessage(error),
          });
        }
      } finally {
        recoveryInFlightRef.current = false;
      }
    },
    [clearReconnectTimer, closeStream, openStream, terminateLocalSession],
  );
  const scheduleRecovery = useCallback(
    (cause?: string) => {
      if (!mountedRef.current || manuallyStoppedRef.current || terminalEventRef.current) return;
      const now = Date.now();
      if (now - recoveryWindowRef.current.startedAt > RECOVERY_WINDOW_MS) {
        recoveryWindowRef.current = { startedAt: now, count: 0 };
      }
      if (recoveryWindowRef.current.count >= MAX_RECOVERY_ATTEMPTS) {
        manuallyStoppedRef.current = true;
        clearReconnectTimer();
        closeStream();
        dispatch({ type: 'safe_dry' });
        dispatch({
          type: 'recovery_blocked',
          message: cause
            ? `${cause} Automatic recovery stopped; start a new debug session to retry.`
            : 'Automatic recovery stopped; start a new debug session to retry.',
        });
        return;
      }
      const attempt = recoveryWindowRef.current.count;
      recoveryWindowRef.current.count += 1;
      const delay = RECOVERY_DELAYS_MS[Math.min(attempt, RECOVERY_DELAYS_MS.length - 1)];
      clearReconnectTimer();
      reconnectTimerRef.current = setTimeout(() => {
        reconnectTimerRef.current = null;
        void recoverRef.current(cause);
      }, delay);
    },
    [clearReconnectTimer, closeStream],
  );
  useEffect(() => {
    recoverRef.current = recoverAndReconnect;
    scheduleRecoveryRef.current = scheduleRecovery;
  }, [recoverAndReconnect, scheduleRecovery]);

  const start = useCallback(async () => {
    const operation = ++operationRef.current;
    setBusy(true);
    manuallyStoppedRef.current = false;
    terminalEventRef.current = false;
    clearReconnectTimer();
    closeStream();
    sessionRef.current = null;
    storeRef.current.clearSession();
    setSnapshot(storeRef.current.snapshot());
    dispatch({ type: 'start_requested' });
    try {
      const metadata = await startDebugSession();
      if (!mountedRef.current || operation !== operationRef.current || manuallyStoppedRef.current)
        return;
      if (metadata.mode !== 'dry') throw new Error('A new debug session must start in dry mode.');
      receiveSession(metadata);
    } catch (error) {
      if (mountedRef.current && operation === operationRef.current) {
        manuallyStoppedRef.current = true;
        if (isControlSessionLost(error)) terminateLocalSession();
        else dispatch({ type: 'error', message: errorMessage(error) });
      }
    } finally {
      if (mountedRef.current && operation === operationRef.current) setBusy(false);
    }
  }, [clearReconnectTimer, closeStream, receiveSession, terminateLocalSession]);

  const stop = useCallback(async () => {
    const operation = ++operationRef.current;
    setBusy(true);
    manuallyStoppedRef.current = true;
    terminalEventRef.current = true;
    clearReconnectTimer();
    closeStream();
    sessionRef.current = null;
    storeRef.current.clearSession();
    setSnapshot(storeRef.current.snapshot());
    dispatch({ type: 'stopped' });
    try {
      await stopDebugSession();
    } catch (error) {
      if (mountedRef.current && operation === operationRef.current)
        if (!isControlSessionLost(error)) dispatch({ type: 'error', message: errorMessage(error) });
    } finally {
      if (mountedRef.current && operation === operationRef.current) setBusy(false);
    }
  }, [clearReconnectTimer, closeStream]);

  const setDry = useCallback(async () => {
    const operation = ++operationRef.current;
    if (!sessionRef.current) return;
    const sessionID = sessionRef.current.id;
    setBusy(true);
    try {
      const metadata = await setDebugMode('dry');
      if (
        !mountedRef.current ||
        manuallyStoppedRef.current ||
        operation !== operationRef.current ||
        sessionRef.current?.id !== sessionID ||
        metadata.id !== sessionID ||
        metadata.generation !== sessionRef.current?.generation ||
        metadata.mode !== 'dry'
      )
        throw new Error('The debug mode response did not match the current dry session.');
      sessionRef.current = metadata;
      setSnapshot(storeRef.current.seedMetadata(metadata));
      dispatch({ type: 'metadata_event', metadata });
    } catch (error) {
      if (mountedRef.current && operation === operationRef.current && !manuallyStoppedRef.current) {
        if (isControlSessionLost(error)) terminateLocalSession();
        else {
          void recoverAndReconnect(errorMessage(error));
          dispatch({ type: 'error', message: errorMessage(error) });
        }
      }
    } finally {
      if (mountedRef.current && operation === operationRef.current) setBusy(false);
    }
  }, [recoverAndReconnect, terminateLocalSession]);

  const enableLive = useCallback(async () => {
    const operation = ++operationRef.current;
    const current = sessionRef.current;
    if (!current || !current.connected || !streamConnectedRef.current) return;
    const sessionID = current.id;
    const sessionGeneration = current.generation;
    setBusy(true);
    try {
      // The challenge is requested only after the page's accessible
      // ConfirmDialog has been confirmed. The returned identifier is held in
      // this call stack and immediately consumed by the mode PUT.
      const confirmationID = await issueLiveChallenge();
      // Challenge issuance and mode activation are separate network turns.
      // Re-check every authority that can invalidate the operation before the
      // live PUT, including the SSE attachment. This prevents a late
      // challenge response from enabling live mode after unmount, stop, or a
      // disconnected/replaced session.
      if (
        !mountedRef.current ||
        manuallyStoppedRef.current ||
        operation !== operationRef.current ||
        sessionRef.current?.id !== sessionID ||
        sessionRef.current?.generation !== sessionGeneration ||
        !sessionRef.current?.connected ||
        !streamConnectedRef.current
      ) {
        throw new Error('The debug session is no longer connected.');
      }
      const metadata = await setDebugMode('live', confirmationID);
      if (
        !mountedRef.current ||
        manuallyStoppedRef.current ||
        operation !== operationRef.current ||
        sessionRef.current?.id !== sessionID ||
        sessionRef.current?.generation !== sessionGeneration ||
        metadata.id !== sessionID ||
        metadata.generation !== sessionGeneration ||
        metadata.mode !== 'live'
      )
        throw new Error('The debug mode response did not match the current live session.');
      sessionRef.current = metadata;
      setSnapshot(storeRef.current.seedMetadata(metadata));
      dispatch({ type: 'metadata_event', metadata });
    } catch (error) {
      if (mountedRef.current && operation === operationRef.current && !manuallyStoppedRef.current) {
        if (isControlSessionLost(error)) terminateLocalSession();
        else {
          void recoverAndReconnect(errorMessage(error));
          dispatch({ type: 'error', message: errorMessage(error) });
        }
      }
    } finally {
      if (mountedRef.current && operation === operationRef.current) setBusy(false);
    }
  }, [recoverAndReconnect, terminateLocalSession]);

  const clearView = useCallback(() => {
    setSnapshot(storeRef.current.clearTraces());
  }, []);

  const restoreExistingSession = useCallback(
    async (metadata: DebugSessionMetadata, operation: number) => {
      try {
        const dry = await setDebugMode('dry');
        if (
          !mountedRef.current ||
          manuallyStoppedRef.current ||
          operation !== operationRef.current ||
          dry.id !== metadata.id ||
          dry.generation !== metadata.generation ||
          dry.mode !== 'dry'
        ) {
          throw new Error('The existing debug session could not be made dry.');
        }
        const confirmed = await getDebugSession();
        if ('active' in confirmed) {
          terminateLocalSession();
          return;
        }
        if (
          confirmed.id !== metadata.id ||
          confirmed.generation !== metadata.generation ||
          confirmed.mode !== 'dry'
        ) {
          throw new Error('The existing debug session could not confirm dry mode.');
        }
        if (operation === operationRef.current && !manuallyStoppedRef.current)
          receiveSession(confirmed);
      } catch (error) {
        if (!mountedRef.current || operation !== operationRef.current) return;
        if (isControlSessionLost(error)) {
          terminateLocalSession();
          return;
        }
        manuallyStoppedRef.current = true;
        clearReconnectTimer();
        closeStream();
        sessionRef.current = null;
        setSnapshot(storeRef.current.clearSession());
        dispatch({ type: 'recovery_blocked', message: errorMessage(error) });
      } finally {
        if (mountedRef.current && operation === operationRef.current) setBusy(false);
      }
    },
    [clearReconnectTimer, closeStream, receiveSession, terminateLocalSession],
  );

  useEffect(() => {
    mountedRef.current = true;
    manuallyStoppedRef.current = false;
    const operation = operationRef.current;
    let cancelled = false;
    void getDebugSession()
      .then((result) => {
        if (cancelled || !mountedRef.current || operation !== operationRef.current) return;
        if ('active' in result && result.active === false) {
          terminateLocalSession('stopped');
          return;
        }
        if (!('active' in result)) {
          setBusy(true);
          void restoreExistingSession(result, operation);
        }
      })
      .catch((error: unknown) => {
        if (!cancelled && mountedRef.current) {
          manuallyStoppedRef.current = true;
          if (isControlSessionLost(error)) terminateLocalSession();
          else dispatch({ type: 'error', message: errorMessage(error) });
        }
      });
    return () => {
      cancelled = true;
      mountedRef.current = false;
      // Invalidate all pending control-plane operations before closing the
      // stream. A challenge response may otherwise resume on the next tick
      // and issue a live PUT after this hook has unmounted.
      operationRef.current += 1;
      manuallyStoppedRef.current = true;
      clearReconnectTimer();
      closeStream();
    };
  }, [
    clearReconnectTimer,
    closeStream,
    receiveSession,
    restoreExistingSession,
    terminateLocalSession,
  ]);

  const effectiveMode = effectiveDebugMode(view);
  return {
    metadata: view.metadata,
    connection: view.connection,
    mode: effectiveMode,
    traces: snapshot.traces,
    lastEventId: snapshot.lastEventId,
    dropped: snapshot.dropped,
    gapReason: snapshot.gapReason,
    endReason: view.endReason ?? snapshot.endReason,
    error: view.error,
    busy,
    start,
    stop,
    setDry,
    enableLive,
    clearView,
  };
}

import { useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import {
  captureStationSession,
  clearStationSession,
  stationSessionMatches,
  StationSessionChangedError,
  type CharityManagementFrame,
  type StationSessionSnapshot,
} from '@shared/charityManagement';
import { operationKey, responseOutcomeUnknown } from '@shared/operations/api';
import { isForbidden, isUnauthorized } from '@shared/query/http';

interface Retained<T> {
  input: T;
  signature: string;
  key: string;
  session: StationSessionSnapshot;
}
/** Payloads and uncertain retry identities remain in this mounted page, never in a mutation cache. */
export function useImageOperation<T, R>(
  frame: CharityManagementFrame,
  execute: (input: T, key: string) => Promise<R>,
  reconcile?: (result: R | null) => Promise<unknown> | unknown,
) {
  const client = useQueryClient();
  const retained = useRef<Retained<T> | null>(null),
    mounted = useRef(true),
    running = useRef(false);
  const [retryInput, setRetryInput] = useState<T | undefined>(undefined);
  const [pending, setPending] = useState(false),
    [error, setError] = useState<unknown>(null),
    [uncertain, setUncertain] = useState(false);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      retained.current = null;
    };
  }, []);
  const run = async (input: T, onSuccess?: (value: R) => void) => {
    if (running.current) return;
    let request: Retained<T>;
    try {
      const snapshot = captureStationSession(client, frame);
      if (retained.current && retained.current.session.subject !== snapshot.subject) {
        retained.current = null;
        throw new StationSessionChangedError();
      }
      const signature = JSON.stringify(input);
      if (retained.current && retained.current.signature !== signature)
        throw new StationSessionChangedError();
      request = retained.current
        ? { ...retained.current, session: snapshot }
        : {
            input: JSON.parse(signature) as T,
            signature,
            key: operationKey(),
            session: snapshot,
          };
      retained.current = request;
    } catch (caught) {
      retained.current = null;
      setRetryInput(undefined);
      setUncertain(false);
      setError(caught);
      return;
    }
    running.current = true;
    setPending(true);
    setError(null);
    let accepted = false;
    try {
      const result = await execute(request.input, request.key);
      if (!mounted.current || !stationSessionMatches(client, frame, request.session)) return;
      accepted = true;
      retained.current = null;
      setUncertain(false);
      setRetryInput(undefined);
      onSuccess?.(result);
      await reconcile?.(result);
    } catch (caught) {
      if (!mounted.current || !stationSessionMatches(client, frame, request.session)) return;
      if (isUnauthorized(caught) || isForbidden(caught)) {
        retained.current = null;
        clearStationSession(client, frame);
        return;
      }
      const unknown = !accepted && responseOutcomeUnknown(caught);
      if (!unknown) retained.current = null;
      setUncertain(unknown);
      setRetryInput(() => (unknown ? request.input : undefined));
      setError(caught);
      try {
        await reconcile?.(null);
      } catch {
        /* The original operation outcome stays visible. */
      }
    } finally {
      running.current = false;
      if (mounted.current) {
        setPending(false);
        if (!accepted && !stationSessionMatches(client, frame, request.session)) {
          let sameSubject = false;
          try {
            sameSubject = captureStationSession(client, frame).subject === request.session.subject;
          } catch {
            /* No current identity can retry. */
          }
          retained.current = sameSubject ? request : null;
          setRetryInput(() => (sameSubject ? request.input : undefined));
          setUncertain(sameSubject);
          setError(new StationSessionChangedError());
        }
      }
    }
  };
  return {
    run,
    pending,
    error,
    uncertain,
    input: retryInput,
    locked: pending || uncertain,
  };
}

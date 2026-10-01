import { useCallback, useEffect, useRef, useState } from 'react';
import { useMutation, useQueryClient, type MutateOptions } from '@tanstack/react-query';
import {
  captureStationSession,
  clearStationSession,
  isStationSessionChanged,
  stationSessionMatches,
  StationSessionChangedError,
  type CharityManagementFrame,
  type StationSessionSnapshot,
} from '@shared/charityManagement';
import { ApiError, isForbidden, isUnauthorized } from '@shared/query/http';
import { conflictOrUnknown, operationKey, responseOutcomeUnknown } from './api';

export interface OperationContext {
  readonly signal: AbortSignal;
  /** Internal recovery identity; never a field the user must copy or manage. */
  readonly operationKey?: string;
  isCurrent: () => boolean;
  assertCurrent: () => void;
  /** Guard synchronous cache writes, downloads and one-shot result delivery. */
  commit: <T>(effect: () => T) => T;
}
export type OperationOutcome =
  'idle' | 'pending' | 'confirmed' | 'unknown' | 'conflict' | 'failed' | 'refresh-failed';
export interface RetainedOperationOptions {
  clearSecrets?: () => void;
}
interface PreparedOperation<T> {
  variables: T;
  frame: CharityManagementFrame | undefined;
  snapshot: StationSessionSnapshot | undefined;
  preparationError: unknown;
  controller: AbortController;
  operationKey?: string;
  dispatched?: boolean;
}
function inputSnapshot<T>(value: T): T {
  return structuredClone(value);
}
function isFinalAuthorityLoss(error: unknown): boolean {
  return (
    isUnauthorized(error) ||
    (isForbidden(error) && !(error instanceof ApiError && error.code === 'elevated_required'))
  );
}

/** Variables are non-secret intent. Use useOperation for transient request secrets. */
export function useRetainedOperation<TVariables, TResult>(
  execute: (variables: TVariables, key: string, context: OperationContext) => Promise<TResult>,
  reconcile: (
    variables: NoInfer<TVariables>,
    error: unknown | null,
    context: OperationContext,
  ) => Promise<unknown> | unknown,
  authorityRoot: readonly unknown[] = ['admin', 'operations'],
  options: RetainedOperationOptions = {},
) {
  const client = useQueryClient();
  const mounted = useRef(true);
  const clearSecrets = useRef(options.clearSecrets);
  useEffect(() => {
    clearSecrets.current = options.clearSecrets;
  }, [options.clearSecrets]);
  const active = useRef(new Set<PreparedOperation<TVariables>>());
  const identities = useRef(new Map<string, string>());
  const identitySubject = useRef<string | undefined>(undefined);
  const confirmed = useRef<PreparedOperation<TVariables> | null>(null);
  const latest = useRef<PreparedOperation<TVariables> | null>(null);
  const inFlight = useRef<{
    input: PreparedOperation<TVariables>;
    promise: Promise<TResult>;
  } | null>(null);
  const [outcome, setOutcome] = useState<OperationOutcome>('idle');
  const [refreshError, setRefreshError] = useState<unknown>(null);
  const [authorityLossError, setAuthorityLossError] = useState<Error | null>(null);
  const deniedFrame = useRef<CharityManagementFrame | undefined>(undefined);
  const sameActor = (input: PreparedOperation<TVariables>) =>
    !input.preparationError &&
    (!input.frame ||
      Boolean(input.snapshot && stationSessionMatches(client, input.frame, input.snapshot)));
  const current = (input: PreparedOperation<TVariables>) =>
    mounted.current && !input.controller.signal.aborted && sameActor(input);
  const contextFor = (input: PreparedOperation<TVariables>): OperationContext => {
    const assertCurrent = () => {
      if (!current(input)) throw new StationSessionChangedError();
    };
    return {
      signal: input.controller.signal,
      get operationKey() {
        return input.operationKey;
      },
      isCurrent: () => current(input),
      assertCurrent,
      commit: (effect) => {
        assertCurrent();
        return effect();
      },
    };
  };
  const signature = (input: PreparedOperation<TVariables>) =>
    JSON.stringify([input.snapshot?.subject, input.variables]);
  const prepare = (variables: TVariables): PreparedOperation<TVariables> => {
    const frame =
      authorityRoot[0] === 'admin' ? 'admin' : authorityRoot[0] === 'user' ? 'steward' : undefined;
    const input: PreparedOperation<TVariables> = {
      variables,
      frame,
      snapshot: undefined,
      preparationError: undefined,
      controller: new AbortController(),
    };
    try {
      input.variables = inputSnapshot(variables);
      input.snapshot = frame ? captureStationSession(client, frame) : undefined;
    } catch (error) {
      input.preparationError = error;
    }
    return input;
  };
  useEffect(() => {
    mounted.current = true;
    const requests = active.current;
    const unsubscribe = client.getQueryCache().subscribe(() => {
      if (deniedFrame.current) {
        const station = deniedFrame.current === 'admin' ? 'admin' : 'user';
        if (client.getQueryData([station, 'session'])) {
          deniedFrame.current = undefined;
          setAuthorityLossError(null);
        }
      }
      let changed = false;
      for (const input of active.current) {
        if (!sameActor(input)) {
          input.controller.abort();
          active.current.delete(input);
          changed = true;
        }
      }
      if (inFlight.current && !sameActor(inFlight.current.input)) {
        inFlight.current.input.controller.abort();
        changed = true;
      }
      if (confirmed.current && !sameActor(confirmed.current)) {
        confirmed.current = null;
        changed = true;
      }
      if (latest.current && !sameActor(latest.current)) {
        latest.current = null;
        changed = true;
      }
      if (changed) {
        inFlight.current = null;
        clearSecrets.current?.();
        setOutcome('idle');
        setRefreshError(null);
      }
    });
    return () => {
      mounted.current = false;
      unsubscribe();
      for (const input of requests) input.controller.abort();
      requests.clear();
      inFlight.current?.input.controller.abort();
      confirmed.current = null;
      clearSecrets.current?.();
    };
    // Account authority is checked from the live QueryClient, not render data.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client]);
  const refresh = async (input: PreparedOperation<TVariables>, error: unknown | null) => {
    const context = contextFor(input);
    context.assertCurrent();
    try {
      const result = await reconcile(input.variables, error, context);
      context.assertCurrent();
      if (result instanceof Error) throw result;
      if (
        result &&
        typeof result === 'object' &&
        'operationConfirmed' in result &&
        result.operationConfirmed === true
      ) {
        // The domain must establish this from its authorized receipt/current resource read.
        identities.current.delete(signature(input));
        confirmed.current = input;
        setOutcome('confirmed');
      }
      setRefreshError(null);
      if (error === null) setOutcome('confirmed');
    } catch (failure) {
      if (!current(input)) return;
      setRefreshError(failure);
      if (error === null) setOutcome('refresh-failed');
    }
  };
  const mutation = useMutation<TResult, Error, PreparedOperation<TVariables>>({
    retry: false,
    mutationFn: async (input) => {
      if (input.preparationError) throw input.preparationError;
      const context = contextFor(input);
      context.assertCurrent();
      active.current.add(input);
      setOutcome('pending');
      setRefreshError(null);
      setAuthorityLossError(null);
      const requestSignature = signature(input);
      if (identitySubject.current !== input.snapshot?.subject) {
        identities.current.clear();
        identitySubject.current = input.snapshot?.subject;
      }
      if (!identities.current.has(requestSignature) && identities.current.size >= 100)
        throw new ApiError(
          'operation_capacity',
          'Check pending actions before starting another.',
          429,
        );
      const key = identities.current.get(requestSignature) ?? operationKey();
      identities.current.set(requestSignature, key);
      input.operationKey = key;
      try {
        input.dispatched = true;
        const result = await execute(input.variables, key, context);
        context.assertCurrent();
        return result;
      } catch (error) {
        context.assertCurrent();
        throw error;
      }
    },
    onSuccess: async (_result, input) => {
      if (!current(input)) return;
      identities.current.delete(signature(input));
      confirmed.current = input;
      setOutcome('confirmed');
      await refresh(input, null);
    },
    onError: async (error, input) => {
      if (isStationSessionChanged(error) || !current(input)) return;
      const unknown = input.dispatched && responseOutcomeUnknown(error);
      if (!unknown) identities.current.delete(signature(input));
      setOutcome(
        unknown ? 'unknown' : input.dispatched && conflictOrUnknown(error) ? 'conflict' : 'failed',
      );
      if (isFinalAuthorityLoss(error)) {
        clearSecrets.current?.();
        if (input.frame) clearStationSession(client, input.frame);
        else {
          void client.cancelQueries({ queryKey: authorityRoot });
          client.removeQueries({ queryKey: authorityRoot });
          client.getMutationCache().clear();
        }
        // Existing editors consume this safe denial to close their local draft.
        // A later confirmed login clears it before another account can inherit it.
        deniedFrame.current = input.frame;
        setAuthorityLossError(error);
      }
      if (input.dispatched && current(input) && conflictOrUnknown(error))
        await refresh(input, error);
    },
    onSettled: (_result, _error, input) => {
      active.current.delete(input);
    },
  });
  const callbacks = (
    options?: MutateOptions<TResult, Error, TVariables>,
  ): MutateOptions<TResult, Error, PreparedOperation<TVariables>> => ({
    onSuccess: (result, input, context, requestContext) => {
      if (current(input)) options?.onSuccess?.(result, input.variables, context, requestContext);
    },
    onError: (error, input, context, requestContext) => {
      if (!isStationSessionChanged(error) && current(input))
        options?.onError?.(error, input.variables, context, requestContext);
    },
    onSettled: (result, error, input, context, requestContext) => {
      if (!isStationSessionChanged(error) && current(input))
        options?.onSettled?.(result, error, input.variables, context, requestContext);
    },
  });
  const mutateAsync = (
    variables: TVariables,
    options?: MutateOptions<TResult, Error, TVariables>,
  ) => {
    const input = prepare(variables);
    if (inFlight.current && current(inFlight.current.input)) {
      if (signature(input) === signature(inFlight.current.input)) return inFlight.current.promise;
      return Promise.reject(
        new ApiError('operation_in_progress', 'This action is still being processed.', 409),
      );
    }
    const promise = mutation.mutateAsync(input, callbacks(options)).then((result) => {
      contextFor(input).assertCurrent();
      return result;
    });
    latest.current = input;
    inFlight.current = { input, promise };
    void promise
      .finally(() => {
        if (inFlight.current?.input === input) inFlight.current = null;
      })
      .catch(() => undefined);
    return promise;
  };
  const resetMutation = mutation.reset;
  const reset = useCallback(() => {
    if (inFlight.current) return;
    resetMutation();
    setOutcome('idle');
    setRefreshError(null);
  }, [resetMutation]);
  return {
    ...mutation,
    outcome,
    refreshError,
    isPending: outcome === 'pending',
    isSuccess: outcome === 'confirmed' || outcome === 'refresh-failed',
    isError: outcome === 'failed' || outcome === 'unknown' || outcome === 'conflict',
    data: outcome === 'idle' ? undefined : mutation.data,
    error:
      authorityLossError ??
      (outcome === 'idle' || outcome === 'confirmed' || outcome === 'refresh-failed'
        ? null
        : mutation.error),
    variables: outcome === 'idle' ? undefined : mutation.variables?.variables,
    mutateAsync,
    reset,
    mutate: (variables: TVariables, options?: MutateOptions<TResult, Error, TVariables>) => {
      void mutateAsync(variables, options).catch(() => undefined);
    },
    cancel: () => {
      for (const input of active.current) input.controller.abort();
      inFlight.current?.input.controller.abort();
      confirmed.current = null;
      clearSecrets.current?.();
      // Abort does not prove that a dispatched server transaction rolled back.
      if (inFlight.current) setOutcome(inFlight.current.input.dispatched ? 'unknown' : 'idle');
    },
    refresh: async () => {
      if (confirmed.current && current(confirmed.current)) await refresh(confirmed.current, null);
    },
    check: async () => {
      const previous = latest.current;
      if (!previous || !sameActor(previous)) return;
      const input = prepare(previous.variables);
      input.operationKey = previous.operationKey;
      active.current.add(input);
      try {
        await refresh(
          input,
          new ApiError('result_unknown', 'Check the original action result.', 0),
        );
      } finally {
        active.current.delete(input);
      }
    },
  };
}

import { useRef } from 'react';
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

function isFinalAuthorityLoss(error: unknown): boolean {
  return (
    isUnauthorized(error) ||
    (isForbidden(error) && !(error instanceof ApiError && error.code === 'elevated_required'))
  );
}

interface PreparedOperation<T> {
  variables: T;
  frame: CharityManagementFrame | undefined;
  snapshot: StationSessionSnapshot | undefined;
  preparationError: unknown;
  authorityRoot: readonly unknown[];
}

export function useRetainedOperation<TVariables, TResult>(
  execute: (variables: TVariables, key: string) => Promise<TResult>,
  reconcile: (variables: NoInfer<TVariables>, error: unknown | null) => Promise<unknown> | unknown,
  authorityRoot: readonly unknown[] = ['admin', 'operations'],
) {
  const client = useQueryClient();
  const identity = useRef<{ signature: string; key: string } | null>(null);
  const signature = (input: PreparedOperation<TVariables>) =>
    JSON.stringify([input.snapshot?.subject, input.variables]);
  const current = (input: PreparedOperation<TVariables>) =>
    !input.preparationError &&
    (!input.frame ||
      Boolean(input.snapshot && stationSessionMatches(client, input.frame, input.snapshot)));
  // Capture before React Query queues the mutation; a changed session must
  // invalidate even an operation that has not reached its request function.
  const prepare = (variables: TVariables): PreparedOperation<TVariables> => {
    const frame =
      authorityRoot[0] === 'admin' ? 'admin' : authorityRoot[0] === 'user' ? 'steward' : undefined;
    const input: PreparedOperation<TVariables> = {
      variables,
      frame,
      authorityRoot,
      snapshot: undefined,
      preparationError: undefined,
    };
    try {
      input.snapshot = frame ? captureStationSession(client, frame) : undefined;
    } catch (error) {
      input.preparationError = error;
    }
    return input;
  };
  const mutation = useMutation<TResult, Error, PreparedOperation<TVariables>>({
    retry: false,
    mutationFn: async (input) => {
      if (input.preparationError) throw input.preparationError;
      if (!current(input)) throw new StationSessionChangedError();
      const requestSignature = signature(input);
      const request =
        identity.current?.signature === requestSignature
          ? identity.current
          : { signature: requestSignature, key: operationKey() };
      identity.current = request;
      try {
        const result = await execute(input.variables, request.key);
        if (!current(input)) throw new StationSessionChangedError();
        return result;
      } catch (error) {
        if (!current(input)) throw new StationSessionChangedError();
        throw error;
      }
    },
    onSuccess: (_result, input) => {
      if (!current(input)) return;
      if (identity.current?.signature === signature(input)) identity.current = null;
      return reconcile(input.variables, null);
    },
    onError: (error, input) => {
      if (isStationSessionChanged(error) || !current(input)) return;
      if (!responseOutcomeUnknown(error) && identity.current?.signature === signature(input))
        identity.current = null;
      if (isFinalAuthorityLoss(error)) {
        if (input.frame) clearStationSession(client, input.frame);
        else {
          void client.cancelQueries({ queryKey: input.authorityRoot });
          client.removeQueries({ queryKey: input.authorityRoot });
          client.getMutationCache().clear();
        }
      }
      if (conflictOrUnknown(error)) return reconcile(input.variables, error);
      return undefined;
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
  return {
    ...mutation,
    variables: mutation.variables?.variables,
    mutate: (variables: TVariables, options?: MutateOptions<TResult, Error, TVariables>) =>
      mutation.mutate(prepare(variables), callbacks(options)),
    mutateAsync: (variables: TVariables, options?: MutateOptions<TResult, Error, TVariables>) =>
      mutation.mutateAsync(prepare(variables), callbacks(options)),
  };
}

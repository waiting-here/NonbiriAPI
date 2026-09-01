import { useRef } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { clearStationSession } from '@shared/charityManagement';
import { ApiError, isForbidden, isUnauthorized } from '@shared/query/http';
import { conflictOrUnknown, operationKey, responseOutcomeUnknown } from './api';

function isFinalAuthorityLoss(error: unknown): boolean {
  return isUnauthorized(error)
    || (isForbidden(error) && !(error instanceof ApiError && error.code === 'elevated_required'));
}

export function useRetainedOperation<TVariables, TResult>(
  execute: (variables: TVariables, key: string) => Promise<TResult>,
  reconcile: (variables: NoInfer<TVariables>, error: unknown | null) => Promise<unknown> | unknown,
  authorityRoot: readonly unknown[] = ['admin', 'operations'],
) {
  const client = useQueryClient();
  const identity = useRef<{ signature: string; key: string } | null>(null);
  return useMutation({
    retry: false,
    mutationFn: async (variables: TVariables) => {
      const signature = JSON.stringify(variables);
      const current = identity.current?.signature === signature
        ? identity.current
        : { signature, key: operationKey() };
      identity.current = current;
      return execute(variables, current.key);
    },
    onSuccess: (_result, variables) => {
      identity.current = null;
      return reconcile(variables, null);
    },
    onError: (error, variables) => {
      if (!responseOutcomeUnknown(error)) identity.current = null;
      if (isFinalAuthorityLoss(error)) {
        if (authorityRoot[0] === 'admin') clearStationSession(client, 'admin');
        else if (authorityRoot[0] === 'user') clearStationSession(client, 'steward');
        else {
          void client.cancelQueries({ queryKey: authorityRoot });
          client.removeQueries({ queryKey: authorityRoot });
          client.getMutationCache().clear();
        }
      }
      if (conflictOrUnknown(error)) return reconcile(variables, error);
      return undefined;
    },
  });
}

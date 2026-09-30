import { useRef } from 'react';
import { ApiError } from '@shared/query/http';
import { useRetainedOperation, type OperationContext } from './useRetainedOperation';

export interface OperationOptions<TIntent, TSecret, TResult> {
  authorityRoot: readonly unknown[];
  /** Return only non-secret data; deliver one-shot output with context.commit. */
  execute: (
    intent: TIntent,
    secret: TSecret | undefined,
    key: string,
    context: OperationContext,
  ) => Promise<TResult>;
  reconcile: (
    intent: NoInfer<TIntent>,
    error: unknown | null,
    context: OperationContext,
  ) => Promise<unknown> | unknown;
  clearSecrets?: () => void;
}

/** Request secrets live only until dispatch/cancel, outside mutation variables and retry identity. */
export function useOperation<TIntent, TSecret = never, TResult = void>(
  options: OperationOptions<TIntent, TSecret, TResult>,
) {
  const pendingSecret = useRef<{ value: TSecret | undefined } | null>(null);
  const pending = useRef<Promise<TResult> | null>(null);
  const operation = useRetainedOperation<TIntent, TResult>(
    async (intent, key, context) => {
      const secret = pendingSecret.current?.value;
      pendingSecret.current = null;
      context.assertCurrent();
      return options.execute(intent, secret, key, context);
    },
    options.reconcile,
    options.authorityRoot,
    {
      clearSecrets: () => {
        pendingSecret.current = null;
        pending.current = null;
        options.clearSecrets?.();
      },
    },
  );
  return {
    ...operation,
    run: (intent: TIntent, secret?: TSecret) => {
      if (pending.current)
        return Promise.reject(
          new ApiError('operation_in_progress', 'This action is still being processed.', 409),
        );
      pendingSecret.current = { value: secret };
      const request = operation.mutateAsync(intent);
      pending.current = request;
      void request
        .finally(() => {
          if (pending.current === request) {
            pendingSecret.current = null;
            pending.current = null;
          }
        })
        .catch(() => undefined);
      return request;
    },
  };
}

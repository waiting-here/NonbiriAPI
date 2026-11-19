import { useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import type { OperationContext } from '@shared/operations/useRetainedOperation';
import { useOperation } from '@shared/operations/useOperation';
import { coreKeys } from '../queries';
import { isOutcomeUnknown } from '../request';
import { resourceStatus, type ResourceStatus } from '../resourceOperation';
import {
  executeConnection,
  readConnectionResult,
  type ConnectionIntent,
  type ConnectionResult,
} from './resourceOperation';

export function useConnectionOperation(
  accountId: string,
  onSaved: (value: ConnectionResult) => void,
  clearSecrets?: () => void,
  options: { deferRefetch?: () => boolean } = {},
) {
  const client = useQueryClient();
  const saved = useRef<ConnectionResult | null>(null);
  const rechecked = useRef<{ status: ResourceStatus; context: OperationContext } | null>(null);
  const [needsSecret, setNeedsSecret] = useState(false);
  const retrying = useRef(false);
  const [receipt, setReceipt] = useState<ResourceStatus | null>(null);
  const operation = useOperation<ConnectionIntent, string, ConnectionResult>({
    authorityRoot: coreKeys.all,
    clearSecrets,
    execute: async (intent, secret, key, context) => {
      context.commit(() => {
        saved.current = null;
        rechecked.current = null;
        setNeedsSecret(secret !== undefined);
        setReceipt(null);
        if (secret !== undefined) clearSecrets?.();
      });
      const result = await executeConnection(intent, secret, key, context.signal);
      context.commit(() => {
        saved.current = result;
      });
      return result;
    },
    reconcile: async (intent, error, context) => {
      let result = saved.current;
      if (error && isOutcomeUnknown(error) && context.operationKey) {
        const status = await resourceStatus(context.operationKey, context.signal);
        context.commit(() => {
          setReceipt(status);
          rechecked.current = { status, context };
        });
        result = await readConnectionResult(intent, status, context.signal);
      }
      await client.invalidateQueries({
        queryKey: coreKeys.account(accountId),
        refetchType: options.deferRefetch?.() ? 'none' : 'active',
      });
      context.assertCurrent();
      if (result) {
        context.commit(() => onSaved(result));
        if (error) return { operationConfirmed: true };
      }
    },
  });
  const retryOriginal = async (secret?: string) => {
    const intent = operation.variables;
    if (retrying.current || !intent || operation.outcome !== 'unknown' || (needsSecret && !secret))
      return;
    retrying.current = true;
    try {
      rechecked.current = null;
      await operation.check();
      const latest = rechecked.current as {
        status: ResourceStatus;
        context: OperationContext;
      } | null;
      if (latest?.status.status !== 'not_recorded' || !latest.context.isCurrent()) return;
      await operation.run(intent, needsSecret ? secret : undefined);
    } finally {
      retrying.current = false;
    }
  };
  return { ...operation, receipt, needsSecret, retryOriginal };
}

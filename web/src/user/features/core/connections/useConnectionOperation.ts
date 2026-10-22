import { useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
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
) {
  const client = useQueryClient();
  const saved = useRef<ConnectionResult | null>(null);
  const [receipt, setReceipt] = useState<ResourceStatus | null>(null);
  const operation = useOperation<ConnectionIntent, string, ConnectionResult>({
    authorityRoot: coreKeys.all,
    clearSecrets,
    execute: async (intent, secret, key, context) => {
      context.commit(() => {
        saved.current = null;
        setReceipt(null);
        clearSecrets?.();
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
        });
        result = await readConnectionResult(intent, status, context.signal);
      }
      await client.invalidateQueries({ queryKey: coreKeys.account(accountId) });
      context.assertCurrent();
      if (result) {
        context.commit(() => onSaved(result));
        if (error) return { operationConfirmed: true };
      }
    },
  });
  return { ...operation, receipt };
}

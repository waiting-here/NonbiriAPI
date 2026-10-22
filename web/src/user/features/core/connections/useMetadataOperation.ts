import { useQueryClient } from '@tanstack/react-query';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { isNotFoundError } from '@shared/query/http';
import { coreKeys } from '../queries';
import { isOutcomeUnknown } from '../request';
import { connectionWrite } from './api';

export function useMetadataOperation<T extends object>(
  accountId: string,
  path: string,
  method: 'PATCH' | 'PUT' | 'DELETE',
  read: (signal: AbortSignal) => Promise<T>,
  onSaved: () => void,
  onConfirmedRead?: (saved: T) => void,
) {
  const client = useQueryClient();
  return useRetainedOperation<Record<string, unknown>, void>(
    async (input, key, context) => {
      await connectionWrite(path, method, input, key, () => undefined, context.signal);
    },
    async (input, error, context) => {
      let confirmed = !error;
      if (error && isOutcomeUnknown(error) && context.operationKey) {
        try {
          await connectionWrite(
            path,
            method,
            input,
            context.operationKey,
            () => undefined,
            context.signal,
          );
          confirmed = true;
        } catch (caught) {
          if (method !== 'DELETE' || !isNotFoundError(caught)) throw caught;
          try {
            await read(context.signal);
          } catch (readError) {
            if (isNotFoundError(readError)) confirmed = true;
            else throw readError;
          }
        }
      }
      const saved =
        confirmed && method !== 'DELETE' && (error || onConfirmedRead)
          ? await read(context.signal)
          : undefined;
      await client.invalidateQueries({ queryKey: coreKeys.account(accountId) });
      if (confirmed) {
        context.commit(() => {
          if (saved) onConfirmedRead?.(saved);
          onSaved();
        });
        if (error) return { operationConfirmed: true };
      }
    },
    coreKeys.all,
  );
}

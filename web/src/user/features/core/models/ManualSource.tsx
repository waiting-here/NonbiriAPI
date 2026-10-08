import { OutcomeNote } from '@shared/components/ui';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { createManualEntries } from '../api';
import { useCoreCopy } from '../copy';
import { coreKeys, invalidateResourceDependents } from '../queries';
import { isOutcomeUnknown } from '../request';
import { readResourceResult, resourceStatus } from '../resourceOperation';
import { useModelText } from './copy';

export function ManualSource({
  accountId,
  endpointId,
  keyId,
  onCreated,
  onLock,
  onEdit,
}: {
  accountId: string;
  endpointId: string;
  keyId: string;
  onCreated: (upstreamModel: string) => void;
  onLock: (locked: boolean) => void;
  onEdit: () => void;
}) {
  const { t } = useCoreCopy();
  const text = useModelText();
  const queryClient = useQueryClient();
  const [upstreamModel, setUpstreamModel] = useState('');
  const [provider, setProvider] = useState('');
  type Intent = { upstream_model_id: string; provider: string };
  const retryAllowed = useRef(false);
  const operation = useRetainedOperation<Intent, void>(
    async (input, key, context) => {
      const response = await createManualEntries(
        endpointId,
        keyId,
        [input],
        { idempotencyKey: key, actionId: key },
        context.signal,
      );
      context.commit(() => {
        onCreated(response.entries[0]!.upstream_model_id);
      });
    },
    async (input, error, context) => {
      let confirmed = false;
      retryAllowed.current = false;
      if (error && isOutcomeUnknown(error) && context.operationKey) {
        const status = await resourceStatus(context.operationKey, context.signal);
        context.assertCurrent();
        retryAllowed.current = status.status === 'not_recorded';
        confirmed = Boolean(
          await readResourceResult(
            { kind: 'manual', endpointId, keyId, entries: [input] },
            status,
            context.signal,
          ),
        );
      }
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: coreKeys.catalogRoot(accountId, endpointId, keyId),
        }),
        queryClient.invalidateQueries({
          queryKey: coreKeys.endpointRoutingRoot(accountId, endpointId),
        }),
        invalidateResourceDependents(queryClient, accountId, { endpointId }),
      ]);
      context.assertCurrent();
      const failed = queryClient
        .getQueryCache()
        .findAll({ queryKey: coreKeys.catalogRoot(accountId, endpointId, keyId) })
        .find((query) => query.state.status === 'error');
      if (failed) throw failed.state.error;
      if (confirmed) {
        context.commit(() => {
          onCreated(input.upstream_model_id);
        });
        return { operationConfirmed: true };
      }
    },
    ['user', 'core'],
    {
      clearSecrets: () => {
        setUpstreamModel('');
        setProvider('');
      },
    },
  );
  const busy = operation.isPending;
  const outcome =
    operation.outcome === 'unknown' || operation.outcome === 'conflict'
      ? operation.outcome
      : operation.outcome === 'failed'
        ? 'error'
        : null;
  const hasAttempt = busy || operation.outcome === 'unknown';
  const edit = () => {
    if (operation.isSuccess) operation.reset();
    onEdit();
  };
  const create = async () => {
    if (operation.outcome === 'refresh-failed') {
      await operation.refresh();
      return;
    }
    if (operation.outcome === 'unknown' && operation.variables) {
      await operation.check();
      if (retryAllowed.current)
        await operation.mutateAsync(operation.variables).catch(() => undefined);
      return;
    }
    await operation
      .mutateAsync({ upstream_model_id: upstreamModel, provider })
      .catch(() => undefined);
  };

  useEffect(() => {
    onLock(hasAttempt);
    return () => onLock(false);
  }, [hasAttempt, onLock]);
  return (
    <div className="core-form model-manual-entry">
      <p className="core-muted">{text('manualHelp')}</p>
      <label>
        <span>{text('modelName')}</span>
        <input
          value={upstreamModel}
          maxLength={512}
          disabled={hasAttempt}
          onChange={(event) => {
            edit();
            setUpstreamModel(event.target.value);
          }}
        />
      </label>
      <label>
        <span>{t('endpoints.manualProvider')}</span>
        <input
          value={provider}
          maxLength={128}
          disabled={hasAttempt}
          onChange={(event) => {
            edit();
            setProvider(event.target.value);
          }}
        />
      </label>
      <OutcomeNote
        busy={busy}
        outcome={
          operation.outcome === 'refresh-failed'
            ? { kind: 'savedRefreshFailed', recheck: () => void operation.refresh() }
            : outcome === 'unknown'
              ? { kind: 'unknown', recheck: () => void operation.check() }
              : outcome === 'conflict'
                ? { kind: 'conflict', reload: () => void operation.check() }
                : outcome === 'error'
                  ? { kind: 'failed', message: t('common.fixedFailure') }
                  : operation.isSuccess
                    ? { kind: 'saved' }
                    : { kind: 'idle' }
        }
      />
      <button
        type="button"
        className="nb-btn nb-btn--secondary"
        disabled={busy || !keyId || (!hasAttempt && !upstreamModel)}
        onClick={() => void create()}
      >
        {busy ? t('common.working') : hasAttempt ? t('common.reconcile') : t('endpoints.manualAdd')}
      </button>
    </div>
  );
}

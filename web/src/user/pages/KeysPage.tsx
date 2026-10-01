import { PersonalAutomationGuide } from '../features/core/PersonalAutomationGuide';
import { useEffect, useReducer, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { PageHeader } from '@shared/components/States';
import { useOperation } from '@shared/operations/useOperation';
import { regenerateCallerKey } from '../features/core/api';
import { CoreErrorPanel, CoreLoading, CoreTime, CoreUserGate } from '../features/core/components';
import { useCoreCopy } from '../features/core/copy';
import { coreKeys, coreSessionMatchesAccount, useCallerKey } from '../features/core/queries';
import { createOperationIdentity, isConflict, isOutcomeUnknown } from '../features/core/request';
import {
  callerKeyMachineReducer,
  initialCallerKeyMachineState,
  type CallerKeyReveal,
} from '../features/core/stateMachines';
import type { CallerKeyAuthority } from '../features/core/types';
import '../features/core/core.css';

function pageInstanceIdentity(): string {
  return createOperationIdentity().actionId;
}

async function copyCallerKeySecret(secret: string): Promise<boolean> {
  if (!secret || typeof navigator === 'undefined') return false;
  try {
    const clipboard = navigator.clipboard;
    if (!clipboard?.writeText) return false;
    await clipboard.writeText(secret);
    return true;
  } catch {
    return false;
  }
}

type SecretCopyResult = {
  actionId: string;
  status: 'copied' | 'failed';
};

export function CallerKeyPanel({ accountId }: { accountId: string }) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const authority = useCallerKey(accountId);
  const [pageInstanceId] = useState(pageInstanceIdentity);
  const [state, dispatch] = useReducer(callerKeyMachineReducer, undefined, () =>
    initialCallerKeyMachineState(accountId, pageInstanceId),
  );
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [copyResult, setCopyResult] = useState<SecretCopyResult | null>(null);
  const revealActionRef = useRef<string | null>(null);
  const copyAttemptRef = useRef(0);

  const clearCopyFeedback = () => {
    copyAttemptRef.current += 1;
    revealActionRef.current = null;
    setCopyResult(null);
  };

  const discardStaleMutation = () => {
    dispatch({ type: 'boundary', accountId, pageInstanceId });
    setConfirmOpen(false);
    clearCopyFeedback();
  };

  useEffect(() => {
    dispatch({ type: 'boundary', accountId, pageInstanceId });
    let active = true;
    queueMicrotask(() => {
      if (!active) return;
      setConfirmOpen(false);
      clearCopyFeedback();
    });
    return () => {
      active = false;
    };
  }, [accountId, pageInstanceId]);

  useEffect(() => {
    if (authority.data) {
      dispatch({ type: 'read-success', accountId, pageInstanceId, authority: authority.data });
    }
  }, [accountId, authority.data, pageInstanceId]);

  useEffect(() => {
    if (authority.error) {
      dispatch({
        type: 'read-error',
        accountId,
        pageInstanceId,
        message: t('common.errorBody'),
      });
    }
  }, [accountId, authority.error, pageInstanceId, t]);

  useEffect(() => {
    revealActionRef.current = state.reveal?.actionId ?? null;
    copyAttemptRef.current += 1;
  }, [state.reveal?.actionId]);

  useEffect(
    () => () => {
      revealActionRef.current = null;
      copyAttemptRef.current += 1;
    },
    [],
  );

  const regeneration = useOperation<{ expectedGeneration: string; actionId: string }>({
    authorityRoot: ['user', 'core'],
    clearSecrets: discardStaleMutation,
    execute: async ({ expectedGeneration, actionId }, _secret, _key, context) => {
      dispatch({
        type: 'regenerate-start',
        accountId,
        pageInstanceId,
        actionId,
        expectedGeneration,
      });
      try {
        const result = await regenerateCallerKey(expectedGeneration, context.signal);
        context.assertCurrent();
        let cacheWasStale = false;
        queryClient.setQueryData<CallerKeyAuthority | undefined>(
          coreKeys.callerKey(accountId),
          (cached) => {
            if (
              cached &&
              cached.generation !== expectedGeneration &&
              cached.generation !== result.metadata.generation
            ) {
              cacheWasStale = true;
              return cached;
            }
            return {
              generation: result.metadata.generation,
              metadata: result.metadata,
            };
          },
        );
        if (cacheWasStale) {
          dispatch({
            type: 'regenerate-failure',
            accountId,
            pageInstanceId,
            actionId,
            expectedGeneration,
            outcome: 'conflict',
          });
          dispatch({ type: 'read-start', accountId, pageInstanceId });
          if (coreSessionMatchesAccount(queryClient, accountId)) void authority.refetch();
          return;
        }
        dispatch({
          type: 'regenerate-success',
          accountId,
          pageInstanceId,
          actionId,
          expectedGeneration,
          secret: result.secret,
          metadata: result.metadata,
        });
        setCopyResult(null);
        setConfirmOpen(false);
        if (coreSessionMatchesAccount(queryClient, accountId)) void authority.refetch();
      } catch (error) {
        context.assertCurrent();
        const outcome = isConflict(error)
          ? 'conflict'
          : isOutcomeUnknown(error)
            ? 'unknown'
            : 'error';
        dispatch({
          type: 'regenerate-failure',
          accountId,
          pageInstanceId,
          actionId,
          expectedGeneration,
          outcome,
          message: t('common.fixedFailure'),
        });
        if (outcome === 'conflict' || outcome === 'unknown') {
          dispatch({ type: 'read-start', accountId, pageInstanceId });
          if (coreSessionMatchesAccount(queryClient, accountId)) void authority.refetch();
        }
        throw error;
      }
    },
    reconcile: () => undefined,
  });

  const executeRegeneration = () => {
    const current = state.authority;
    if (!current || state.reveal || regeneration.isPending) return;
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    void regeneration
      .run({
        expectedGeneration: current.generation,
        actionId: createOperationIdentity().actionId,
      })
      .catch(() => undefined);
  };

  const trigger = () => {
    if (!state.authority || state.reveal || state.mutation === 'pending') return;
    if (state.authority.metadata) setConfirmOpen(true);
    else void executeRegeneration();
  };

  const copying = useOperation<{ actionId: string; generation: string; attempt: number }, string>({
    authorityRoot: ['user', 'core'],
    execute: async (intent, secret, _key, context) => {
      const ok = await copyCallerKeySecret(secret ?? '');
      context.commit(() => {
        const current = queryClient.getQueryData<CallerKeyAuthority>(coreKeys.callerKey(accountId));
        if (
          revealActionRef.current !== intent.actionId ||
          copyAttemptRef.current !== intent.attempt ||
          current?.generation !== intent.generation
        )
          return;
        setCopyResult({ actionId: intent.actionId, status: ok ? 'copied' : 'failed' });
      });
    },
    reconcile: () => undefined,
  });
  const closeReveal = () => {
    copying.cancel();
    dispatch({ type: 'close-reveal', accountId, pageInstanceId });
    clearCopyFeedback();
  };
  const copyReveal = (reveal: CallerKeyReveal) => {
    const current = queryClient.getQueryData<CallerKeyAuthority>(coreKeys.callerKey(accountId));
    if (
      !coreSessionMatchesAccount(queryClient, accountId) ||
      current?.generation !== reveal.generation ||
      copying.isPending
    )
      return;
    const attempt = copyAttemptRef.current + 1;
    copyAttemptRef.current = attempt;
    setCopyResult(null);
    void copying
      .run({ actionId: reveal.actionId, generation: reveal.generation, attempt }, reveal.secret)
      .catch(() => undefined);
  };

  const metadata = state.authority?.metadata ?? null;
  const actionDisabled =
    !state.authority ||
    state.readState !== 'ready' ||
    state.mutation === 'pending' ||
    Boolean(state.reveal);

  return (
    <div className="page core-page core-stack">
      <PageHeader icon="keys" title={t('keys.title')} description={t('keys.description')} />

      {state.reveal ? (
        <section className="core-card core-secret-panel" aria-live="polite">
          <div className="core-card__header">
            <h2>{t('keys.oneTimeTitle')}</h2>
          </div>
          <p>{t('keys.oneTimeBody')}</p>
          <code className="core-secret-value">{state.reveal.secret}</code>
          {state.readState === 'error' ? (
            <p className="core-inline-warning">{t('keys.refreshError')}</p>
          ) : null}
          <div className="core-form-actions">
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => {
                if (state.reveal) copyReveal(state.reveal);
              }}
            >
              {copyResult?.actionId === state.reveal.actionId && copyResult.status === 'copied'
                ? t('common.copied')
                : t('common.copy')}
            </button>
            <button type="button" className="btn btn-danger" onClick={closeReveal}>
              {t('keys.closeReveal')}
            </button>
          </div>
          {copyResult?.actionId === state.reveal.actionId && copyResult.status === 'failed' ? (
            <p role="status" className="core-inline-warning">
              {t('common.copyFailed')}
            </p>
          ) : null}
        </section>
      ) : null}

      <section className="core-card">
        <div className="core-card__header">
          <h2>{t('keys.metadataTitle')}</h2>
          <button
            type="button"
            className="btn btn-primary"
            disabled={actionDisabled}
            onClick={trigger}
          >
            {state.mutation === 'pending'
              ? t('common.working')
              : metadata
                ? t('keys.rotate')
                : t('keys.generate')}
          </button>
        </div>
        {authority.isPending && !state.authority ? (
          <CoreLoading compact />
        ) : authority.error && !state.authority ? (
          <CoreErrorPanel
            error={authority.error}
            compact
            onRetry={() => void authority.refetch()}
          />
        ) : state.authority && metadata ? (
          <dl className="core-detail-list">
            <div>
              <dt>{t('keys.display')}</dt>
              <dd>
                <code className="core-mono">{metadata.display}</code>
              </dd>
            </div>
            <div>
              <dt>{t('common.created')}</dt>
              <dd>
                <CoreTime value={metadata.created_at} />
              </dd>
            </div>
            <div>
              <dt>{t('common.updated')}</dt>
              <dd>
                <CoreTime value={metadata.updated_at} />
              </dd>
            </div>
          </dl>
        ) : state.authority ? (
          <div className="core-state core-state--empty">
            <div>
              <strong>{t('keys.noKeyTitle')}</strong>
              <p>{t('keys.noKeyBody')}</p>
            </div>
          </div>
        ) : null}
        {state.mutation === 'conflict' ? (
          <p className="core-inline-warning">{t('common.conflict')}</p>
        ) : null}
        {state.mutation === 'unknown' ? (
          <p className="core-inline-warning">{t('common.outcomeUnknown')}</p>
        ) : null}
        {state.mutation === 'error' ? (
          <p className="core-inline-error">{t('common.fixedFailure')}</p>
        ) : null}
      </section>

      <ConfirmDialog
        open={confirmOpen}
        title={t('keys.rotateTitle')}
        description={t('keys.rotateBody')}
        confirmLabel={t('keys.rotate')}
        danger
        busy={state.mutation === 'pending'}
        onCancel={() => {
          if (state.mutation !== 'pending') setConfirmOpen(false);
        }}
        onConfirm={() => void executeRegeneration()}
      />
    </div>
  );
}

export function KeysPage() {
  return (
    <CoreUserGate>
      {(user) => (
        <>
          <CallerKeyPanel key={user.id} accountId={user.id} />
          <div className="page core-page">
            <PersonalAutomationGuide />
          </div>
        </>
      )}
    </CoreUserGate>
  );
}

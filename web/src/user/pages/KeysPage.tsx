import { useEffect, useReducer, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { PageHeader } from '@shared/components/States';
import { copyText } from '@shared/utils/clipboard';
import { regenerateCallerKey } from '../features/core/api';
import {
  CoreErrorPanel,
  CoreLoading,
  CoreTime,
  CoreUserGate,
  SafeCopyValue,
} from '../features/core/components';
import { useCoreCopy } from '../features/core/copy';
import { coreKeys, coreSessionMatchesAccount, useCallerKey } from '../features/core/queries';
import { createOperationIdentity, isConflict, isOutcomeUnknown } from '../features/core/request';
import {
  callerKeyMachineReducer,
  initialCallerKeyMachineState,
} from '../features/core/stateMachines';
import '../features/core/core.css';

function pageInstanceIdentity(): string {
  return createOperationIdentity().actionId;
}

export function CallerKeyPanel({ accountId }: { accountId: string }) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const authority = useCallerKey(accountId);
  const [pageInstanceId] = useState(pageInstanceIdentity);
  const abortRef = useRef<AbortController | null>(null);
  const busyRef = useRef(false);
  const [state, dispatch] = useReducer(callerKeyMachineReducer, undefined, () =>
    initialCallerKeyMachineState(accountId, pageInstanceId),
  );
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [copied, setCopied] = useState(false);

  const discardStaleMutation = () => {
    abortRef.current?.abort();
    abortRef.current = null;
    busyRef.current = false;
    dispatch({ type: 'boundary', accountId, pageInstanceId });
    setConfirmOpen(false);
    setCopied(false);
  };

  useEffect(() => {
    dispatch({ type: 'boundary', accountId, pageInstanceId });
    busyRef.current = false;
    abortRef.current?.abort();
    abortRef.current = null;
    let active = true;
    queueMicrotask(() => {
      if (!active) return;
      setConfirmOpen(false);
      setCopied(false);
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

  useEffect(
    () => () => {
      abortRef.current?.abort();
      busyRef.current = false;
    },
    [],
  );

  const executeRegeneration = async () => {
    const current = state.authority;
    if (!current || state.reveal || busyRef.current) return;
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    const actionId = createOperationIdentity().actionId;
    const expectedGeneration = current.generation;
    const controller = new AbortController();
    abortRef.current?.abort();
    abortRef.current = controller;
    busyRef.current = true;
    dispatch({
      type: 'regenerate-start',
      accountId,
      pageInstanceId,
      actionId,
      expectedGeneration,
    });
    try {
      const result = await regenerateCallerKey(expectedGeneration, controller.signal);
      if (controller.signal.aborted || abortRef.current !== controller) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
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
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      queryClient.setQueryData(coreKeys.callerKey(accountId), {
        generation: result.metadata.generation,
        metadata: result.metadata,
      });
      setCopied(false);
      setConfirmOpen(false);
      if (coreSessionMatchesAccount(queryClient, accountId)) void authority.refetch();
    } catch (error) {
      if (controller.signal.aborted || abortRef.current !== controller) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
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
    } finally {
      if (abortRef.current === controller) {
        abortRef.current = null;
        busyRef.current = false;
      }
    }
  };

  const trigger = () => {
    if (!state.authority || state.reveal || state.mutation === 'pending') return;
    if (state.authority.metadata) setConfirmOpen(true);
    else void executeRegeneration();
  };

  const closeReveal = () => {
    dispatch({ type: 'close-reveal', accountId, pageInstanceId });
    setCopied(false);
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
                void copyText(state.reveal?.secret ?? '').then((ok) => setCopied(ok));
              }}
            >
              {copied ? t('common.copied') : t('common.copy')}
            </button>
            <button type="button" className="btn btn-danger" onClick={closeReveal}>
              {t('keys.closeReveal')}
            </button>
          </div>
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
                <SafeCopyValue value={metadata.display} label={t('keys.display')} />
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
    <CoreUserGate>{(user) => <CallerKeyPanel key={user.id} accountId={user.id} />}</CoreUserGate>
  );
}

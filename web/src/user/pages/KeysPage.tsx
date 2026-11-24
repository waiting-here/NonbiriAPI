import { copyText } from '@shared/utils/clipboard';
import { CopyValue } from '@shared/components/CopyValue';
import { Fold, OutcomeNote, PanelBody, PanelHead } from '@shared/components/ui';
import { useRegisteredCopy } from '@shared/i18n/useRegisteredCopy';
import { useLocation } from 'react-router';
import { ApiAddressCopy, apiAddress, markOnboarding } from '../features/core/onboarding';
import { PersonalAutomationGuide } from '../features/core/PersonalAutomationGuide';
import { ClientModelField } from '../features/core/ClientModelField';
import { useEffect, useReducer, useRef, useState, type ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { PageHeader } from '@shared/components/States';
import { useOperation } from '@shared/operations/useOperation';
import { regenerateCallerKey } from '../features/core/api';
import { CoreErrorPanel, CoreLoading, CoreTime, CoreUserGate } from '../features/core/components';
import { useCoreCopy } from '../features/core/copy';
import { coreKeys, coreSessionMatchesAccount, useCallerKey } from '../features/core/queries';
import {
  callerSafeError,
  createOperationIdentity,
  isConflict,
  isOutcomeUnknown,
} from '../features/core/request';
import {
  callerKeyMachineReducer,
  initialCallerKeyMachineState,
  type CallerKeyReveal,
} from '../features/core/stateMachines';
import type { CallerKeyAuthority } from '../features/core/types';
import '../features/core/core.css';
import '../features/core/home.css';
import '../features/core/api-access.css';

const pageCopyKeys = {
  apiAddress: 'user.core.keys.apiAddress',
  clientHelp: 'user.core.keys.clientHelp',
  clientTitle: 'user.core.keys.clientTitle',
  savedKey: 'user.core.keys.savedKey',
  curlTitle: 'user.core.keys.curlTitle',
  curlHelp: 'user.core.keys.curlHelp',
  curlPlaceholder: 'user.core.keys.curlPlaceholder',
  curlResult: 'user.core.keys.curlResult',
} as const;

function pageInstanceIdentity(): string {
  return createOperationIdentity().actionId;
}

type SecretCopyResult = {
  actionId: string;
  status: 'copied' | 'failed';
};

function AccessCheck() {
  const { t } = useRegisteredCopy(pageCopyKeys);
  const modelsUrl = (apiAddress() + '/models').replaceAll("'", "'\\''");
  const curl = `curl -i -H "Authorization: Bearer $NONBIRI_KEY" '${modelsUrl}'`;
  return (
    <Fold lazy title={t('curlTitle')} summary={t('curlHelp')}>
      <div className="api-access-check">
        <p>{t('curlPlaceholder')}</p>
        <CopyValue value={curl} label={t('curlTitle')} variant="secondary" />
        <p className="nb-small nb-muted">{t('curlResult')}</p>
      </div>
    </Fold>
  );
}

export function CallerKeyPanel({
  accountId,
  children,
}: {
  accountId: string;
  /** Extra guides shown after the client instructions. */
  children?: ReactNode;
}) {
  const { t } = useCoreCopy();
  const { t: text } = useRegisteredCopy(pageCopyKeys);
  const location = useLocation();
  const clientRef = useRef<HTMLElement>(null);
  const revealRef = useRef<HTMLElement>(null);
  const queryClient = useQueryClient();
  const authority = useCallerKey(accountId);
  const [pageInstanceId] = useState(pageInstanceIdentity);
  const [state, dispatch] = useReducer(callerKeyMachineReducer, undefined, () =>
    initialCallerKeyMachineState(accountId, pageInstanceId),
  );
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [discardId, setDiscardId] = useState<string | null>(null);
  const [copyResult, setCopyResult] = useState<SecretCopyResult | null>(null);
  const revealActionRef = useRef<string | null>(null);
  const copyAttemptRef = useRef(0);

  useEffect(() => {
    if (location.hash !== '#client') return;
    markOnboarding(accountId, 'client');
    clientRef.current?.scrollIntoView?.({ block: 'start' });
  }, [location.hash, accountId]);

  const clearCopyFeedback = () => {
    setDiscardId(null);
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
    if (state.reveal?.actionId) revealRef.current?.focus();
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
          message: callerSafeError(error) || t('common.fixedFailure'),
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
      const ok = await copyText(secret ?? '', { fallbackOnRejection: false });
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
    <div className="page core-page core-stack api-access-page">
      <PageHeader icon="keys" title={t('keys.title')} description={t('keys.description')} />

      <section
        className="nb-panel api-client"
        id="client"
        aria-label={text('clientTitle')}
        ref={clientRef}
      >
        <PanelHead title={text('clientTitle')} description={text('clientHelp')} />
        <PanelBody>
          <div className="api-client-row">
            <h3>{text('apiAddress')}</h3>
            <div className="api-client-row__value">
              <ApiAddressCopy accountId={accountId} />
            </div>
          </div>

          <div className="api-client-row">
            <h3>{t('keys.metadataTitle')}</h3>
            <div className="api-client-row__value">
              <div className="api-key-summary">
                {authority.isPending && !state.authority ? (
                  <CoreLoading compact />
                ) : authority.error && !state.authority ? (
                  <CoreErrorPanel
                    error={authority.error}
                    compact
                    onRetry={() => void authority.refetch()}
                  />
                ) : metadata ? (
                  <div className="nb-stack nb-stack--sm">
                    {!state.reveal ? <p className="nb-small nb-muted">{text('savedKey')}</p> : null}
                    <dl className="api-key-facts">
                      <div>
                        <dt>{t('keys.display')}</dt>
                        <dd>
                          <code>{metadata.display}</code>
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
                  </div>
                ) : state.authority ? (
                  <div className="nb-stack nb-stack--xs">
                    <strong>{t('keys.noKeyTitle')}</strong>
                    <p className="nb-small nb-muted">{t('keys.noKeyBody')}</p>
                  </div>
                ) : null}
                <button
                  type="button"
                  className={metadata ? 'nb-btn nb-btn--secondary' : 'nb-btn nb-btn--primary'}
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

              {state.reveal ? (
                <section
                  className="core-secret-panel api-secret-panel"
                  aria-labelledby="api-secret-title"
                  aria-live="polite"
                  tabIndex={-1}
                  ref={revealRef}
                >
                  <h4 id="api-secret-title">{t('keys.oneTimeTitle')}</h4>
                  <p>{t('keys.oneTimeBody')}</p>
                  <code className="core-secret-value">{state.reveal.secret}</code>
                  {state.readState === 'error' ? (
                    <p className="core-inline-warning">{t('keys.refreshError')}</p>
                  ) : null}
                  <div className="nb-actions">
                    <button
                      type="button"
                      className="nb-btn nb-btn--primary"
                      onClick={() => {
                        if (state.reveal) copyReveal(state.reveal);
                      }}
                    >
                      {copyResult?.actionId === state.reveal.actionId &&
                      copyResult.status === 'copied'
                        ? t('common.copied')
                        : t('common.copy')}
                    </button>
                    <button
                      type="button"
                      className="nb-btn nb-btn--secondary"
                      onClick={() => {
                        if (
                          copyResult?.actionId === state.reveal?.actionId &&
                          copyResult?.status === 'copied'
                        )
                          closeReveal();
                        else setDiscardId(state.reveal?.actionId ?? null);
                      }}
                    >
                      {t('keys.closeReveal')}
                    </button>
                  </div>
                  {copyResult?.actionId === state.reveal.actionId &&
                  copyResult.status === 'failed' ? (
                    <p role="status" className="core-inline-warning">
                      {t('common.copyFailed')}
                    </p>
                  ) : null}
                </section>
              ) : null}

              <OutcomeNote
                busy={regeneration.isPending || authority.isFetching}
                outcome={
                  state.mutation === 'conflict'
                    ? { kind: 'conflict', reload: () => void authority.refetch() }
                    : state.mutation === 'unknown'
                      ? {
                          kind: 'unknown',
                          oneTimeSecret: true,
                          recheck: () => void authority.refetch(),
                        }
                      : state.mutation === 'error'
                        ? {
                            kind: 'failed',
                            message: state.mutationError || t('common.fixedFailure'),
                          }
                        : { kind: 'idle' }
                }
              />
            </div>
          </div>

          <ClientModelField key={accountId} accountId={accountId} />
        </PanelBody>
      </section>

      <div className="api-reference">
        <AccessCheck />
        {children}
      </div>

      <ConfirmDialog
        open={Boolean(discardId && state.reveal?.actionId === discardId)}
        title={t('keys.closeReveal')}
        description={t('keys.oneTimeBody')}
        confirmLabel={t('keys.closeReveal')}
        danger
        onCancel={() => setDiscardId(null)}
        onConfirm={() => {
          if (state.reveal?.actionId === discardId) closeReveal();
        }}
      />
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
        <CallerKeyPanel key={user.id} accountId={user.id}>
          <PersonalAutomationGuide />
        </CallerKeyPanel>
      )}
    </CoreUserGate>
  );
}

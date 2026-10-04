import { Fold, Note, OutcomeNote } from '@shared/components/ui';
import { useRegisteredCopy } from '@shared/i18n/useRegisteredCopy';
import { Link, useLocation } from 'react-router';
import { ApiAddressCopy, apiAddress, markOnboarding } from '../features/core/onboarding';
import { PersonalAutomationGuide } from '../features/core/PersonalAutomationGuide';
import { useEffect, useReducer, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { PageHeader } from '@shared/components/States';
import { useOperation } from '@shared/operations/useOperation';
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
  type CallerKeyReveal,
} from '../features/core/stateMachines';
import type { CallerKeyAuthority } from '../features/core/types';
import '../features/core/core.css';
import '../features/core/home.css';

const pageCopyKeys = {
  'user.core.keys.apiAddress': 'user.core.keys.apiAddress',
  'user.core.keys.charityModels': 'user.core.keys.charityModels',
  'user.core.keys.clientHelp': 'user.core.keys.clientHelp',
  'user.core.keys.clientKey': 'user.core.keys.clientKey',
  'user.core.keys.clientTitle': 'user.core.keys.clientTitle',
  'user.core.keys.copyModel': 'user.core.keys.copyModel',
  'user.core.keys.curlHelp': 'user.core.keys.curlHelp',
  'user.core.keys.curlPlaceholder': 'user.core.keys.curlPlaceholder',
  'user.core.keys.curlTitle': 'user.core.keys.curlTitle',
  'user.core.keys.metadataTitle': 'user.core.keys.metadataTitle',
  'user.core.keys.modelName': 'user.core.keys.modelName',
  'user.core.keys.ownModels': 'user.core.keys.ownModels',
  'user.core.keys.supportedPaths': 'user.core.keys.supportedPaths',
} as const;

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

function ClientInstructions() {
  const { t } = useRegisteredCopy(pageCopyKeys);
  const location = useLocation();
  useEffect(() => {
    if (location.hash === '#client') markOnboarding('client');
  }, [location.hash]);
  const curl = `curl "${apiAddress()}/chat/completions" \\
  -H "Authorization: Bearer $NONBIRI_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"my/model","messages":[{"role":"user","content":"Hello"}]}'`;
  return (
    <>
      <section className="nb-panel" id="client" aria-labelledby="client-title">
        <div className="nb-panel__head">
          <div>
            <h2 id="client-title">{t('user.core.keys.clientTitle')}</h2>
            <p>{t('user.core.keys.clientHelp')}</p>
          </div>
        </div>
        <div className="nb-panel__body">
          <dl className="nb-facts nb-facts--inline">
            <div>
              <dt>{t('user.core.keys.apiAddress')}</dt>
              <dd>
                <ApiAddressCopy />
              </dd>
            </div>
            <div>
              <dt>{t('user.core.keys.metadataTitle')}</dt>
              <dd>{t('user.core.keys.clientKey')}</dd>
            </div>
            <div>
              <dt>{t('user.core.keys.modelName')}</dt>
              <dd>
                <p>
                  <Link to="/charity">{t('user.core.keys.charityModels')}</Link> ·{' '}
                  <code>[公益]…</code>
                </p>
                <p>
                  <Link to="/models">{t('user.core.keys.ownModels')}</Link> · <code>my/…</code>
                </p>
                <span className="nb-small nb-muted">{t('user.core.keys.copyModel')}</span>
              </dd>
            </div>
          </dl>
          <p className="nb-small nb-muted api-paths">
            <code>/v1/chat/completions</code> · <code>/v1/embeddings</code>
            <br />
            {t('user.core.keys.supportedPaths')} <code>/v1/models</code>
          </p>
        </div>
      </section>
      <Fold title={t('user.core.keys.curlTitle')} summary={t('user.core.keys.curlHelp')}>
        <p>{t('user.core.keys.curlPlaceholder')}</p>
        <SafeCopyValue value={curl} label={t('user.core.keys.curlTitle')} />
      </Fold>
    </>
  );
}

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
    <div className="page core-page core-stack api-access-page">
      <PageHeader icon="keys" title={t('keys.title')} description={t('keys.description')} />

      {state.reveal ? (
        <section className="nb-panel core-secret-panel api-secret-panel" aria-live="polite">
          <div className="nb-panel__head">
            <h2>{t('keys.oneTimeTitle')}</h2>
          </div>
          <p>{t('keys.oneTimeBody')}</p>
          <div className="nb-copy api-secret-copy">
            <code className="core-secret-value">{state.reveal.secret}</code>
            {state.readState === 'error' ? (
              <p className="core-inline-warning">{t('keys.refreshError')}</p>
            ) : null}
            <button
              type="button"
              className="nb-btn nb-btn--primary"
              onClick={() => {
                if (state.reveal) copyReveal(state.reveal);
              }}
            >
              {copyResult?.actionId === state.reveal.actionId && copyResult.status === 'copied'
                ? t('common.copied')
                : t('common.copy')}
            </button>
          </div>
          <div className="core-form-actions">
            <button type="button" className="nb-btn nb-btn--secondary" onClick={closeReveal}>
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

      <section className="nb-panel">
        <div className="nb-panel__head">
          <h2>{t('keys.metadataTitle')}</h2>
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
        {authority.isPending && !state.authority ? (
          <CoreLoading compact />
        ) : authority.error && !state.authority ? (
          <CoreErrorPanel
            error={authority.error}
            compact
            onRetry={() => void authority.refetch()}
          />
        ) : state.authority && metadata ? (
          <dl className="nb-facts">
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
          <Note title={t('keys.noKeyTitle')}>{t('keys.noKeyBody')}</Note>
        ) : null}
        <OutcomeNote
          busy={regeneration.isPending || authority.isFetching}
          outcome={
            state.mutation === 'conflict'
              ? { kind: 'conflict', reload: () => void authority.refetch() }
              : state.mutation === 'unknown'
                ? { kind: 'unknown', oneTimeSecret: true, recheck: () => void authority.refetch() }
                : state.mutation === 'error'
                  ? { kind: 'oneTimeMissed' }
                  : { kind: 'idle' }
          }
        />
      </section>

      <ClientInstructions />

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
          <div className="page core-page api-access-page">
            <PersonalAutomationGuide />
          </div>
        </>
      )}
    </CoreUserGate>
  );
}

import { OutcomeNote } from '@shared/components/ui';
import { useResourceFilters, useResourceListScroll } from './useResourceFilters';
import { ResourceFilterBar, FilteredResourceEmpty } from './ResourceFilterControls';
import { useEffect, useReducer, useRef, useState, type FormEvent } from 'react';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { useOperation } from '@shared/operations/useOperation';
import { readEndpointKey, readResourceResult, resourceStatus } from './resourceOperation';
import { useQueryClient } from '@tanstack/react-query';
import { Link, useLocation, useNavigate } from 'react-router';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { KeyLimitFields, KeyLimitSummary } from '@shared/components/KeyRoutingLimits';
import { PageHeader } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { RequestAdaptationEditor } from '@shared/components/RequestAdaptationEditor';
import { listReturnPath } from '@shared/operations/listReturn';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { usePagePager } from '@shared/operations/usePagePager';
import { isForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import {
  getEndpoint,
  getCatalog,
  createEndpointKey,
  createManualEntries,
  deleteEndpoint,
  deleteEndpointKey,
  deleteManualEntry,
  patchEndpoint,
  patchEndpointKey,
  refreshDiscovery,
  updateManualEntry,
} from './api';
import {
  ConnectorLabel,
  CoreEmpty,
  CoreErrorPanel,
  CoreLoading,
  MutationNotice,
  CoreTime,
  DiscoveryStatus,
  SafeCopyValue,
  StatusPill,
} from './components';
import { useCoreCopy } from './copy';
import { useQuickstartCopy } from './quickstartCopy';
import {
  applyManualUpdateToCache,
  coreKeys,
  invalidateResourceDependents,
  useEndpoint,
} from './queries';
import { useNumberedCatalog, useNumberedEndpointKeys } from './numberedQueries';
import { useManualImpacts } from './manualImpacts';
import { KeyBrowseSummary } from './ResourceBrowse';
import { createOperationIdentity, isOutcomeUnknown } from './request';
import { endpointSecretDraftReducer, initialEndpointSecretDraftState } from './stateMachines';
import { validateEndpointSecret, validateManualValue, validateResourceId } from './normalizers';
import type {
  BindingReplacement,
  CatalogEntry,
  Endpoint,
  EndpointPatchInput,
  EndpointKey,
  EndpointKeyCreateInput,
  EndpointKeyPatchInput,
} from './types';

type ActionOutcome = 'conflict' | 'unknown' | 'error' | null;

function manualCatalogPagerParams(keyId: string): {
  pageParam: string;
  pageSizeParam: string;
} {
  const safeKeyId = validateResourceId(keyId, 'endpoint key id');
  return {
    pageParam: `manual_${safeKeyId}_page`,
    pageSizeParam: `manual_${safeKeyId}_page_size`,
  };
}

function OutcomeNotice({
  outcome,
  onCheck,
  busy,
  savedRefreshFailed,
}: {
  outcome: ActionOutcome;
  onCheck: () => void;
  busy?: boolean;
  savedRefreshFailed?: boolean;
}) {
  return savedRefreshFailed ? (
    <OutcomeNote outcome={{ kind: 'savedRefreshFailed', recheck: onCheck }} busy={busy} />
  ) : (
    <MutationNotice outcome={outcome} onCheck={onCheck} busy={busy} />
  );
}

function AddEndpointKeyForm({
  accountId,
  endpoint,
  onClose,
}: {
  accountId: string;
  endpoint: Endpoint;
  onClose: () => void;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const [instance] = useState(() => createOperationIdentity().actionId);
  const [draft, dispatch] = useReducer(endpointSecretDraftReducer, undefined, () =>
    initialEndpointSecretDraftState(accountId, instance),
  );
  const [note, setNote] = useState('');
  const [forceStoreFalse, setForceStoreFalse] = useState(false);
  const [maxConcurrency, setMaxConcurrency] = useState('0');
  const [maxRPM, setMaxRPM] = useState('0');
  const retryAllowed = useRef(false);
  const [needsSecret, setNeedsSecret] = useState(false);
  const { t: text } = useQuickstartCopy();
  const operation = useOperation<Omit<EndpointKeyCreateInput, 'secret'>, string, EndpointKey>({
    authorityRoot: ['user', 'core'],
    clearSecrets: () => dispatch({ type: 'cancel', accountId, pageInstanceId: instance }),
    execute: async (input, secretValue, key, context) => {
      if (!secretValue) throw new Error(t('endpoints.secretRequired'));
      context.commit(() => dispatch({ type: 'clear-secret', accountId, pageInstanceId: instance }));
      context.commit(() => setNeedsSecret(false));
      const saved = await createEndpointKey(
        endpoint.id,
        { ...input, secret: secretValue },
        { idempotencyKey: key, actionId: key },
        context.signal,
      );
      context.commit(() => dispatch({ type: 'success', accountId, pageInstanceId: instance }));
      return saved;
    },
    reconcile: async (input, error, context) => {
      let confirmed = false;
      retryAllowed.current = false;
      if (error && context.operationKey && isOutcomeUnknown(error)) {
        const status = await resourceStatus(context.operationKey, context.signal);
        context.assertCurrent();
        retryAllowed.current = status.status === 'not_recorded';
        context.commit(() => setNeedsSecret(status.status === 'not_recorded'));
        const result = await readResourceResult(
          { kind: 'key', endpointId: endpoint.id, input },
          status,
          context.signal,
        );
        if (result?.kind === 'key') {
          context.commit(() => dispatch({ type: 'success', accountId, pageInstanceId: instance }));
          confirmed = true;
        }
      }
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: coreKeys.endpointKeysRoot(accountId, endpoint.id),
        }),
        queryClient.invalidateQueries({ queryKey: coreKeys.endpoint(accountId, endpoint.id) }),
        queryClient.invalidateQueries({ queryKey: coreKeys.endpointsRoot(accountId) }),
        invalidateResourceDependents(queryClient, accountId, { endpointId: endpoint.id }),
      ]);
      context.assertCurrent();
      if (!error || confirmed) context.commit(onClose);
      if (confirmed) return { operationConfirmed: true };
    },
  });
  const busy = operation.isPending,
    hasAttempt = busy || operation.outcome === 'unknown';
  const outcome: ActionOutcome =
    operation.outcome === 'unknown' || operation.outcome === 'conflict'
      ? operation.outcome
      : operation.outcome === 'failed'
        ? 'error'
        : null;
  const close = () => {
    operation.cancel();
    onClose();
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (operation.isSuccess) {
      await operation.refresh();
      return;
    }
    if (operation.outcome === 'unknown') {
      await operation.check();
      if (!retryAllowed.current) return;
    }
    try {
      validateEndpointSecret(draft.secret);
      if (!draft.ownershipConfirmed) throw new Error('ownership');
    } catch {
      dispatch({
        type: 'local-error',
        accountId,
        pageInstanceId: instance,
        message: t('endpoints.secretRequired'),
      });
      return;
    }
    const input =
      operation.outcome === 'unknown' && operation.variables
        ? operation.variables
        : {
            note,
            enabled: true,
            force_store_false: endpoint.connector_type === 'openai-compatible' && forceStoreFalse,
            ownership_confirmed: true as const,
            max_concurrency: Number(maxConcurrency),
            max_rpm: Number(maxRPM),
          };
    await operation.run(input, draft.secret).catch(() => undefined);
  };

  return (
    <form className="core-card core-wizard core-form" onSubmit={(event) => void submit(event)}>
      <div className="core-card__header">
        <h2>{t('endpoints.addKey')}</h2>
        <button type="button" className="btn btn-secondary" onClick={close}>
          {t('common.cancel')}
        </button>
      </div>
      <div className="core-field-grid">
        <label>
          <span>{t('endpoints.secret')}</span>
          <input
            type="password"
            autoComplete="new-password"
            maxLength={65536}
            disabled={busy}
            value={draft.secret}
            onChange={(event) =>
              dispatch({
                type: 'change',
                accountId,
                pageInstanceId: instance,
                secret: event.target.value,
              })
            }
          />
        </label>
        <label>
          <span>{t('endpoints.keyNote')}</span>
          <input
            maxLength={2048}
            disabled={hasAttempt}
            value={note}
            onChange={(event) => setNote(event.target.value)}
          />
        </label>
      </div>
      <KeyLimitFields
        concurrency={maxConcurrency}
        rpm={maxRPM}
        onConcurrency={setMaxConcurrency}
        onRPM={setMaxRPM}
        disabled={hasAttempt}
      />
      <label className="core-checkbox">
        <input
          type="checkbox"
          checked={draft.ownershipConfirmed}
          disabled={hasAttempt}
          onChange={(event) =>
            dispatch({
              type: 'ownership',
              accountId,
              pageInstanceId: instance,
              confirmed: event.target.checked,
            })
          }
        />
        <span>{t('endpoints.ownership')}</span>
      </label>
      {endpoint.connector_type === 'openai-compatible' ? (
        <label className="core-checkbox">
          <input
            type="checkbox"
            checked={forceStoreFalse}
            disabled={hasAttempt}
            onChange={(event) => setForceStoreFalse(event.target.checked)}
          />
          <span>{t('endpoints.storePolicy')}</span>
        </label>
      ) : null}
      <p className="core-inline-warning">{t('endpoints.costWarning')}</p>
      {outcome === 'unknown' && needsSecret ? <p>{text('secretAgain')}</p> : null}
      {draft.message && !outcome ? <p className="core-inline-error">{draft.message}</p> : null}
      <OutcomeNotice
        outcome={outcome}
        savedRefreshFailed={operation.outcome === 'refresh-failed'}
        onCheck={() => void (operation.isSuccess ? operation.refresh() : operation.check())}
        busy={busy}
      />
      <div className="core-form-actions">
        <span />
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy
            ? t('common.working')
            : hasAttempt
              ? text('checkResult')
              : t('endpoints.addKeyStep')}
        </button>
      </div>
    </form>
  );
}

function ManualEntryRow({
  accountId,
  endpointId,
  keyId,
  entry,
  onChanged,
}: {
  accountId: string;
  endpointId: string;
  keyId: string;
  entry: CatalogEntry;
  onChanged: () => Promise<boolean>;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  const impactQuery = useManualImpacts(
    accountId,
    endpointId,
    keyId,
    entry.upstream_model_id,
    entry.pair_revision,
    editing,
  );
  const impacts =
    !impactQuery.error && impactQuery.data?.state === 'complete' ? impactQuery.data.impacts : [];
  const impactsKnown =
    !impactQuery.error && !impactQuery.isFetching && impactQuery.data?.state === 'complete';
  const impactPager = usePagePager({
    station: 'user',
    listType: 'manual-impact',
    scopeKey: `${accountId}:${endpointId}:${keyId}:${entry.id}`,
    resetKey: entry.pair_revision,
  });
  const impactPages = Math.max(1, Math.ceil(impacts.length / impactPager.pageSize));
  const impactPage = Math.min(Number(impactPager.page), impactPages);
  const visibleImpacts = impacts.slice(
    (impactPage - 1) * impactPager.pageSize,
    impactPage * impactPager.pageSize,
  );
  const [model, setModel] = useState(entry.upstream_model_id);
  const [provider, setProvider] = useState(entry.provider);
  const [replacements, setReplacements] = useState<Record<string, string>>({});
  const replacementPayload = (): BindingReplacement[] =>
    impacts.map((impact) => ({
      binding_id: impact.bindingId,
      replacement_upstream_model_id: replacements[impact.bindingId] ?? '',
    }));
  const replacementsReady = impacts.every((impact) => {
    try {
      return Boolean(validateManualValue(replacements[impact.bindingId] ?? '', 512, false));
    } catch {
      return false;
    }
  });
  const updateNeedsReplacements = impacts.length > 0 && model !== entry.upstream_model_id;
  const updateImpactUnknown = !impactsKnown && model !== entry.upstream_model_id;

  type Intent =
    | {
        kind: 'update';
        input: {
          upstream_model_id: string;
          provider: string;
          expected_pair_revision: string;
          replacements: BindingReplacement[];
        };
      }
    | { kind: 'delete'; expectedPairRevision: string; replacements: BindingReplacement[] };
  const retryAllowed = useRef(false);
  const operation = useRetainedOperation<Intent, void>(
    async (intent, key, context) => {
      const identity = { idempotencyKey: key, actionId: key };
      if (intent.kind === 'update') {
        const response = await updateManualEntry(
          endpointId,
          keyId,
          entry.id,
          intent.input,
          identity,
          context.signal,
        );
        context.commit(() => applyManualUpdateToCache(queryClient, accountId, response));
      } else
        await deleteManualEntry(
          endpointId,
          keyId,
          entry.id,
          intent.expectedPairRevision,
          intent.replacements,
          identity,
          context.signal,
        );
      context.commit(() => setEditing(false));
    },
    async (intent, error, context) => {
      let confirmed = false;
      retryAllowed.current = false;
      if (error && isOutcomeUnknown(error)) {
        let cursor: string | undefined, current: CatalogEntry | undefined;
        do {
          const page = await getCatalog(endpointId, keyId, cursor, context.signal);
          current = page.manual_entries.find((item) => item.id === entry.id);
          cursor = page.next_cursor ?? undefined;
          if (current) break;
        } while (cursor);
        context.assertCurrent();
        if (intent.kind === 'delete') {
          confirmed = !current;
          retryAllowed.current = current?.pair_revision === intent.expectedPairRevision;
        } else if (current) {
          confirmed =
            BigInt(current.pair_revision) > BigInt(intent.input.expected_pair_revision) &&
            current.upstream_model_id === intent.input.upstream_model_id &&
            current.provider === intent.input.provider;
          retryAllowed.current = current.pair_revision === intent.input.expected_pair_revision;
        }
      }
      await invalidateResourceDependents(queryClient, accountId, { endpointId, modelIds: 'all' });
      context.assertCurrent();
      if (!(await onChanged())) throw new Error(t('common.errorBody'));
      context.assertCurrent();
      if (confirmed) {
        context.commit(() => setEditing(false));
        return { operationConfirmed: true };
      }
    },
    ['user', 'core'],
    {
      clearSecrets: () => {
        setEditing(false);
        setReplacements({});
      },
    },
  );
  const busy = operation.isPending;
  const outcome: ActionOutcome =
    operation.outcome === 'unknown' || operation.outcome === 'conflict'
      ? operation.outcome
      : operation.outcome === 'failed'
        ? 'error'
        : null;
  const retained = operation.outcome === 'unknown' ? operation.variables : undefined;
  const attemptKind = retained?.kind ?? null;
  const run = async (intent: Intent) => {
    if (operation.outcome === 'refresh-failed') {
      await operation.refresh();
      return;
    }
    if (retained) {
      await operation.check();
      if (retryAllowed.current) await operation.mutateAsync(retained).catch(() => undefined);
      return;
    }
    await operation.mutateAsync(intent).catch(() => undefined);
  };
  const update = async () => {
    if (
      busy ||
      attemptKind === 'delete' ||
      (!retained && (updateImpactUnknown || (updateNeedsReplacements && !replacementsReady)))
    )
      return;
    await run({
      kind: 'update',
      input: {
        upstream_model_id: model,
        provider,
        expected_pair_revision: entry.pair_revision,
        replacements: updateNeedsReplacements ? replacementPayload() : [],
      },
    });
  };
  const remove = async () => {
    if (
      busy ||
      attemptKind === 'update' ||
      !impactsKnown ||
      (!retained && impacts.length > 0 && !replacementsReady)
    )
      return;
    await run({
      kind: 'delete',
      expectedPairRevision: entry.pair_revision,
      replacements: replacementPayload(),
    });
  };
  useEffect(() => {
    let active = true;
    queueMicrotask(() => {
      if (active) {
        setModel(entry.upstream_model_id);
        setProvider(entry.provider);
      }
    });
    return () => {
      active = false;
    };
  }, [entry.pair_revision, entry.provider, entry.upstream_model_id]);

  if (isForbidden(impactQuery.error) || isUnauthorized(impactQuery.error))
    return (
      <li>
        <CoreErrorPanel
          compact
          error={impactQuery.error}
          onRetry={() => void impactQuery.refetch()}
        />
      </li>
    );

  return (
    <li className="core-manual-row">
      <div className="core-binding-row__top">
        <div>
          <strong className="core-mono">{entry.upstream_model_id}</strong>
          <div className="core-muted">{entry.provider || t('common.notSet')}</div>
        </div>
        <button
          type="button"
          className="btn btn-secondary"
          disabled={busy || Boolean(attemptKind)}
          onClick={() => setEditing((value) => !value)}
        >
          {editing ? t('common.close') : t('common.edit')}
        </button>
      </div>
      {editing && impacts.length > 0 ? (
        <div className="core-inline-warning">
          <p>{t('endpoints.manualImpact', { count: impacts.length })}</p>
        </div>
      ) : null}
      {editing && impactQuery.data?.state === 'too_many' ? (
        <p className="core-inline-warning" role="alert">
          {t('endpoints.manualImpactTooMany', { count: impactQuery.data.count })}
        </p>
      ) : null}
      {editing && impactQuery.isFetching ? <CoreLoading compact /> : null}
      {editing &&
      !impactsKnown &&
      !impactQuery.isFetching &&
      impactQuery.data?.state !== 'too_many' ? (
        <div className="core-inline-warning" role="alert">
          <p>{t('endpoints.manualImpactUnknown')}</p>
          <button
            className="btn btn-secondary"
            type="button"
            onClick={() => void impactQuery.refetch()}
          >
            {t('common.retry')}
          </button>
        </div>
      ) : null}
      {editing ? (
        <div className="core-form">
          <div className="core-field-grid">
            <label>
              <span>{t('endpoints.manualModel')}</span>
              <input
                value={model}
                maxLength={1024}
                disabled={Boolean(attemptKind)}
                onChange={(event) => setModel(event.target.value)}
              />
            </label>
            <label>
              <span>{t('endpoints.manualProvider')}</span>
              <input
                value={provider}
                maxLength={256}
                disabled={Boolean(attemptKind)}
                onChange={(event) => setProvider(event.target.value)}
              />
            </label>
            {visibleImpacts.map((impact) => (
              <label key={impact.bindingId}>
                <span>
                  {t('endpoints.replacement', { id: impact.bindingId, model: impact.modelName })}
                </span>
                <input
                  className="core-mono"
                  maxLength={1024}
                  disabled={Boolean(attemptKind)}
                  value={replacements[impact.bindingId] ?? ''}
                  onChange={(event) =>
                    setReplacements((current) => ({
                      ...current,
                      [impact.bindingId]: event.target.value,
                    }))
                  }
                />
              </label>
            ))}
          </div>
          {impactsKnown && impacts.length > 0 ? (
            <PagePagination
              metadata={{
                page: String(impactPage),
                page_size: impactPager.pageSize,
                total_items: String(impacts.length),
                total_pages: String(impactPages),
              }}
              requestedPage={impactPager.page}
              busy={busy}
              onPageChange={impactPager.setPage}
              onPageSizeChange={impactPager.setPageSize}
            />
          ) : null}
          <OutcomeNotice
            outcome={outcome}
            savedRefreshFailed={operation.outcome === 'refresh-failed'}
            onCheck={() => void (operation.isSuccess ? operation.refresh() : operation.check())}
            busy={busy}
          />
          <div className="core-form-actions">
            <button
              type="button"
              className="btn btn-danger"
              disabled={
                busy ||
                attemptKind === 'update' ||
                !impactsKnown ||
                (attemptKind !== 'delete' && impacts.length > 0 && !replacementsReady)
              }
              onClick={() => void remove()}
            >
              {attemptKind === 'delete' ? t('common.reconcile') : t('endpoints.deleteManual')}
            </button>
            <button
              type="button"
              className="btn btn-primary"
              disabled={
                busy ||
                attemptKind === 'delete' ||
                updateImpactUnknown ||
                (attemptKind !== 'update' && updateNeedsReplacements && !replacementsReady)
              }
              onClick={() => void update()}
            >
              {busy
                ? t('common.working')
                : attemptKind === 'update'
                  ? t('common.reconcile')
                  : t('endpoints.updateManual')}
            </button>
          </div>
        </div>
      ) : null}
    </li>
  );
}

function ManualCatalog({
  accountId,
  endpointId,
  keyId,
}: {
  accountId: string;
  endpointId: string;
  keyId: string;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const { pageParam, pageSizeParam } = manualCatalogPagerParams(keyId);
  const pager = useUrlPagePager({
    station: 'user',
    listType: 'manual-catalog',
    scopeKey: `${accountId}:${endpointId}:${keyId}`,
    scopeReady: true,
    pageParam,
    pageSizeParam,
  });
  const { page, pageSize, setPage, setPageSize } = pager;
  const catalog = useNumberedCatalog(
    accountId,
    endpointId,
    keyId,
    'manual',
    {
      page,
      pageSize,
    },
    Boolean(accountId && endpointId && keyId),
  );
  const [upstreamModel, setUpstreamModel] = useState('');
  const [provider, setProvider] = useState('');
  type Intent = { upstream_model_id: string; provider: string };
  const retryAllowed = useRef(false);
  const operation = useRetainedOperation<Intent, void>(
    async (input, key, context) => {
      await createManualEntries(
        endpointId,
        keyId,
        [input],
        { idempotencyKey: key, actionId: key },
        context.signal,
      );
      context.commit(() => {
        setUpstreamModel('');
        setProvider('');
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
          setUpstreamModel('');
          setProvider('');
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
  const outcome: ActionOutcome =
    operation.outcome === 'unknown' || operation.outcome === 'conflict'
      ? operation.outcome
      : operation.outcome === 'failed'
        ? 'error'
        : null;
  const hasAttempt = busy || operation.outcome === 'unknown';
  const refresh = async (): Promise<boolean> => {
    await Promise.all([
      queryClient.invalidateQueries({
        queryKey: coreKeys.catalogRoot(accountId, endpointId, keyId),
      }),
      invalidateResourceDependents(queryClient, accountId, { endpointId }),
    ]);
    return !queryClient
      .getQueryCache()
      .findAll({ queryKey: coreKeys.catalogRoot(accountId, endpointId, keyId) })
      .some((query) => query.state.status === 'error');
  };
  const create = async (event: FormEvent) => {
    event.preventDefault();
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

  if (isForbidden(catalog.error) || isUnauthorized(catalog.error) || isNotFoundError(catalog.error))
    return <CoreErrorPanel compact error={catalog.error} onRetry={() => void catalog.refetch()} />;

  return (
    <section className="core-card" aria-busy={busy || catalog.isFetching}>
      <div className="core-card__header">
        <div>
          <h3>{t('endpoints.manualTitle')}</h3>
          <p className="core-muted">{t('endpoints.manualAlways')}</p>
        </div>
      </div>
      <form className="core-form" onSubmit={(event) => void create(event)}>
        <div className="core-field-grid">
          <label>
            <span>{t('endpoints.manualModel')}</span>
            <input
              className="core-mono"
              value={upstreamModel}
              maxLength={1024}
              disabled={hasAttempt}
              onChange={(event) => setUpstreamModel(event.target.value)}
            />
          </label>
          <label>
            <span>{t('endpoints.manualProvider')}</span>
            <input
              value={provider}
              maxLength={256}
              disabled={hasAttempt}
              onChange={(event) => setProvider(event.target.value)}
            />
          </label>
        </div>
        <OutcomeNotice
          outcome={outcome}
          savedRefreshFailed={operation.outcome === 'refresh-failed'}
          onCheck={() => void (operation.isSuccess ? operation.refresh() : operation.check())}
          busy={busy}
        />
        <div className="core-form-actions">
          <span />
          <button
            type="submit"
            className="btn btn-primary"
            disabled={busy || (!hasAttempt && !upstreamModel)}
          >
            {busy
              ? t('common.working')
              : hasAttempt
                ? t('common.reconcile')
                : t('endpoints.manualAdd')}
          </button>
        </div>
      </form>
      {catalog.isPending && !catalog.data ? (
        <CoreLoading compact />
      ) : !catalog.data ? (
        <CoreErrorPanel
          compact
          error={catalog.error ?? new Error('The manually added model list is unavailable.')}
          onRetry={() => void catalog.refetch()}
        />
      ) : (
        <>
          {catalog.error ? (
            <CoreErrorPanel compact error={catalog.error} onRetry={() => void catalog.refetch()} />
          ) : null}
          {catalog.data.manual_entries.length === 0 ? (
            <p className="core-muted">{t('endpoints.manualEmpty')}</p>
          ) : (
            <ul className="core-manual-list">
              {catalog.data.manual_entries.map((entry) => (
                <ManualEntryRow
                  key={entry.id}
                  accountId={accountId}
                  endpointId={endpointId}
                  keyId={keyId}
                  entry={entry}
                  onChanged={refresh}
                />
              ))}
            </ul>
          )}
          <PagePagination
            metadata={catalog.data.pagination}
            requestedPage={pager.page}
            busy={busy || catalog.isFetching}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        </>
      )}
    </section>
  );
}

function EndpointKeyCard({
  accountId,
  endpoint,
  keyData,
  onRefresh,
}: {
  accountId: string;
  endpoint: Endpoint;
  keyData: EndpointKey;
  onRefresh: () => void;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const evidence = keyData.browse?.discovery;
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [note, setNote] = useState(keyData.note);
  const [editRevision, setEditRevision] = useState(keyData.revision);
  const [maxConcurrency, setMaxConcurrency] = useState(String(keyData.max_concurrency));
  const [maxRPM, setMaxRPM] = useState(String(keyData.max_rpm));
  const [manualCatalogOpen, setManualCatalogOpen] = useState(false);
  type Intent =
    | { kind: 'refresh'; evidenceRevision: string }
    | { kind: 'patch'; input: EndpointKeyPatchInput }
    | { kind: 'delete'; expectedRevision: string };
  const retryAllowed = useRef(false);
  const operation = useRetainedOperation<Intent, void>(
    async (intent, key, context) => {
      const identity = { idempotencyKey: key, actionId: key };
      if (intent.kind === 'refresh')
        await refreshDiscovery(endpoint.id, keyData.id, identity, context.signal);
      else if (intent.kind === 'patch')
        await patchEndpointKey(endpoint.id, keyData.id, intent.input, identity, context.signal);
      else
        await deleteEndpointKey(
          endpoint.id,
          keyData.id,
          intent.expectedRevision,
          identity,
          context.signal,
        );
      context.commit(() => {
        if (intent.kind === 'patch' && intent.input.note !== undefined) setEditing(false);
        if (intent.kind === 'delete') setDeleteOpen(false);
      });
    },
    async (intent, error, context) => {
      let confirmed = false;
      retryAllowed.current = false;
      if (error && isOutcomeUnknown(error)) {
        if (intent.kind === 'refresh' && context.operationKey) {
          const status = await resourceStatus(context.operationKey, context.signal);
          context.assertCurrent();
          retryAllowed.current = status.status === 'not_recorded';
          confirmed = Boolean(
            await readResourceResult(
              { kind: 'refresh', endpointId: endpoint.id, keyId: keyData.id },
              status,
              context.signal,
            ),
          );
        } else {
          try {
            const current = await readEndpointKey(endpoint.id, keyData.id, context.signal);
            context.assertCurrent();
            retryAllowed.current =
              current.revision ===
              (intent.kind === 'patch'
                ? intent.input.expected_revision
                : intent.kind === 'delete'
                  ? intent.expectedRevision
                  : '');
            if (intent.kind === 'patch')
              confirmed =
                BigInt(current.revision) > BigInt(intent.input.expected_revision) &&
                Object.entries(intent.input).every(
                  ([field, value]) =>
                    field === 'expected_revision' || current[field as keyof EndpointKey] === value,
                );
          } catch (caught) {
            if (intent.kind === 'delete' && isNotFoundError(caught)) confirmed = true;
            else throw caught;
          }
        }
      }
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: coreKeys.endpointKeysRoot(accountId, endpoint.id),
        }),
        queryClient.invalidateQueries({ queryKey: coreKeys.endpoint(accountId, endpoint.id) }),
        queryClient.invalidateQueries({ queryKey: coreKeys.endpointsRoot(accountId) }),
        queryClient.invalidateQueries({
          queryKey: coreKeys.catalogRoot(accountId, endpoint.id, keyData.id),
        }),
        invalidateResourceDependents(queryClient, accountId, {
          endpointId: endpoint.id,
          ...(intent.kind === 'delete' ? { modelIds: 'all' as const, charity: true } : {}),
        }),
      ]);
      context.assertCurrent();
      const failed = queryClient
        .getQueryCache()
        .findAll({ queryKey: coreKeys.endpointKeysRoot(accountId, endpoint.id) })
        .find((query) => query.state.status === 'error');
      if (failed) throw failed.state.error;
      if (confirmed) {
        context.commit(() => {
          if (intent.kind === 'patch') setEditing(false);
          if (intent.kind === 'delete') setDeleteOpen(false);
        });
        return { operationConfirmed: true };
      }
    },
    ['user', 'core'],
    {
      clearSecrets: () => {
        setDeleteOpen(false);
        setEditing(false);
      },
    },
  );
  const busy = operation.isPending;
  const outcome: ActionOutcome =
    operation.outcome === 'unknown' || operation.outcome === 'conflict'
      ? operation.outcome
      : operation.outcome === 'failed'
        ? 'error'
        : null;
  const reconciliationRequired = operation.outcome === 'refresh-failed';
  const replayAttempt = operation.outcome === 'unknown' ? (operation.variables ?? null) : null;
  const reconcile = () => operation.refresh();
  const run = async (intent: Intent) => {
    if (reconciliationRequired) {
      await operation.refresh();
      return;
    }
    if (replayAttempt) {
      await operation.check();
      if (retryAllowed.current) await operation.mutateAsync(replayAttempt).catch(() => undefined);
      return;
    }
    await operation.mutateAsync(intent).catch(() => undefined);
  };

  const physicalAvailable =
    endpoint.enabled && keyData.enabled && keyData.suspension_state === 'none';
  const display =
    `${keyData.display_head}${keyData.display_head && keyData.display_tail ? '…' : ''}${keyData.display_tail}` ||
    t('common.notSet');

  return (
    <li className="core-key-card">
      <div className="core-key-card__top">
        <div>
          <strong>{keyData.note || t('endpoints.key')}</strong>
          <div className="core-muted">{t('endpoints.keyIdentifier')}</div>
          <div>
            <SafeCopyValue value={display} label={t('endpoints.keyIdentifier')} />
          </div>
        </div>
        {!endpoint.enabled ? (
          <StatusPill tone="warning">{t('browse.state.endpoint_disabled')}</StatusPill>
        ) : keyData.suspension_state === 'security_processing' ? (
          <StatusPill tone="danger">{t('endpoints.securityProcessing')}</StatusPill>
        ) : (
          <StatusPill tone={keyData.enabled ? 'success' : 'neutral'}>
            {keyData.enabled ? t('common.enabled') : t('common.disabled')}
          </StatusPill>
        )}
      </div>
      <KeyLimitSummary concurrency={keyData.max_concurrency} rpm={keyData.max_rpm} />
      <p className="core-muted">{t('browse.personalLimits')}</p>
      <dl className="core-detail-list">
        <div>
          <dt>{t('endpoints.storePolicy')}</dt>
          <dd>{keyData.force_store_false ? t('common.yes') : t('common.no')}</dd>
        </div>
        <div>
          <dt>{t('common.updated')}</dt>
          <dd>
            <CoreTime value={keyData.updated_at} />
          </dd>
        </div>
      </dl>

      <section className="core-card">
        <div className="core-card__header">
          <h3>{t('endpoints.discovery')}</h3>
        </div>
        {!evidence ? (
          <p className="core-muted">{t('common.unknown')}</p>
        ) : (
          <>
            <DiscoveryStatus evidence={evidence} />
            {evidence.observed_at !== null ? (
              <p>
                <span className="core-muted">{t('endpoints.observedAt')}: </span>
                <CoreTime value={evidence.observed_at} />
              </p>
            ) : null}
          </>
        )}
        <button
          type="button"
          className="btn btn-secondary"
          disabled={
            busy ||
            !evidence ||
            evidence.state === 'checking' ||
            reconciliationRequired ||
            !physicalAvailable ||
            Boolean(replayAttempt)
          }
          onClick={() =>
            void run({
              kind: 'refresh',
              evidenceRevision: evidence?.revision ?? '0',
            })
          }
        >
          {busy ? t('common.working') : t('endpoints.refreshDiscovery')}
        </button>
      </section>

      <KeyBrowseSummary
        accountId={accountId}
        endpointId={endpoint.id}
        keyData={keyData}
        onRefresh={onRefresh}
      />

      <details
        className="core-manual-details"
        onToggle={(event) => setManualCatalogOpen(event.currentTarget.open)}
      >
        <summary>{t('endpoints.manualTitle')}</summary>
        {manualCatalogOpen ? (
          <ManualCatalog accountId={accountId} endpointId={endpoint.id} keyId={keyData.id} />
        ) : null}
      </details>
      <OutcomeNotice
        outcome={outcome}
        savedRefreshFailed={operation.outcome === 'refresh-failed'}
        onCheck={() => void (operation.isSuccess ? operation.refresh() : operation.check())}
        busy={busy}
      />
      {reconciliationRequired ? (
        <button
          type="button"
          className="btn btn-secondary"
          disabled={busy}
          onClick={() => void reconcile()}
        >
          {t('common.reconcile')}
        </button>
      ) : null}
      {replayAttempt && (replayAttempt.kind !== 'delete' || !deleteOpen) ? (
        <button
          type="button"
          className="btn btn-secondary"
          disabled={busy || reconciliationRequired}
          onClick={() => void run(replayAttempt)}
        >
          {t('common.reconcile')}
        </button>
      ) : null}
      {editing ? (
        <form
          className="core-form"
          onSubmit={(event) => {
            event.preventDefault();
            void run({
              kind: 'patch',
              input: {
                note,
                max_concurrency: Number(maxConcurrency),
                max_rpm: Number(maxRPM),
                expected_revision: editRevision,
              },
            });
          }}
        >
          <div className="core-field-grid">
            <label>
              <span>{t('endpoints.keyNote')}</span>
              <input
                value={note}
                maxLength={2048}
                disabled={Boolean(replayAttempt)}
                onChange={(event) => setNote(event.target.value)}
              />
            </label>
          </div>
          <KeyLimitFields
            concurrency={maxConcurrency}
            rpm={maxRPM}
            onConcurrency={setMaxConcurrency}
            onRPM={setMaxRPM}
            disabled={busy || Boolean(replayAttempt)}
          />
          <div className="core-form-actions">
            <button
              type="button"
              className="btn btn-secondary"
              disabled={busy || Boolean(replayAttempt)}
              onClick={() => {
                setNote(keyData.note);
                setEditing(false);
              }}
            >
              {t('common.cancel')}
            </button>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={
                busy ||
                reconciliationRequired ||
                Boolean(replayAttempt) ||
                (note === keyData.note &&
                  Number(maxConcurrency) === keyData.max_concurrency &&
                  Number(maxRPM) === keyData.max_rpm)
              }
            >
              {t('common.save')}
            </button>
          </div>
        </form>
      ) : null}
      <div className="core-row-actions">
        <button
          type="button"
          className="btn btn-secondary"
          disabled={
            busy ||
            reconciliationRequired ||
            Boolean(replayAttempt) ||
            keyData.suspension_state !== 'none'
          }
          onClick={() => {
            setNote(keyData.note);
            setEditRevision(keyData.revision);
            setMaxConcurrency(String(keyData.max_concurrency));
            setMaxRPM(String(keyData.max_rpm));
            setEditing((value) => !value);
          }}
        >
          {t('common.edit')}
        </button>
        <button
          type="button"
          className="btn btn-secondary"
          disabled={
            busy ||
            reconciliationRequired ||
            Boolean(replayAttempt) ||
            keyData.suspension_state !== 'none'
          }
          onClick={() =>
            void run({
              kind: 'patch',
              input: { enabled: !keyData.enabled, expected_revision: keyData.revision },
            })
          }
        >
          {keyData.enabled ? t('endpoints.keyToggleOff') : t('endpoints.keyToggleOn')}
        </button>
        {endpoint.connector_type === 'openai-compatible' ? (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={
              busy ||
              reconciliationRequired ||
              Boolean(replayAttempt) ||
              keyData.suspension_state !== 'none'
            }
            onClick={() =>
              void run({
                kind: 'patch',
                input: {
                  force_store_false: !keyData.force_store_false,
                  expected_revision: keyData.revision,
                },
              })
            }
          >
            {keyData.force_store_false
              ? t('endpoints.storePolicyOff')
              : t('endpoints.storePolicyOn')}
          </button>
        ) : null}
        <button
          type="button"
          className="btn btn-danger"
          disabled={
            busy ||
            reconciliationRequired ||
            Boolean(replayAttempt) ||
            keyData.suspension_state !== 'none'
          }
          onClick={() => setDeleteOpen(true)}
        >
          {t('endpoints.deleteKey')}
        </button>
      </div>
      <ConfirmDialog
        open={deleteOpen}
        title={t('endpoints.deleteKeyTitle')}
        description={t('endpoints.deleteKeyBody')}
        confirmLabel={
          replayAttempt?.kind === 'delete' ? t('common.retrySame') : t('endpoints.deleteKey')
        }
        danger
        busy={busy}
        onCancel={() => {
          if (!busy) setDeleteOpen(false);
        }}
        onConfirm={() =>
          void run(
            replayAttempt?.kind === 'delete'
              ? replayAttempt
              : {
                  kind: 'delete',
                  expectedRevision: keyData.revision,
                },
          )
        }
      />
    </li>
  );
}

export function EndpointDetail({
  accountId,
  endpointId,
}: {
  accountId: string;
  endpointId: string;
}) {
  const { t } = useCoreCopy();
  const location = useLocation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const filters = useResourceFilters('keys', accountId, 'keys_', 'keys_page');
  const keyPager = useUrlPagePager({
    station: 'user',
    listType: 'endpoint-keys',
    scopeKey: `${accountId}:${endpointId}`,
    scopeReady: true,
    resetKey: filters.identity,
    pageParam: 'keys_page',
    pageSizeParam: 'keys_page_size',
  });
  const { page, pageSize, setPage, setPageSize } = keyPager;
  const endpoint = useEndpoint(accountId, endpointId, Boolean(accountId && endpointId));
  const keys = useNumberedEndpointKeys(
    accountId,
    endpointId,
    {
      page,
      pageSize,
    },
    Boolean(accountId && endpointId),
    filters.filters,
  );
  useResourceListScroll(accountId, Boolean(keys.data) && !keys.isFetching);
  const [addingKey, setAddingKey] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [endpointNote, setEndpointNote] = useState('');
  const returnTo = listReturnPath(location.state, '/endpoints');
  type Intent =
    { kind: 'patch'; input: EndpointPatchInput } | { kind: 'delete'; expectedRevision: string };
  const retryAllowed = useRef(false);
  const operation = useRetainedOperation<Intent, void>(
    async (intent, key, context) => {
      const identity = { idempotencyKey: key, actionId: key };
      if (intent.kind === 'patch')
        await patchEndpoint(endpointId, intent.input, identity, context.signal);
      else await deleteEndpoint(endpointId, intent.expectedRevision, identity, context.signal);
      context.commit(() => {
        if (intent.kind === 'patch') setEditing(false);
        else setDeleteOpen(false);
      });
    },
    async (intent, error, context) => {
      let confirmed = false;
      retryAllowed.current = false;
      if (error && isOutcomeUnknown(error)) {
        try {
          const current = await getEndpoint(endpointId, context.signal);
          context.assertCurrent();
          retryAllowed.current =
            current.revision ===
            (intent.kind === 'patch' ? intent.input.expected_revision : intent.expectedRevision);
          if (intent.kind === 'patch')
            confirmed =
              BigInt(current.revision) > BigInt(intent.input.expected_revision) &&
              Object.entries(intent.input).every(
                ([field, value]) =>
                  field === 'expected_revision' || current[field as keyof Endpoint] === value,
              );
        } catch (caught) {
          if (intent.kind === 'delete' && isNotFoundError(caught)) confirmed = true;
          else throw caught;
        }
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: coreKeys.endpointsRoot(accountId) }),
        invalidateResourceDependents(queryClient, accountId, {
          endpointId,
          ...(intent.kind === 'delete' ? { modelIds: 'all' as const, charity: true } : {}),
        }),
      ]);
      context.assertCurrent();
      if (intent.kind === 'delete' && (!error || confirmed)) {
        context.commit(() => {
          queryClient.removeQueries({ queryKey: coreKeys.endpoint(accountId, endpointId) });
          navigate(returnTo);
        });
      } else {
        const [current, currentKeys] = await Promise.all([endpoint.refetch(), keys.refetch()]);
        context.assertCurrent();
        if (current.error || currentKeys.error) throw current.error ?? currentKeys.error;
        if (!error || confirmed) context.commit(() => setEditing(false));
      }
      if (confirmed) return { operationConfirmed: true };
    },
    ['user', 'core'],
    {
      clearSecrets: () => {
        setDeleteOpen(false);
        setEditing(false);
        setAddingKey(false);
      },
    },
  );
  const busy = operation.isPending;
  const outcome: ActionOutcome =
    operation.outcome === 'unknown' || operation.outcome === 'conflict'
      ? operation.outcome
      : operation.outcome === 'failed'
        ? 'error'
        : null;
  const reconciliationRequired = operation.outcome === 'refresh-failed';
  const replayAttempt = operation.outcome === 'unknown' ? (operation.variables ?? null) : null;
  const reconcile = () => operation.refresh();
  const runEndpointAction = async (intent: Intent) => {
    if (reconciliationRequired) {
      await operation.refresh();
      return;
    }
    if (replayAttempt) {
      await operation.check();
      if (retryAllowed.current) await operation.mutateAsync(replayAttempt).catch(() => undefined);
      return;
    }
    await operation.mutateAsync(intent).catch(() => undefined);
  };
  useEffect(() => {
    let active = true;
    queueMicrotask(() => {
      if (active) setEndpointNote(endpoint.data?.note ?? '');
    });
    return () => {
      active = false;
    };
  }, [endpoint.data?.note]);

  if (endpoint.isPending && !endpoint.data)
    return (
      <div className="page core-page">
        <CoreLoading />
      </div>
    );
  if (
    !endpoint.data ||
    isForbidden(endpoint.error) ||
    isUnauthorized(endpoint.error) ||
    (endpoint.error && !replayAttempt) ||
    isForbidden(keys.error) ||
    isUnauthorized(keys.error) ||
    (isNotFoundError(keys.error) && replayAttempt?.kind !== 'delete')
  )
    return (
      <div className="page core-page">
        <CoreErrorPanel
          error={endpoint.error ?? keys.error ?? new Error('The endpoint details are unavailable.')}
          onRetry={() => void endpoint.refetch()}
        />
      </div>
    );

  const removeEndpoint = async () => {
    if (busy || reconciliationRequired || replayAttempt?.kind === 'patch') return;
    await runEndpointAction(
      replayAttempt?.kind === 'delete'
        ? replayAttempt
        : {
            kind: 'delete',
            expectedRevision: endpoint.data.revision,
          },
    );
  };

  return (
    <div className="page core-page core-stack">
      <PageHeader
        icon="endpoints"
        title={t('endpoints.detailsTitle')}
        description={t('endpoints.detailsDescription')}
        back={
          <Link to={returnTo} state={location.state}>
            {t('common.back')}
          </Link>
        }
      />
      <section className="core-card">
        <div className="core-card__header">
          <h2>
            <ConnectorLabel value={endpoint.data.connector_type} />
          </h2>
          <StatusPill tone={endpoint.data.enabled ? 'success' : 'neutral'}>
            {endpoint.data.enabled ? t('common.enabled') : t('common.disabled')}
          </StatusPill>
        </div>
        <dl className="core-detail-list">
          <div>
            <dt>{t('endpoints.baseUrl')}</dt>
            <dd>
              <SafeCopyValue value={endpoint.data.base_url} label={t('endpoints.baseUrl')} />
            </dd>
          </div>
          <div>
            <dt>{t('endpoints.origin')}</dt>
            <dd>
              {endpoint.data.origin.kind === 'mainstream'
                ? t('endpoints.originMainstream', { name: endpoint.data.origin.name })
                : t('endpoints.originCustom')}
            </dd>
          </div>
          <div>
            <dt>{t('endpoints.note')}</dt>
            <dd>{endpoint.data.note || t('common.notSet')}</dd>
          </div>
          <div>
            <dt>{t('endpoints.keyCount')}</dt>
            <dd className="core-number">{endpoint.data.key_count}</dd>
          </div>
        </dl>
        <div className="core-row-actions">
          <button
            type="button"
            className="btn btn-secondary"
            disabled={busy || reconciliationRequired || Boolean(replayAttempt)}
            onClick={() =>
              void runEndpointAction({
                kind: 'patch',
                input: {
                  enabled: !endpoint.data.enabled,
                  expected_revision: endpoint.data.revision,
                },
              })
            }
          >
            {endpoint.data.enabled ? t('endpoints.toggleOff') : t('endpoints.toggleOn')}
          </button>
        </div>
        <OutcomeNotice
          outcome={outcome}
          savedRefreshFailed={operation.outcome === 'refresh-failed'}
          onCheck={() => void (operation.isSuccess ? operation.refresh() : operation.check())}
          busy={busy}
        />
        {reconciliationRequired ? (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={busy}
            onClick={() => void reconcile()}
          >
            {t('common.reconcile')}
          </button>
        ) : null}
        {replayAttempt && (replayAttempt.kind !== 'delete' || !deleteOpen) ? (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={busy || reconciliationRequired}
            onClick={() => void runEndpointAction(replayAttempt)}
          >
            {t('common.reconcile')}
          </button>
        ) : null}
        {editing ? (
          <form
            className="core-form"
            onSubmit={(event) => {
              event.preventDefault();
              void runEndpointAction({
                kind: 'patch',
                input: { note: endpointNote, expected_revision: endpoint.data.revision },
              });
            }}
          >
            <div className="core-field-grid">
              <label>
                <span>{t('endpoints.note')}</span>
                <input
                  value={endpointNote}
                  maxLength={2048}
                  disabled={Boolean(replayAttempt)}
                  onChange={(event) => setEndpointNote(event.target.value)}
                />
              </label>
            </div>
            <div className="core-form-actions">
              <button
                type="button"
                className="btn btn-secondary"
                disabled={busy || Boolean(replayAttempt)}
                onClick={() => {
                  setEndpointNote(endpoint.data.note);
                  setEditing(false);
                }}
              >
                {t('common.cancel')}
              </button>
              <button
                type="submit"
                className="btn btn-primary"
                disabled={
                  busy ||
                  reconciliationRequired ||
                  Boolean(replayAttempt) ||
                  endpointNote === endpoint.data.note
                }
              >
                {t('common.save')}
              </button>
            </div>
          </form>
        ) : (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={busy || reconciliationRequired || Boolean(replayAttempt)}
            onClick={() => setEditing(true)}
          >
            {t('common.edit')}
          </button>
        )}
      </section>

      <RequestAdaptationEditor
        key={`${accountId}:${endpoint.data.id}`}
        url={`/api/endpoints/${encodeURIComponent(endpoint.data.id)}/request-adaptation`}
        scope="endpoint"
        connectorType={endpoint.data.connector_type}
        editable
      />

      <section className="core-card" aria-busy={keys.isFetching}>
        <div className="core-card__header">
          <h2>{t('endpoints.key')}</h2>
          <button
            type="button"
            className="btn btn-primary"
            disabled={addingKey || reconciliationRequired || Boolean(replayAttempt)}
            onClick={() => setAddingKey(true)}
          >
            {t('endpoints.addKey')}
          </button>
        </div>
        <ResourceFilterBar control={filters} />
        {addingKey ? (
          <AddEndpointKeyForm
            accountId={accountId}
            endpoint={endpoint.data}
            onClose={() => setAddingKey(false)}
          />
        ) : null}
        {keys.error && keys.data ? (
          <CoreErrorPanel compact error={keys.error} onRetry={() => void keys.refetch()} />
        ) : null}
        {keys.isPending && !keys.data ? (
          <CoreLoading />
        ) : !keys.data ? (
          <CoreErrorPanel
            error={keys.error ?? new Error('The key details are unavailable.')}
            onRetry={() => void keys.refetch()}
          />
        ) : keys.data.data.length === 0 && filters.active ? (
          <FilteredResourceEmpty control={filters} />
        ) : keys.data.data.length === 0 ? (
          <CoreEmpty title={t('endpoints.noKeysTitle')} body={t('endpoints.noKeysBody')} />
        ) : (
          <ul className="core-key-list">
            {keys.data.data.map((keyData) => (
              <EndpointKeyCard
                key={keyData.id}
                accountId={accountId}
                endpoint={endpoint.data}
                keyData={keyData}
                onRefresh={() => void keys.refetch()}
              />
            ))}
          </ul>
        )}
        {keys.data ? (
          <PagePagination
            metadata={keys.data.pagination}
            requestedPage={keyPager.page}
            busy={keys.isFetching || addingKey || busy}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        ) : null}
      </section>

      <section className="core-card core-danger-zone">
        <div className="core-card__header">
          <h2>{t('endpoints.dangerTitle')}</h2>
        </div>
        <p>{t('endpoints.deleteEndpointBody')}</p>
        <div className="core-row-actions">
          <span />
          <button
            type="button"
            className="btn btn-danger"
            disabled={busy || reconciliationRequired || Boolean(replayAttempt)}
            onClick={() => setDeleteOpen(true)}
          >
            {t('endpoints.deleteEndpoint')}
          </button>
        </div>
      </section>
      <ConfirmDialog
        open={deleteOpen}
        title={t('endpoints.deleteEndpointTitle')}
        description={t('endpoints.deleteEndpointBody')}
        confirmLabel={
          replayAttempt?.kind === 'delete' ? t('common.retrySame') : t('endpoints.deleteEndpoint')
        }
        danger
        busy={busy}
        onCancel={() => {
          if (!busy) setDeleteOpen(false);
        }}
        onConfirm={() => void removeEndpoint()}
      />
    </div>
  );
}

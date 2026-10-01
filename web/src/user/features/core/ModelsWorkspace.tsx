import { useResourceFilters, useResourceListScroll } from './useResourceFilters';
import { ResourceFilterBar, FilteredResourceEmpty } from './ResourceFilterControls';
import {
  useEffect,
  useEffectEvent,
  useMemo,
  useReducer,
  useRef,
  useState,
  type DragEvent,
  type FormEvent,
} from 'react';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { readResourceResult, resourceStatus } from './resourceOperation';
import { useQueryClient } from '@tanstack/react-query';
import { useSearchState } from '@shared/operations/useSearchState';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ChoiceList } from '@shared/components/ChoiceList';
import { PageHeader } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { usePagePager, type PagePager } from '@shared/operations/usePagePager';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isForbidden, isNotFoundError, isUnauthorized } from '@shared/query/http';
import {
  getBindings,
  getModel,
  addBindings,
  createModel,
  deleteBinding,
  deleteModel,
  orderBindings,
  patchModel,
} from './api';
import { getBindingCandidatesPage } from './pageApi';
import { ModelBrowseSummary } from './ResourceBrowse';
import { validateResourceId } from './normalizers';
import {
  ConnectorLabel,
  CoreEmpty,
  CoreErrorPanel,
  CoreLoading,
  CoreTime,
  MutationNotice,
  SafeCopyValue,
  StatusPill,
} from './components';
import { useQuickstartCopy } from './quickstartCopy';
import { useCoreCopy } from './copy';
import { validateLogicalName, validatePersonalProviderName } from './normalizers';
import {
  applyBindingsResponse,
  coreKeys,
  invalidateResourceDependents,
  useCoreSession,
  useBindings,
  useModel,
} from './queries';
import { useNumberedBindingCandidates, useNumberedModels } from './modelNumberedQueries';
import { useNumberedEndpointKeys, useNumberedEndpoints } from './numberedQueries';
import { isConflict, isOutcomeUnknown } from './request';
import { bindingDraftReducer, initialBindingDraftState } from './stateMachines';
import type {
  Binding,
  BindingCandidate,
  Endpoint,
  EndpointKey,
  BindingsResponse,
  BindingSelection,
  CatalogSourceType,
  Model,
  ModelCreateInput,
  ModelPatchInput,
  RouteStrategy,
  UserProfile,
} from './types';
import type { PageMetadata, PageSize } from '@shared/operations/pageNumbers';
import type { NumberedPage } from './pageTypes';

type VisibleOutcome = 'conflict' | 'unknown' | 'error' | null;
type PermissionLoss = { scope: string; error: unknown };

function asNotice(outcome: VisibleOutcome) {
  return outcome ? <MutationNotice outcome={outcome} /> : null;
}

function isAccessLoss(error: unknown): boolean {
  return isUnauthorized(error) || isForbidden(error);
}

function selectedModelID(searchParams: URLSearchParams): string | null {
  const values = searchParams.getAll('model_id');
  if (values.length !== 1 || !values[0]) return null;
  try {
    return validateResourceId(values[0], 'model id');
  } catch {
    return null;
  }
}

function ModelEditor({
  accountId,
  initial,
  onCancel,
  onSaved,
  onCapabilityLoss,
}: {
  accountId: string;
  initial?: Model;
  onCancel: () => void;
  onSaved: (model: Model) => void;
  onCapabilityLoss?: (error: unknown) => void;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const [provider, setProvider] = useState(initial?.provider ?? '');
  const [modelName, setModelName] = useState(initial?.model ?? '');
  const [strategy, setStrategy] = useState<RouteStrategy>(initial?.route_strategy ?? 'ordered');
  const [silentRetry, setSilentRetry] = useState(initial?.silent_retry ?? false);
  const [flattenTools, setFlattenTools] = useState(initial?.flatten_tool_calls ?? false);
  const { t: text } = useQuickstartCopy();
  const [validation, setValidation] = useState(false);
  const [permissionLost, setPermissionLost] = useState<unknown>(null);
  const savedResult = useRef<Model | null>(null);
  const retryAllowed = useRef(false);
  const operation = useRetainedOperation<ModelCreateInput | ModelPatchInput, Model>(
    async (input, key, context) => {
      try {
        const identity = { idempotencyKey: key, actionId: key };
        const saved = initial
          ? await patchModel(initial.id, input as ModelPatchInput, identity, context.signal)
          : await createModel(input as ModelCreateInput, identity, context.signal);
        context.commit(() => {
          savedResult.current = saved;
          queryClient.setQueryData(coreKeys.model(accountId, saved.id), saved);
        });
        return saved;
      } catch (error) {
        if (isAccessLoss(error))
          context.commit(() => {
            setPermissionLost(error);
            onCapabilityLoss?.(error);
          });
        throw error;
      }
    },
    async (input, error, context) => {
      let confirmed = false;
      retryAllowed.current = false;
      if (error && !initial && isOutcomeUnknown(error) && context.operationKey) {
        const status = await resourceStatus(context.operationKey, context.signal);
        context.assertCurrent();
        retryAllowed.current = status.status === 'not_recorded';
        const result = await readResourceResult(
          { kind: 'model', row: '', input: input as ModelCreateInput },
          status,
          context.signal,
        );
        if (result?.kind === 'model') {
          context.commit(() => {
            savedResult.current = result.model;
            queryClient.setQueryData(coreKeys.model(accountId, result.model.id), result.model);
          });
          confirmed = true;
        }
      } else if (error && initial && isOutcomeUnknown(error)) {
        const current = await getModel(initial.id, context.signal);
        context.assertCurrent();
        const patch = input as ModelPatchInput;
        retryAllowed.current = current.revision === patch.expected_revision;
        confirmed =
          BigInt(current.revision) > BigInt(patch.expected_revision) &&
          Object.entries(patch).every(
            ([field, value]) =>
              field === 'expected_revision' || current[field as keyof Model] === value,
          );
        if (confirmed)
          context.commit(() => {
            savedResult.current = current;
          });
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: coreKeys.modelsRoot(accountId) }),
        initial
          ? queryClient.invalidateQueries({ queryKey: coreKeys.model(accountId, initial.id) })
          : Promise.resolve(),
        invalidateResourceDependents(queryClient, accountId),
      ]);
      context.assertCurrent();
      if (!error || confirmed) {
        const saved = savedResult.current;
        if (saved) context.commit(() => onSaved(saved));
      }
      if (confirmed) return { operationConfirmed: true };
    },
    ['user', 'core'],
    {
      clearSecrets: () => {
        savedResult.current = null;
      },
    },
  );
  const busy = operation.isPending,
    hasAttempt = busy || operation.outcome === 'unknown';
  const outcome: VisibleOutcome =
    operation.outcome === 'unknown' || operation.outcome === 'conflict'
      ? operation.outcome
      : operation.outcome === 'failed'
        ? 'error'
        : null;
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setValidation(false);
    if (operation.isSuccess) {
      await operation.refresh();
      return;
    }
    if (operation.outcome === 'unknown' && operation.variables) {
      await operation.check();
      if (retryAllowed.current)
        await operation.mutateAsync(operation.variables).catch(() => undefined);
      return;
    }
    let input = operation.outcome === 'unknown' ? operation.variables : undefined;
    if (!input) {
      try {
        validatePersonalProviderName(provider);
        validateLogicalName(modelName);
      } catch {
        setValidation(true);
        return;
      }
      input = {
        provider,
        model: modelName,
        route_strategy: strategy,
        silent_retry: silentRetry,
        flatten_tool_calls: flattenTools,
        ...(initial ? { expected_revision: initial.revision } : {}),
      };
    }
    await operation.mutateAsync(input).catch(() => undefined);
  };

  if (permissionLost) {
    return (
      <section className="core-card">
        <CoreErrorPanel compact error={permissionLost} />
      </section>
    );
  }

  return (
    <form className="core-card core-wizard core-form" onSubmit={(event) => void submit(event)}>
      <div className="core-card__header">
        <h2>{initial ? t('models.editModel') : t('models.create')}</h2>
        <button
          type="button"
          className="btn btn-secondary"
          disabled={busy || hasAttempt}
          onClick={onCancel}
        >
          {t('common.cancel')}
        </button>
      </div>
      <p className="core-muted">{t('models.namingHelp')}</p>
      <div className="core-field-grid">
        <label>
          <span>{t('models.provider')}</span>
          <input
            value={provider}
            maxLength={128}
            required
            disabled={hasAttempt}
            onChange={(event) => setProvider(event.target.value)}
          />
        </label>
        <label>
          <span>{t('models.model')}</span>
          <input
            value={modelName}
            maxLength={128}
            required
            disabled={hasAttempt}
            onChange={(event) => setModelName(event.target.value)}
          />
        </label>
        <label>
          <span>{t('models.strategy')}</span>
          <select
            value={strategy}
            disabled={hasAttempt}
            onChange={(event) => setStrategy(event.target.value as RouteStrategy)}
          >
            <option value="ordered">{t('models.ordered')}</option>
            <option value="random">{t('models.random')}</option>
          </select>
        </label>
      </div>
      <p className="core-muted">{text('strategyHelp')}</p>
      <div className="core-model-preview">
        <span>{t('models.namePreview')}</span>
        <output className="core-mono">
          {provider || t('models.provider')}/{modelName || t('models.model')}
        </output>
      </div>
      <label className="core-checkbox">
        <input
          type="checkbox"
          checked={silentRetry}
          disabled={hasAttempt}
          onChange={(event) => setSilentRetry(event.target.checked)}
        />
        <span>{t('models.silentRetry')}</span>
      </label>
      <label className="core-checkbox">
        <input
          type="checkbox"
          checked={flattenTools}
          disabled={hasAttempt}
          onChange={(event) => setFlattenTools(event.target.checked)}
        />
        <span>{t('models.flattenTools')}</span>
      </label>
      <p className="core-muted">{text('retryHelp')}</p>
      <p className="core-muted">{text('toolsHelp')}</p>
      {validation ? (
        <p className="core-inline-error" role="alert">
          {t('models.invalidName')}
        </p>
      ) : null}
      {operation.outcome === 'refresh-failed' ? (
        <p role="status">
          {text('savedRefresh')}
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => void operation.refresh()}
          >
            {t('common.refresh')}
          </button>
        </p>
      ) : (
        asNotice(outcome)
      )}
      <div className="core-form-actions">
        <span />
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? t('common.working') : hasAttempt ? text('checkResult') : t('common.save')}
        </button>
      </div>
    </form>
  );
}

function candidateIdentity(
  candidate: Pick<BindingCandidate, 'endpoint_key_id' | 'upstream_model_id'>,
): string {
  return `${candidate.endpoint_key_id}\u0000${candidate.upstream_model_id}`;
}

function CandidateSource({
  source,
  page,
  pending,
  busy,
  error,
  selected,
  bound,
  onToggle,
  onRetry,
  pager,
  locked = false,
}: {
  source: CatalogSourceType;
  page: NumberedPage<BindingCandidate> | undefined;
  pending: boolean;
  busy: boolean;
  error: unknown;
  selected: ReadonlySet<string>;
  bound: ReadonlySet<string>;
  onToggle: (candidate: BindingCandidate) => void;
  onRetry: () => void;
  pager: PagePager;
  locked?: boolean;
}) {
  const { t } = useCoreCopy();
  return (
    <section className="core-selector__level" aria-busy={busy}>
      <div className="core-card__header">
        <h3>{source === 'automatic' ? t('models.automatic') : t('models.manual')}</h3>
      </div>
      {pending ? (
        <CoreLoading compact />
      ) : error ? (
        <CoreErrorPanel compact error={error} onRetry={onRetry} />
      ) : !page || page.data.length === 0 ? (
        <p className="core-muted">{t('models.candidateEmpty')}</p>
      ) : (
        <ChoiceList
          searchable={false}
          items={page.data}
          getKey={candidateIdentity}
          getSearchText={(candidate) => candidate.upstream_model_id}
          label={source === 'automatic' ? t('models.automatic') : t('models.manual')}
        >
          {(candidate) => {
            const identity = candidateIdentity(candidate);
            const isSelected = selected.has(identity);
            const isBound = bound.has(identity);
            return (
              <button
                key={`${source}:${identity}`}
                type="button"
                className={`core-choice${isSelected ? ' is-selected' : ''}`}
                aria-pressed={isSelected}
                disabled={isBound || locked || busy}
                onClick={() => onToggle(candidate)}
              >
                <strong className="core-mono">{candidate.upstream_model_id}</strong>
                <span>
                  {candidate.source_types
                    .map((value) =>
                      value === 'automatic' ? t('models.automatic') : t('models.manual'),
                    )
                    .join(' + ')}
                </span>
                <span className="core-muted">
                  {isBound
                    ? t('models.alreadyBound')
                    : isSelected
                      ? t('models.candidateSelected')
                      : candidate.endpoint_key_note || t('common.notSet')}
                </span>
              </button>
            );
          }}
        </ChoiceList>
      )}
      {page ? (
        <PagePagination
          metadata={page.pagination}
          requestedPage={pager.page}
          busy={busy || locked}
          onPageChange={pager.setPage}
          onPageSizeChange={pager.setPageSize}
        />
      ) : null}
    </section>
  );
}

function BindingSelector({ accountId, model }: { accountId: string; model: Model }) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const bindings = useBindings(accountId, model.id);
  const [endpointQuery, setEndpointQuery] = useState(''),
    [keyQuery, setKeyQuery] = useState('');
  const { t: text } = useQuickstartCopy();
  const [selectedEndpoint, setSelectedEndpoint] = useState<Endpoint | undefined>();
  const [selectedKey, setSelectedKey] = useState<EndpointKey | undefined>();
  const [endpointId, setEndpointId] = useState('');
  const [keyId, setKeyId] = useState('');
  const [modelQuery, setModelQuery] = useState('');
  const [queryDraft, setQueryDraft] = useState('');
  const [selectionDetails, setSelectionDetails] = useState<Record<string, BindingCandidate>>({});
  const [invalidSelection, setInvalidSelection] = useState(false);
  const endpointPager = usePagePager({
    station: 'user',
    listType: 'models-binding-endpoints',
    scopeKey: accountId,
    resetKey: endpointQuery,
  });
  const keyPager = usePagePager({
    station: 'user',
    listType: 'models-binding-keys',
    scopeKey: `${accountId}\u0000${model.id}`,
    resetKey: `${endpointId}:${keyQuery}`,
  });
  const automaticPager = usePagePager({
    station: 'user',
    listType: 'models-binding-candidates-automatic',
    scopeKey: `${accountId}\u0000${model.id}`,
    resetKey: `${endpointId}\u0000${keyId}\u0000${modelQuery}`,
  });
  const manualPager = usePagePager({
    station: 'user',
    listType: 'models-binding-candidates-manual',
    scopeKey: `${accountId}\u0000${model.id}`,
    resetKey: `${endpointId}\u0000${keyId}\u0000${modelQuery}`,
  });
  const endpoints = useNumberedEndpoints(
    accountId,
    { page: endpointPager.page, pageSize: endpointPager.pageSize },
    true,
    { q: endpointQuery },
  );
  const keys = useNumberedEndpointKeys(
    accountId,
    endpointId || undefined,
    { page: keyPager.page, pageSize: keyPager.pageSize },
    Boolean(endpointId),
    { q: keyQuery },
  );
  const automatic = useNumberedBindingCandidates(
    accountId,
    model.id,
    {
      endpointId: endpointId || undefined,
      keyId: keyId || undefined,
      source: 'automatic',
      query: modelQuery,
    },
    { page: automaticPager.page, pageSize: automaticPager.pageSize },
    Boolean(endpointId && keyId),
  );
  const manual = useNumberedBindingCandidates(
    accountId,
    model.id,
    {
      endpointId: endpointId || undefined,
      keyId: keyId || undefined,
      source: 'manual',
      query: modelQuery,
    },
    { page: manualPager.page, pageSize: manualPager.pageSize },
    Boolean(endpointId && keyId),
  );
  const [draft, dispatch] = useReducer(bindingDraftReducer, undefined, () =>
    initialBindingDraftState(accountId, model.id, model.binding_revision),
  );
  const bindingsKnown = Boolean(bindings.data);
  const queryPermissionError = [
    bindings.error,
    endpoints.error,
    keys.error,
    automatic.error,
    manual.error,
  ].find((error) => isAccessLoss(error));
  const resetDraft = useEffectEvent(() =>
    dispatch({
      type: 'boundary',
      accountId,
      modelId: model.id,
      bindingRevision: model.binding_revision,
    }),
  );
  useEffect(() => {
    resetDraft();
    let active = true;
    queueMicrotask(() => {
      if (!active) return;
      setSelectedEndpoint(undefined);
      setSelectedKey(undefined);
      setEndpointId('');
      setKeyId('');
      setModelQuery('');
      setQueryDraft('');
      setSelectionDetails({});
      setInvalidSelection(false);
    });
    return () => {
      active = false;
    };
  }, [accountId, model.id]);

  useEffect(() => {
    const revision = bindings.data?.binding_revision;
    if (revision && revision !== draft.bindingRevision) {
      dispatch({ type: 'authoritative', accountId, modelId: model.id, bindingRevision: revision });
    }
  }, [accountId, bindings.data?.binding_revision, draft.bindingRevision, model.id]);

  const selected = useMemo(
    () => new Set(draft.selections.map(candidateIdentity)),
    [draft.selections],
  );
  const bound = useMemo(
    () => new Set((bindings.data?.bindings ?? []).map(candidateIdentity)),
    [bindings.data?.bindings],
  );

  const chooseEndpoint = (next: string) => {
    setSelectedEndpoint(endpoints.data?.data.find((entry) => entry.id === next));
    setSelectedKey(undefined);
    setEndpointId(next);
    setKeyId('');
    setModelQuery('');
    setQueryDraft('');
  };

  const chooseKey = (next: string) => {
    setSelectedKey(keys.data?.data.find((entry) => entry.id === next));
    setKeyId(next);
    setModelQuery('');
    setQueryDraft('');
  };

  const toggleCandidate = (candidate: BindingCandidate) => {
    setSelectionDetails((current) => ({ ...current, [candidateIdentity(candidate)]: candidate }));
    dispatch({ type: 'toggle', accountId, modelId: model.id, candidate });
  };

  type Intent = { revision: string; selections: BindingSelection[] };
  const retryAllowed = useRef(false);
  const publish = (response: BindingsResponse, intent: Intent) => {
    applyBindingsResponse(queryClient, accountId, model.id, response);
    const bound = new Set(response.bindings.map(candidateIdentity));
    for (const candidate of intent.selections)
      if (bound.has(candidateIdentity(candidate)))
        dispatch({ type: 'candidate-invalid', accountId, modelId: model.id, candidate });
    dispatch({
      type: 'authoritative',
      accountId,
      modelId: model.id,
      bindingRevision: response.binding_revision,
    });
  };
  const operation = useRetainedOperation<Intent, BindingsResponse>(
    async (intent, key, context) => {
      try {
        const response = await addBindings(
          model.id,
          intent.revision,
          intent.selections,
          { idempotencyKey: key, actionId: key },
          context.signal,
        );
        context.commit(() => publish(response, intent));
        return response;
      } catch (error) {
        if (!isConflict(error) && !isOutcomeUnknown(error) && !isAccessLoss(error)) {
          let removed = false;
          for (const candidate of intent.selections) {
            const page = await getBindingCandidatesPage(
              model.id,
              { keyId: candidate.endpoint_key_id, query: candidate.upstream_model_id },
              { page: '1', pageSize: 100 },
              context.signal,
            );
            context.assertCurrent();
            if (
              page.pagination.total_pages === '1' &&
              !page.data.some((item) => candidateIdentity(item) === candidateIdentity(candidate))
            ) {
              removed = true;
              context.commit(() =>
                dispatch({ type: 'candidate-invalid', accountId, modelId: model.id, candidate }),
              );
            }
          }
          context.commit(() => setInvalidSelection(removed));
        }
        throw error;
      }
    },
    async (intent, error, context) => {
      retryAllowed.current = false;
      if (error && isOutcomeUnknown(error) && context.operationKey) {
        const status = await resourceStatus(context.operationKey, context.signal);
        context.assertCurrent();
        retryAllowed.current = status.status === 'not_recorded';
      }
      const response = await getBindings(model.id, context.signal);
      context.commit(() =>
        queryClient.setQueryData(coreKeys.bindings(accountId, model.id), response),
      );
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: coreKeys.model(accountId, model.id) }),
        queryClient.invalidateQueries({ queryKey: coreKeys.modelsRoot(accountId) }),
        queryClient.invalidateQueries({ queryKey: coreKeys.candidatesRoot(accountId, model.id) }),
      ]);
      context.assertCurrent();
      const bound = new Set(response.bindings.map(candidateIdentity));
      const confirmed = intent.selections.every((item) => bound.has(candidateIdentity(item)));
      if (!error || confirmed) context.commit(() => publish(response, intent));
      if (error && confirmed) return { operationConfirmed: true };
    },
    ['user', 'core'],
    {
      clearSecrets: () => {
        setSelectionDetails({});
        dispatch({
          type: 'boundary',
          accountId,
          modelId: model.id,
          bindingRevision: model.binding_revision,
        });
      },
    },
  );
  const replayAttempt = operation.outcome === 'unknown' ? (operation.variables ?? null) : null;
  const operationStatus = operation.outcome === 'failed' ? 'error' : operation.outcome;
  const reconcileAuthority = async () => {
    if (operation.outcome === 'unknown') await operation.check();
    else if (operation.isSuccess) await operation.refresh();
    else await bindings.refetch();
  };
  const submit = async () => {
    setInvalidSelection(false);
    if (operation.outcome === 'refresh-failed') {
      await operation.refresh();
      return;
    }
    if (replayAttempt) {
      await operation.check();
      if (retryAllowed.current) await operation.mutateAsync(replayAttempt).catch(() => undefined);
      return;
    }
    if (!draft.selections.length) return;
    await operation
      .mutateAsync({ revision: draft.bindingRevision, selections: [...draft.selections] })
      .catch(() => undefined);
  };

  const accessLossError =
    (isAccessLoss(operation.error) ? operation.error : null) ?? queryPermissionError;
  if (accessLossError) {
    return (
      <section className="core-card">
        <div className="core-card__header">
          <h2>{t('models.selectorTitle')}</h2>
        </div>
        <CoreErrorPanel compact error={accessLossError} />
      </section>
    );
  }

  return (
    <section className="core-card">
      <div className="core-card__header">
        <h2>{t('models.selectorTitle')}</h2>
      </div>
      {bindings.isPending && !bindings.data ? <CoreLoading compact /> : null}
      {!bindings.data && bindings.error ? (
        <CoreErrorPanel compact error={bindings.error} onRetry={() => void bindings.refetch()} />
      ) : null}
      <nav className="core-selector-path" aria-label={t('models.selectorTitle')}>
        <button
          type="button"
          className="btn btn-quiet"
          onClick={() => chooseEndpoint('')}
          aria-current={!endpointId ? 'step' : undefined}
        >
          {t('models.levelEndpoint')}
        </button>
        {endpointId ? (
          <>
            <span aria-hidden="true">/</span>
            <button
              type="button"
              className="btn btn-quiet"
              onClick={() => chooseKey('')}
              aria-current={!keyId ? 'step' : undefined}
            >
              {t('models.levelKey')}
            </button>
          </>
        ) : null}
        {keyId ? (
          <>
            <span aria-hidden="true">/</span>
            <span aria-current="step">{t('models.levelCandidate')}</span>
          </>
        ) : null}
      </nav>
      {endpointId ? (
        <p className="core-muted core-selector-context">
          {selectedEndpoint?.note} · {selectedEndpoint?.base_url}
          {keyId
            ? ` / ${selectedKey?.note || ''} · ${selectedKey?.display_head || ''}…${selectedKey?.display_tail || ''}`
            : ''}
        </p>
      ) : null}
      <div className="core-selector">
        <section className="core-selector__level" hidden={Boolean(endpointId)}>
          <h3>{t('models.levelEndpoint')}</h3>
          <label>
            {text('serviceSearch')}
            <input
              type="search"
              maxLength={128}
              value={endpointQuery}
              onChange={(e) => setEndpointQuery(e.target.value)}
            />
            <small>{text('searchHelp')}</small>
          </label>
          {endpoints.isPending ? (
            <CoreLoading compact />
          ) : endpoints.error ? (
            <CoreErrorPanel
              compact
              error={endpoints.error}
              onRetry={() => void endpoints.refetch()}
            />
          ) : endpoints.data.data.length === 0 ? (
            <p className="core-muted">{t('models.endpointEmpty')}</p>
          ) : (
            <ChoiceList
              items={endpoints.data.data}
              getKey={(endpoint) => endpoint.id}
              getSearchText={(endpoint) =>
                `${endpoint.note} ${endpoint.base_url} ${endpoint.connector_type}`
              }
              label={t('models.levelEndpoint')}
              searchable={false}
            >
              {(endpoint) => (
                <button
                  key={endpoint.id}
                  type="button"
                  className={`core-choice${endpointId === endpoint.id ? ' is-selected' : ''}`}
                  disabled={
                    !bindingsKnown ||
                    Boolean(replayAttempt) ||
                    !endpoint.enabled ||
                    endpoints.isFetching
                  }
                  onClick={() => chooseEndpoint(endpoint.id)}
                >
                  <strong>{endpoint.note || endpoint.base_url}</strong>
                  <span className="core-mono">{endpoint.base_url}</span>
                  <span>
                    <ConnectorLabel value={endpoint.connector_type} />
                    {!endpoint.enabled ? ` · ${t('common.disabled')}` : ''}
                  </span>
                </button>
              )}
            </ChoiceList>
          )}
          {endpoints.data ? (
            <PagePagination
              metadata={endpoints.data.pagination}
              requestedPage={endpointPager.page}
              busy={endpoints.isFetching || Boolean(replayAttempt)}
              onPageChange={endpointPager.setPage}
              onPageSizeChange={endpointPager.setPageSize}
            />
          ) : null}
        </section>

        <section className="core-selector__level" hidden={!endpointId || Boolean(keyId)}>
          <h3>{t('models.levelKey')}</h3>
          <label>
            {text('keySearch')}
            <input
              type="search"
              maxLength={128}
              value={keyQuery}
              onChange={(e) => setKeyQuery(e.target.value)}
            />
            <small>{text('searchHelp')}</small>
          </label>
          {!endpointId ? (
            <p className="core-muted">{t('models.chooseEndpoint')}</p>
          ) : keys.isPending ? (
            <CoreLoading compact />
          ) : keys.error ? (
            <CoreErrorPanel compact error={keys.error} onRetry={() => void keys.refetch()} />
          ) : keys.data.data.length === 0 ? (
            <p className="core-muted">{t('models.keyEmpty')}</p>
          ) : (
            <ChoiceList
              items={keys.data.data}
              getKey={(key) => key.id}
              getSearchText={(key) => `${key.note} ${key.display_head} ${key.display_tail}`}
              label={t('models.levelKey')}
              searchable={false}
            >
              {(key) => {
                const unavailable = !key.enabled || key.suspension_state !== 'none';
                return (
                  <button
                    key={key.id}
                    type="button"
                    className={`core-choice${keyId === key.id ? ' is-selected' : ''}`}
                    disabled={
                      !bindingsKnown || Boolean(replayAttempt) || unavailable || keys.isFetching
                    }
                    onClick={() => chooseKey(key.id)}
                  >
                    <strong>{key.note || `${key.display_head}…${key.display_tail}`}</strong>
                    <span className="core-mono">
                      {key.display_head}…{key.display_tail}
                    </span>
                    {unavailable ? (
                      <span>
                        {key.suspension_state === 'security_processing'
                          ? t('models.securityLocked')
                          : t('common.disabled')}
                      </span>
                    ) : null}
                  </button>
                );
              }}
            </ChoiceList>
          )}
          {keys.data ? (
            <PagePagination
              metadata={keys.data.pagination}
              requestedPage={keyPager.page}
              busy={keys.isFetching || Boolean(replayAttempt)}
              onPageChange={keyPager.setPage}
              onPageSizeChange={keyPager.setPageSize}
            />
          ) : null}
        </section>

        <section className="core-selector__level" hidden={!keyId}>
          <h3>{t('models.levelCandidate')}</h3>
          <form
            className="core-selector-search"
            onSubmit={(event) => {
              event.preventDefault();
              setModelQuery(queryDraft.trim());
            }}
          >
            <label>
              <span>{t('models.searchCandidates')}</span>
              <input
                value={queryDraft}
                onChange={(event) => setQueryDraft(event.target.value)}
                maxLength={256}
                disabled={!bindingsKnown || Boolean(replayAttempt)}
              />
            </label>
            <button
              type="submit"
              className="btn btn-secondary"
              disabled={!bindingsKnown || Boolean(replayAttempt)}
            >
              {t('common.search')}
            </button>
          </form>
          {!keyId ? (
            <p className="core-muted">{t('models.chooseKey')}</p>
          ) : (
            <div className="core-selector__sources">
              <CandidateSource
                source="automatic"
                page={automatic.data}
                pending={automatic.isPending}
                busy={automatic.isFetching}
                error={automatic.error}
                selected={selected}
                bound={bound}
                onToggle={toggleCandidate}
                onRetry={() => void automatic.refetch()}
                pager={automaticPager}
                locked={!bindingsKnown || Boolean(replayAttempt)}
              />
              <CandidateSource
                source="manual"
                page={manual.data}
                pending={manual.isPending}
                busy={manual.isFetching}
                error={manual.error}
                selected={selected}
                bound={bound}
                onToggle={toggleCandidate}
                onRetry={() => void manual.refetch()}
                pager={manualPager}
                locked={!bindingsKnown || Boolean(replayAttempt)}
              />
            </div>
          )}
        </section>
      </div>
      <p className="core-muted">{t('models.selectedCount', { count: draft.selections.length })}</p>
      {draft.selections.length ? (
        <ul className="core-selection-list">
          {draft.selections.map((candidate) => {
            const detail = selectionDetails[candidateIdentity(candidate)];
            return (
              <li key={candidateIdentity(candidate)}>
                <div>
                  <strong>{candidate.upstream_model_id}</strong>
                  {detail ? (
                    <span>
                      {detail.endpoint_note || detail.endpoint_base_url} ·{' '}
                      {detail.endpoint_key_note} · {detail.endpoint_key_display_head}…
                      {detail.endpoint_key_display_tail}
                    </span>
                  ) : null}
                </div>
                <button
                  type="button"
                  className="btn btn-quiet"
                  disabled={operation.isPending || Boolean(replayAttempt)}
                  onClick={() =>
                    dispatch({ type: 'candidate-invalid', accountId, modelId: model.id, candidate })
                  }
                >
                  {t('common.remove')}
                </button>
              </li>
            );
          })}
        </ul>
      ) : null}
      {invalidSelection ? (
        <p className="core-inline-warning">{t('models.selectionInvalid')}</p>
      ) : null}
      <MutationNotice
        outcome={
          replayAttempt
            ? 'unknown'
            : operationStatus === 'conflict' ||
                operationStatus === 'unknown' ||
                operationStatus === 'error'
              ? operationStatus
              : null
        }
      />
      <div className="core-form-actions">
        {operationStatus === 'conflict' || operationStatus === 'unknown' ? (
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => void reconcileAuthority()}
          >
            {t('common.reconcile')}
          </button>
        ) : (
          <span />
        )}
        <button
          type="button"
          className="btn btn-primary"
          disabled={
            !bindingsKnown ||
            operation.isPending ||
            operationStatus === 'conflict' ||
            (!replayAttempt && draft.selections.length === 0)
          }
          onClick={() => void submit()}
        >
          {operation.isPending
            ? t('common.working')
            : replayAttempt
              ? t('common.reconcile')
              : t('models.addSelected', { count: draft.selections.length })}
        </button>
      </div>
    </section>
  );
}

function moveBinding(order: string[], from: number, to: number): string[] {
  if (from < 0 || to < 0 || from >= order.length || to >= order.length || from === to) return order;
  const next = [...order];
  const [item] = next.splice(from, 1);
  if (!item) return order;
  next.splice(to, 0, item);
  return next;
}

function localBindingPagination(
  totalItems: number,
  requestedPage: string,
  pageSize: PageSize,
): PageMetadata {
  const totalPages = Math.max(1, Math.ceil(totalItems / pageSize));
  const requested = BigInt(requestedPage);
  const page = requested > BigInt(totalPages) ? BigInt(totalPages) : requested;
  return {
    page: page.toString(),
    page_size: pageSize,
    total_items: String(totalItems),
    total_pages: String(totalPages),
  };
}

function BindingOrder({ accountId, model }: { accountId: string; model: Model }) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const bindings = useBindings(accountId, model.id);
  const pager = usePagePager({
    station: 'user',
    listType: 'models-binding-order',
    scopeKey: `${accountId}\u0000${model.id}`,
    resetKey: model.binding_revision,
  });
  const [order, setOrder] = useState<string[]>([]);
  const [dragged, setDragged] = useState<string | null>(null);
  const [removing, setRemoving] = useState<Binding | null>(null);
  type Intent =
    | { kind: 'order'; expectedRevision: string; order: string[] }
    | { kind: 'delete'; expectedRevision: string; bindingId: string };
  const retryAllowed = useRef(false);
  const operation = useRetainedOperation<Intent, BindingsResponse>(
    async (intent, key, context) => {
      const identity = { idempotencyKey: key, actionId: key };
      const response =
        intent.kind === 'order'
          ? await orderBindings(
              model.id,
              intent.expectedRevision,
              intent.order,
              identity,
              context.signal,
            )
          : await deleteBinding(
              model.id,
              intent.bindingId,
              intent.expectedRevision,
              identity,
              context.signal,
            );
      context.commit(() => {
        applyBindingsResponse(queryClient, accountId, model.id, response);
        if (intent.kind === 'delete') setRemoving(null);
      });
      return response;
    },
    async (intent, error, context) => {
      const current = await getBindings(model.id, context.signal);
      context.commit(() => {
        applyBindingsResponse(queryClient, accountId, model.id, current);
        retryAllowed.current = current.binding_revision === intent.expectedRevision;
        setOrder(current.bindings.map((binding) => binding.id));
      });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: coreKeys.model(accountId, model.id) }),
        queryClient.invalidateQueries({ queryKey: coreKeys.modelsRoot(accountId) }),
        queryClient.invalidateQueries({ queryKey: coreKeys.candidatesRoot(accountId, model.id) }),
      ]);
      context.assertCurrent();
      const confirmed =
        intent.kind === 'delete'
          ? !current.bindings.some((binding) => binding.id === intent.bindingId)
          : current.bindings.length === intent.order.length &&
            current.bindings.every((binding, index) => binding.id === intent.order[index]);
      if (confirmed && intent.kind === 'delete') context.commit(() => setRemoving(null));
      if (error && confirmed) return { operationConfirmed: true };
    },
    ['user', 'core'],
    {
      clearSecrets: () => {
        setRemoving(null);
        setDragged(null);
      },
    },
  );
  const busy = operation.isPending;
  const outcome: VisibleOutcome =
    operation.outcome === 'unknown' || operation.outcome === 'conflict'
      ? operation.outcome
      : operation.outcome === 'failed'
        ? 'error'
        : null;
  const reconciliationRequired = operation.outcome === 'refresh-failed';
  const replayAttempt = operation.outcome === 'unknown' ? (operation.variables ?? null) : null;

  useEffect(() => {
    if (!bindings.data) return;
    let active = true;
    queueMicrotask(() => {
      if (active) setOrder(bindings.data?.bindings.map((binding) => binding.id) ?? []);
    });
    return () => {
      active = false;
    };
  }, [bindings.data]);

  const byId = useMemo(
    () => new Map((bindings.data?.bindings ?? []).map((binding) => [binding.id, binding])),
    [bindings.data?.bindings],
  );
  const authoritativeOrder = bindings.data?.bindings.map((binding) => binding.id) ?? [];
  const pagination = localBindingPagination(order.length, pager.page, pager.pageSize);
  const visiblePage = Number(BigInt(pagination.page));
  const visibleStart = (visiblePage - 1) * pager.pageSize;
  const visibleOrder = order.slice(visibleStart, visibleStart + pager.pageSize);
  const dirty =
    order.length === authoritativeOrder.length &&
    order.some((id, index) => id !== authoritativeOrder[index]);

  useEffect(() => {
    if (BigInt(pager.page) > BigInt(pagination.total_pages)) pager.setPage(pagination.page);
  }, [pager, pagination.page, pagination.total_pages]);

  const reconcile = () => (operation.isSuccess ? operation.refresh() : operation.check());
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
  const saveOrder = async () => {
    if (!bindings.data || busy || replayAttempt?.kind === 'delete' || (!replayAttempt && !dirty))
      return;
    await run({
      kind: 'order',
      expectedRevision: bindings.data.binding_revision,
      order: [...order],
    });
  };
  const remove = async () => {
    if (!bindings.data || !removing || busy || replayAttempt?.kind === 'order') return;
    await run({
      kind: 'delete',
      expectedRevision: bindings.data.binding_revision,
      bindingId: removing.id,
    });
  };

  const drop = (event: DragEvent, targetId: string) => {
    event.preventDefault();
    if (!dragged) return;
    setOrder((current) =>
      moveBinding(current, current.indexOf(dragged), current.indexOf(targetId)),
    );
    setDragged(null);
  };

  const accessLossError =
    (isAccessLoss(operation.error) ? operation.error : null) ??
    (isAccessLoss(bindings.error) ? bindings.error : null);
  if (accessLossError) {
    return (
      <section className="core-card">
        <div className="core-card__header">
          <h2>{t('models.bindingsTitle')}</h2>
        </div>
        <CoreErrorPanel compact error={accessLossError} />
      </section>
    );
  }

  return (
    <section className="core-card">
      <div className="core-card__header">
        <h2>{t('models.bindingsTitle')}</h2>
      </div>
      {bindings.isPending && !bindings.data ? (
        <CoreLoading />
      ) : !bindings.data ? (
        <CoreErrorPanel
          error={bindings.error ?? new Error('The model connections are unavailable.')}
          onRetry={() => void bindings.refetch()}
        />
      ) : (
        <>
          {visibleOrder.length === 0 ? (
            <p className="core-muted">{t('models.noBindings')}</p>
          ) : (
            <ul className="core-binding-list">
              {visibleOrder.map((bindingId, index) => {
                const absoluteIndex = visibleStart + index;
                const binding = byId.get(bindingId);
                if (!binding) return null;
                return (
                  <li
                    key={binding.id}
                    className={`core-binding-row${dragged === binding.id ? ' is-dragging' : ''}`}
                    draggable={!busy && !reconciliationRequired && !replayAttempt}
                    onDragStart={() => setDragged(binding.id)}
                    onDragEnd={() => setDragged(null)}
                    onDragOver={(event) => event.preventDefault()}
                    onDrop={(event) => drop(event, binding.id)}
                  >
                    <div className="core-binding-row__top">
                      <div>
                        <strong className="core-mono">{binding.upstream_model_id}</strong>
                        <div className="core-muted">
                          <ConnectorLabel value={binding.connector_type} /> ·{' '}
                          {binding.endpoint_base_url}
                        </div>
                      </div>
                      <StatusPill tone="neutral">#{absoluteIndex + 1}</StatusPill>
                    </div>
                    <div className="core-muted core-mono">
                      {binding.endpoint_key_display_head}…{binding.endpoint_key_display_tail}
                    </div>
                    {binding.endpoint_key_note ? <div>{binding.endpoint_key_note}</div> : null}
                    {binding.endpoint_note ? (
                      <div className="core-muted">{binding.endpoint_note}</div>
                    ) : null}
                    <div className="core-row-actions">
                      <div className="core-order-controls">
                        <button
                          type="button"
                          className="btn btn-secondary"
                          disabled={
                            busy ||
                            reconciliationRequired ||
                            Boolean(replayAttempt) ||
                            absoluteIndex === 0
                          }
                          onClick={() =>
                            setOrder((current) =>
                              moveBinding(current, absoluteIndex, absoluteIndex - 1),
                            )
                          }
                        >
                          {t('models.moveUp')}
                        </button>
                        <button
                          type="button"
                          className="btn btn-secondary"
                          disabled={
                            busy ||
                            reconciliationRequired ||
                            Boolean(replayAttempt) ||
                            absoluteIndex === order.length - 1
                          }
                          onClick={() =>
                            setOrder((current) =>
                              moveBinding(current, absoluteIndex, absoluteIndex + 1),
                            )
                          }
                        >
                          {t('models.moveDown')}
                        </button>
                      </div>
                      <button
                        type="button"
                        className="btn btn-danger"
                        disabled={busy || reconciliationRequired || Boolean(replayAttempt)}
                        onClick={() => setRemoving(binding)}
                      >
                        {t('models.removeBinding')}
                      </button>
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
          <PagePagination
            metadata={pagination}
            requestedPage={pager.page}
            busy={bindings.isFetching || busy || reconciliationRequired || Boolean(replayAttempt)}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
          />
        </>
      )}
      {asNotice(outcome)}
      <div className="core-form-actions">
        {reconciliationRequired ? (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={busy}
            onClick={() => void reconcile()}
          >
            {t('common.reconcile')}
          </button>
        ) : (
          <span />
        )}
        <button
          type="button"
          className="btn btn-primary"
          disabled={
            busy ||
            reconciliationRequired ||
            replayAttempt?.kind === 'delete' ||
            (!replayAttempt && !dirty)
          }
          onClick={() => void saveOrder()}
        >
          {busy
            ? t('common.working')
            : replayAttempt?.kind === 'order'
              ? t('common.reconcile')
              : t('models.saveOrder')}
        </button>
      </div>
      <ConfirmDialog
        open={Boolean(removing)}
        title={t('models.removeBindingTitle')}
        description={t('models.removeBindingBody')}
        confirmLabel={
          replayAttempt?.kind === 'delete' ? t('common.reconcile') : t('models.removeBinding')
        }
        danger
        busy={busy}
        onCancel={() => {
          if (!busy) setRemoving(null);
        }}
        onConfirm={() => void remove()}
      />
    </section>
  );
}

function ModelDetail({
  accountId,
  modelId,
  onBack,
  onDeleted,
}: {
  accountId: string;
  modelId: string;
  onBack: () => void;
  onDeleted: () => void;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const model = useModel(accountId, modelId);
  const [editing, setEditing] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const permissionScope = `${accountId}\u0000${modelId}`;
  const [permissionLost, setPermissionLost] = useState<PermissionLoss | null>(null);
  const retryAllowed = useRef(false);
  const operation = useRetainedOperation<{ expectedRevision: string }, void>(
    async (intent, key, context) => {
      await deleteModel(
        modelId,
        intent.expectedRevision,
        { idempotencyKey: key, actionId: key },
        context.signal,
      );
    },
    async (intent, error, context) => {
      let deleted = !error;
      if (error) {
        try {
          const current = await getModel(modelId, context.signal);
          context.commit(() => {
            retryAllowed.current = current.revision === intent.expectedRevision;
          });
        } catch (caught) {
          if (isNotFoundError(caught)) deleted = true;
          else throw caught;
        }
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: coreKeys.modelsRoot(accountId) }),
        invalidateResourceDependents(queryClient, accountId),
      ]);
      context.assertCurrent();
      if (deleted) {
        context.commit(onDeleted);
        if (error) return { operationConfirmed: true };
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
  const outcome: VisibleOutcome =
    operation.outcome === 'unknown' || operation.outcome === 'conflict'
      ? operation.outcome
      : operation.outcome === 'failed'
        ? 'error'
        : null;
  const reconciliationRequired = operation.outcome === 'refresh-failed';
  const replayAttempt = operation.outcome === 'unknown' ? (operation.variables ?? null) : null;
  const reconcileDeletion = () => (operation.isSuccess ? operation.refresh() : operation.check());
  const remove = async () => {
    if (reconciliationRequired) {
      await operation.refresh();
      return;
    }
    if (replayAttempt) {
      await operation.check();
      if (retryAllowed.current) await operation.mutateAsync(replayAttempt).catch(() => undefined);
      return;
    }
    if (model.data)
      await operation.mutateAsync({ expectedRevision: model.data.revision }).catch(() => undefined);
  };

  const accessLossError =
    (permissionLost?.scope === permissionScope ? permissionLost.error : null) ??
    (isAccessLoss(model.error) ? model.error : null);

  if (accessLossError)
    return (
      <div className="page core-page core-stack">
        <PageHeader
          icon="models"
          title={t('models.detailTitle')}
          description={t('models.detailDescription')}
          back={
            <button type="button" className="btn btn-quiet" onClick={onBack}>
              {t('common.back')}
            </button>
          }
        />
        <CoreErrorPanel error={accessLossError} />
      </div>
    );
  if (model.isPending && !model.data)
    return (
      <div className="page core-page">
        <CoreLoading />
      </div>
    );
  if (!model.data)
    return (
      <div className="page core-page">
        <CoreErrorPanel
          error={model.error ?? new Error('The model details are unavailable.')}
          onRetry={() => void model.refetch()}
        />
      </div>
    );

  return (
    <div className="page core-page core-stack">
      <PageHeader
        icon="models"
        title={t('models.detailTitle')}
        description={t('models.detailDescription')}
        back={
          <button type="button" className="btn btn-quiet" onClick={onBack}>
            {t('common.back')}
          </button>
        }
        actions={
          <button
            type="button"
            className="btn btn-secondary"
            disabled={reconciliationRequired || Boolean(replayAttempt)}
            onClick={() => setEditing(true)}
          >
            {t('models.editModel')}
          </button>
        }
      />
      {editing ? (
        <ModelEditor
          accountId={accountId}
          key={model.data.revision}
          initial={model.data}
          onCancel={() => setEditing(false)}
          onSaved={() => setEditing(false)}
          onCapabilityLoss={(error) => setPermissionLost({ scope: permissionScope, error })}
        />
      ) : (
        <section className="core-card">
          <div className="core-card__header">
            <h2>{t('models.configurationTitle')}</h2>
          </div>
          <dl className="core-detail-list">
            <div>
              <dt>{t('models.fullName')}</dt>
              <dd>
                <SafeCopyValue value={model.data.full_name} label={t('models.fullName')} />
              </dd>
            </div>
            <div>
              <dt>{t('models.strategy')}</dt>
              <dd>
                {model.data.route_strategy === 'ordered' ? t('models.ordered') : t('models.random')}
              </dd>
            </div>
            <div>
              <dt>{t('models.silentRetry')}</dt>
              <dd>{model.data.silent_retry ? t('common.yes') : t('common.no')}</dd>
            </div>
            <div>
              <dt>{t('models.flattenTools')}</dt>
              <dd>{model.data.flatten_tool_calls ? t('common.yes') : t('common.no')}</dd>
            </div>
            <div>
              <dt>{t('models.bindingCount')}</dt>
              <dd className="core-number">{model.data.binding_count}</dd>
            </div>
            <div>
              <dt>{t('common.updated')}</dt>
              <dd>
                <CoreTime value={model.data.updated_at} />
              </dd>
            </div>
          </dl>
        </section>
      )}
      <BindingSelector accountId={accountId} model={model.data} />
      <BindingOrder accountId={accountId} model={model.data} />
      <section className="core-card core-danger-zone">
        <div className="core-card__header">
          <h2>{t('endpoints.dangerTitle')}</h2>
        </div>
        <p>{t('models.deleteModelBody')}</p>
        {asNotice(outcome)}
        <div className="core-form-actions">
          {reconciliationRequired ? (
            <button
              type="button"
              className="btn btn-secondary"
              disabled={busy}
              onClick={() => void reconcileDeletion()}
            >
              {t('common.reconcile')}
            </button>
          ) : (
            <span />
          )}
          {replayAttempt && !deleteOpen ? (
            <button
              type="button"
              className="btn btn-secondary"
              disabled={busy || reconciliationRequired}
              onClick={() => void remove()}
            >
              {t('common.reconcile')}
            </button>
          ) : null}
          <button
            type="button"
            className="btn btn-danger"
            disabled={busy || reconciliationRequired || Boolean(replayAttempt)}
            onClick={() => setDeleteOpen(true)}
          >
            {t('models.deleteModel')}
          </button>
        </div>
      </section>
      <ConfirmDialog
        open={deleteOpen}
        title={t('models.deleteModelTitle')}
        description={t('models.deleteModelBody')}
        confirmLabel={replayAttempt ? t('common.reconcile') : t('models.deleteModel')}
        danger
        busy={busy}
        onCancel={() => {
          if (!busy) setDeleteOpen(false);
        }}
        onConfirm={() => void remove()}
      />
    </div>
  );
}

export function ModelsWorkspace({ user }: { user: UserProfile }) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const session = useCoreSession(false);
  const [searchParams, setSearchParams] = useSearchState();
  const [creating, setCreating] = useState(false);
  const [permissionLost, setPermissionLost] = useState<PermissionLoss | null>(null);
  const deletedModelRef = useRef<{ accountId: string; id: string } | null>(null);
  const selectedModelId = selectedModelID(searchParams);
  useEffect(() => {
    const deletedModel = deletedModelRef.current;
    if (
      !deletedModel ||
      (deletedModel.accountId === user.id && deletedModel.id === selectedModelId)
    )
      return;
    // The detail has unmounted before eviction, so its observer cannot start
    // another request for a model whose deletion has already been confirmed.
    for (const queryKey of [
      coreKeys.model(deletedModel.accountId, deletedModel.id),
      coreKeys.bindings(deletedModel.accountId, deletedModel.id),
      coreKeys.candidatesRoot(deletedModel.accountId, deletedModel.id),
    ])
      queryClient.removeQueries({ queryKey, type: 'inactive' });
    deletedModelRef.current = null;
  }, [queryClient, selectedModelId, user.id]);
  const scopeReady = !session.error && session.data?.accountId === user.id;
  const filters = useResourceFilters('models', user.id);
  const pager = useUrlPagePager({
    station: 'user',
    listType: 'models',
    scopeKey: user.id,
    scopeReady,
    resetKey: filters.identity,
    pageParam: 'page',
    pageSizeParam: 'page_size',
  });
  const models = useNumberedModels(
    user.id,
    { page: pager.page, pageSize: pager.pageSize },
    scopeReady && !selectedModelId,
    filters.filters,
  );
  useResourceListScroll(user.id, Boolean(models.data) && !models.isFetching, !selectedModelId);
  const accessLossError =
    (permissionLost?.scope === user.id ? permissionLost.error : null) ??
    (isAccessLoss(models.error) ? models.error : null);

  const setSelectedModelID = (modelId: string | null) => {
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.delete('model_id');
      if (modelId) next.set('model_id', modelId);
      return next;
    });
  };

  if (!scopeReady) {
    return (
      <div className="page core-page">
        {session.error ? <CoreErrorPanel error={session.error} /> : <CoreLoading />}
      </div>
    );
  }

  if (selectedModelId) {
    return (
      <ModelDetail
        key={`${user.id}:${selectedModelId}`}
        accountId={user.id}
        modelId={selectedModelId}
        onBack={() => setSelectedModelID(null)}
        onDeleted={() => {
          deletedModelRef.current = { accountId: user.id, id: selectedModelId };
          setSearchParams((previous) => {
            if (selectedModelID(previous) !== selectedModelId) return previous;
            const next = new URLSearchParams(previous);
            next.delete('model_id');
            return next;
          });
        }}
      />
    );
  }

  if (accessLossError) {
    return (
      <div className="page core-page core-stack">
        <PageHeader icon="models" title={t('models.title')} description={t('models.description')} />
        <CoreErrorPanel error={accessLossError} />
      </div>
    );
  }

  return (
    <div className="page core-page core-stack">
      <PageHeader
        icon="models"
        title={t('models.title')}
        description={t('models.description')}
        actions={
          <button type="button" className="btn btn-primary" onClick={() => setCreating(true)}>
            {t('models.create')}
          </button>
        }
      />
      {creating ? (
        <ModelEditor
          key={user.id}
          accountId={user.id}
          onCancel={() => setCreating(false)}
          onCapabilityLoss={(error) => setPermissionLost({ scope: user.id, error })}
          onSaved={(saved) => {
            setCreating(false);
            setSelectedModelID(saved.id);
          }}
        />
      ) : null}
      <ResourceFilterBar control={filters} />
      {models.isPending && !models.data ? (
        <CoreLoading />
      ) : models.error && !models.data ? (
        <CoreErrorPanel error={models.error} onRetry={() => void models.refetch()} />
      ) : models.data ? (
        <section className="core-card">
          {models.error ? (
            <CoreErrorPanel compact error={models.error} onRetry={() => void models.refetch()} />
          ) : null}
          {models.data.data.length === 0 && filters.active ? (
            <FilteredResourceEmpty control={filters} />
          ) : models.data.data.length === 0 ? (
            <CoreEmpty
              title={t('models.emptyTitle')}
              body={t('models.emptyBody')}
              action={
                <button type="button" className="btn btn-primary" onClick={() => setCreating(true)}>
                  {t('models.create')}
                </button>
              }
            />
          ) : (
            <ul className="core-endpoint-list">
              {models.data.data.map((model) => (
                <li key={model.id} className="core-endpoint-card">
                  <div className="core-endpoint-card__top">
                    <div>
                      <strong className="core-mono">{model.full_name}</strong>
                      <div className="core-muted">
                        {model.route_strategy === 'ordered'
                          ? t('models.ordered')
                          : t('models.random')}
                      </div>
                    </div>
                  </div>
                  <ModelBrowseSummary model={model} />
                  <dl className="core-detail-list">
                    <div>
                      <dt>{t('models.silentRetry')}</dt>
                      <dd>{model.silent_retry ? t('common.yes') : t('common.no')}</dd>
                    </div>
                    <div>
                      <dt>{t('models.flattenTools')}</dt>
                      <dd>{model.flatten_tool_calls ? t('common.yes') : t('common.no')}</dd>
                    </div>
                    <div>
                      <dt>{t('common.updated')}</dt>
                      <dd>
                        <CoreTime value={model.updated_at} />
                      </dd>
                    </div>
                  </dl>
                  <div className="core-row-actions">
                    <span />
                    <button
                      type="button"
                      className="btn btn-secondary"
                      disabled={models.isFetching || Boolean(models.error)}
                      onClick={() => setSelectedModelID(model.id)}
                    >
                      {t('models.manage')}
                    </button>
                  </div>
                </li>
              ))}
            </ul>
          )}
          <PagePagination
            metadata={models.data.pagination}
            requestedPage={pager.page}
            busy={models.isFetching}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
          />
        </section>
      ) : (
        <CoreLoading />
      )}
    </div>
  );
}

import {
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
  type DragEvent,
  type FormEvent,
} from 'react';
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
import { useCoreCopy } from './copy';
import { validateLogicalName, validatePersonalProviderName } from './normalizers';
import {
  applyBindingsResponse,
  coreKeys,
  coreSessionMatchesAccount,
  invalidateResourceDependents,
  useCoreSession,
  useBindings,
  useModel,
} from './queries';
import { useNumberedBindingCandidates, useNumberedModels } from './modelNumberedQueries';
import { useNumberedEndpointKeys, useNumberedEndpoints } from './numberedQueries';
import { createOperationIdentity, isConflict, isOutcomeUnknown } from './request';
import {
  bindingDraftReducer,
  initialBindingDraftState,
  type MutationOutcome,
} from './stateMachines';
import type {
  Binding,
  BindingCandidate,
  BindingSelection,
  CatalogSourceType,
  Model,
  ModelCreateInput,
  ModelPatchInput,
  OperationIdentity,
  RouteStrategy,
  UserProfile,
} from './types';
import type { PageMetadata, PageSize } from '@shared/operations/pageNumbers';
import type { NumberedPage } from './pageTypes';

type VisibleOutcome = 'conflict' | 'unknown' | 'error' | null;
type PermissionLoss = { scope: string; error: unknown };

function visibleOutcome(error: unknown): VisibleOutcome {
  if (isConflict(error)) return 'conflict';
  if (isOutcomeUnknown(error)) return 'unknown';
  return 'error';
}

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
  const [busy, setBusy] = useState(false);
  const [validation, setValidation] = useState(false);
  const [outcome, setOutcome] = useState<VisibleOutcome>(null);
  const [permissionLost, setPermissionLost] = useState<unknown>(null);
  const attemptRef = useRef<{
    operation: OperationIdentity;
    input: ModelCreateInput | ModelPatchInput;
  } | null>(null);
  const [hasAttempt, setHasAttempt] = useState(false);

  const discardStaleMutation = () => {
    attemptRef.current = null;
    setHasAttempt(false);
    setBusy(false);
    setOutcome(null);
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setValidation(false);
    setOutcome(null);
    try {
      validatePersonalProviderName(provider);
      validateLogicalName(modelName);
    } catch {
      setValidation(true);
      return;
    }
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }

    const input: ModelCreateInput | ModelPatchInput = initial
      ? {
          provider,
          model: modelName,
          route_strategy: strategy,
          silent_retry: silentRetry,
          flatten_tool_calls: flattenTools,
          expected_revision: initial.revision,
        }
      : {
          provider,
          model: modelName,
          route_strategy: strategy,
          silent_retry: silentRetry,
          flatten_tool_calls: flattenTools,
        };
    const attempt = attemptRef.current ?? { input, operation: createOperationIdentity() };
    attemptRef.current = attempt;
    setHasAttempt(true);
    setBusy(true);
    try {
      const saved = initial
        ? await patchModel(initial.id, attempt.input as ModelPatchInput, attempt.operation)
        : await createModel(attempt.input as ModelCreateInput, attempt.operation);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      attemptRef.current = null;
      setHasAttempt(false);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      queryClient.setQueryData(coreKeys.model(accountId, saved.id), saved);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: coreKeys.modelsRoot(accountId) }),
        invalidateResourceDependents(queryClient, accountId),
      ]);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      onSaved(saved);
    } catch (error) {
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      if (isAccessLoss(error)) {
        setPermissionLost(error);
        onCapabilityLoss?.(error);
        discardStaleMutation();
        return;
      }
      const nextOutcome = visibleOutcome(error);
      if (nextOutcome !== 'unknown') {
        attemptRef.current = null;
        setHasAttempt(false);
      }
      setOutcome(nextOutcome);
      if (initial && (isConflict(error) || isOutcomeUnknown(error))) {
        if (!coreSessionMatchesAccount(queryClient, accountId)) {
          discardStaleMutation();
          return;
        }
        await Promise.all([
          queryClient.invalidateQueries({ queryKey: coreKeys.model(accountId, initial.id) }),
          queryClient.invalidateQueries({ queryKey: coreKeys.modelsRoot(accountId) }),
        ]);
        if (!coreSessionMatchesAccount(queryClient, accountId)) discardStaleMutation();
      } else if (!initial && isOutcomeUnknown(error)) {
        if (!coreSessionMatchesAccount(queryClient, accountId)) {
          discardStaleMutation();
          return;
        }
        await queryClient.invalidateQueries({ queryKey: coreKeys.modelsRoot(accountId) });
        if (!coreSessionMatchesAccount(queryClient, accountId)) discardStaleMutation();
      }
    } finally {
      setBusy(false);
    }
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
      {validation ? (
        <p className="core-inline-error" role="alert">
          {t('models.invalidName')}
        </p>
      ) : null}
      {asNotice(outcome)}
      <div className="core-form-actions">
        <span />
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? t('common.working') : hasAttempt ? t('common.retrySame') : t('common.save')}
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
  const permissionScope = `${accountId}\u0000${model.id}`;
  const [endpointId, setEndpointId] = useState('');
  const [keyId, setKeyId] = useState('');
  const [modelQuery, setModelQuery] = useState('');
  const [queryDraft, setQueryDraft] = useState('');
  const [selectionDetails, setSelectionDetails] = useState<Record<string, BindingCandidate>>({});
  const [invalidSelection, setInvalidSelection] = useState(false);
  const [replayAttempt, setReplayAttempt] = useState<{
    revision: string;
    selections: BindingSelection[];
    operation: OperationIdentity;
  } | null>(null);
  const [permissionLost, setPermissionLost] = useState<PermissionLoss | null>(null);
  const endpointPager = usePagePager({
    station: 'user',
    listType: 'models-binding-endpoints',
    scopeKey: accountId,
  });
  const keyPager = usePagePager({
    station: 'user',
    listType: 'models-binding-keys',
    scopeKey: `${accountId}\u0000${model.id}`,
    resetKey: endpointId,
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
  const endpoints = useNumberedEndpoints(accountId, {
    page: endpointPager.page,
    pageSize: endpointPager.pageSize,
  });
  const keys = useNumberedEndpointKeys(
    accountId,
    endpointId || undefined,
    { page: keyPager.page, pageSize: keyPager.pageSize },
    Boolean(endpointId),
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
  const accessLossError =
    (permissionLost?.scope === permissionScope ? permissionLost.error : null) ??
    queryPermissionError ??
    null;

  const discardStaleMutation = () => {
    setReplayAttempt(null);
    setInvalidSelection(false);
    dispatch({
      type: 'boundary',
      accountId,
      modelId: model.id,
      bindingRevision: model.binding_revision,
    });
  };

  useEffect(() => {
    dispatch({
      type: 'boundary',
      accountId,
      modelId: model.id,
      bindingRevision: model.binding_revision,
    });
    let active = true;
    queueMicrotask(() => {
      if (!active) return;
      setEndpointId('');
      setKeyId('');
      setModelQuery('');
      setQueryDraft('');
      setSelectionDetails({});
      setInvalidSelection(false);
      setReplayAttempt(null);
    });
    return () => {
      active = false;
    };
  }, [accountId, model.binding_revision, model.id]);

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
    setEndpointId(next);
    setKeyId('');
    setModelQuery('');
    setQueryDraft('');
  };

  const chooseKey = (next: string) => {
    setKeyId(next);
    setModelQuery('');
    setQueryDraft('');
  };

  const toggleCandidate = (candidate: BindingCandidate) => {
    setSelectionDetails((current) => ({ ...current, [candidateIdentity(candidate)]: candidate }));
    dispatch({ type: 'toggle', accountId, modelId: model.id, candidate });
  };

  const reconcileSelections = async (): Promise<boolean> => {
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return false;
    }
    let removed = false;
    for (const selection of draft.selections) {
      try {
        const page = await getBindingCandidatesPage(
          model.id,
          {
            keyId: selection.endpoint_key_id,
            query: selection.upstream_model_id,
          },
          { page: '1', pageSize: 100 },
        );
        if (!coreSessionMatchesAccount(queryClient, accountId)) {
          discardStaleMutation();
          return false;
        }
        const stillValid = page.data.some(
          (candidate) => candidateIdentity(candidate) === candidateIdentity(selection),
        );
        if (!stillValid && page.pagination.total_pages === '1') {
          removed = true;
          dispatch({
            type: 'candidate-invalid',
            accountId,
            modelId: model.id,
            candidate: selection,
          });
        }
      } catch (error) {
        if (isAccessLoss(error)) {
          setPermissionLost({ scope: permissionScope, error });
          discardStaleMutation();
          return false;
        }
        // A failed verifier is not evidence that a selection disappeared.
      }
    }
    return removed;
  };

  const reconcileAuthority = async (attempt = replayAttempt): Promise<boolean> => {
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return false;
    }
    const refreshed = await bindings.refetch();
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return false;
    }
    if (isAccessLoss(refreshed.error)) {
      setPermissionLost({ scope: permissionScope, error: refreshed.error });
      discardStaleMutation();
      return false;
    }
    if (refreshed.error || !refreshed.data) return false;
    const authoritativeBound = new Set(refreshed.data.bindings.map(candidateIdentity));
    const confirmed =
      Boolean(attempt) &&
      attempt!.selections.every((selection) =>
        authoritativeBound.has(candidateIdentity(selection)),
      );
    if (!attempt || confirmed) {
      for (const selection of attempt?.selections ?? draft.selections) {
        if (!authoritativeBound.has(candidateIdentity(selection))) continue;
        dispatch({ type: 'candidate-invalid', accountId, modelId: model.id, candidate: selection });
      }
    }
    if (confirmed) setReplayAttempt(null);
    dispatch({
      type: 'authoritative',
      accountId,
      modelId: model.id,
      bindingRevision: refreshed.data.binding_revision,
    });
    return true;
  };

  const submit = async () => {
    if (draft.status === 'pending' || (!replayAttempt && draft.selections.length === 0)) return;
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    setInvalidSelection(false);
    const attempt = replayAttempt ?? {
      operation: createOperationIdentity(),
      revision: draft.bindingRevision,
      selections: [...draft.selections],
    };
    const { operation, revision, selections } = attempt;
    dispatch({ type: 'submit', accountId, modelId: model.id, actionId: operation.actionId });
    try {
      const response = await addBindings(model.id, revision, selections, operation);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      setReplayAttempt(null);
      if (!applyBindingsResponse(queryClient, accountId, model.id, response)) {
        discardStaleMutation();
        return;
      }
      dispatch({
        type: 'result',
        accountId,
        modelId: model.id,
        actionId: operation.actionId,
        outcome: 'success',
        bindingRevision: response.binding_revision,
      });
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      await queryClient.invalidateQueries({
        queryKey: coreKeys.candidatesRoot(accountId, model.id),
      });
      if (!coreSessionMatchesAccount(queryClient, accountId)) discardStaleMutation();
    } catch (error) {
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      if (isAccessLoss(error)) {
        setPermissionLost({ scope: permissionScope, error });
        discardStaleMutation();
        return;
      }
      const outcome: Exclude<MutationOutcome, 'idle' | 'pending' | 'success'> = isConflict(error)
        ? 'conflict'
        : isOutcomeUnknown(error)
          ? 'unknown'
          : 'error';
      if (outcome === 'error') {
        const invalid = await reconcileSelections();
        if (!coreSessionMatchesAccount(queryClient, accountId)) {
          discardStaleMutation();
          return;
        }
        setInvalidSelection(invalid);
      }
      if (outcome === 'conflict' || outcome === 'unknown') {
        setReplayAttempt(outcome === 'unknown' ? attempt : null);
        dispatch({
          type: 'result',
          accountId,
          modelId: model.id,
          actionId: operation.actionId,
          outcome,
          bindingRevision: revision,
        });
        await reconcileAuthority(outcome === 'unknown' ? attempt : null);
        return;
      }
      dispatch({
        type: 'result',
        accountId,
        modelId: model.id,
        actionId: operation.actionId,
        outcome,
        bindingRevision: revision,
      });
    }
  };

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
          {endpoints.data?.data.find((entry) => entry.id === endpointId)?.note} ·{' '}
          {endpoints.data?.data.find((entry) => entry.id === endpointId)?.base_url}
          {keyId
            ? ` / ${keys.data?.data.find((entry) => entry.id === keyId)?.note || ''} · ${keys.data?.data.find((entry) => entry.id === keyId)?.display_head || ''}…${keys.data?.data.find((entry) => entry.id === keyId)?.display_tail || ''}`
            : ''}
        </p>
      ) : null}
      <div className="core-selector">
        <section className="core-selector__level" hidden={Boolean(endpointId)}>
          <h3>{t('models.levelEndpoint')}</h3>
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
                  disabled={draft.status === 'pending' || Boolean(replayAttempt)}
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
            : draft.status === 'conflict' || draft.status === 'unknown' || draft.status === 'error'
              ? draft.status
              : null
        }
      />
      <div className="core-form-actions">
        {draft.status === 'conflict' || draft.status === 'unknown' ? (
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
            draft.status === 'pending' ||
            draft.status === 'conflict' ||
            draft.status === 'unknown' ||
            (!replayAttempt && draft.selections.length === 0)
          }
          onClick={() => void submit()}
        >
          {draft.status === 'pending'
            ? t('common.working')
            : replayAttempt
              ? t('common.retrySame')
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
  const permissionScope = `${accountId}\u0000${model.id}`;
  const pager = usePagePager({
    station: 'user',
    listType: 'models-binding-order',
    scopeKey: `${accountId}\u0000${model.id}`,
    resetKey: model.binding_revision,
  });
  const [order, setOrder] = useState<string[]>([]);
  const [dragged, setDragged] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<VisibleOutcome>(null);
  const [reconciliationRequired, setReconciliationRequired] = useState(false);
  const [removing, setRemoving] = useState<Binding | null>(null);
  const [replayAttempt, setReplayAttempt] = useState<
    | { kind: 'order'; expectedRevision: string; order: string[]; operation: OperationIdentity }
    | { kind: 'delete'; expectedRevision: string; bindingId: string; operation: OperationIdentity }
    | null
  >(null);
  const [permissionLost, setPermissionLost] = useState<PermissionLoss | null>(null);

  const discardStaleMutation = () => {
    setReplayAttempt(null);
    setBusy(false);
    setOutcome(null);
    setReconciliationRequired(false);
    setRemoving(null);
  };

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

  const reconcile = async (attempt = replayAttempt) => {
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return false;
    }
    const [refreshed] = await Promise.all([
      bindings.refetch(),
      queryClient.invalidateQueries({ queryKey: coreKeys.model(accountId, model.id) }),
      queryClient.invalidateQueries({ queryKey: coreKeys.modelsRoot(accountId) }),
    ]);
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return false;
    }
    if (isAccessLoss(refreshed.error)) {
      setPermissionLost({ scope: permissionScope, error: refreshed.error });
      discardStaleMutation();
      return false;
    }
    const ready = !refreshed.error && Boolean(refreshed.data);
    setReconciliationRequired(!ready);
    if (refreshed.data) {
      const authoritativeOrder = refreshed.data.bindings.map((binding) => binding.id);
      setOrder(authoritativeOrder);
      if (removing && !refreshed.data.bindings.some((binding) => binding.id === removing.id)) {
        setRemoving(null);
      }
      const confirmed =
        attempt?.kind === 'order'
          ? authoritativeOrder.length === attempt.order.length &&
            authoritativeOrder.every((id, index) => id === attempt.order[index])
          : attempt?.kind === 'delete'
            ? !refreshed.data.bindings.some((binding) => binding.id === attempt.bindingId)
            : false;
      if (confirmed) {
        setReplayAttempt(null);
        setOutcome(null);
        if (attempt?.kind === 'delete') setRemoving(null);
      } else if (attempt) {
        setOutcome('unknown');
      } else if (ready) {
        setOutcome(null);
      }
    }
    return ready;
  };

  const saveOrder = async () => {
    if (
      !bindings.data ||
      busy ||
      reconciliationRequired ||
      replayAttempt?.kind === 'delete' ||
      (!replayAttempt && !dirty)
    )
      return;
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    const attempt =
      replayAttempt?.kind === 'order'
        ? replayAttempt
        : {
            kind: 'order' as const,
            expectedRevision: bindings.data.binding_revision,
            order: [...order],
            operation: createOperationIdentity(),
          };
    setBusy(true);
    setOutcome(null);
    try {
      const response = await orderBindings(
        model.id,
        attempt.expectedRevision,
        attempt.order,
        attempt.operation,
      );
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      setReplayAttempt(null);
      if (!applyBindingsResponse(queryClient, accountId, model.id, response))
        discardStaleMutation();
    } catch (error) {
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      if (isAccessLoss(error)) {
        setPermissionLost({ scope: permissionScope, error });
        discardStaleMutation();
        return;
      }
      const nextOutcome = visibleOutcome(error);
      setReplayAttempt(nextOutcome === 'unknown' ? attempt : null);
      setOutcome(nextOutcome);
      if (isConflict(error) || isOutcomeUnknown(error))
        await reconcile(nextOutcome === 'unknown' ? attempt : null);
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (
      !bindings.data ||
      !removing ||
      busy ||
      reconciliationRequired ||
      replayAttempt?.kind === 'order'
    )
      return;
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    const attempt =
      replayAttempt?.kind === 'delete'
        ? replayAttempt
        : {
            kind: 'delete' as const,
            expectedRevision: bindings.data.binding_revision,
            bindingId: removing.id,
            operation: createOperationIdentity(),
          };
    setBusy(true);
    setOutcome(null);
    try {
      const response = await deleteBinding(
        model.id,
        attempt.bindingId,
        attempt.expectedRevision,
        attempt.operation,
      );
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      setReplayAttempt(null);
      if (!applyBindingsResponse(queryClient, accountId, model.id, response)) {
        discardStaleMutation();
        return;
      }
      setRemoving(null);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      await queryClient.invalidateQueries({
        queryKey: coreKeys.candidatesRoot(accountId, model.id),
      });
      if (!coreSessionMatchesAccount(queryClient, accountId)) discardStaleMutation();
    } catch (error) {
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      if (isAccessLoss(error)) {
        setPermissionLost({ scope: permissionScope, error });
        discardStaleMutation();
        return;
      }
      const nextOutcome = visibleOutcome(error);
      setReplayAttempt(nextOutcome === 'unknown' ? attempt : null);
      setOutcome(nextOutcome);
      if (isConflict(error) || isOutcomeUnknown(error))
        await reconcile(nextOutcome === 'unknown' ? attempt : null);
    } finally {
      setBusy(false);
    }
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
    (permissionLost?.scope === permissionScope ? permissionLost.error : null) ??
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
              ? t('common.retrySame')
              : t('models.saveOrder')}
        </button>
      </div>
      <ConfirmDialog
        open={Boolean(removing)}
        title={t('models.removeBindingTitle')}
        description={t('models.removeBindingBody')}
        confirmLabel={
          replayAttempt?.kind === 'delete' ? t('common.retrySame') : t('models.removeBinding')
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
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<VisibleOutcome>(null);
  const [reconciliationRequired, setReconciliationRequired] = useState(false);
  const [replayAttempt, setReplayAttempt] = useState<{
    expectedRevision: string;
    operation: OperationIdentity;
  } | null>(null);
  const permissionScope = `${accountId}\u0000${modelId}`;
  const [permissionLost, setPermissionLost] = useState<PermissionLoss | null>(null);

  const accessLossError =
    (permissionLost?.scope === permissionScope ? permissionLost.error : null) ??
    (isAccessLoss(model.error) ? model.error : null);

  const discardStaleMutation = () => {
    setReplayAttempt(null);
    setBusy(false);
    setOutcome(null);
    setReconciliationRequired(false);
    setDeleteOpen(false);
    setEditing(false);
  };

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

  const reconcileDeletion = async () => {
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    const result = await model.refetch();
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    if (isAccessLoss(result.error)) {
      setPermissionLost({ scope: permissionScope, error: result.error });
      discardStaleMutation();
      return;
    }
    if (result.error && isNotFoundError(result.error)) {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: coreKeys.modelsRoot(accountId) }),
        invalidateResourceDependents(queryClient, accountId),
      ]);
      setReplayAttempt(null);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      onDeleted();
      return;
    }
    setReconciliationRequired(Boolean(result.error));
    if (!result.error && !replayAttempt) setOutcome(null);
  };

  const remove = async () => {
    if (reconciliationRequired) return;
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    const attempt = replayAttempt ?? {
      expectedRevision: model.data.revision,
      operation: createOperationIdentity(),
    };
    setBusy(true);
    setOutcome(null);
    try {
      await deleteModel(model.data.id, attempt.expectedRevision, attempt.operation);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      setReplayAttempt(null);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: coreKeys.modelsRoot(accountId) }),
        invalidateResourceDependents(queryClient, accountId),
      ]);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      onDeleted();
    } catch (error) {
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      if (isAccessLoss(error)) {
        setPermissionLost({ scope: permissionScope, error });
        discardStaleMutation();
        return;
      }
      const nextOutcome = visibleOutcome(error);
      setReplayAttempt(nextOutcome === 'unknown' ? attempt : null);
      setOutcome(nextOutcome);
      if (nextOutcome === 'unknown' || nextOutcome === 'conflict') {
        if (!coreSessionMatchesAccount(queryClient, accountId)) {
          discardStaleMutation();
          return;
        }
        const refreshed = await model.refetch();
        if (!coreSessionMatchesAccount(queryClient, accountId)) {
          discardStaleMutation();
          return;
        }
        if (refreshed.error && isNotFoundError(refreshed.error)) {
          await Promise.all([
            queryClient.invalidateQueries({ queryKey: coreKeys.modelsRoot(accountId) }),
            invalidateResourceDependents(queryClient, accountId),
          ]);
          setReplayAttempt(null);
          if (!coreSessionMatchesAccount(queryClient, accountId)) {
            discardStaleMutation();
            return;
          }
          onDeleted();
          return;
        }
        setReconciliationRequired(Boolean(refreshed.error));
        if (!refreshed.error) setOutcome(null);
      }
    } finally {
      setBusy(false);
    }
  };

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
              {t('common.retrySame')}
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
        confirmLabel={replayAttempt ? t('common.retrySame') : t('models.deleteModel')}
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
  const pager = useUrlPagePager({
    station: 'user',
    listType: 'models',
    scopeKey: user.id,
    scopeReady,
    resetKey: undefined,
    pageParam: 'page',
    pageSizeParam: 'page_size',
  });
  const models = useNumberedModels(
    user.id,
    { page: pager.page, pageSize: pager.pageSize },
    scopeReady && !selectedModelId,
  );
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
      {models.isPending && !models.data ? (
        <CoreLoading />
      ) : models.error && !models.data ? (
        <CoreErrorPanel error={models.error} onRetry={() => void models.refetch()} />
      ) : models.data ? (
        <section className="core-card">
          {models.error ? (
            <CoreErrorPanel compact error={models.error} onRetry={() => void models.refetch()} />
          ) : null}
          {models.data.data.length === 0 ? (
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

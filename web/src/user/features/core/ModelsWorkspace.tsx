import { Link } from 'react-router';
import { useModelText } from './models/copy';
import { ManualSource } from './models/ManualSource';
import {
  Panel,
  PanelHead,
  PanelBody,
  PanelFoot,
  Fold,
  Toggle,
  Segmented,
  SaveBar,
  MoreMenu,
  DataTable,
  type DataColumn,
  OutcomeNote,
} from '@shared/components/ui';
import { TransportRuleField, TransportRuleSummary } from '@shared/components/TransportRuleField';
import type { TransportRule } from '@shared/transportRule';
import { RolePolicyEditor } from '@shared/components/RolePolicyEditor';
import { buildRolePolicy, draftFromRolePolicy, type RolePolicy } from '@shared/rolePolicy';
import { useRegisteredCopy } from '@shared/i18n/useRegisteredCopy';
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
  type ReactNode,
} from 'react';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { readResourceResult, resourceStatus } from './resourceOperation';
import { useQueryClient } from '@tanstack/react-query';
import { useSearchState } from '@shared/operations/useSearchState';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { PageHeader } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { usePagePager } from '@shared/operations/usePagePager';
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
import { validateResourceId } from './normalizers';
import {
  CoreEmpty,
  CoreErrorPanel,
  CoreLoading,
  MutationNotice,
  SafeCopyValue,
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
  Model,
  ModelCreateInput,
  ModelPatchInput,
  RouteStrategy,
  UserProfile,
} from './types';
import type { PageMetadata, PageSize } from '@shared/operations/pageNumbers';

type VisibleOutcome = 'conflict' | 'unknown' | 'error' | null;
type PermissionLoss = { scope: string; error: unknown };

function asNotice(
  outcome: VisibleOutcome,
  onCheck: () => void,
  busy: boolean,
  savedRefreshFailed = false,
) {
  return savedRefreshFailed ? (
    <OutcomeNote busy={busy} outcome={{ kind: 'savedRefreshFailed', recheck: onCheck }} />
  ) : (
    <MutationNotice outcome={outcome} onCheck={onCheck} busy={busy} />
  );
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

function sameRolePolicy(current: RolePolicy | undefined, expected: RolePolicy): boolean {
  const policy = current ?? { default_action: 'native', rules: {} };
  return (
    policy.default_action === expected.default_action &&
    Object.keys(policy.rules).length === Object.keys(expected.rules).length &&
    Object.entries(expected.rules).every(([role, action]) => policy.rules[role] === action)
  );
}

const roleSummaryKeys = {
  title: 'common.rolePolicy.title',
  defaultAction: 'common.rolePolicy.defaultAction',
  nativeHelp: 'common.rolePolicy.nativeHelp',
  toolsHelp: 'common.rolePolicy.toolsHelp',
  native: 'common.rolePolicy.action.native',
  passthrough: 'common.rolePolicy.action.passthrough',
  system: 'common.rolePolicy.action.system',
  user: 'common.rolePolicy.action.user',
  assistant: 'common.rolePolicy.action.assistant',
  reject: 'common.rolePolicy.action.reject',
} as const;

function ModelRoleSummary({ policy }: { policy?: RolePolicy }) {
  const { t: text } = useRegisteredCopy(roleSummaryKeys);
  const current = policy ?? { default_action: 'native', rules: {} };
  return (
    <section className="core-card">
      <h2>{text('title')}</h2>
      <dl className="core-detail-list">
        <div>
          <dt>{text('defaultAction')}</dt>
          <dd>{text(current.default_action)}</dd>
        </div>
        {Object.entries(current.rules).map(([role, action]) => (
          <div key={role}>
            <dt>{role}</dt>
            <dd>{text(action)}</dd>
          </div>
        ))}
      </dl>
      <p className="core-muted">{text('nativeHelp')}</p>
      <p className="core-muted">{text('toolsHelp')}</p>
    </section>
  );
}

function ModelEditor({
  accountId,
  initial,
  onCancel,
  onSaved,
  onCapabilityLoss,
  sources,
  initialStrategy,
}: {
  accountId: string;
  initial?: Model;
  initialStrategy?: RouteStrategy;
  sources?: (
    strategy: RouteStrategy,
    onChange: (value: RouteStrategy) => void,
    locked: boolean,
  ) => ReactNode;
  onCancel: () => void;
  onSaved: (model: Model) => void;
  onCapabilityLoss?: (error: unknown) => void;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const [provider, setProvider] = useState(initial?.provider ?? '');
  const [modelName, setModelName] = useState(initial?.model ?? '');
  const [strategy, setStrategy] = useState<RouteStrategy>(
    initialStrategy ?? initial?.route_strategy ?? 'ordered',
  );
  const [silentRetry, setSilentRetry] = useState(initial?.silent_retry ?? false);
  const [transportRule, setTransportRule] = useState<TransportRule>(
    initial?.transport_rule ?? 'passthrough',
  );
  const [flattenTools, setFlattenTools] = useState(initial?.flatten_tool_calls ?? false);
  const [roleDraft, setRoleDraft] = useState(() => draftFromRolePolicy(initial?.role_policy));
  const roleResult = buildRolePolicy(roleDraft);
  const textModel = useModelText();
  const formRef = useRef<HTMLFormElement>(null);
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
              field === 'expected_revision' ||
              (field === 'role_policy'
                ? sameRolePolicy(current.role_policy, value as RolePolicy)
                : current[field as keyof Model] === value),
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
      if (!roleResult.policy) return;
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
        transport_rule: transportRule,
        flatten_tool_calls: flattenTools,
        role_policy: roleResult.policy,
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

  const dirtyCount = initial
    ? [
        provider !== initial.provider,
        modelName !== initial.model,
        strategy !== initial.route_strategy,
        silentRetry !== initial.silent_retry,
        transportRule !== initial.transport_rule,
        flattenTools !== initial.flatten_tool_calls,
        !roleResult.policy || !sameRolePolicy(initial.role_policy, roleResult.policy),
      ].filter(Boolean).length
    : 0;
  const isDefault =
    !silentRetry &&
    !flattenTools &&
    transportRule === 'passthrough' &&
    roleResult.policy &&
    sameRolePolicy(undefined, roleResult.policy);
  const discard = () => {
    if (!initial || hasAttempt) return;
    setProvider(initial.provider);
    setModelName(initial.model);
    setStrategy(initial.route_strategy);
    setSilentRetry(initial.silent_retry);
    setTransportRule(initial.transport_rule);
    setFlattenTools(initial.flatten_tool_calls);
    setRoleDraft(draftFromRolePolicy(initial.role_policy));
  };
  return (
    <form ref={formRef} className="model-editor core-form" onSubmit={(event) => void submit(event)}>
      <Panel>
        <PanelHead
          title={initial ? textModel('callName') : t('models.create')}
          actions={
            <button
              type="button"
              className="btn btn-secondary"
              disabled={hasAttempt}
              onClick={onCancel}
            >
              {t('common.cancel')}
            </button>
          }
        />
        <PanelBody>
          <div className="core-field-grid">
            <label>
              <span>{t('models.provider')}</span>
              <input
                aria-label={t('models.provider')}
                className="core-mono"
                value={provider}
                maxLength={128}
                required
                disabled={hasAttempt}
                onChange={(event) => setProvider(event.target.value)}
              />
              <small>{textModel('prefixHelp')}</small>
            </label>
            <label>
              <span>{t('models.model')}</span>
              <input
                className="core-mono"
                value={modelName}
                maxLength={128}
                required
                disabled={hasAttempt}
                onChange={(event) => setModelName(event.target.value)}
              />
            </label>
          </div>
          <div className="core-model-preview">
            <span>{textModel('clientValue')}</span>
            <SafeCopyValue
              value={`${provider || t('models.provider')}/${modelName || t('models.model')}`}
              label={textModel('callName')}
            />
          </div>
        </PanelBody>
      </Panel>
      {initial ? (
        <>
          {sources?.(strategy, setStrategy, hasAttempt)}
          <Fold
            title={textModel('advanced')}
            summary={textModel('advancedHelp')}
            persistKey="model-advanced"
            meta={textModel(isDefault ? 'default' : 'customized')}
          >
            <Toggle
              label={t('models.silentRetry')}
              description={textModel('retryHelp')}
              checked={silentRetry}
              disabled={hasAttempt}
              onChange={setSilentRetry}
            />
            <TransportRuleField
              value={transportRule}
              onChange={setTransportRule}
              disabled={hasAttempt}
            />
            <Toggle
              label={t('models.flattenTools')}
              description={textModel('toolsHelp')}
              checked={flattenTools}
              disabled={hasAttempt}
              onChange={setFlattenTools}
            />
            <RolePolicyEditor value={roleDraft} onChange={setRoleDraft} disabled={hasAttempt} />
          </Fold>
        </>
      ) : null}
      {validation ? (
        <p className="core-inline-error" role="alert">
          {t('models.invalidName')}
        </p>
      ) : null}
      {asNotice(
        outcome,
        () => void (operation.isSuccess ? operation.refresh() : operation.check()),
        busy,
        operation.outcome === 'refresh-failed',
      )}
      {initial && dirtyCount > 3 && !hasAttempt ? (
        <SaveBar
          dirtyCount={dirtyCount}
          busy={busy}
          saveDisabled={!!roleResult.error}
          onSave={() => formRef.current?.requestSubmit()}
          onDiscard={discard}
          saveLabel={t('common.save')}
          discardLabel={textModel('discard')}
          dirtyLabel={(count) => textModel('dirtyCount', { count })}
        />
      ) : (
        <PanelFoot>
          <button
            type="submit"
            className="btn btn-primary"
            disabled={busy || (!hasAttempt && !!roleResult.error)}
          >
            {busy ? t('common.working') : hasAttempt ? text('checkResult') : t('common.save')}
          </button>
        </PanelFoot>
      )}
    </form>
  );
}

function candidateIdentity(
  candidate: Pick<BindingCandidate, 'endpoint_key_id' | 'upstream_model_id'>,
): string {
  return `${candidate.endpoint_key_id}\u0000${candidate.upstream_model_id}`;
}

function BindingSelector({
  accountId,
  model,
  focusSearch = false,
  onSearchFocused,
}: {
  accountId: string;
  model: Model;
  focusSearch?: boolean;
  onSearchFocused?: () => void;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const textModel = useModelText();
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
  const [manualOpen, setManualOpen] = useState(false);
  const serviceFilter = useRef<HTMLDetailsElement>(null);
  const [candidatePageSize, setCandidatePageSize] = useState<PageSize>(10);
  const [manualLocked, setManualLocked] = useState(false);
  const [manualSaved, setManualSaved] = useState(false);
  const candidatePager = usePagePager({
    station: 'user',
    listType: 'models-binding-candidates',
    scopeKey: `${accountId}\u0000${model.id}`,
    resetKey: `${endpointId}\u0000${modelQuery}\u0000${candidatePageSize}`,
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
    Boolean(manualOpen && endpointId),
    { q: keyQuery },
  );
  const candidates = useNumberedBindingCandidates(
    accountId,
    model.id,
    { endpointId: endpointId || undefined, query: modelQuery },
    { page: candidatePager.page, pageSize: candidatePageSize },
  );
  useEffect(() => {
    const timer = window.setTimeout(() => setModelQuery(queryDraft.trim()), 300);
    return () => window.clearTimeout(timer);
  }, [queryDraft]);
  const [draft, dispatch] = useReducer(bindingDraftReducer, undefined, () =>
    initialBindingDraftState(accountId, model.id, model.binding_revision),
  );
  const bindingsKnown = Boolean(bindings.data);
  const searchInput = useRef<HTMLInputElement>(null);
  const focusedSearch = useRef(false);
  useEffect(() => {
    if (focusSearch && bindingsKnown && !focusedSearch.current) {
      searchInput.current?.focus();
      focusedSearch.current = true;
      onSearchFocused?.();
    }
  }, [focusSearch, bindingsKnown, onSearchFocused]);
  const queryPermissionError = [bindings.error, endpoints.error, keys.error, candidates.error].find(
    (error) => isAccessLoss(error),
  );
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
    setManualSaved(false);
  };
  const chooseKey = (next: string) => {
    setSelectedKey(keys.data?.data.find((entry) => entry.id === next));
    setKeyId(next);
    setManualSaved(false);
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
      <label className="model-source-search">
        <span>{textModel('sourceSearch')}</span>
        <input
          type="search"
          ref={searchInput}
          data-testid="model-source-search"
          aria-label={textModel('sourceSearch')}
          maxLength={256}
          placeholder={textModel('sourcePlaceholder')}
          value={queryDraft}
          disabled={!bindingsKnown || operation.isPending || Boolean(replayAttempt)}
          onChange={(event) => setQueryDraft(event.target.value)}
        />
        <small>{textModel('sourceSearchHelp')}</small>
      </label>
      <details ref={serviceFilter} className="nb-fold nb-fold--plain model-service-filter">
        <summary>
          {textModel('filterService')}
          {selectedEndpoint ? ` · ${selectedEndpoint.note || selectedEndpoint.base_url}` : ''}
        </summary>
        <div className="nb-fold__body core-form">
          <label>
            <span>{text('serviceSearch')}</span>
            <input
              type="search"
              value={endpointQuery}
              maxLength={128}
              disabled={manualLocked || operation.isPending || Boolean(replayAttempt)}
              onChange={(event) => setEndpointQuery(event.target.value)}
            />
          </label>
          <label>
            <span>{textModel('service')}</span>
            <select
              value={endpointId}
              disabled={
                manualLocked ||
                endpoints.isFetching ||
                operation.isPending ||
                Boolean(replayAttempt)
              }
              onChange={(event) => chooseEndpoint(event.target.value)}
            >
              <option value="">{textModel('allServices')}</option>
              {selectedEndpoint &&
              !endpoints.data?.data.some((endpoint) => endpoint.id === endpointId) ? (
                <option value={selectedEndpoint.id}>
                  {selectedEndpoint.note || selectedEndpoint.base_url}
                </option>
              ) : null}
              {endpoints.data?.data.map((endpoint) => (
                <option key={endpoint.id} value={endpoint.id} disabled={!endpoint.enabled}>
                  {endpoint.note || endpoint.base_url}
                </option>
              ))}
            </select>
          </label>
          {endpoints.error ? (
            <CoreErrorPanel
              compact
              error={endpoints.error}
              onRetry={() => void endpoints.refetch()}
            />
          ) : null}
          {endpoints.data ? (
            <PagePagination
              metadata={endpoints.data.pagination}
              requestedPage={endpointPager.page}
              busy={endpoints.isFetching || manualLocked}
              onPageChange={endpointPager.setPage}
              onPageSizeChange={endpointPager.setPageSize}
            />
          ) : null}
        </div>
      </details>
      <section
        className="model-source-results"
        aria-label={textModel('sourceSearch')}
        aria-busy={candidates.isFetching}
      >
        {candidates.isPending || queryDraft.trim() !== modelQuery ? (
          <CoreLoading compact />
        ) : candidates.error ? (
          <CoreErrorPanel
            compact
            error={candidates.error}
            onRetry={() => void candidates.refetch()}
          />
        ) : !candidates.data?.data.length ? (
          <p className="core-muted">{t('models.candidateEmpty')}</p>
        ) : (
          <ul className="model-source-list">
            {candidates.data.data.map((candidate) => {
              const identity = candidateIdentity(candidate);
              return (
                <li key={identity}>
                  <button
                    type="button"
                    className="model-source-result"
                    aria-pressed={selected.has(identity)}
                    disabled={
                      bound.has(identity) ||
                      !bindingsKnown ||
                      candidates.isFetching ||
                      operation.isPending ||
                      Boolean(replayAttempt)
                    }
                    onClick={() => toggleCandidate(candidate)}
                  >
                    <span className="model-source-main">
                      <strong className="core-mono">{candidate.upstream_model_id}</strong>
                      <span className="nb-sub">
                        {candidate.endpoint_note || candidate.endpoint_base_url} ·{' '}
                        {candidate.endpoint_key_note ||
                          `${candidate.endpoint_key_display_head}…${candidate.endpoint_key_display_tail}`}{' '}
                        ·{' '}
                        {candidate.source_types
                          .map((source) =>
                            source === 'automatic' ? t('models.automatic') : t('models.manual'),
                          )
                          .join(' + ')}
                      </span>
                    </span>
                    <span className="nb-badge">
                      {bound.has(identity)
                        ? t('models.alreadyBound')
                        : selected.has(identity)
                          ? t('models.candidateSelected')
                          : text('choose')}
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
        {candidates.data && queryDraft.trim() === modelQuery ? (
          <>
            <p className="core-muted">
              {textModel('searchCount', { count: candidates.data.pagination.total_items })}
            </p>
            <PagePagination
              metadata={candidates.data.pagination}
              requestedPage={candidatePager.page}
              busy={candidates.isFetching || operation.isPending || Boolean(replayAttempt)}
              onPageChange={candidatePager.setPage}
              onPageSizeChange={setCandidatePageSize}
            />
          </>
        ) : null}
      </section>
      <button
        type="button"
        className="btn btn-quiet"
        aria-expanded={manualOpen}
        disabled={manualLocked || operation.isPending || Boolean(replayAttempt)}
        onClick={() => {
          if (!manualOpen && serviceFilter.current) serviceFilter.current.open = true;
          setManualOpen(!manualOpen);
        }}
      >
        {textModel('manual')}
      </button>
      {manualOpen ? (
        <section className="model-manual core-form">
          {!endpointId ? (
            <p className="core-muted">{t('models.chooseEndpoint')}</p>
          ) : (
            <>
              <label>
                <span>{text('keySearch')}</span>
                <input
                  type="search"
                  value={keyQuery}
                  maxLength={128}
                  disabled={manualLocked}
                  onChange={(event) => setKeyQuery(event.target.value)}
                />
              </label>
              <label>
                <span>{textModel('key')}</span>
                <select
                  value={keyId}
                  disabled={manualLocked || keys.isFetching}
                  onChange={(event) => chooseKey(event.target.value)}
                >
                  <option value="">{t('models.chooseKey')}</option>
                  {selectedKey && !keys.data?.data.some((key) => key.id === keyId) ? (
                    <option value={selectedKey.id}>{selectedKey.note}</option>
                  ) : null}
                  {keys.data?.data.map((key) => (
                    <option
                      key={key.id}
                      value={key.id}
                      disabled={!key.enabled || key.suspension_state !== 'none'}
                    >
                      {key.note || `${key.display_head}…${key.display_tail}`}
                    </option>
                  ))}
                </select>
              </label>
              {keys.error ? (
                <CoreErrorPanel compact error={keys.error} onRetry={() => void keys.refetch()} />
              ) : null}
              {keys.data ? (
                <PagePagination
                  metadata={keys.data.pagination}
                  requestedPage={keyPager.page}
                  busy={keys.isFetching || manualLocked}
                  onPageChange={keyPager.setPage}
                  onPageSizeChange={keyPager.setPageSize}
                />
              ) : null}
              {keyId && selectedKey && selectedEndpoint ? (
                <ManualSource
                  key={`${accountId}:${endpointId}:${keyId}`}
                  accountId={accountId}
                  endpointId={endpointId}
                  keyId={keyId}
                  onLock={setManualLocked}
                  onEdit={() => setManualSaved(false)}
                  onCreated={(upstreamModel) => {
                    if (
                      !bound.has(
                        candidateIdentity({
                          endpoint_key_id: keyId,
                          upstream_model_id: upstreamModel,
                        }),
                      ) &&
                      !selected.has(
                        candidateIdentity({
                          endpoint_key_id: keyId,
                          upstream_model_id: upstreamModel,
                        }),
                      )
                    )
                      toggleCandidate({
                        endpoint_key_id: keyId,
                        endpoint_base_url: selectedEndpoint.base_url,
                        connector_type: selectedEndpoint.connector_type,
                        endpoint_note: selectedEndpoint.note,
                        endpoint_key_display_head: selectedKey.display_head,
                        endpoint_key_display_tail: selectedKey.display_tail,
                        endpoint_key_note: selectedKey.note,
                        upstream_model_id: upstreamModel,
                        source_types: ['manual'],
                      });
                    setManualSaved(true);
                  }}
                />
              ) : null}
            </>
          )}
          {manualSaved ? <p role="status">{textModel('manualSaved')}</p> : null}
        </section>
      ) : null}

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
        onCheck={() => void reconcileAuthority()}
        busy={operation.isPending}
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
  const textModel = useModelText();
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
                    <span className="model-source-number">{absoluteIndex + 1}</span>
                    <span className="model-source-main">
                      <strong className="core-mono">{binding.upstream_model_id}</strong>
                      <span className="nb-sub">
                        {binding.endpoint_note || binding.endpoint_base_url} ·{' '}
                        {binding.endpoint_key_note ||
                          `${binding.endpoint_key_display_head}…${binding.endpoint_key_display_tail}`}
                      </span>
                    </span>
                    {model.browse?.preview.find((entry) => entry.id === binding.id)?.state ? (
                      <span className="nb-badge">
                        {t(
                          `browse.state.${model.browse.preview.find((entry) => entry.id === binding.id)!.state}`,
                        )}
                      </span>
                    ) : null}
                    <MoreMenu
                      label={textModel('moreSource', { name: binding.upstream_model_id })}
                      items={[
                        {
                          label: t('models.moveUp'),
                          disabled:
                            busy ||
                            reconciliationRequired ||
                            Boolean(replayAttempt) ||
                            absoluteIndex === 0,
                          onSelect: () =>
                            setOrder((current) =>
                              moveBinding(current, absoluteIndex, absoluteIndex - 1),
                            ),
                        },
                        {
                          label: t('models.moveDown'),
                          disabled:
                            busy ||
                            reconciliationRequired ||
                            Boolean(replayAttempt) ||
                            absoluteIndex === order.length - 1,
                          onSelect: () =>
                            setOrder((current) =>
                              moveBinding(current, absoluteIndex, absoluteIndex + 1),
                            ),
                        },
                        'separator',
                        {
                          label: t('models.removeBinding'),
                          danger: true,
                          disabled: busy || reconciliationRequired || Boolean(replayAttempt),
                          onSelect: () => setRemoving(binding),
                        },
                      ]}
                    />
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
      {asNotice(
        outcome,
        () => void (operation.isSuccess ? operation.refresh() : operation.check()),
        busy,
        operation.outcome === 'refresh-failed',
      )}
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

function ModelSources({
  accountId,
  model,
  strategy,
  onStrategy,
  locked = false,
  focusSearch = false,
  onSearchFocused,
}: {
  accountId: string;
  model: Model;
  strategy: RouteStrategy;
  onStrategy: (value: RouteStrategy) => void;
  locked?: boolean;
  focusSearch?: boolean;
  onSearchFocused?: () => void;
}) {
  const { t } = useCoreCopy();
  const text = useModelText();
  return (
    <Panel className="model-sources">
      <PanelHead
        title={text('sources')}
        actions={
          <Segmented
            label={t('models.strategy')}
            value={strategy}
            onChange={onStrategy}
            disabled={locked}
            options={[
              { value: 'ordered', label: t('models.ordered') },
              { value: 'random', label: t('models.random') },
            ]}
          />
        }
      />
      <PanelBody>
        <BindingOrder accountId={accountId} model={model} />
        <BindingSelector
          accountId={accountId}
          model={model}
          focusSearch={focusSearch}
          onSearchFocused={onSearchFocused}
        />
      </PanelBody>
    </Panel>
  );
}

function ModelDetail({
  accountId,
  modelId,
  onBack,
  onDeleted,
  focusSources = false,
}: {
  accountId: string;
  modelId: string;
  focusSources?: boolean;
  onBack: () => void;
  onDeleted: () => void;
}) {
  const { t } = useCoreCopy();
  const textModel = useModelText();
  const [sourceFocusPending, setSourceFocusPending] = useState(focusSources);
  const [requestedStrategy, setRequestedStrategy] = useState<RouteStrategy>();
  const queryClient = useQueryClient();
  const model = useModel(accountId, modelId);
  const [editing, setEditing] = useState<Model | null>(null);
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
        setEditing(null);
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
      <div className="page core-page core-stack models-workspace">
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
      <div className="page core-page models-workspace">
        <CoreLoading />
      </div>
    );
  if (!model.data)
    return (
      <div className="page core-page models-workspace">
        <CoreErrorPanel
          error={model.error ?? new Error('The model details are unavailable.')}
          onRetry={() => void model.refetch()}
        />
      </div>
    );

  return (
    <div className="page core-page core-stack models-workspace">
      <PageHeader
        icon="models"
        title={model.data.full_name}
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
            onClick={() => setEditing(model.data ?? null)}
          >
            {t('models.editModel')}
          </button>
        }
      />
      <div className="model-detail-body">
        {editing ? (
          <ModelEditor
            accountId={accountId}
            key={editing.revision}
            initial={editing}
            initialStrategy={requestedStrategy}
            onCancel={() => {
              setEditing(null);
              setRequestedStrategy(undefined);
            }}
            onSaved={() => {
              setEditing(null);
              setRequestedStrategy(undefined);
            }}
            onCapabilityLoss={(error) => setPermissionLost({ scope: permissionScope, error })}
            sources={(strategy, onChange, locked) => (
              <ModelSources
                accountId={accountId}
                model={model.data!}
                strategy={strategy}
                onStrategy={onChange}
                locked={locked}
              />
            )}
          />
        ) : (
          <>
            <Panel>
              <PanelHead title={textModel('callName')} />
              <PanelBody>
                <SafeCopyValue value={model.data.full_name} label={textModel('callName')} />
              </PanelBody>
            </Panel>
            <ModelSources
              accountId={accountId}
              model={model.data}
              focusSearch={sourceFocusPending}
              onSearchFocused={() => setSourceFocusPending(false)}
              strategy={model.data.route_strategy}
              locked={reconciliationRequired || Boolean(replayAttempt)}
              onStrategy={(strategy) => {
                setRequestedStrategy(strategy);
                setEditing(model.data ?? null);
              }}
            />
            <Fold
              title={textModel('advanced')}
              summary={textModel('advancedHelp')}
              meta={textModel(
                !model.data.silent_retry &&
                  !model.data.flatten_tool_calls &&
                  model.data.transport_rule === 'passthrough' &&
                  sameRolePolicy(model.data.role_policy, { default_action: 'native', rules: {} })
                  ? 'default'
                  : 'customized',
              )}
            >
              <dl className="core-detail-list">
                <TransportRuleSummary value={model.data.transport_rule} />
                <div>
                  <dt>{t('models.silentRetry')}</dt>
                  <dd>{model.data.silent_retry ? t('common.yes') : t('common.no')}</dd>
                </div>
                <div>
                  <dt>{t('models.flattenTools')}</dt>
                  <dd>{model.data.flatten_tool_calls ? t('common.yes') : t('common.no')}</dd>
                </div>
              </dl>
              <ModelRoleSummary policy={model.data.role_policy} />
            </Fold>
          </>
        )}
      </div>
      <section className="core-card core-danger-zone">
        <div className="core-card__header">
          <h2>{t('models.deleteModel')}</h2>
        </div>
        <p>{textModel('deleteHelp', { name: model.data.full_name })}</p>
        {asNotice(
          outcome,
          () => void (operation.isSuccess ? operation.refresh() : operation.check()),
          busy,
          operation.outcome === 'refresh-failed',
        )}
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
            className="nb-btn nb-btn--danger-outline"
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
  const textModel = useModelText();
  const [sourceFocusId, setSourceFocusId] = useState<string | null>(null);
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

  const columns: DataColumn<Model>[] = [
    {
      key: 'name',
      header: textModel('callName'),
      cell: 'title',
      render: (model) => (
        <>
          <button
            type="button"
            className="model-name-link core-mono"
            disabled={models.isFetching || Boolean(models.error)}
            onClick={() => setSelectedModelID(model.id)}
          >
            {model.full_name}
          </button>
          <span className="nb-sub">
            {textModel(model.route_strategy === 'ordered' ? 'orderedSummary' : 'randomSummary')}
            {model.silent_retry ? ` · ${textModel('retrySummary')}` : ''}
          </span>
        </>
      ),
    },
    {
      key: 'sources',
      header: textModel('sources'),
      mobileLabel: textModel('sources'),
      cell: 'meta',
      render: (model) =>
        model.browse
          ? textModel('sourceCount', {
              total: model.binding_count,
              available: model.browse.available_binding_count,
            })
          : textModel('sourceTotal', { total: model.binding_count }),
    },
    {
      key: 'status',
      header: t('filters.connection'),
      cell: 'status',
      render: (model) => {
        const state =
          model.binding_count === '0'
            ? 'unconfigured'
            : !model.browse
              ? 'unknown'
              : model.browse.available_binding_count === '0'
                ? 'unavailable'
                : 'available';
        return (
          <span
            className={`nb-badge nb-badge--${state === 'available' ? 'ok' : state === 'unavailable' ? 'bad' : 'plain'}`}
          >
            {state === 'available'
              ? t('common.available')
              : state === 'unknown'
                ? t('common.unknown')
                : t(`filters.${state}`)}
          </span>
        );
      },
    },
    {
      key: 'actions',
      header: t('models.editModel'),
      cell: 'action',
      align: 'action',
      render: (model) => (
        <button
          type="button"
          className="btn btn-secondary"
          disabled={models.isFetching || Boolean(models.error)}
          onClick={() => setSelectedModelID(model.id)}
        >
          {t('models.editModel')}
        </button>
      ),
    },
  ];
  if (!scopeReady) {
    return (
      <div className="page core-page models-workspace">
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
        focusSources={sourceFocusId === selectedModelId}
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
      <div className="page core-page core-stack models-workspace">
        <PageHeader
          icon="models"
          title={t('models.title')}
          description={textModel('description')}
        />
        <CoreErrorPanel error={accessLossError} />
      </div>
    );
  }

  return (
    <div className="page core-page core-stack models-workspace">
      <PageHeader
        icon="models"
        title={t('models.title')}
        description={textModel('description')}
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
            setSourceFocusId(saved.id);
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
              body={textModel('emptyBody')}
              action={
                <span className="nb-inline">
                  <Link className="btn btn-secondary" to="/endpoints?quickstart=1">
                    {textModel('addService')}
                  </Link>
                  <button
                    type="button"
                    className="btn btn-primary"
                    onClick={() => setCreating(true)}
                  >
                    {t('models.create')}
                  </button>
                </span>
              }
            />
          ) : (
            <DataTable
              caption={t('models.title')}
              columns={columns}
              rows={models.data.data}
              rowKey={(model) => model.id}
              onRowClick={(model, event) => {
                if (
                  !models.isFetching &&
                  !models.error &&
                  !(event.target as HTMLElement).closest('button,a,input,select,textarea,summary')
                )
                  setSelectedModelID(model.id);
              }}
            />
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

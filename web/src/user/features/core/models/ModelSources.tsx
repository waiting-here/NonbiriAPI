import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { MoreMenu, Panel, PanelBody, PanelHead, Segmented } from '@shared/components/ui';
import type { PageMetadata, PageSize } from '@shared/operations/pageNumbers';
import { PagePagination } from '@shared/operations/PagePagination';
import { usePagePager } from '@shared/operations/usePagePager';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { useQueryClient } from '@tanstack/react-query';
import {
  useEffect,
  useEffectEvent,
  useMemo,
  useReducer,
  useRef,
  useState,
  type DragEvent,
} from 'react';
import { addBindings, deleteBinding, getBindings, orderBindings } from '../api';
import { CoreErrorPanel, CoreLoading, MutationNotice } from '../components';
import { useCoreCopy } from '../copy';
import { useNumberedBindingCandidates } from '../modelNumberedQueries';
import { useNumberedEndpointKeys, useNumberedEndpoints } from '../numberedQueries';
import { getBindingCandidatesPage } from '../pageApi';
import { applyBindingsResponse, coreKeys, useBindings } from '../queries';
import { useQuickstartCopy } from '../quickstartCopy';
import { isConflict, isOutcomeUnknown } from '../request';
import { resourceStatus } from '../resourceOperation';
import { bindingDraftReducer, initialBindingDraftState } from '../stateMachines';
import type {
  Binding,
  BindingCandidate,
  BindingSelection,
  BindingsResponse,
  Endpoint,
  EndpointKey,
  Model,
  RouteStrategy,
} from '../types';
import { useModelText } from './copy';
import { ManualSource } from './ManualSource';

import { asNotice, isAccessLoss, type VisibleOutcome } from './feedback';

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
        className="nb-btn nb-btn--ghost"
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
                  className="nb-btn nb-btn--ghost"
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
            className="nb-btn nb-btn--secondary"
            onClick={() => void reconcileAuthority()}
          >
            {t('common.reconcile')}
          </button>
        ) : (
          <span />
        )}
        <button
          type="button"
          className="nb-btn nb-btn--primary"
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
            className="nb-btn nb-btn--secondary"
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
          className="nb-btn nb-btn--primary"
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

export function ModelSources({
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
              { value: 'cache_balanced', label: text('balancedSummary') },
            ]}
          />
        }
      />
      <PanelBody>
        {strategy === 'cache_balanced' ? <p className="nb-sub">{text('balancedHelp')}</p> : null}
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

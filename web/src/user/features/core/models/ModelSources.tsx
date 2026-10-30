import { NativeServicesField } from '@shared/components/NativeServicesField';
import type { NativeService } from '@shared/nativeServices';
import { BindingTargets } from '../connections/BindingTargets';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import {
  FormActions,
  Fold,
  MoreMenu,
  Panel,
  PanelBody,
  PanelHead,
  Segmented,
} from '@shared/components/ui';
import type { PageMetadata, PageSize } from '@shared/operations/pageNumbers';
import { PagePagination } from '@shared/operations/PagePagination';
import { usePagePager } from '@shared/operations/usePagePager';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useMemo, useReducer, useRef, useState, type DragEvent } from 'react';
import { addBindings, deleteBinding, getBindings, orderBindings } from '../api';
import { CoreErrorPanel, CoreLoading, MutationNotice } from '../components';
import { useCoreCopy } from '../copy';
import { getBindingCandidatesPage } from '../pageApi';
import { applyBindingsResponse, coreKeys, useBindings } from '../queries';
import { isConflict, isOutcomeUnknown } from '../request';
import { resourceStatus } from '../resourceOperation';
import { bindingDraftReducer, initialBindingDraftState } from '../stateMachines';
import type {
  Binding,
  BindingCandidate,
  BindingSelection,
  BindingsResponse,
  Model,
  RouteStrategy,
} from '../types';
import { useModelText } from './copy';
import { PersonalBindingPicker } from './PersonalBindingPicker';
import { boundIdentities, candidateIdentity } from './bindingIdentity';
import { useBindingCopy } from './bindingCopy';

import { asNotice, isAccessLoss, type VisibleOutcome } from './feedback';

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
  const copy = useBindingCopy();
  const queryClient = useQueryClient();
  const bindings = useBindings(accountId, model.id);
  const [selectionDetails, setSelectionDetails] = useState<Record<string, BindingCandidate>>({});
  const [invalidSelection, setInvalidSelection] = useState(false);
  const [readAccessError, setReadAccessError] = useState<Error | null>(null);
  const [nativeSelections, setNativeSelections] = useState<Record<string, NativeService[]>>({});
  const servicesFor = (id: string) =>
    nativeSelections[id] ?? selectionDetails[id]?.native_services ?? [];
  const [draft, dispatch] = useReducer(bindingDraftReducer, undefined, () =>
    initialBindingDraftState(accountId, model.id, model.binding_revision),
  );
  const bindingsKnown = Boolean(bindings.data);
  useEffect(() => {
    const revision = bindings.data?.binding_revision;
    if (revision && revision !== draft.bindingRevision)
      dispatch({ type: 'authoritative', accountId, modelId: model.id, bindingRevision: revision });
  }, [accountId, bindings.data?.binding_revision, draft.bindingRevision, model.id]);
  const selected = useMemo(
    () => new Set(draft.selections.map(candidateIdentity)),
    [draft.selections],
  );
  const bound = useMemo(
    () => boundIdentities(bindings.data?.bindings ?? []),
    [bindings.data?.bindings],
  );
  const toggleCandidate = (candidate: BindingCandidate) => {
    if (candidate.capabilities?.can_bind === false) return;
    setSelectionDetails((current) => ({ ...current, [candidateIdentity(candidate)]: candidate }));
    dispatch({ type: 'toggle', accountId, modelId: model.id, candidate });
  };

  type Intent = { revision: string; selections: BindingSelection[] };
  const retryAllowed = useRef(false);
  const publish = (response: BindingsResponse, intent: Intent) => {
    applyBindingsResponse(queryClient, accountId, model.id, response);
    const bound = boundIdentities(response.bindings);
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
      const bound = boundIdentities(response.bindings);
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
      .mutateAsync({
        revision: draft.bindingRevision,
        selections: draft.selections.map((candidate) => ({
          ...candidate,
          ...(selectionDetails[candidateIdentity(candidate)]?.native_services === undefined
            ? {}
            : { native_services: servicesFor(candidateIdentity(candidate)) }),
        })),
      })
      .catch(() => undefined);
  };

  const accessLossError =
    (isAccessLoss(operation.error) ? operation.error : null) ??
    (isAccessLoss(bindings.error) ? bindings.error : null) ??
    readAccessError;
  if (accessLossError) {
    return (
      <section className="core-card">
        <div className="core-card__header">
          <h2>{copy.addSources}</h2>
        </div>
        <CoreErrorPanel compact error={accessLossError} />
      </section>
    );
  }

  return (
    <section className="core-card">
      <div className="core-card__header">
        <h2>{copy.addSources}</h2>
      </div>
      {bindings.isPending && !bindings.data ? <CoreLoading compact /> : null}
      {!bindings.data && bindings.error ? (
        <CoreErrorPanel compact error={bindings.error} onRetry={() => void bindings.refetch()} />
      ) : null}
      <PersonalBindingPicker
        key={`${accountId}:${model.id}`}
        accountId={accountId}
        modelId={model.id}
        selected={selected}
        bound={bound}
        onToggle={toggleCandidate}
        locked={!bindingsKnown || operation.isPending || Boolean(replayAttempt)}
        focusSearch={focusSearch}
        onSearchFocused={onSearchFocused}
        onAccessLoss={setReadAccessError}
      />
      <p className="nb-small nb-muted">{copy.selectedHelp}</p>
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
                {detail?.native_services ? (
                  <Fold title={copy.calls}>
                    <NativeServicesField
                      options={detail.native_services}
                      value={servicesFor(candidateIdentity(candidate))}
                      onChange={(value) =>
                        setNativeSelections((previous) => ({
                          ...previous,
                          [candidateIdentity(candidate)]: value,
                        }))
                      }
                      disabled={operation.isPending || Boolean(replayAttempt)}
                    />
                  </Fold>
                ) : null}
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
      {draft.selections.length ? (
        <button
          type="button"
          className="nb-btn nb-btn--ghost"
          disabled={operation.isPending || Boolean(replayAttempt)}
          onClick={() => {
            for (const candidate of draft.selections)
              dispatch({ type: 'candidate-invalid', accountId, modelId: model.id, candidate });
          }}
        >
          {copy.clear}
        </button>
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
      <FormActions>
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
            (!replayAttempt &&
              (draft.selections.length === 0 ||
                draft.selections.some(
                  (candidate) =>
                    selectionDetails[candidateIdentity(candidate)]?.native_services !== undefined &&
                    !servicesFor(candidateIdentity(candidate)).length,
                )))
          }
          onClick={() => void submit()}
        >
          {operation.isPending
            ? t('common.working')
            : replayAttempt
              ? t('common.reconcile')
              : t('models.addSelected', { count: draft.selections.length })}
        </button>
      </FormActions>
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
  const copy = useBindingCopy();
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
                      {binding.targets && binding.targets.length > 1 ? (
                        <>
                          <strong>
                            {copy.combination} · {binding.targets.length} {copy.combinationCount}
                          </strong>
                          {binding.targets.map((target) => (
                            <span key={target.id} className="nb-sub">
                              {target.upstream_model_id} · {target.base_url}
                            </span>
                          ))}
                        </>
                      ) : (
                        <>
                          <strong className="core-mono">{binding.upstream_model_id}</strong>
                          <span className="nb-sub">
                            {binding.endpoint_note || binding.endpoint_base_url} ·{' '}
                            {binding.endpoint_key_note ||
                              `${binding.endpoint_key_display_head}…${binding.endpoint_key_display_tail}`}
                          </span>
                        </>
                      )}
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
      <FormActions>
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
      </FormActions>
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
        <BindingSelector
          key={`${accountId}:${model.id}`}
          accountId={accountId}
          model={model}
          focusSearch={focusSearch}
          onSearchFocused={onSearchFocused}
        />
        <BindingOrder accountId={accountId} model={model} />
        <BindingTargets accountId={accountId} model={model} />
      </PanelBody>
    </Panel>
  );
}

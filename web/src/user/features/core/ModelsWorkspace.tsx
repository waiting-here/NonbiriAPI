import { sameRolePolicy } from './models/policy';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { ModelTypesSummary } from '@shared/components/ModelTypesField';
import { PageHeader } from '@shared/components/States';
import { TransportRuleSummary } from '@shared/components/TransportRuleField';
import {
  DataTable,
  Fold,
  Panel,
  PanelBody,
  PanelHead,
  type DataColumn,
} from '@shared/components/ui';
import { PagePagination } from '@shared/operations/PagePagination';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { useSearchState } from '@shared/operations/useSearchState';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isNotFoundError } from '@shared/query/http';
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
import { deleteModel, getModel } from './api';
import { CoreEmpty, CoreErrorPanel, CoreLoading, SafeCopyValue } from './components';
import { useCoreCopy } from './copy';
import { useNumberedModels } from './modelNumberedQueries';
import { useModelText } from './models/copy';
import { validateResourceId } from './normalizers';
import { coreKeys, invalidateResourceDependents, useCoreSession, useModel } from './queries';
import { FilteredResourceEmpty, ResourceFilterBar } from './ResourceFilterControls';
import type { Model, RouteStrategy, UserProfile } from './types';
import { useResourceFilters, useResourceListScroll } from './useResourceFilters';

import {
  asNotice,
  isAccessLoss,
  type PermissionLoss,
  type VisibleOutcome,
} from './models/feedback';
import { ModelEditor, ModelRoleSummary } from './models/ModelEditor';
import { ModelSources } from './models/ModelSources';

function selectedModelID(searchParams: URLSearchParams): string | null {
  const values = searchParams.getAll('model_id');
  if (values.length !== 1 || !values[0]) return null;
  try {
    return validateResourceId(values[0], 'model id');
  } catch {
    return null;
  }
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
            <button type="button" className="nb-btn nb-btn--ghost" onClick={onBack}>
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
          <button type="button" className="nb-btn nb-btn--ghost" onClick={onBack}>
            {t('common.back')}
          </button>
        }
        actions={
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
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
                <ModelTypesSummary value={model.data.model_types} />
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
                <div>
                  <dt>{t('models.silentRetry')}</dt>
                  <dd>{model.data.silent_retry ? t('common.yes') : t('common.no')}</dd>
                </div>
                {model.data.model_types.includes('chat_completions') ? (
                  <>
                    <TransportRuleSummary value={model.data.transport_rule} />
                    <div>
                      <dt>{t('models.flattenTools')}</dt>
                      <dd>{model.data.flatten_tool_calls ? t('common.yes') : t('common.no')}</dd>
                    </div>
                  </>
                ) : null}
              </dl>
              {model.data.model_types.includes('chat_completions') ? (
                <ModelRoleSummary policy={model.data.role_policy} />
              ) : null}
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
              className="nb-btn nb-btn--secondary"
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
              className="nb-btn nb-btn--secondary"
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
            title={model.full_name}
            disabled={models.isFetching || Boolean(models.error)}
            onClick={() => setSelectedModelID(model.id)}
          >
            {model.full_name}
          </button>
          <ModelTypesSummary value={model.model_types} />
          <span className="nb-sub">
            {textModel(
              model.route_strategy === 'ordered'
                ? 'orderedSummary'
                : model.route_strategy === 'cache_balanced'
                  ? 'balancedSummary'
                  : 'randomSummary',
            )}
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
      header: textModel('status'),
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
      header: textModel('actions'),
      cell: 'action',
      align: 'action',
      render: (model) => (
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
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
          <button
            type="button"
            className="nb-btn nb-btn--primary"
            onClick={() => setCreating(true)}
          >
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
                  <Link className="nb-btn nb-btn--secondary" to="/endpoints?quickstart=1">
                    {textModel('addService')}
                  </Link>
                  <button
                    type="button"
                    className="nb-btn nb-btn--primary"
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

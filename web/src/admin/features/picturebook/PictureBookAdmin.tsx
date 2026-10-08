import { SimplePager } from '@shared/operations/SimplePager';
import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react';
import { useBlocker } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { stationSessionWrite } from '@shared/charityManagement';
import { Card, ErrorState, LoadingState } from '@shared/components/States';
import { usePictureBookText } from '@shared/picturebook/copy';
import { useImageOperation } from '@shared/picturebook/useImageOperation';
import { useAdminSession } from '../../data';
import {
  getAdminModel,
  getCatalog,
  getRefresh,
  getUpstream,
  refreshModels,
  saveModelsBatch,
  type AdminModel,
  type CatalogFilter,
  type ModelInput,
} from './adminApi';
import { UpstreamForm, type UpstreamDraftHandle } from './UpstreamForm';
import { ModelEditor, type ModelDraftHandle } from './ModelEditor';
import { RecoveryPanel } from './RecoveryPanel';
import { DiscoveryFailureDiagnostics } from './DiscoveryFailureDiagnostics';
import { discoveryError } from './discoveryError';
import '@shared/picturebook/picturebook.css';
import type { PictureBookSection } from '../../pages/LimitedActivitiesPage';

interface CatalogDraftHandle {
  saveDrafts: () => Promise<boolean>;
  discardDrafts: () => void;
}

const Catalog = forwardRef<
  CatalogDraftHandle,
  {
    readonly account: string;
    readonly onDirty: (dirty: boolean) => void;
    readonly onLocked: (locked: boolean) => void;
    readonly upstreamDirty: boolean;
    readonly upstreamLocked: boolean;
    readonly onRefreshed: () => Promise<void>;
  }
>(function Catalog(
  { account, onDirty, onLocked, upstreamDirty, upstreamLocked, onRefreshed },
  ref,
) {
  const { t: copy } = useTranslation();
  const t = usePictureBookText(),
    client = useQueryClient();
  const [filter, setFilter] = useState<CatalogFilter>({
    q: '',
    type: 'image',
    configured: 'all',
    enabled: 'all',
    page: 1,
    page_size: 20,
  });
  const [reloadError, setReloadError] = useState<unknown>(null);
  const [selected, setSelected] = useState<AdminModel | null>(null),
    [operationID, setOperationID] = useState('');
  const [openModels, setOpenModels] = useState<Record<string, AdminModel>>({});
  const [drafts, setDrafts] = useState<Record<string, ModelInput | null>>({});
  const [dirty, setDirty] = useState<Record<string, boolean>>({});
  const [lockedEditors, setLockedEditors] = useState<Record<string, boolean>>({});
  const [epochs, setEpochs] = useState<Record<string, number>>({});
  const [pendingSelection, setPendingSelection] = useState<{ model: AdminModel | null } | null>(
    null,
  );
  const editorRefs = useRef<Record<string, ModelDraftHandle | null>>({});
  const [batchIDs, setBatchIDs] = useState<string[]>([]);
  const [batchResult, setBatchResult] = useState<Awaited<
    ReturnType<typeof saveModelsBatch>
  > | null>(null);
  const [reloading, setReloading] = useState(false);
  const completedRefresh = useRef('');
  const batch = useImageOperation('admin', saveModelsBatch);
  const locked = Object.values(lockedEditors).some(Boolean) || batch.locked || reloading;
  const finishSelection = (model: AdminModel | null) => {
    if (model) setOpenModels((current) => ({ ...current, [model.id]: current[model.id] ?? model }));
    setSelected(model);
    setPendingSelection(null);
  };
  const selectModel = (model: AdminModel | null) => {
    if (selected && dirty[selected.id] && selected.id !== model?.id) {
      setPendingSelection({ model });
      return;
    }
    finishSelection(model);
  };
  const draftDirty = Object.values(dirty).some(Boolean);
  const reloadModels = useCallback(
    async (ids: string[]) => {
      setReloading(true);
      try {
        const [fresh] = await Promise.all([
          Promise.all(
            ids.map((id) => stationSessionWrite(client, 'admin', () => getAdminModel(id))),
          ),
          onRefreshed(),
        ]);
        setOpenModels((current) => ({
          ...current,
          ...Object.fromEntries(fresh.map((model) => [model.id, model])),
        }));
        setSelected((current) => fresh.find((model) => model.id === current?.id) ?? current);
        setDirty((current) => ({
          ...current,
          ...Object.fromEntries(ids.map((id) => [id, false])),
        }));
        setEpochs((current) => ({
          ...current,
          ...Object.fromEntries(ids.map((id) => [id, (current[id] ?? 0) + 1])),
        }));
        setReloadError(null);
        await client.invalidateQueries({ queryKey: ['admin', 'picture-book', account, 'models'] });
      } catch (error) {
        setReloadError(error);
      } finally {
        setReloading(false);
      }
    },
    [client, account, onRefreshed],
  );
  const root = ['admin', 'picture-book', account, 'models'] as const;
  const models = useQuery({
    queryKey: [...root, filter],
    queryFn: ({ signal }) => stationSessionWrite(client, 'admin', () => getCatalog(filter, signal)),
    refetchOnWindowFocus: false,
    retry: false,
  });
  const discovery = useImageOperation('admin', refreshModels);
  const operation = useQuery({
    queryKey: ['admin', 'picture-book', account, 'refresh', operationID],
    queryFn: async ({ signal }) => {
      const result = await stationSessionWrite(client, 'admin', () =>
        getRefresh(operationID, signal),
      );
      if (result.state === 'succeeded')
        setFilter((current) =>
          current.page === 1 && !current.catalog_revision
            ? current
            : { ...current, page: 1, catalog_revision: undefined },
        );
      return result;
    },
    enabled: !!operationID,
    retry: false,
    refetchInterval: (query) =>
      query.state.error ||
      (query.state.data && ['succeeded', 'failed'].includes(query.state.data.state))
        ? false
        : 2000,
  });
  const state = operation.data?.state;
  const refreshActive = reloading || discovery.locked || state === 'queued' || state === 'running';
  const anyDirty = draftDirty || batch.locked || discovery.locked;
  useEffect(() => onDirty(anyDirty), [anyDirty, onDirty]);
  useEffect(() => onLocked(locked || refreshActive), [locked, refreshActive, onLocked]);
  useImperativeHandle(ref, () => ({
    saveDrafts: async () => {
      if (batch.locked || refreshActive) return false;
      for (const id of Object.keys(dirty).filter((item) => dirty[item])) {
        if (!(await editorRefs.current[id]?.saveDraft())) return false;
      }
      return true;
    },
    discardDrafts: () => {
      if (locked || refreshActive) return;
      const ids = Object.keys(dirty).filter((id) => dirty[id]);
      setEpochs((current) => ({
        ...current,
        ...Object.fromEntries(ids.map((id) => [id, (current[id] ?? 0) + 1])),
      }));
      setDirty((current) => ({ ...current, ...Object.fromEntries(ids.map((id) => [id, false])) }));
      setPendingSelection(null);
    },
  }));
  const openModelIDs = useRef<string[]>([]);
  useEffect(() => {
    openModelIDs.current = Object.keys(openModels);
  }, [openModels]);
  useEffect(() => {
    if (state === 'succeeded' && completedRefresh.current !== operationID) {
      completedRefresh.current = operationID;
      void reloadModels(openModelIDs.current);
    }
  }, [operationID, state, onRefreshed, reloadModels]);
  return (
    <div className="picturebook-stack">
      <Card>
        <h2>{t('图像模型目录', 'Image model catalog')}</h2>
        <p>
          {t(
            '拉取使用已保存的服务地址和密钥，支持参数与尺寸自动配置。选择模型、填写价格后即可开放；刷新会保留已有价格和开放状态，不会自动开放新模型。',
            'Discovery uses the saved URL and key and automatically configures supported parameters and sizes. Choose a model, set prices and enable it. Refreshing preserves prices and availability and does not enable new models.',
          )}
        </p>
        <button
          className="nb-btn nb-btn--primary"
          disabled={
            discovery.pending ||
            reloading ||
            state === 'queued' ||
            state === 'running' ||
            locked ||
            draftDirty ||
            upstreamDirty ||
            upstreamLocked
          }
          onClick={() => {
            if (!discovery.uncertain) setOperationID('');
            void discovery.run(discovery.input ?? {}, (result) => {
              client.setQueryData(['admin', 'picture-book', account, 'refresh', result.id], result);
              setOperationID(result.id);
            });
          }}
        >
          {discovery.uncertain
            ? t('重试同一次模型拉取', 'Retry the same model refresh')
            : state === 'failed'
              ? t('重新拉取模型目录', 'Start a new model refresh')
              : t('拉取模型目录', 'Refresh model catalog')}
        </button>
        {draftDirty || upstreamDirty ? (
          <p role="status">
            {t(
              '请先保存或放弃草稿，再刷新模型目录。',
              'Save or discard drafts before refreshing the model catalog.',
            )}
          </p>
        ) : null}
        {discovery.uncertain ? (
          <p role="status">
            {t(
              '拉取回执尚未确认，请重试同一次操作。',
              'The refresh receipt is unconfirmed. Retry the same operation.',
            )}
          </p>
        ) : null}
        {discovery.error ? <ErrorState error={discovery.error} /> : null}
        {operation.error ? (
          <ErrorState error={operation.error} onRetry={() => void operation.refetch()} />
        ) : null}
        {operation.data?.state === 'failed' ? (
          <section role="alert" className="picturebook-discovery-error">
            <h3>{t('模型拉取失败', 'Model discovery failed')}</h3>
            <p>{discoveryError(operation.data.error_code, operation.data.http_status, t)}</p>
            {operation.data.http_status ? <p>HTTP {operation.data.http_status}</p> : null}
            <DiscoveryFailureDiagnostics operationID={operation.data.id} />
            <details>
              <summary>{t('诊断信息', 'Diagnostic information')}</summary>
              <p>
                {t('错误类别：', 'Error class: ')}
                <code>{operation.data.error_code ?? 'unknown'}</code>
              </p>
              <p>
                {t('操作编号：', 'Operation ID: ')}
                <code>{operation.data.id}</code>
              </p>
            </details>
          </section>
        ) : operation.data ? (
          <p role="status">
            {state === 'succeeded'
              ? operation.data.model_count === 0
                ? t(
                    '服务返回了空模型目录。请核对服务地址，或稍后再刷新。',
                    'The service returned an empty catalog. Check the service URL or refresh again later.',
                  )
                : t('目录已更新，模型数：', 'Catalog updated. Models: ') +
                  operation.data.model_count
              : t('正在等待或拉取模型目录。', 'Model discovery is queued or running.')}
          </p>
        ) : null}
        {models.isPending ? (
          <LoadingState />
        ) : models.error ? (
          <ErrorState error={models.error} onRetry={() => void models.refetch()} />
        ) : null}
        <div className="picturebook-grid">
          <label>
            {t('搜索全部模型目录', 'Search the full model catalog')}
            <input
              value={filter.q}
              maxLength={128}
              onChange={(event) =>
                setFilter((current) => ({
                  ...current,
                  q: event.target.value,
                  page: 1,
                  catalog_revision: undefined,
                }))
              }
            />
          </label>
          <label>
            {t('模型类型', 'Model type')}
            <select
              value={filter.type}
              onChange={(event) =>
                setFilter((current) => ({
                  ...current,
                  type: event.target.value as CatalogFilter['type'],
                  page: 1,
                  catalog_revision: undefined,
                }))
              }
            >
              <option value="image">{t('图像', 'Image')}</option>
              <option value="unknown">{t('未分类', 'Unknown')}</option>
              <option value="other">{t('其他类型', 'Other types')}</option>
              <option value="all">{t('全部类型', 'All types')}</option>
            </select>
          </label>
          <label>
            {t('配置状态', 'Configuration')}
            <select
              value={filter.configured}
              onChange={(event) =>
                setFilter((current) => ({
                  ...current,
                  configured: event.target.value as CatalogFilter['configured'],
                  page: 1,
                  catalog_revision: undefined,
                }))
              }
            >
              <option value="all">{t('全部', 'All')}</option>
              <option value="true">{t('已配置', 'Configured')}</option>
              <option value="false">{t('未配置', 'Unconfigured')}</option>
            </select>
          </label>
          <label>
            {t('开放状态', 'Availability')}
            <select
              value={filter.enabled}
              onChange={(event) =>
                setFilter((current) => ({
                  ...current,
                  enabled: event.target.value as CatalogFilter['enabled'],
                  page: 1,
                  catalog_revision: undefined,
                }))
              }
            >
              <option value="all">{t('全部', 'All')}</option>
              <option value="true">{t('已开放', 'Enabled')}</option>
              <option value="false">{t('未开放', 'Disabled')}</option>
            </select>
          </label>
        </div>
        {models.data ? (
          <p role="status">
            {t('匹配模型数：', 'Matching models: ')}
            {models.data.total} · {filter.page}/
            {Math.max(1, Math.ceil(models.data.total / filter.page_size))}
          </p>
        ) : null}
        <div className="picturebook-form">
          <label>
            {t('选择要配置的模型', 'Choose a model to configure')}
            <select
              disabled={locked || refreshActive || upstreamLocked}
              value={selected?.id ?? ''}
              onChange={(event) =>
                selectModel(
                  models.data?.data.find((model) => model.id === event.target.value) ?? null,
                )
              }
            >
              <option value="">{t('请选择', 'Select a model')}</option>
              {selected && !models.data?.data.some((model) => model.id === selected.id) ? (
                <option value={selected.id}>{selected.upstream_model_id}</option>
              ) : null}
              {models.data?.data.map((model) => (
                <option value={model.id} key={model.id}>
                  {model.upstream_model_id} ·{' '}
                  {model.enabled ? t('已开放', 'Enabled') : t('未开放', 'Not enabled')}
                  {model.missing ? ` · ${t('最新目录未返回', 'Missing from latest catalog')}` : ''}
                </option>
              ))}
            </select>
          </label>
        </div>
        {pendingSelection ? (
          <div role="alert" className="picturebook-stack">
            <p>
              {t(
                '当前模型有未保存草稿。请先保存，或明确放弃后切换；也可以继续编辑。',
                'The current model has an unsaved draft. Save it first, explicitly discard it, or continue editing.',
              )}
            </p>
            <div className="picturebook-actions">
              <button
                className="nb-btn nb-btn--primary"
                type="button"
                disabled={locked}
                onClick={() => {
                  if (selected) void editorRefs.current[selected.id]?.saveDraft();
                }}
              >
                {t('保存并切换', 'Save and switch')}
              </button>
              <button
                className="nb-btn nb-btn--secondary"
                type="button"
                onClick={() => setPendingSelection(null)}
              >
                {t('继续编辑', 'Continue editing')}
              </button>
              <button
                className="nb-btn nb-btn--secondary"
                type="button"
                disabled={locked || refreshActive || upstreamLocked}
                onClick={() => {
                  if (locked || refreshActive || upstreamLocked) return;
                  if (selected) {
                    setEpochs((current) => ({
                      ...current,
                      [selected.id]: (current[selected.id] ?? 0) + 1,
                    }));
                    setDirty((current) => ({ ...current, [selected.id]: false }));
                  }
                  finishSelection(pendingSelection.model);
                }}
              >
                {t('放弃草稿并切换', 'Discard draft and switch')}
              </button>
            </div>
          </div>
        ) : null}
        {models.data ? (
          <div className="picturebook-stack">
            <p>
              {t(
                '批量选择模型（最多50项，跨页保留）',
                'Select up to 50 models across pages for an atomic batch save',
              )}
            </p>
            {models.data.data.map((model) => (
              <label className="picturebook-checkbox" key={model.id}>
                <input
                  type="checkbox"
                  checked={batchIDs.includes(model.id)}
                  disabled={
                    locked ||
                    refreshActive ||
                    upstreamLocked ||
                    (!batchIDs.includes(model.id) && batchIDs.length >= 50)
                  }
                  onChange={(event) => {
                    setBatchResult(null);
                    if (event.target.checked) {
                      setBatchIDs((current) => [...current, model.id]);
                      setOpenModels((current) => ({
                        ...current,
                        [model.id]: current[model.id] ?? model,
                      }));
                    } else setBatchIDs((current) => current.filter((id) => id !== model.id));
                  }}
                />
                {model.upstream_model_id}
              </label>
            ))}
            {batchIDs.length ? (
              <p>
                {t('已选择：', 'Selected: ')}
                {batchIDs.length} ·{' '}
                {batchIDs.map((id) => openModels[id]?.upstream_model_id ?? id).join(', ')}
              </p>
            ) : null}
            <button
              className="nb-btn nb-btn--secondary"
              type="button"
              disabled={
                !batchIDs.length ||
                batch.pending ||
                refreshActive ||
                upstreamLocked ||
                upstreamDirty ||
                Object.values(lockedEditors).some(Boolean) ||
                (!batch.uncertain && batchIDs.some((id) => !drafts[id]))
              }
              onClick={() => {
                const input =
                  batch.uncertain && batch.input
                    ? batch.input
                    : { models: batchIDs.map((id) => ({ id, input: drafts[id]! })) };
                void batch.run(input, (result) => {
                  setBatchResult(result);
                  if (result.applied)
                    void reloadModels(result.receipts.map((receipt) => receipt.id));
                });
              }}
            >
              {batch.uncertain
                ? t('重试同一次批量保存', 'Retry the same batch save')
                : copy('admin.picturebook.saveSelectedModels')}
            </button>
            {batch.error ? <ErrorState error={batch.error} /> : null}
            {batchResult?.issues.length ? (
              <ul role="alert">
                {batchResult.issues.map((issue, index) => (
                  <li key={index}>
                    {openModels[issue.model_id]?.upstream_model_id ?? issue.model_id}:{' '}
                    {issue.field_path}: {issue.safe_message}
                  </li>
                ))}
              </ul>
            ) : null}
            {batchResult?.applied ? (
              <p role="status">
                {t(
                  '批量保存成功，正在读回权威修订。',
                  'Batch saved; reloading authoritative revisions.',
                )}
              </p>
            ) : null}
          </div>
        ) : null}
        <div className="picturebook-actions">
          <SimplePager
            page={filter.page}
            hasMore={Boolean(models.data && filter.page * filter.page_size < models.data.total)}
            disabled={locked || models.isFetching}
            onPrev={() =>
              setFilter((current) => ({
                ...current,
                page: current.page - 1,
                catalog_revision: models.data?.revision,
              }))
            }
            onNext={() =>
              setFilter((current) => ({
                ...current,
                page: current.page + 1,
                catalog_revision: models.data?.revision,
              }))
            }
            labels={{ previous: t('上一页', 'Previous'), next: t('下一页', 'Next') }}
          />
          {selected ? (
            <button
              className="nb-btn nb-btn--secondary"
              disabled={locked || refreshActive || upstreamLocked || models.isFetching}
              onClick={async () => {
                await reloadModels([selected.id]);
              }}
            >
              {dirty[selected.id]
                ? t('放弃草稿并重新读取模型', 'Discard draft and reload model')
                : t('重新读取选中模型', 'Reload selected model')}
            </button>
          ) : null}
        </div>
      </Card>
      {reloadError ? <ErrorState error={reloadError} /> : null}
      {Object.values(openModels).map((model) => (
        <div key={model.id} hidden={selected?.id !== model.id}>
          <ModelEditor
            ref={(handle) => {
              editorRefs.current[model.id] = handle;
            }}
            key={model.id + ':' + model.revision + ':' + (epochs[model.id] ?? 0)}
            value={model}
            disabled={
              batch.locked || refreshActive || upstreamLocked || upstreamDirty || !!reloadError
            }
            onLocked={(value) =>
              setLockedEditors((current) =>
                current[model.id] === value ? current : { ...current, [model.id]: value },
              )
            }
            onDraft={(input) =>
              setDrafts((current) =>
                current[model.id] === input ? current : { ...current, [model.id]: input },
              )
            }
            onDirty={(value) =>
              setDirty((current) =>
                current[model.id] === value ? current : { ...current, [model.id]: value },
              )
            }
            onSaved={(receipt) => {
              setReloadError(null);
              void reloadModels([receipt.id]);
              if (pendingSelection) {
                finishSelection(pendingSelection.model);
              }
            }}
          />
        </div>
      ))}
    </div>
  );
});
export function AdminContent({
  account,
  section,
}: {
  readonly account: string;
  readonly section?: PictureBookSection;
}) {
  const client = useQueryClient(),
    key = ['admin', 'picture-book', account, 'upstream'] as const;
  const t = usePictureBookText();
  const catalogRef = useRef<CatalogDraftHandle>(null);
  const upstreamRef = useRef<UpstreamDraftHandle>(null);
  const [modelDirty, setModelDirty] = useState(false);
  const [upstreamDirty, setUpstreamDirty] = useState(false);
  const [modelLocked, setModelLocked] = useState(false);
  const [upstreamLocked, setUpstreamLocked] = useState(false);
  const [savingDrafts, setSavingDrafts] = useState(false);
  const anyDirty = modelDirty || upstreamDirty;
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      anyDirty &&
      (currentLocation.pathname !== nextLocation.pathname ||
        currentLocation.search !== nextLocation.search),
  );
  useEffect(() => {
    if (!anyDirty) return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [anyDirty]);
  const leaveAfterSave = async () => {
    if (savingDrafts) return;
    setSavingDrafts(true);
    try {
      if (modelDirty && !(await catalogRef.current?.saveDrafts())) return;
      if (upstreamDirty && !(await upstreamRef.current?.saveDraft())) return;
      if (blocker.state === 'blocked') blocker.proceed();
    } finally {
      setSavingDrafts(false);
    }
  };
  const discardAndLeave = () => {
    if (upstreamDirty) upstreamRef.current?.discardDraft();
    if (modelDirty) catalogRef.current?.discardDrafts();
    if (blocker.state === 'blocked') blocker.proceed();
  };
  const upstream = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => stationSessionWrite(client, 'admin', () => getUpstream(signal)),
    refetchOnWindowFocus: false,
    retry: false,
  });
  const refetchUpstream = upstream.refetch;
  const onRefreshed = useCallback(async () => {
    const result = await refetchUpstream();
    if (result.error) throw result.error;
  }, [refetchUpstream]);
  return (
    <div className="picturebook-stack">
      {blocker.state === 'blocked' ? (
        <Card>
          <div role="alert" className="picturebook-stack">
            <p>
              {t(
                '绘本配置有未保存草稿。保存成功后离开、放弃草稿，或继续编辑。',
                'Picture book settings have unsaved drafts. Save successfully before leaving, discard the drafts, or continue editing.',
              )}
            </p>
            <div className="picturebook-actions">
              <button
                className="nb-btn nb-btn--primary"
                type="button"
                disabled={savingDrafts}
                onClick={() => void leaveAfterSave()}
              >
                {t('保存并离开', 'Save and leave')}
              </button>
              <button
                className="nb-btn nb-btn--secondary"
                type="button"
                disabled={savingDrafts || modelLocked || upstreamLocked}
                onClick={discardAndLeave}
              >
                {t('放弃草稿并离开', 'Discard drafts and leave')}
              </button>
              <button
                className="nb-btn nb-btn--secondary"
                type="button"
                disabled={savingDrafts}
                onClick={() => blocker.reset()}
              >
                {t('继续编辑', 'Continue editing')}
              </button>
            </div>
          </div>
        </Card>
      ) : null}
      <div
        id="picture-book-panel-service"
        role="tabpanel"
        aria-labelledby="picture-book-tab-service"
        hidden={section !== undefined && section !== 'service'}
      >
        {upstream.isPending ? (
          <LoadingState />
        ) : upstream.error ? (
          <ErrorState error={upstream.error} onRetry={() => void upstream.refetch()} />
        ) : null}
        {upstream.data ? (
          <UpstreamForm
            ref={upstreamRef}
            key={upstream.data.revision}
            value={upstream.data}
            onDirty={setUpstreamDirty}
            onLocked={setUpstreamLocked}
            disabled={modelDirty || modelLocked || upstream.isFetching || upstream.isError}
            onSaved={() => {
              void client.invalidateQueries({ queryKey: key });
              void client.invalidateQueries({
                queryKey: ['admin', 'picture-book', account, 'controls'],
              });
              void client.invalidateQueries({
                queryKey: ['admin', 'picture-book', account, 'models'],
              });
            }}
            onReload={() => void upstream.refetch()}
          />
        ) : null}
      </div>
      <div
        id="picture-book-panel-models"
        role="tabpanel"
        aria-labelledby="picture-book-tab-models"
        hidden={section !== undefined && section !== 'models'}
      >
        {upstream.isPending ? (
          <LoadingState />
        ) : upstream.error ? (
          <ErrorState error={upstream.error} onRetry={() => void upstream.refetch()} />
        ) : !upstream.data?.configured ? (
          <p className="table-note">
            {t(
              '请先在“图片生成服务”中配置服务，再读取模型目录。',
              'Configure the Image generation service first, then load its model catalog.',
            )}
          </p>
        ) : null}
        {upstream.data?.configured ? (
          <>
            <Catalog
              ref={catalogRef}
              key={upstream.data.control?.id}
              account={account}
              onDirty={setModelDirty}
              onLocked={setModelLocked}
              upstreamDirty={upstreamDirty}
              upstreamLocked={upstreamLocked || upstream.isFetching || upstream.isError}
              onRefreshed={onRefreshed}
            />
          </>
        ) : null}
      </div>
      <div
        id="picture-book-panel-recovery"
        role="tabpanel"
        aria-labelledby="picture-book-tab-recovery"
        hidden={section !== undefined && section !== 'recovery'}
      >
        <RecoveryPanel account={account} />
      </div>
    </div>
  );
}
/** Compose inside the administrator LimitedActivitiesPage. */
export function PictureBookAdmin({ section }: { readonly section?: PictureBookSection }) {
  const session = useAdminSession(),
    account = session.data?.admin.username;
  return account && !session.error ? (
    <AdminContent key={account} account={account} section={section} />
  ) : null;
}

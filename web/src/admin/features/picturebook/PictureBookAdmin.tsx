import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { stationSessionWrite } from '@shared/charityManagement';
import { Card, ErrorState, LoadingState } from '@shared/components/States';
import { taskErrorLabel, usePictureBookText } from '@shared/picturebook/copy';
import { useImageOperation } from '@shared/picturebook/useImageOperation';
import { useAdminSession } from '../../data';
import {
  getAdminModel,
  getAdminModels,
  getRefresh,
  getUpstream,
  refreshModels,
  type AdminModel,
} from './adminApi';
import { UpstreamForm } from './UpstreamForm';
import { ModelEditor } from './ModelEditor';
import { RecoveryPanel } from './RecoveryPanel';
import '@shared/picturebook/picturebook.css';

function Catalog({ account }: { readonly account: string }) {
  const t = usePictureBookText(),
    client = useQueryClient();
  const [cursors, setCursors] = useState<(string | undefined)[]>([undefined]),
    [page, setPage] = useState(0);
  const [reloadError, setReloadError] = useState<unknown>(null);
  const [selected, setSelected] = useState<AdminModel | null>(null),
    [locked, setLocked] = useState(false),
    [operationID, setOperationID] = useState('');
  const root = ['admin', 'picture-book', account, 'models'] as const;
  const models = useQuery({
    queryKey: [...root, cursors[page] ?? ''],
    queryFn: ({ signal }) =>
      stationSessionWrite(client, 'admin', () => getAdminModels(cursors[page], signal)),
    refetchOnWindowFocus: false,
    retry: false,
  });
  const discovery = useImageOperation('admin', refreshModels);
  const operation = useQuery({
    queryKey: ['admin', 'picture-book', account, 'refresh', operationID],
    queryFn: ({ signal }) =>
      stationSessionWrite(client, 'admin', () => getRefresh(operationID, signal)),
    enabled: !!operationID,
    retry: false,
    refetchInterval: (query) =>
      query.state.data && ['succeeded', 'failed'].includes(query.state.data.state) ? false : 2000,
  });
  const state = operation.data?.state;
  useEffect(() => {
    if (state === 'succeeded')
      void client.invalidateQueries({ queryKey: ['admin', 'picture-book', account, 'models'] });
  }, [client, account, operationID, state]);
  return (
    <div className="picturebook-stack">
      <Card>
        <h2>{t('图像模型目录', 'Image model catalog')}</h2>
        <p>
          {t(
            '拉取目录不会自动向用户开放模型。逐个设置用户名称、价格和支持参数后再开放。',
            'Refreshing the catalog does not expose models to users. Configure each display name, price and supported parameters before enabling it.',
          )}
        </p>
        <button
          className="btn btn-primary"
          disabled={discovery.pending || state === 'queued' || state === 'running'}
          onClick={() =>
            void discovery.run(discovery.input ?? {}, (result) => {
              client.setQueryData(['admin', 'picture-book', account, 'refresh', result.id], result);
              setOperationID(result.id);
            })
          }
        >
          {discovery.uncertain
            ? t('重试同一次模型拉取', 'Retry the same model refresh')
            : t('拉取模型目录', 'Refresh model catalog')}
        </button>
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
        {operation.data ? (
          <p role="status">
            {state === 'succeeded'
              ? t('目录已更新，模型数：', 'Catalog updated. Models: ') + operation.data.model_count
              : state === 'failed'
                ? taskErrorLabel(operation.data.error_code, t) ||
                  t('模型拉取失败。', 'Model discovery failed.')
                : t('正在等待或拉取模型目录。', 'Model discovery is queued or running.')}
          </p>
        ) : null}
        {models.isPending ? (
          <LoadingState />
        ) : models.error ? (
          <ErrorState error={models.error} onRetry={() => void models.refetch()} />
        ) : null}
        <div className="picturebook-form">
          <label>
            {t('选择要配置的模型', 'Choose a model to configure')}
            <select
              disabled={locked}
              value={selected?.id ?? ''}
              onChange={(event) =>
                setSelected(
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
                </option>
              ))}
            </select>
          </label>
        </div>
        <div className="picturebook-actions">
          <button
            className="btn btn-secondary"
            disabled={!page || locked || models.isFetching}
            onClick={() => setPage((value) => value - 1)}
          >
            {t('上一页', 'Previous')}
          </button>
          <button
            className="btn btn-secondary"
            disabled={!models.data?.next_cursor || locked || models.isFetching}
            onClick={() => {
              if (models.data?.next_cursor) {
                setCursors((old) => [
                  ...old.slice(0, page + 1),
                  models.data.next_cursor ?? undefined,
                ]);
                setPage((value) => value + 1);
              }
            }}
          >
            {t('下一页', 'Next')}
          </button>
          {selected ? (
            <button
              className="btn btn-secondary"
              disabled={locked || models.isFetching}
              onClick={async () => {
                try {
                  setReloadError(null);
                  setSelected(
                    await stationSessionWrite(client, 'admin', () => getAdminModel(selected.id)),
                  );
                } catch (error) {
                  setReloadError(error);
                }
              }}
            >
              {t('重新读取选中模型', 'Reload selected model')}
            </button>
          ) : null}
        </div>
      </Card>
      {reloadError ? <ErrorState error={reloadError} /> : null}
      {selected ? (
        <ModelEditor
          key={selected.id + ':' + selected.revision}
          value={selected}
          onLocked={setLocked}
          onSaved={(receipt) => {
            setReloadError(null);
            void stationSessionWrite(client, 'admin', () => getAdminModel(receipt.id))
              .then(setSelected)
              .catch(setReloadError);
            void client.invalidateQueries({ queryKey: root });
          }}
        />
      ) : null}
    </div>
  );
}
function AdminContent({ account }: { readonly account: string }) {
  const client = useQueryClient(),
    key = ['admin', 'picture-book', account, 'upstream'] as const;
  const upstream = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => stationSessionWrite(client, 'admin', () => getUpstream(signal)),
    refetchOnWindowFocus: false,
    retry: false,
  });
  return (
    <div className="picturebook-stack">
      {upstream.isPending ? (
        <LoadingState />
      ) : upstream.error ? (
        <ErrorState error={upstream.error} onRetry={() => void upstream.refetch()} />
      ) : null}
      {upstream.data ? (
        <UpstreamForm
          key={upstream.data.revision}
          value={upstream.data}
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
      {upstream.data?.configured ? (
        <Catalog key={upstream.data.control?.id} account={account} />
      ) : null}
      <RecoveryPanel account={account} />
    </div>
  );
}
/** Compose inside the administrator LimitedActivitiesPage. */
export function PictureBookAdmin() {
  const session = useAdminSession(),
    account = session.data?.admin.username;
  return account && !session.error ? <AdminContent key={account} account={account} /> : null;
}

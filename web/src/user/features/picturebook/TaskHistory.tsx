import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { limitedActivityKeys } from '../limitedactivities/queries';
import { Card, ErrorState, LoadingState } from '@shared/components/States';
import { taskErrorLabel, taskStatusLabel, usePictureBookText } from '@shared/picturebook/copy';
import { cancelTask } from '@shared/picturebook/publicApi';
import { useImageOperation } from '@shared/picturebook/useImageOperation';
import { useImageReconcile, useImageTask, useImageTasks } from './queries';
import { ImageResults } from './ImageResults';
import { useDateTimeFormatter } from '@shared/utils/datetime';

export function TaskDetail({ id, account }: { readonly id: string; readonly account: string }) {
  const t = usePictureBookText(),
    query = useImageTask(account, id),
    reconcile = useImageReconcile(account);
  const cancel = useImageOperation('steward', cancelTask, reconcile);
  const task = query.data;
  const client = useQueryClient();
  const taskID = task?.id,
    billing = task?.billing_state;
  useEffect(() => {
    if (taskID && billing && billing !== 'reserved') {
      void client.invalidateQueries({ queryKey: limitedActivityKeys.wallet(account) });
    }
  }, [account, billing, client, taskID]);
  return (
    <Card>
      <h2>{t('任务详情', 'Task details')}</h2>
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : null}
      {task ? (
        <>
          <p role="status">{taskStatusLabel(task.status, t)}</p>
          <dl className="picturebook-facts">
            <dt>{t('请求张数', 'Requested images')}</dt>
            <dd>{task.n}</dd>
            <dt>
              {task.billing_state === 'reserved'
                ? t('已预扣', 'Reserved')
                : t('实际收费', 'Charged')}
            </dt>
            <dd>
              {task.charge.paper} {t('草稿纸', 'paper')} + {task.charge.brush}{' '}
              {t('画笔', 'brushes')}
            </dd>
            <dt>{t('已退款', 'Refunded')}</dt>
            <dd>
              {task.refund.paper} {t('草稿纸', 'paper')} + {task.refund.brush}{' '}
              {t('画笔', 'brushes')}
            </dd>
            {task.queue_position !== null ? (
              <>
                <dt>{t('当前排队位置', 'Queue position')}</dt>
                <dd>{task.queue_position}</dd>
              </>
            ) : null}
          </dl>
          {task.error_code ? <p>{taskErrorLabel(task.error_code, t)}</p> : null}
          {task.status === 'queued' || cancel.uncertain ? (
            <button
              className="btn btn-secondary"
              disabled={cancel.pending}
              onClick={() => void cancel.run(cancel.input ?? task.id)}
            >
              {cancel.uncertain
                ? t('重试同一次撤回', 'Retry the same cancellation')
                : t('撤回并退还预扣', 'Cancel and refund reservation')}
            </button>
          ) : null}
          {cancel.uncertain ? (
            <p role="status">
              {t(
                '撤回结果尚未确认，请重试同一次撤回。',
                'Cancellation is unconfirmed. Retry the same cancellation.',
              )}
            </p>
          ) : null}
          {cancel.error ? <ErrorState error={cancel.error} /> : null}
          {task.status === 'succeeded' ? (
            <ImageResults key={account + ':' + id} task={task} account={account} />
          ) : null}
        </>
      ) : null}
    </Card>
  );
}
export function TaskHistory({
  account,
  onSelect,
}: {
  readonly account: string;
  readonly onSelect: (id: string) => void;
}) {
  const formatDateTime = useDateTimeFormatter();
  const t = usePictureBookText();
  const [cursors, setCursors] = useState<(string | undefined)[]>([undefined]),
    [page, setPage] = useState(0);
  const query = useImageTasks(account, cursors[page]);
  return (
    <Card>
      <h2>{t('近30天任务', 'Tasks from the last 30 days')}</h2>
      <p className="field-help">{t('本地时间', 'Local time')}</p>
      <p>
        {t(
          '任务记录不包含提示词或图片存档。兑换和收费流水可在账号导出中查看。',
          'Task records do not contain saved prompts or images. Account exports include exchange and billing entries.',
        )}
      </p>
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : null}
      {query.data?.data.length === 0 ? <p>{t('暂无任务。', 'No tasks yet.')}</p> : null}
      <div className="picturebook-stack">
        {query.data?.data.map((task) => (
          <article className="picturebook-card" key={task.id}>
            <p>
              {formatDateTime(task.created_at)} · {taskStatusLabel(task.status, t)} · {task.n}{' '}
              {t('张', 'images')}
            </p>
            <button className="btn btn-secondary" onClick={() => onSelect(task.id)}>
              {t('查看任务', 'View task')}
            </button>
          </article>
        ))}
      </div>
      <div className="picturebook-actions">
        <button
          className="btn btn-secondary"
          disabled={page === 0 || query.isFetching}
          onClick={() => setPage((value) => value - 1)}
        >
          {t('上一页', 'Previous')}
        </button>
        <button
          className="btn btn-secondary"
          disabled={!query.data?.next_cursor || query.isFetching}
          onClick={() => {
            if (!query.data?.next_cursor) return;
            setCursors((values) => [
              ...values.slice(0, page + 1),
              query.data.next_cursor ?? undefined,
            ]);
            setPage((value) => value + 1);
          }}
        >
          {t('下一页', 'Next')}
        </button>
      </div>
    </Card>
  );
}

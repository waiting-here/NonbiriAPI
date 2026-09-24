import { Card, ErrorState, LoadingState } from '@shared/components/States';
import { usePictureBookText } from '@shared/picturebook/copy';
import { useImageQueue } from './queries';

export function TaskQueue({
  account,
  onSelect,
}: {
  readonly account: string;
  readonly onSelect: (id: string) => void;
}) {
  const t = usePictureBookText(),
    query = useImageQueue(account);
  return (
    <Card>
      <h2>{t('生成队列', 'Generation queue')}</h2>
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : null}
      {query.data ? (
        <>
          <dl className="picturebook-facts">
            <dt>{t('等待中', 'Queued')}</dt>
            <dd>{query.data.queued}</dd>
            <dt>{t('执行中', 'Running')}</dt>
            <dd>{query.data.running}</dd>
          </dl>
          {query.data.dispatch_paused ? (
            <p role="status">
              {t(
                '新任务派发已暂停。未开始的任务仍可撤回，并可能在等待超时后退款。',
                'New task dispatch is paused. Tasks that have not started can still be cancelled and may be refunded when their queue wait expires.',
              )}
            </p>
          ) : null}
          <h3>{t('我的未完成任务', 'My unfinished tasks')}</h3>
          {query.data.own.length ? (
            <ul>
              {query.data.own.map((task) => (
                <li key={task.task_id}>
                  <button className="btn btn-secondary" onClick={() => onSelect(task.task_id)}>
                    {task.position === null
                      ? t('查看执行中的任务', 'View running task')
                      : t('查看排队任务，位置 ', 'View queued task, position ') + task.position}
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p>{t('没有未完成任务。', 'No unfinished tasks.')}</p>
          )}
        </>
      ) : null}
    </Card>
  );
}

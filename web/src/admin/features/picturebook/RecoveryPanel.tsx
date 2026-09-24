import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { stationSessionWrite } from '@shared/charityManagement';
import { Card, ErrorState, LoadingState } from '@shared/components/States';
import { usePictureBookText } from '@shared/picturebook/copy';
import { useImageOperation } from '@shared/picturebook/useImageOperation';
import { getControls, resumeUpstream, type ControlRow } from './adminApi';

function RecoveryAction({
  control,
  onRecovered,
}: {
  readonly control: ControlRow;
  readonly onRecovered: () => void;
}) {
  const t = usePictureBookText(),
    [reason, setReason] = useState(''),
    [confirmed, setConfirmed] = useState(false);
  const operation = useImageOperation('admin', resumeUpstream);
  return (
    <form
      className="picturebook-form"
      onSubmit={(event) => {
        event.preventDefault();
        const input = operation.input ?? {
          control_id: control.id,
          expected_revision: control.revision,
          reason,
        };
        if (operation.uncertain || (confirmed && reason.trim()))
          void operation.run(input, onRecovered);
      }}
    >
      <p>
        {t(
          '恢复不会重发生成或再次扣款。请先确认仍可能在运行的请求已妥善处理，再释放其并发占位。',
          'Resuming does not resubmit generation or charge again. Confirm that potentially running requests have been handled before releasing their concurrency slots.',
        )}
      </p>
      <fieldset disabled={operation.locked}>
        <label>
          {t('恢复原因', 'Reason for resuming')}
          <textarea
            value={reason}
            maxLength={1024}
            required
            onChange={(event) => setReason(event.target.value.replace(/\r\n?/g, '\n'))}
          />
        </label>
        <label className="picturebook-checkbox">
          <input
            type="checkbox"
            checked={confirmed}
            required
            onChange={(event) => setConfirmed(event.target.checked)}
          />
          {t(
            '我已核实残留请求，确认恢复新任务派发',
            'I checked outstanding requests and confirm resuming dispatch',
          )}
        </label>
      </fieldset>
      {operation.uncertain ? (
        <p role="status">
          {t(
            '恢复结果尚未确认，请重试同一次恢复。',
            'The resume result is unconfirmed. Retry the same operation.',
          )}
        </p>
      ) : null}
      {operation.error ? <ErrorState error={operation.error} /> : null}
      <button
        className="btn btn-primary"
        type="submit"
        disabled={operation.pending || (!operation.uncertain && (!confirmed || !reason.trim()))}
      >
        {operation.uncertain
          ? t('重试同一次恢复', 'Retry the same resume')
          : t('确认恢复', 'Confirm resume')}
      </button>
    </form>
  );
}
export function RecoveryPanel({ account }: { readonly account: string }) {
  const t = usePictureBookText(),
    client = useQueryClient();
  const [cursors, setCursors] = useState<(string | undefined)[]>([undefined]),
    [page, setPage] = useState(0);
  const query = useQuery({
    queryKey: ['admin', 'picture-book', account, 'controls', cursors[page] ?? ''],
    queryFn: ({ signal }) =>
      stationSessionWrite(client, 'admin', () => getControls(cursors[page], signal)),
    refetchOnWindowFocus: false,
    retry: false,
  });
  return (
    <Card>
      <h2>{t('服务保护与恢复', 'Service protection and recovery')}</h2>
      <p>
        {t(
          '当前及旧服务配置均列在这里。未知结果会暂停对应服务的新派发，直到管理员核实后恢复。',
          'Current and previous service configurations are listed here. Unknown results pause new dispatch for that service until an administrator checks and resumes it.',
        )}
      </p>
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : null}
      {query.data?.data.map((control) => (
        <article className="picturebook-card" key={control.id + ':' + control.revision}>
          <h3>
            {control.current ? t('当前服务', 'Current service') : t('旧服务', 'Previous service')}
          </h3>
          <p className="picturebook-prewrap">{control.id}</p>
          <dl className="picturebook-facts">
            <dt>{t('保护状态', 'Protection')}</dt>
            <dd>{control.paused ? t('已暂停派发', 'Dispatch paused') : t('正常', 'Normal')}</dd>
            <dt>{t('等待／执行中', 'Queued / running')}</dt>
            <dd>
              {control.queued} / {control.running}
            </dd>
            <dt>{t('未确认占位', 'Uncertain slots')}</dt>
            <dd>{control.uncertain_slots}</dd>
          </dl>
          {control.paused ? (
            <RecoveryAction control={control} onRecovered={() => void query.refetch()} />
          ) : null}
        </article>
      ))}
      {query.data?.data.length === 0 ? (
        <p>{t('尚未配置服务。', 'No service configured yet.')}</p>
      ) : null}
      <div className="picturebook-actions">
        <button
          className="btn btn-secondary"
          disabled={!page || query.isFetching}
          onClick={() => setPage((value) => value - 1)}
        >
          {t('上一页', 'Previous')}
        </button>
        <button
          className="btn btn-secondary"
          disabled={!query.data?.next_cursor || query.isFetching}
          onClick={() => {
            if (query.data?.next_cursor) {
              setCursors((old) => [...old.slice(0, page + 1), query.data.next_cursor ?? undefined]);
              setPage((value) => value + 1);
            }
          }}
        >
          {t('下一页', 'Next')}
        </button>
      </div>
    </Card>
  );
}

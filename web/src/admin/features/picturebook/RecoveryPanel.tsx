import { SimplePager } from '@shared/operations/SimplePager';
import { useState } from 'react';
import {
  Field,
  Fields,
  FormActions,
  Panel,
  PanelBody,
  PanelHead,
  StatusBadge,
} from '@shared/components/ui';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { stationSessionWrite } from '@shared/charityManagement';
import { EmptyState, ErrorState, LoadingState } from '@shared/components/States';
import { formatCount } from '@shared/utils/formatNumber';
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
        <Fields>
          <Field size="long" label={t('恢复原因', 'Reason for resuming')}>
            {(props) => (
              <textarea
                {...props}
                value={reason}
                maxLength={1024}
                required
                onChange={(event) => setReason(event.target.value.replace(/\r\n?/g, '\n'))}
              />
            )}
          </Field>
          <Field
            label={t(
              '我已核实残留请求，确认恢复新任务派发',
              'I checked outstanding requests and confirm resuming dispatch',
            )}
          >
            {(props) => (
              <input
                {...props}
                type="checkbox"
                checked={confirmed}
                required
                onChange={(event) => setConfirmed(event.target.checked)}
              />
            )}
          </Field>
        </Fields>
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
      <FormActions>
        <button
          className="nb-btn nb-btn--primary"
          type="submit"
          disabled={operation.pending || (!operation.uncertain && (!confirmed || !reason.trim()))}
        >
          {operation.uncertain
            ? t('重试同一次恢复', 'Retry the same resume')
            : t('确认恢复', 'Confirm resume')}
        </button>
      </FormActions>
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
    <Panel>
      <PanelHead
        title={t('服务保护与恢复', 'Service protection and recovery')}
        description={t(
          '当前及旧服务配置均列在这里。未知结果会暂停对应服务的新派发，直到管理员核实后恢复。',
          'Current and previous service configurations are listed here. Unknown results pause new dispatch for that service until an administrator checks and resumes it.',
        )}
      />
      <PanelBody>
        {query.isPending ? (
          <LoadingState />
        ) : query.error ? (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} />
        ) : null}
        {query.data?.data.map((control) => (
          <article
            className="picturebook-card picturebook-card--surface picturebook-stack"
            key={control.id + ':' + control.revision}
          >
            <div className="picturebook-actions">
              <h3>
                {control.current
                  ? t('当前服务', 'Current service')
                  : t('旧服务', 'Previous service')}
              </h3>
              <StatusBadge tone={control.paused ? 'warn' : 'ok'}>
                {control.paused ? t('已暂停派发', 'Dispatch paused') : t('正常', 'Normal')}
              </StatusBadge>
            </div>
            <p className="picturebook-prewrap mono">{control.id}</p>
            <dl className="picturebook-facts">
              <dt>{t('等待／执行中', 'Queued / running')}</dt>
              <dd>
                {formatCount(control.queued).display} / {formatCount(control.running).display}
              </dd>
              <dt>{t('未确认占位', 'Uncertain slots')}</dt>
              <dd>{formatCount(control.uncertain_slots).display}</dd>
            </dl>
            {control.paused ? (
              <RecoveryAction control={control} onRecovered={() => void query.refetch()} />
            ) : null}
          </article>
        ))}
        {query.data?.data.length === 0 ? (
          <EmptyState
            title={t('尚未配置服务', 'No service configured yet')}
            body={t(
              '保存图片生成服务后，这里会显示派发保护状态。',
              'Protection status appears here once an image generation service is saved.',
            )}
          />
        ) : null}
        {(query.data?.data.length ?? 0) > 0 || page > 0 ? (
          <SimplePager
            page={page + 1}
            hasMore={Boolean(query.data?.next_cursor)}
            disabled={query.isFetching}
            onPrev={() => setPage((value) => value - 1)}
            onNext={() => {
              if (query.data?.next_cursor) {
                setCursors((old) => [
                  ...old.slice(0, page + 1),
                  query.data.next_cursor ?? undefined,
                ]);
                setPage((value) => value + 1);
              }
            }}
            labels={{ previous: t('上一页', 'Previous'), next: t('下一页', 'Next') }}
          />
        ) : null}
      </PanelBody>
    </Panel>
  );
}

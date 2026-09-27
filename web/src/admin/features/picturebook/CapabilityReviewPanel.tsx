import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { stationSessionWrite } from '@shared/charityManagement';
import { Card, ErrorState, LoadingState } from '@shared/components/States';
import { usePictureBookText } from '@shared/picturebook/copy';
import { useImageOperation } from '@shared/picturebook/useImageOperation';
import { applyCapabilities, getCapabilityReview, type AdminModel } from './adminApi';

export function CapabilityReviewPanel({
  account,
  model,
  onApplied,
}: {
  readonly account: string;
  readonly model: AdminModel;
  readonly onApplied: (id: string) => void;
}) {
  const t = usePictureBookText();
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const review = useQuery({
    queryKey: ['admin', 'picture-book', account, 'capability-review', model.id],
    queryFn: ({ signal }) =>
      stationSessionWrite(client, 'admin', () => getCapabilityReview(model.id, signal)),
    enabled: open,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const apply = useImageOperation('admin', applyCapabilities);
  const source =
    review.data?.source && typeof review.data.source === 'object'
      ? (review.data.source as Record<string, unknown>)
      : null;
  const conflicts = review.data?.changes.some((change) => change.conflict) ?? false;
  return (
    <Card>
      <h3>{t('刷新能力差异', 'Refreshed capability differences')}</h3>
      <p>
        {t(
          '查看候选来源与人工覆盖冲突。应用只更新能力修订，名称、开放状态和价格保持不变。',
          'Review candidate sources and manual conflicts. Applying changes only the capability revision; name, availability, and pricing stay as saved.',
        )}
      </p>
      <button
        className="btn btn-secondary"
        type="button"
        onClick={() => {
          setOpen(true);
          setConfirm(false);
          void review.refetch();
        }}
      >
        {t('读取最新候选能力', 'Load latest capability candidate')}
      </button>
      {open && review.isPending ? <LoadingState /> : null}
      {open && review.error ? (
        <ErrorState error={review.error} onRetry={() => void review.refetch()} />
      ) : null}
      {review.data ? (
        <div className="picturebook-stack">
          <p>
            {t('候选修订：', 'Candidate snapshot: ')}
            <code>{review.data.snapshot_id}</code> · {t('有效至：', 'Expires: ')}
            {new Date(review.data.expires_at * 1000).toLocaleString()}
          </p>
          <p>
            {t('来源能力状态：', 'Source capability status: ')}
            {String(source?.readiness ?? 'unknown')}
          </p>
          {review.data.changes.length ? (
            <ul>
              {review.data.changes.map((change) => (
                <li key={change.key}>
                  {change.key}: {change.kind}
                  {change.conflict ? ` · ${t('人工覆盖冲突', 'Manual override conflict')}` : ''}
                </li>
              ))}
            </ul>
          ) : (
            <p>{t('未检测到参数差异。', 'No parameter changes detected.')}</p>
          )}
          <details>
            <summary>
              {t('仅管理员可见的候选能力', 'Candidate capability, administrators only')}
            </summary>
            <pre className="picturebook-prewrap">{JSON.stringify(review.data.source, null, 2)}</pre>
          </details>
          <label className="picturebook-checkbox">
            <input
              type="checkbox"
              checked={confirm}
              disabled={apply.locked}
              onChange={(event) => setConfirm(event.target.checked)}
            />
            {t('我已检查差异并确认应用', 'I reviewed the differences and confirm application')}
          </label>
          {conflicts ? (
            <p role="alert">
              {t(
                '存在人工覆盖冲突，需先调整模型规则。',
                'Manual overrides conflict with the candidate. Adjust the model rules first.',
              )}
            </p>
          ) : null}
          {apply.error ? <ErrorState error={apply.error} /> : null}
          {apply.uncertain ? (
            <p role="status">
              {t(
                '应用回执未确认，请重试同一次操作。',
                'Apply receipt unconfirmed. Retry the same operation.',
              )}
            </p>
          ) : null}
          <button
            className="btn btn-primary"
            type="button"
            disabled={!confirm || conflicts || apply.pending}
            onClick={() => {
              const input =
                apply.uncertain && apply.input
                  ? apply.input
                  : {
                      id: model.id,
                      snapshot_id: review.data.snapshot_id,
                      expected_revision: model.revision,
                      confirm: true,
                    };
              void apply.run(input, (receipt) => onApplied(receipt.id));
            }}
          >
            {apply.uncertain
              ? t('重试同一次应用', 'Retry the same apply')
              : t('应用候选能力', 'Apply candidate capability')}
          </button>
        </div>
      ) : null}
    </Card>
  );
}

import { rejectionLabels } from '@shared/rewards/copy';
import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { apiFetch } from '@shared/query/http';
import { queryPath } from '@shared/operations/api';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { ErrorState, EmptyState, LoadingState } from '@shared/components/States';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { rewardKeys, type Page } from './api';
interface Failure {
  id: number;
  user_id: string;
  reason: string;
  at: number;
}
export function DeliveryFailures({ campaign }: { campaign: string }) {
  const { t } = useTranslation();
  const time = useDateTimeFormatter();
  const [cursor, setCursor] = useState('');
  const [pages, setPages] = useState<string[]>([]);
  const query = useQuery({
    queryKey: [...rewardKeys.admin, 'failures', campaign, cursor],
    queryFn: ({ signal }) =>
      apiFetch<Page<Failure>>(
        queryPath('/admin/api/rewards/failures', { campaign_id: campaign, cursor }),
        { signal },
      ),
  });
  if (query.isPending) return <LoadingState />;
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  return (
    <div className="reward-stack">
      {query.data?.items.length === 0 ? (
        <EmptyState title={t('common.rewards.noFailures')} body={t('common.rewards.retryHelp')} />
      ) : (
        query.data?.items.map((f) => (
          <p key={f.id}>
            {t('common.rewards.recipient', { id: f.user_id })} ·{' '}
            {t(rejectionLabels[f.reason === 'capacity' ? 'capacity' : 'delivery'])} · {time(f.at)}
          </p>
        ))
      )}
      {(pages.length > 0 || query.data?.next_cursor) && (
        <CursorPagination
          page={pages.length + 1}
          nextCursor={query.data?.next_cursor}
          onPrevious={() => {
            setCursor(pages.at(-1) ?? '');
            setPages(pages.slice(0, -1));
          }}
          onNext={(next) => {
            setPages([...pages, cursor]);
            setCursor(next);
          }}
        />
      )}
    </div>
  );
}

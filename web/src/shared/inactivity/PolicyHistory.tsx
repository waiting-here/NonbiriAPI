import { useState, type ReactNode } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { ErrorState } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import type { PageMetadata, PageSize } from '@shared/operations/pageNumbers';

export function PolicyHistory<T>({
  kind,
  title,
  zh,
  load,
  children,
}: {
  kind: 'runs' | 'audits';
  title: string;
  zh: boolean;
  load: (
    page: string,
    size: PageSize,
    signal: AbortSignal,
  ) => Promise<{ data: T[]; pagination: PageMetadata }>;
  children: (rows: T[]) => ReactNode;
}) {
  const [enabled, setEnabled] = useState(false);
  const pager = useUrlPagePager({
    station: 'admin',
    listType: `inactivity-${kind}`,
    scopeKey: 'administrator',
    pageParam: `${kind}_page`,
    pageSizeParam: `${kind}_page_size`,
  });
  const query = useQuery({
    queryKey: ['admin', 'inactivity-policy-history', kind, pager.page, pager.pageSize],
    queryFn: ({ signal }) => load(pager.page, pager.pageSize, signal),
    enabled,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    placeholderData: keepPreviousData,
  });
  // Keep navigation available after a failed page request, without showing old rows.
  const [lastPage, setLastPage] = useState<PageMetadata>();
  if (query.data && !query.isPlaceholderData && lastPage !== query.data.pagination) {
    setLastPage(query.data.pagination);
  }
  const metadata = query.data?.pagination ?? lastPage;
  const ready = query.data && !query.isPlaceholderData && !query.isError;
  return (
    <details
      className="nb-fold"
      aria-label={title}
      onToggle={(event) => setEnabled(event.currentTarget.open)}
    >
      <summary>
        <span className="nb-fold__title">
          <strong>{title}</strong>
        </span>
      </summary>
      <div className="nb-fold__body">
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {zh ? '刷新记录' : 'Refresh records'}
        </button>
        {enabled && query.isFetching && <p role="status">{zh ? '正在读取…' : 'Loading…'}</p>}
        {query.isError && <ErrorState error={query.error} onRetry={() => void query.refetch()} />}
        {query.isError && !metadata && pager.page !== '1' && (
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            onClick={() => pager.setPage('1')}
          >
            {zh ? '返回第一页' : 'Return to first page'}
          </button>
        )}
        {ready &&
          (query.data.data.length ? (
            children(query.data.data)
          ) : (
            <p>{zh ? '暂无记录。' : 'No records yet.'}</p>
          ))}
        {metadata && (
          <PagePagination
            alwaysShowJump
            metadata={metadata}
            requestedPage={ready ? pager.page : undefined}
            busy={query.isFetching}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
          />
        )}
      </div>
    </details>
  );
}

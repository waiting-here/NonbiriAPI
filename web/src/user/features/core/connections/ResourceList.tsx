import { useState, type ReactNode } from 'react';
import { useSearchState } from '@shared/operations/useSearchState';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  DataTable,
  Field,
  FilterBar,
  Panel,
  PanelBody,
  PanelHead,
  type DataColumn,
} from '@shared/components/ui';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { CoreEmpty, CoreErrorPanel, CoreLoading } from '../components';
import { coreKeys } from '../queries';
import { useResourceListScroll } from '../useResourceFilters';
import type { NumberedPage, PageWindow } from '../pageTypes';

export const connectionKeys = {
  root: (accountId: string) => [...coreKeys.account(accountId), 'connections'] as const,
};

export function ConnectionResourceList<T>({
  accountId,
  scope,
  listType,
  title,
  actions,
  load,
  columns,
  rowKey,
  searchPlaceholder,
  emptyAction,
  refetchInterval = false,
  embedded = false,
  restoreScroll = false,
  filters,
  filterKey = '',
}: {
  accountId: string;
  scope: string;
  listType: string;
  title: string;
  actions?: ReactNode;
  load: (window: PageWindow, q: string, signal: AbortSignal) => Promise<NumberedPage<T>>;
  columns: readonly DataColumn<T>[];
  rowKey: (row: T) => string;
  searchPlaceholder: string;
  emptyAction?: ReactNode;
  refetchInterval?: number | false;
  embedded?: boolean;
  restoreScroll?: boolean;
  filters?: ReactNode;
  filterKey?: string;
}) {
  const { t } = useTranslation();
  const [params, setParams] = useSearchState();
  const queryParam = listType + '_q';
  const q = params.get(queryParam) ?? '';
  const [draft, setDraft] = useState(q);
  const [previousQ, setPreviousQ] = useState(q);
  if (previousQ !== q) {
    setPreviousQ(q);
    setDraft(q);
  }
  const search = () =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (draft.trim()) next.set(queryParam, draft.trim());
      else next.delete(queryParam);
      return next;
    });
  const pager = useUrlPagePager({
    station: 'user',
    listType,
    scopeKey: accountId + '\u0000' + scope,
    resetKey: q + filterKey,
    pageParam: listType + '_page',
    pageSizeParam: listType + '_page_size',
  });
  const query = useQuery({
    queryKey: [
      ...connectionKeys.root(accountId),
      scope,
      listType,
      q,
      filterKey,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) => load({ page: pager.page, pageSize: pager.pageSize }, q, signal),
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey.at(-1) === pager.pageSize &&
      JSON.stringify(previousQuery.queryKey.slice(0, -2)) ===
        JSON.stringify([...connectionKeys.root(accountId), scope, listType, q, filterKey])
        ? previous
        : undefined,
    refetchInterval,
  });
  useResourceListScroll(accountId, Boolean(query.data) && !query.isFetching, restoreScroll);
  const content = (
    <>
      <PanelHead title={title} level={embedded ? 3 : 2} actions={actions} />
      <PanelBody>
        <FilterBar
          as="div"
          activeCount={q ? 1 : 0}
          secondaryLabel={t('common.connections.filters')}
          search={
            <div className="nb-inline">
              <Field label={t('common.connections.search')}>
                {(props) => (
                  <input
                    {...props}
                    type="search"
                    placeholder={searchPlaceholder}
                    value={draft}
                    onChange={(event) => setDraft(event.target.value)}
                  />
                )}
              </Field>
              <button type="button" className="nb-btn nb-btn--secondary" onClick={search}>
                {t('common.connections.search')}
              </button>
            </div>
          }
          secondary={filters}
          onSubmit={search}
        />
        {query.error ? (
          <CoreErrorPanel error={query.error} onRetry={() => void query.refetch()} />
        ) : null}
        {query.isPending ? (
          <CoreLoading />
        ) : query.data ? (
          <>
            {query.data.data.length ? (
              <DataTable caption={title} columns={columns} rows={query.data.data} rowKey={rowKey} />
            ) : (
              <CoreEmpty
                title={t('common.connections.emptyTitle')}
                body={q ? t('common.connections.noMatches') : t('common.connections.emptyBody')}
                action={emptyAction}
              />
            )}
            <PagePagination
              metadata={query.data.pagination}
              requestedPage={pager.page}
              busy={query.isFetching}
              onPageChange={pager.setPage}
              onPageSizeChange={pager.setPageSize}
            />
          </>
        ) : null}
      </PanelBody>
    </>
  );
  return embedded ? (
    <div className="nb-stack">{content}</div>
  ) : (
    <Panel aria-busy={query.isFetching}>{content}</Panel>
  );
}

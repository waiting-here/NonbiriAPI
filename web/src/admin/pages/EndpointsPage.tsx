import { Fragment, useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchState } from '@shared/operations/useSearchState';
import { useTranslation } from 'react-i18next';
import { clearStationSession } from '@shared/charityManagement';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import {
  adminPageKeys,
  getAdminEndpointUsersPage,
  getAdminEndpointsPage,
} from '../features/operations/adminPages';
import { useAdminSession } from '../data';
import '@shared/operations/operations.css';

interface EndpointUsersPanelProps {
  account: string;
  scopeReady: boolean;
  baseURL: string;
}

function EndpointUsersPanel({ account, scopeReady, baseURL }: EndpointUsersPanelProps) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const pager = useUrlPagePager({
    station: 'admin',
    listType: 'admin.endpoint-users',
    scopeKey: account,
    scopeReady,
    resetKey: baseURL,
    pageParam: 'endpoint_user_page',
    pageSizeParam: 'endpoint_user_page_size',
  });
  const result = useQuery({
    queryKey: adminPageKeys.endpointUsers(account, baseURL, pager.page, pager.pageSize),
    queryFn: ({ signal }) => getAdminEndpointUsersPage(baseURL, pager.page, pager.pageSize, signal),
    retry: false,
    enabled: scopeReady,
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === account && previousQuery.queryKey[4] === baseURL
        ? previous
        : undefined,
  });
  useEffect(() => {
    if (isUnauthorized(result.error) || isForbidden(result.error)) {
      clearStationSession(client, 'admin');
    }
  }, [client, result.error]);
  return (
    <>
      {result.isPending ? (
        <LoadingState />
      ) : result.error ? (
        <ErrorState error={result.error} onRetry={() => void result.refetch()} />
      ) : result.data.data.length === 0 ? (
        <EmptyState title={t('common.noResults')} body={t('common.noResultsBody')} />
      ) : (
        <div aria-busy={result.isFetching}>
          {result.isFetching ? <LoadingState /> : null}
          <div className="ops-table-scroll">
            <table className="ops-table ops-table--responsive">
              <thead>
                <tr>
                  <th>{t('common.userId')}</th>
                  <th>{t('admin.endpoints.endpointCount')}</th>
                  <th>{t('admin.endpoints.keys')}</th>
                  <th>{t('admin.endpoints.enabledCount')}</th>
                </tr>
              </thead>
              <tbody>
                {result.data.data.map((user) => (
                  <tr key={user.user_id}>
                    <td data-label={t('common.userId')}>{user.user_id}</td>
                    <td data-label={t('admin.endpoints.endpointCount')}>{user.endpoint_count}</td>
                    <td data-label={t('admin.endpoints.keys')}>{user.key_count}</td>
                    <td data-label={t('admin.endpoints.enabledCount')}>{user.enabled_count}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
      {scopeReady && !result.error && result.data ? (
        <PagePagination
          metadata={result.data.pagination}
          requestedPage={pager.page}
          busy={result.isFetching}
          onPageChange={pager.setPage}
          onPageSizeChange={pager.setPageSize}
        />
      ) : null}
    </>
  );
}

interface EndpointsPageContentProps {
  account: string;
  scopeReady: boolean;
  sessionError: unknown;
}

function EndpointsPageContent({ account, scopeReady, sessionError }: EndpointsPageContentProps) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [searchParams, setSearchParams] = useSearchState();
  const query = searchParams.get('q') ?? '';
  const expanded = searchParams.get('expanded_base_url');
  const [draft, setDraft] = useState(query);
  const [queryError, setQueryError] = useState<string | null>(null);
  const pager = useUrlPagePager({
    station: 'admin',
    listType: 'admin.endpoints',
    scopeKey: account,
    scopeReady,
    resetKey: query,
  });
  const result = useQuery({
    queryKey: adminPageKeys.endpoints(account, query, pager.page, pager.pageSize),
    queryFn: ({ signal }) => getAdminEndpointsPage(query, pager.page, pager.pageSize, signal),
    retry: false,
    enabled: scopeReady,
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === account && previousQuery.queryKey[4] === query
        ? previous
        : undefined,
  });
  useEffect(() => {
    // POP navigation restores the committed search value while this input is
    // still mounted.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setDraft(query);
  }, [query]);
  useEffect(() => {
    if (isUnauthorized(result.error) || isForbidden(result.error)) {
      clearStationSession(client, 'admin');
    }
  }, [client, result.error]);
  const commitFilter = (nextQuery: string) => {
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      if (nextQuery) next.set('q', nextQuery);
      else next.delete('q');
      next.delete('page');
      next.set('page', '1');
      next.delete('page_size');
      next.set('page_size', String(pager.pageSize));
      next.delete('expanded_base_url');
      next.delete('endpoint_user_page');
      return next;
    });
  };
  const toggleExpanded = (baseURL: string) => {
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      if (result.data) next.set('page', result.data.pagination.page);
      if (next.get('expanded_base_url') === baseURL) next.delete('expanded_base_url');
      else next.set('expanded_base_url', baseURL);
      next.delete('endpoint_user_page');
      return next;
    });
  };
  return (
    <div className="page ops-page">
      <PageHeader
        title={t('admin.endpoints.title')}
        description={t('admin.endpoints.description')}
      />
      <Card>
        <form
          className="ops-toolbar"
          role="search"
          onSubmit={(event) => {
            event.preventDefault();
            const bytes = new TextEncoder().encode(draft).byteLength;
            if (bytes > 512) {
              setQueryError(t('admin.endpoints.queryTooLong', { bytes }));
              return;
            }
            setQueryError(null);
            commitFilter(draft);
          }}
        >
          <label className="ops-form-field">
            <span>{t('admin.endpoints.searchPlaceholder')}</span>
            <input
              type="search"
              value={draft}
              aria-label={t('admin.endpoints.searchAria')}
              aria-invalid={queryError ? 'true' : undefined}
              aria-describedby={queryError ? 'endpoint-query-error' : undefined}
              onChange={(event) => {
                const next = event.target.value;
                setDraft(next);
                if (new TextEncoder().encode(next).byteLength <= 512) setQueryError(null);
              }}
            />
          </label>
          <button className="btn btn-secondary" type="submit">
            {t('common.applyFilter')}
          </button>
          <button
            className="btn btn-quiet"
            type="button"
            onClick={() => {
              setDraft('');
              setQueryError(null);
              commitFilter('');
            }}
          >
            {t('common.resetFilter')}
          </button>
        </form>
        {queryError ? (
          <p id="endpoint-query-error" className="inline-error" role="alert">
            {queryError}
          </p>
        ) : null}
        {sessionError ? (
          <ErrorState error={sessionError} />
        ) : result.isPending ? (
          <LoadingState />
        ) : result.error ? (
          <ErrorState error={result.error} onRetry={() => void result.refetch()} />
        ) : result.data.data.length === 0 ? (
          <EmptyState
            title={query ? t('common.noResults') : t('admin.endpoints.empty')}
            body={query ? t('common.noResultsBody') : t('admin.endpoints.emptyBody')}
          />
        ) : (
          <div aria-busy={result.isFetching}>
            {result.isFetching ? <LoadingState /> : null}
            <div className="ops-table-scroll">
              <table className="ops-table ops-table--responsive">
                <thead>
                  <tr>
                    <th>{t('admin.endpoints.baseUrl')}</th>
                    <th>{t('admin.endpoints.users')}</th>
                    <th>{t('admin.endpoints.endpointCount')}</th>
                    <th>{t('admin.endpoints.keys')}</th>
                    <th>{t('admin.endpoints.expand')}</th>
                  </tr>
                </thead>
                <tbody>
                  {result.data.data.map((group) => {
                    const open = expanded === group.base_url;
                    return (
                      <Fragment key={group.base_url}>
                        <tr>
                          <td
                            className="ops-cell-wide ops-wrap"
                            data-label={t('admin.endpoints.baseUrl')}
                          >
                            {group.base_url}
                          </td>
                          <td data-label={t('admin.endpoints.users')}>{group.user_count}</td>
                          <td data-label={t('admin.endpoints.endpointCount')}>
                            {group.endpoint_count}
                          </td>
                          <td data-label={t('admin.endpoints.keys')}>{group.key_count}</td>
                          <td className="ops-cell-wide" data-label={t('admin.endpoints.expand')}>
                            <button
                              className="btn btn-secondary"
                              type="button"
                              aria-expanded={open}
                              disabled={!scopeReady || result.isFetching}
                              onClick={() => toggleExpanded(group.base_url)}
                            >
                              {open
                                ? t('admin.endpoints.hideUsers')
                                : t('admin.endpoints.showUsers')}
                            </button>
                          </td>
                        </tr>
                        {open ? (
                          <tr>
                            <td colSpan={5}>
                              <EndpointUsersPanel
                                account={account}
                                scopeReady={scopeReady}
                                baseURL={group.base_url}
                              />
                            </td>
                          </tr>
                        ) : null}
                      </Fragment>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        )}
        {scopeReady && !sessionError && !result.error && result.data ? (
          <PagePagination
            metadata={result.data.pagination}
            requestedPage={pager.page}
            busy={result.isFetching}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
          />
        ) : null}
        <p className="inline-notice">{t('admin.endpoints.noProbeNotice')}</p>
      </Card>
    </div>
  );
}

export function EndpointsPage() {
  const [, setSearchParams] = useSearchState();
  const session = useAdminSession();
  const account = session.data?.admin.username;
  const scopeReady = Boolean(account) && !session.error;
  const previousAccount = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (previousAccount.current !== undefined && previousAccount.current !== account) {
      setSearchParams(
        (previous) => {
          const next = new URLSearchParams(previous);
          next.delete('expanded_base_url');
          next.delete('endpoint_user_page');
          return next;
        },
        { replace: true },
      );
    }
    previousAccount.current = account;
  }, [account, setSearchParams]);
  return (
    <EndpointsPageContent
      key={account ?? 'anonymous'}
      account={account ?? ''}
      scopeReady={scopeReady}
      sessionError={session.error}
    />
  );
}

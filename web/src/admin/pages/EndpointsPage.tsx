import { Fragment, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { adminCoreKeys, getAdminEndpoints } from '../features/operations/core';
import '@shared/operations/operations.css';

export function EndpointsPage() {
  const { t } = useTranslation();
  const pager = useCursorPager();
  const [draft, setDraft] = useState('');
  const [query, setQuery] = useState('');
  const [queryError, setQueryError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const result = useQuery({
    queryKey: adminCoreKeys.endpoints(query, pager.cursor),
    queryFn: () => getAdminEndpoints(query, pager.cursor),
    retry: false,
  });
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
            setQuery(draft);
            pager.reset();
            setExpanded(new Set());
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
              setQuery('');
              pager.reset();
              setExpanded(new Set());
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
        {result.isPending ? (
          <LoadingState />
        ) : result.error ? (
          <ErrorState error={result.error} onRetry={() => void result.refetch()} />
        ) : result.data.data.length === 0 ? (
          <EmptyState
            title={query ? t('common.noResults') : t('admin.endpoints.empty')}
            body={query ? t('common.noResultsBody') : t('admin.endpoints.emptyBody')}
          />
        ) : (
          <>
            <div className="ops-table-scroll">
              <table className="ops-table">
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
                    const open = expanded.has(group.base_url);
                    return (
                      <Fragment key={group.base_url}>
                        <tr>
                          <td className="ops-wrap">{group.base_url}</td>
                          <td>{group.user_count}</td>
                          <td>{group.endpoint_count}</td>
                          <td>{group.key_count}</td>
                          <td>
                            <button
                              className="btn btn-secondary"
                              type="button"
                              aria-expanded={open}
                              onClick={() =>
                                setExpanded((current) => {
                                  const next = new Set(current);
                                  if (next.has(group.base_url)) next.delete(group.base_url);
                                  else next.add(group.base_url);
                                  return next;
                                })
                              }
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
                              <table className="ops-table">
                                <thead>
                                  <tr>
                                    <th>{t('common.userId')}</th>
                                    <th>{t('admin.endpoints.endpointCount')}</th>
                                    <th>{t('admin.endpoints.keys')}</th>
                                    <th>{t('admin.endpoints.enabledCount')}</th>
                                  </tr>
                                </thead>
                                <tbody>
                                  {group.users.map((user) => (
                                    <tr key={user.user_id}>
                                      <td>{user.user_id}</td>
                                      <td>{user.endpoint_count}</td>
                                      <td>{user.key_count}</td>
                                      <td>{user.enabled_count}</td>
                                    </tr>
                                  ))}
                                </tbody>
                              </table>
                            </td>
                          </tr>
                        ) : null}
                      </Fragment>
                    );
                  })}
                </tbody>
              </table>
            </div>
            <CursorPagination
              page={pager.page}
              nextCursor={result.data.next_cursor}
              onPrevious={pager.previous}
              onNext={pager.next}
            />
          </>
        )}
        <p className="inline-notice">{t('admin.endpoints.noProbeNotice')}</p>
      </Card>
    </div>
  );
}

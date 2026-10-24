import { useEffect, useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { clearStationSession } from '@shared/charityManagement';
import {
  endpointKeys,
  ENDPOINT_TAGS,
  getEndpointsPage,
  getEndpointUsersPage,
  setEndpointTags,
} from '@shared/operations/endpoints';
import type { ManagementRole } from '@shared/operations/managedUsers';
import { useSearchState } from '@shared/operations/useSearchState';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isStewardPermissionDenied, isUnauthorized } from '@shared/query/http';
import { formatCount } from '@shared/utils/formatNumber';
import { EmptyState, ErrorState, LoadingState, PageHeader, SectionHead } from './States';
import { StewardPermissionNotice } from './StewardPermissionNotice';
import { Button, DataTable, Field, FilterBar, Note, Panel, PanelBody } from './ui';
import './EndpointManagement.css';

const endpointTagKeys = {
  abusive_third_party: 'management.endpoints.tag.abusive_third_party',
  community_charity: 'management.endpoints.tag.community_charity',
  noncommercial_gateway: 'management.endpoints.tag.noncommercial_gateway',
  user_gateway: 'management.endpoints.tag.user_gateway',
  steward_gateway: 'management.endpoints.tag.steward_gateway',
  invalid_entry: 'management.endpoints.tag.invalid_entry',
} as const;
type Props = {
  role: ManagementRole;
  account: string;
  scopeReady: boolean;
  canRead?: boolean;
  canTag?: boolean;
  sessionError?: unknown;
  back?: ReactNode;
  embedded?: boolean;
};

function useEndpointAccessError(role: ManagementRole, error: unknown) {
  const client = useQueryClient();
  useEffect(() => {
    if (isUnauthorized(error)) clearStationSession(client, role);
    else if (role === 'steward' && isStewardPermissionDenied(error))
      void client.invalidateQueries({ queryKey: ['user', 'session'] });
  }, [client, role, error]);
}

function EndpointUsersPanel({
  role,
  account,
  baseURL,
}: {
  role: ManagementRole;
  account: string;
  baseURL: string;
}) {
  const { t } = useTranslation();
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'admin.endpoint-users',
    scopeKey: account,
    resetKey: baseURL,
    pageParam: 'endpoint_user_page',
    pageSizeParam: 'endpoint_user_page_size',
  });
  const result = useQuery({
    queryKey: endpointKeys.users(role, account, baseURL, pager.page, pager.pageSize),
    queryFn: ({ signal }) =>
      getEndpointUsersPage(role, baseURL, pager.page, pager.pageSize, signal),
    retry: false,
    placeholderData: (previous, query) =>
      query?.queryKey[3] === account && query.queryKey[4] === baseURL ? previous : undefined,
  });
  useEndpointAccessError(role, result.error);
  return (
    <div className="nb-stack" aria-busy={result.isFetching}>
      {result.isPending ? (
        <LoadingState />
      ) : isStewardPermissionDenied(result.error) ? (
        <StewardPermissionNotice />
      ) : result.error ? (
        <ErrorState error={result.error} onRetry={() => void result.refetch()} />
      ) : result.data.data.length === 0 ? (
        <EmptyState title={t('common.noResults')} body={t('common.noResultsBody')} />
      ) : (
        <DataTable
          caption={t('management.endpoints.users')}
          rows={result.data.data}
          rowKey={(user) => user.user_id}
          columns={[
            {
              key: 'user',
              header: t('common.userId'),
              cell: 'title',
              render: (user) => <span className="mono">{user.user_id}</span>,
            },
            ...(['endpoint_count', 'key_count', 'enabled_count'] as const).map((key, index) => ({
              key,
              header: t(
                [
                  'management.endpoints.endpointCount',
                  'management.endpoints.keys',
                  'management.endpoints.enabledCount',
                ][index],
              ),
              cell: 'meta' as const,
              align: 'num' as const,
              mobileLabel: t(
                [
                  'management.endpoints.endpointCount',
                  'management.endpoints.keys',
                  'management.endpoints.enabledCount',
                ][index],
              ),
              render: (user: {
                endpoint_count: string;
                key_count: string;
                enabled_count: string;
              }) => formatCount(user[key]).display,
            })),
          ]}
        />
      )}
      {!result.error && result.data ? (
        <PagePagination
          alwaysShowJump
          metadata={result.data.pagination}
          requestedPage={pager.page}
          busy={result.isFetching}
          onPageChange={pager.setPage}
          onPageSizeChange={pager.setPageSize}
        />
      ) : null}
    </div>
  );
}

export function EndpointManagement({
  role,
  account,
  scopeReady,
  canRead = true,
  canTag = true,
  sessionError,
  back,
  embedded = false,
}: Props) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [params, setParams] = useSearchState();
  const query = params.get('q') ?? '';
  const tag = params.get('tag') ?? '';
  const userId = params.get('user_id') ?? '';
  const expanded = params.get('expanded_base_url');
  const [draft, setDraft] = useState(query);
  const [userDraft, setUserDraft] = useState(userId);
  const [previousFilter, setPreviousFilter] = useState({ query, userId });
  if (previousFilter.query !== query || previousFilter.userId !== userId) {
    setPreviousFilter({ query, userId });
    setDraft(query);
    setUserDraft(userId);
  }
  const [queryError, setQueryError] = useState<string | null>(null);
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'admin.endpoints',
    scopeKey: account,
    scopeReady,
    resetKey: `${query}:${tag}:${userId}`,
  });
  const result = useQuery({
    queryKey: endpointKeys.list(role, account, query, pager.page, pager.pageSize, tag, userId),
    queryFn: ({ signal }) =>
      getEndpointsPage(role, query, pager.page, pager.pageSize, signal, tag, userId),
    retry: false,
    enabled: scopeReady && canRead,
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === account &&
      previousQuery.queryKey[4] === query &&
      previousQuery.queryKey[7] === tag &&
      previousQuery.queryKey[8] === userId
        ? previous
        : undefined,
  });
  const selectionScope = `${query}:${tag}:${userId}:${pager.page}:${pager.pageSize}`;
  const [selection, setSelection] = useState<{ scope: string; urls: string[] }>({
    scope: '',
    urls: [],
  });
  const selected = selection.scope === selectionScope ? selection.urls : [];
  const [editTag, setEditTag] = useState<string>(ENDPOINT_TAGS[0]);
  const tags = useMutation({
    mutationFn: (add: boolean) => setEndpointTags(role, selected, editTag, add),
    onSuccess: async () => {
      setSelection({ scope: '', urls: [] });
      await client.invalidateQueries({
        queryKey: [role === 'admin' ? 'admin' : 'user', 'operations', 'endpoints', account],
      });
    },
  });
  useEndpointAccessError(role, result.error ?? tags.error);
  const select = (urls: string[]) => setSelection({ scope: selectionScope, urls });
  const commitFilter = (nextQuery: string, nextUser: string, resetTags = false) => {
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (resetTags) next.delete('tag');
      if (nextQuery) next.set('q', nextQuery);
      else next.delete('q');
      if (nextUser) next.set('user_id', nextUser);
      else next.delete('user_id');
      next.set('page', '1');
      next.set('page_size', String(pager.pageSize));
      next.delete('expanded_base_url');
      next.delete('endpoint_user_page');
      return next;
    });
  };
  const toggleExpanded = (baseURL: string) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (result.data) next.set('page', result.data.pagination.page);
      if (next.get('expanded_base_url') === baseURL) next.delete('expanded_base_url');
      else next.set('expanded_base_url', baseURL);
      next.delete('endpoint_user_page');
      return next;
    });
  const header = embedded ? (
    <SectionHead
      title={t('management.endpoints.title')}
      description={t('management.endpoints.description')}
    />
  ) : (
    <PageHeader
      title={t('management.endpoints.title')}
      description={t('management.endpoints.description')}
      back={back}
    />
  );
  if (!canRead)
    return (
      <div className={embedded ? 'nb-stack' : 'page nb-stack'}>
        {header}
        <StewardPermissionNotice />
      </div>
    );
  return (
    <div
      className={embedded ? 'nb-stack endpoint-management' : 'page nb-stack endpoint-management'}
    >
      {header}
      <FilterBar
        secondaryLabel={t('common.filter')}
        activeCount={Number(Boolean(query)) + Number(Boolean(userId)) + Number(Boolean(tag))}
        onSubmit={() => {
          const bytes = new TextEncoder().encode(draft).byteLength;
          if (bytes > 512) {
            setQueryError(t('management.endpoints.queryTooLong', { bytes }));
            return;
          }
          setQueryError(null);
          commitFilter(draft, userDraft.trim());
        }}
        search={
          <>
            <Field label={t('management.endpoints.searchAria')} error={queryError ?? undefined}>
              {(field) => (
                <input
                  {...field}
                  type="search"
                  value={draft}
                  placeholder={t('management.endpoints.searchPlaceholder')}
                  onChange={(event) => {
                    setDraft(event.target.value);
                    if (new TextEncoder().encode(event.target.value).byteLength <= 512)
                      setQueryError(null);
                  }}
                />
              )}
            </Field>
            <Button type="submit">{t('common.applyFilter')}</Button>
            <Button
              type="button"
              onClick={() => {
                setDraft('');
                setUserDraft('');
                setQueryError(null);
                commitFilter('', '', true);
              }}
            >
              {t('common.resetFilter')}
            </Button>
          </>
        }
        secondaryAlwaysVisible
        secondary={
          <>
            <Field label={t('common.userId')}>
              {(field) => (
                <input
                  {...field}
                  type="text"
                  inputMode="numeric"
                  pattern="[1-9][0-9]*"
                  value={userDraft}
                  placeholder={t('management.endpoints.userPlaceholder')}
                  onChange={(event) => setUserDraft(event.target.value)}
                />
              )}
            </Field>
            <Field label={t('management.endpoints.tags')}>
              {(field) => (
                <select
                  {...field}
                  value={tag}
                  onChange={(event) => {
                    const value = event.target.value;
                    setParams((previous) => {
                      const next = new URLSearchParams(previous);
                      if (value) next.set('tag', value);
                      else next.delete('tag');
                      next.set('page', '1');
                      next.delete('expanded_base_url');
                      next.delete('endpoint_user_page');
                      return next;
                    });
                  }}
                >
                  <option value="">{t('management.endpoints.allTags')}</option>
                  <option value="untagged">{t('management.endpoints.untagged')}</option>
                  {ENDPOINT_TAGS.map((value) => (
                    <option key={value} value={value}>
                      {t(endpointTagKeys[value])}
                    </option>
                  ))}
                </select>
              )}
            </Field>
          </>
        }
      />
      <Note>{t('management.endpoints.noProbeNotice')}</Note>
      {canTag ? (
        <fieldset className="endpoint-batch" disabled={tags.isPending || result.isFetching}>
          <legend>{t('management.endpoints.selected', { count: selected.length })}</legend>
          <div className="nb-toolbar">
            <p className="nb-muted">{t('management.endpoints.selectForBatch')}</p>
            <Field label={t('management.endpoints.tags')}>
              {(field) => (
                <select
                  {...field}
                  value={editTag}
                  onChange={(event) => setEditTag(event.target.value)}
                >
                  {ENDPOINT_TAGS.map((value) => (
                    <option key={value} value={value}>
                      {t(endpointTagKeys[value])}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Button
              type="button"
              disabled={selected.length === 0}
              onClick={() => tags.mutate(true)}
            >
              {t('management.endpoints.addTag')}
            </Button>
            <Button
              type="button"
              disabled={selected.length === 0}
              onClick={() => tags.mutate(false)}
            >
              {t('management.endpoints.removeTag')}
            </Button>
          </div>
        </fieldset>
      ) : (
        <Note>{t('management.endpoints.readOnly')}</Note>
      )}
      {isStewardPermissionDenied(tags.error) ? (
        <StewardPermissionNotice />
      ) : tags.error ? (
        <ErrorState error={tags.error} />
      ) : null}
      {tags.isSuccess ? <Note tone="ok">{t('management.endpoints.tagsSaved')}</Note> : null}
      {sessionError ? <ErrorState error={sessionError} /> : null}
      {result.isPending ? (
        <LoadingState />
      ) : isStewardPermissionDenied(result.error) ? (
        <StewardPermissionNotice />
      ) : result.error ? (
        <ErrorState error={result.error} onRetry={() => void result.refetch()} />
      ) : result.data.data.length === 0 ? (
        <EmptyState title={t('common.noResults')} body={t('common.noResultsBody')} />
      ) : (
        <Panel>
          <PanelBody>
            <DataTable
              caption={t('management.endpoints.title')}
              rows={result.data.data}
              rowKey={(group) => group.base_url}
              columns={[
                ...(canTag
                  ? [
                      {
                        key: 'select',
                        header: (
                          <input
                            type="checkbox"
                            aria-label={t('management.endpoints.selectAll')}
                            checked={
                              result.data.data.length > 0 &&
                              result.data.data.every((group) => selected.includes(group.base_url))
                            }
                            disabled={tags.isPending || result.isFetching}
                            onChange={(event) =>
                              select(
                                event.target.checked
                                  ? result.data.data.map((group) => group.base_url)
                                  : [],
                              )
                            }
                          />
                        ),
                        cell: 'meta' as const,
                        render: (group: { base_url: string }) => (
                          <input
                            type="checkbox"
                            aria-label={t('management.endpoints.selectEndpoint', {
                              url: group.base_url,
                            })}
                            checked={selected.includes(group.base_url)}
                            disabled={tags.isPending || result.isFetching}
                            onChange={(event) =>
                              select(
                                event.target.checked
                                  ? [...selected, group.base_url]
                                  : selected.filter((url) => url !== group.base_url),
                              )
                            }
                          />
                        ),
                      },
                    ]
                  : []),
                {
                  key: 'url',
                  header: t('management.endpoints.baseUrl'),
                  cell: 'title',
                  render: (group) => (
                    <span className="mono endpoint-management-url">{group.base_url}</span>
                  ),
                },
                {
                  key: 'tags',
                  header: t('management.endpoints.tags'),
                  cell: 'status',
                  mobileLabel: t('management.endpoints.tags'),
                  render: (group) =>
                    group.tags.length
                      ? group.tags.map((value) => (
                          <span className="nb-badge" key={value}>
                            {endpointTagKeys[value as keyof typeof endpointTagKeys]
                              ? t(endpointTagKeys[value as keyof typeof endpointTagKeys])
                              : value}
                          </span>
                        ))
                      : t('management.endpoints.untagged'),
                },
                ...(['user_count', 'endpoint_count', 'key_count'] as const).map((key, index) => ({
                  key,
                  header: t(
                    [
                      'management.endpoints.users',
                      'management.endpoints.endpointCount',
                      'management.endpoints.keys',
                    ][index],
                  ),
                  cell: 'meta' as const,
                  align: 'num' as const,
                  mobileLabel: t(
                    [
                      'management.endpoints.users',
                      'management.endpoints.endpointCount',
                      'management.endpoints.keys',
                    ][index],
                  ),
                  render: (group: {
                    user_count: string;
                    endpoint_count: string;
                    key_count: string;
                  }) => formatCount(group[key]).display,
                })),
                {
                  key: 'expand',
                  header: t('common.workflow.actions'),
                  cell: 'action',
                  render: (group) => (
                    <Button
                      type="button"
                      aria-expanded={expanded === group.base_url}
                      onClick={() => toggleExpanded(group.base_url)}
                    >
                      {t(
                        expanded === group.base_url
                          ? 'management.endpoints.hideUsers'
                          : 'management.endpoints.showUsers',
                      )}
                    </Button>
                  ),
                },
              ]}
              renderDetail={(group) =>
                expanded === group.base_url ? (
                  <EndpointUsersPanel role={role} account={account} baseURL={group.base_url} />
                ) : null
              }
            />
          </PanelBody>
        </Panel>
      )}
      {!result.error && result.data ? (
        <PagePagination
          alwaysShowJump
          metadata={result.data.pagination}
          requestedPage={pager.page}
          busy={result.isFetching}
          onPageChange={pager.setPage}
          onPageSizeChange={pager.setPageSize}
        />
      ) : null}
      {userId ? <p className="nb-muted">{t('management.endpoints.userFilterHint')}</p> : null}
    </div>
  );
}

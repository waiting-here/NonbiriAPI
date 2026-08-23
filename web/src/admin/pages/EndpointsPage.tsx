import { useState } from 'react';
import { useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  Pagination,
  ReadOnlyValue,
} from '@shared/components/States';
import { formatCount } from '@shared/utils/formatNumber';
import { useAdminEndpointOverview } from '../data';

const PAGE_SIZE_OPTIONS = [10, 20, 50] as const;
const MAX_FILTER_LENGTH = 256;

/** Parse a positive integer page; anything else falls back to page 1. */
function parsePage(raw: string | null): number {
  const value = Number(raw);
  return Number.isSafeInteger(value) && value >= 1 ? value : 1;
}

/** Parse a page size; only the offered options are accepted. */
function parsePageSize(raw: string | null): number {
  const value = Number(raw);
  return PAGE_SIZE_OPTIONS.includes(value as (typeof PAGE_SIZE_OPTIONS)[number])
    ? value
    : PAGE_SIZE_OPTIONS[1];
}

export function EndpointsPage() {
  const { t } = useTranslation();
  const [searchParams, setSearchParams] = useSearchParams();

  // Page, page size, and filter live in the URL so a reload or shared link
  // restores the exact server-side view. The filter is a literal substring
  // evaluated by the server; the client never filters cross-user rows itself.
  const page = parsePage(searchParams.get('page'));
  const pageSize = parsePageSize(searchParams.get('page_size'));
  const filter = (searchParams.get('filter') ?? '').trim().slice(0, MAX_FILTER_LENGTH);

  const [draftFilter, setDraftFilter] = useState(filter);
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const overview = useAdminEndpointOverview(page, pageSize, filter);

  // A new server view replaces expanded rows and the draft filter; resetting
  // during render (not in an effect) keeps a single committed render.
  const viewSignature = `${page}|${pageSize}|${filter}`;
  const [lastView, setLastView] = useState(viewSignature);
  if (lastView !== viewSignature) {
    setLastView(viewSignature);
    setExpanded(new Set());
    setDraftFilter(filter);
  }

  const updateParams = (changes: { page?: number; pageSize?: number; filter?: string }) => {
    const next = new URLSearchParams(searchParams);
    const nextPage = changes.page ?? (changes.pageSize !== undefined || changes.filter !== undefined ? 1 : page);
    if (nextPage > 1) next.set('page', String(nextPage));
    else next.delete('page');
    if (changes.pageSize !== undefined && changes.pageSize !== PAGE_SIZE_OPTIONS[1]) {
      next.set('page_size', String(changes.pageSize));
    } else if (changes.pageSize !== undefined) {
      next.delete('page_size');
    }
    const nextFilter = changes.filter?.trim().slice(0, MAX_FILTER_LENGTH) ?? '';
    if (nextFilter) next.set('filter', nextFilter);
    else if (changes.filter !== undefined) next.delete('filter');
    setSearchParams(next, { preventScrollReset: true });
  };

  const toggleGroup = (baseUrl: string) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(baseUrl)) next.delete(baseUrl);
      else next.add(baseUrl);
      return next;
    });
  };

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('admin.endpoints.title')}
        description={t('admin.endpoints.description')}
      />
      <Card>
        <div className="card-title-row">
          <h2>{t('admin.endpoints.listTitle')}</h2>
          {overview.data ? (
            <span className="muted">{t('common.page', { page })}</span>
          ) : null}
        </div>
        <form
          className="filter-bar"
          role="search"
          onSubmit={(event) => {
            event.preventDefault();
            updateParams({ filter: draftFilter });
          }}
        >
          <label>
            <span>{t('common.filter')}</span>
            <input
              type="search"
              value={draftFilter}
              maxLength={MAX_FILTER_LENGTH}
              onChange={(event) => setDraftFilter(event.target.value)}
              placeholder={t('admin.endpoints.searchPlaceholder')}
              aria-label={t('admin.endpoints.searchAria')}
            />
          </label>
          <div className="filter-actions">
            <button type="submit" className="btn btn-quiet">
              {t('common.applyFilter')}
            </button>
            <button
              type="button"
              className="btn btn-link"
              onClick={() => {
                setDraftFilter('');
                updateParams({ filter: '' });
              }}
            >
              {t('common.resetFilter')}
            </button>
          </div>
        </form>
        {overview.isPending ? (
          <LoadingState />
        ) : overview.error ? (
          <ErrorState error={overview.error} onRetry={() => void overview.refetch()} />
        ) : overview.data.items.length === 0 ? (
          filter ? (
            <EmptyState title={t('common.noResults')} body={t('common.noResultsBody')} />
          ) : (
            <EmptyState title={t('admin.endpoints.empty')} body={t('admin.endpoints.emptyBody')} />
          )
        ) : (
          <>
            <div className="table-wrap">
              <table>
                <caption>{t('admin.endpoints.listTitle')}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t('admin.endpoints.baseUrl')}</th>
                    <th scope="col">{t('admin.endpoints.users')}</th>
                    <th scope="col">{t('admin.endpoints.endpointCount')}</th>
                    <th scope="col">{t('admin.endpoints.keys')}</th>
                    <th scope="col">{t('admin.endpoints.expand')}</th>
                  </tr>
                </thead>
                <tbody>
                  {overview.data.items.map((group, index) => {
                    const isExpanded = expanded.has(group.base_url);
                    const detailId = `endpoint-group-users-${index}`;
                    return (
                      <EndpointGroupRow
                        key={group.base_url}
                        group={group}
                        detailId={detailId}
                        isExpanded={isExpanded}
                        onToggle={() => toggleGroup(group.base_url)}
                      />
                    );
                  })}
                </tbody>
              </table>
            </div>
            <Pagination
              page={page}
              hasNext={overview.data.hasMore}
              onChange={(nextPage) => updateParams({ page: nextPage })}
              pageSize={pageSize}
              pageSizeOptions={PAGE_SIZE_OPTIONS}
              onPageSizeChange={(size) => updateParams({ pageSize: size })}
              onJumpToPage={(target) => updateParams({ page: target })}
            />
          </>
        )}
      </Card>
    </div>
  );
}

interface EndpointGroupRowProps {
  group: {
    base_url: string;
    user_count: number;
    endpoint_count: number;
    key_count: number;
    users: {
      user_id: string;
      endpoint_count: number;
      key_count: number;
      enabled_count: number;
    }[];
  };
  detailId: string;
  isExpanded: boolean;
  onToggle: () => void;
}

function EndpointGroupRow({ group, detailId, isExpanded, onToggle }: EndpointGroupRowProps) {
  const { t } = useTranslation();
  return (
    <>
      <tr>
        <td>
          <span className="mono">{group.base_url}</span>
        </td>
        <td>{formatCount(group.user_count).display}</td>
        <td>{formatCount(group.endpoint_count).display}</td>
        <td>{formatCount(group.key_count).display}</td>
        <td>
          <button
            type="button"
            className="btn btn-secondary row-toggle"
            aria-expanded={isExpanded}
            aria-controls={detailId}
            onClick={onToggle}
          >
            {isExpanded ? t('admin.endpoints.hideUsers') : t('admin.endpoints.showUsers')}
          </button>
        </td>
      </tr>
      {isExpanded ? (
        <tr className="detail-row">
          <td colSpan={5} id={detailId}>
            {group.users.length === 0 ? (
              <p className="table-note">{t('common.none')}</p>
            ) : (
              <table className="subtable">
                <caption className="sr-only">
                  {t('admin.endpoints.usersFor', { url: group.base_url })}
                </caption>
                <thead>
                  <tr>
                    <th scope="col">{t('common.userId')}</th>
                    <th scope="col">{t('admin.endpoints.endpointCount')}</th>
                    <th scope="col">{t('admin.endpoints.keys')}</th>
                    <th scope="col">{t('admin.endpoints.enabledCount')}</th>
                  </tr>
                </thead>
                <tbody>
                  {group.users.map((user) => (
                    <tr key={user.user_id}>
                      <td><ReadOnlyValue value={user.user_id} /></td>
                      <td>{formatCount(user.endpoint_count).display}</td>
                      <td>{formatCount(user.key_count).display}</td>
                      <td>{formatCount(user.enabled_count).display}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </td>
        </tr>
      ) : null}
    </>
  );
}

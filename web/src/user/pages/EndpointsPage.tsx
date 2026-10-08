import { useRegisteredCopy } from '@shared/i18n/useRegisteredCopy';
import { ExpandablePanel } from '@shared/components/ui/ExpandablePanel';
import { useResourceFilters, useResourceListScroll } from '../features/core/useResourceFilters';
import { ResourceFilterBar, FilteredResourceEmpty } from '../features/core/ResourceFilterControls';
import { useState } from 'react';
import { Link, useLocation, useParams, useSearchParams } from 'react-router';
import { PageHeader } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import {
  ConnectorLabel,
  CoreEmpty,
  CoreErrorPanel,
  CoreLoading,
  CoreUserGate,
} from '../features/core/components';
import { EndpointDetail } from '../features/core/EndpointDetail';
import { Quickstart } from '../features/core/Quickstart';
import { EndpointWizard } from '../features/core/EndpointWizard';
import { useCoreCopy } from '../features/core/copy';
import { CORE_ROUTE_PATHS } from '../features/core/descriptors';
import { useNumberedEndpoints } from '../features/core/numberedQueries';
import type { UserProfile } from '../features/core/types';
import '../features/core/core.css';

const pageCopyKeys = {
  'user.services.add': 'user.services.add',
  'user.services.charity': 'user.services.charity',
  'user.services.description': 'user.services.description',
  'user.services.emptyBody': 'user.services.emptyBody',
  'user.services.emptyTitle': 'user.services.emptyTitle',
} as const;

function EndpointList({ user }: { user: UserProfile }) {
  const { t } = useCoreCopy();
  const { t: ui } = useRegisteredCopy(pageCopyKeys);
  const [search, setSearch] = useSearchParams();
  const quickstart = search.get('quickstart') === '1';
  const location = useLocation();
  const filters = useResourceFilters('endpoints', user.id);
  const pager = useUrlPagePager({
    station: 'user',
    listType: 'endpoints',
    scopeKey: user.id,
    scopeReady: true,
    resetKey: filters.identity,
  });
  const { page, pageSize, setPage, setPageSize } = pager;
  const endpoints = useNumberedEndpoints(
    user.id,
    {
      page,
      pageSize,
    },
    Boolean(user.id),
    filters.filters,
  );
  const [creating, setCreating] = useState(false);

  const pageData = endpoints.data;
  useResourceListScroll(user.id, Boolean(pageData) && !endpoints.isFetching, !quickstart);
  const returnTo = pageData
    ? (() => {
        const params = new URLSearchParams(location.search);
        params.delete('page');
        params.set('page', pageData.pagination.page);
        params.delete('page_size');
        params.set('page_size', String(pageData.pagination.page_size));
        return `${location.pathname}?${params.toString()}`;
      })()
    : undefined;

  if (isForbidden(endpoints.error) || isUnauthorized(endpoints.error)) {
    return <CoreErrorPanel error={endpoints.error} onRetry={() => void endpoints.refetch()} />;
  }

  const start = () =>
    setSearch((previous) => {
      const next = new URLSearchParams(previous);
      next.set('quickstart', '1');
      return next;
    });
  const manualWizard = creating ? (
    <ExpandablePanel
      open
      onClose={() => setCreating(false)}
      title={t('endpoints.wizardTitle')}
      closeLabel={t('common.close')}
    >
      <EndpointWizard
        accountId={user.id}
        onClose={() => setCreating(false)}
        onCreated={() => void endpoints.refetch()}
      />
    </ExpandablePanel>
  ) : null;
  if (quickstart)
    return (
      <div className="page core-page services-quickstart-page">
        <Quickstart
          accountId={user.id}
          onManual={() => setCreating(true)}
          onClose={() =>
            setSearch((previous) => {
              const next = new URLSearchParams(previous);
              next.delete('quickstart');
              return next;
            })
          }
        />
        {manualWizard}
      </div>
    );
  return (
    <div className="page core-page core-stack services-page">
      <PageHeader
        icon="resources"
        title={t('endpoints.title')}
        description={ui('user.services.description')}
        actions={
          <button type="button" className="nb-btn nb-btn--primary" onClick={start}>
            ＋ {ui('user.services.add')}
          </button>
        }
      />
      {manualWizard}
      <section className="nb-panel" aria-busy={endpoints.isFetching}>
        <div className="nb-panel__body">
          <ResourceFilterBar control={filters} />
        </div>
        {endpoints.isPending && !pageData ? (
          <CoreLoading />
        ) : endpoints.error && !pageData ? (
          <CoreErrorPanel error={endpoints.error} onRetry={() => void endpoints.refetch()} />
        ) : pageData ? (
          <>
            {endpoints.error ? (
              <CoreErrorPanel
                compact
                error={endpoints.error}
                onRetry={() => void endpoints.refetch()}
              />
            ) : null}
            {pageData.data.length === 0 && filters.active ? (
              <FilteredResourceEmpty control={filters} />
            ) : pageData.data.length === 0 ? (
              <CoreEmpty
                title={ui('user.services.emptyTitle')}
                body={ui('user.services.emptyBody')}
                action={
                  <div className="nb-inline">
                    <button type="button" className="nb-btn nb-btn--primary" onClick={start}>
                      {ui('user.services.add')}
                    </button>
                    <Link className="nb-btn nb-btn--secondary" to="/charity">
                      {ui('user.services.charity')}
                    </Link>
                  </div>
                }
              />
            ) : (
              <ul className="nb-list services-list">
                {pageData.data.map((item) => {
                  const name =
                    item.note ||
                    (item.origin.kind === 'mainstream' ? item.origin.name : item.base_url);
                  return (
                    <li key={item.id}>
                      <Link
                        className="nb-row core-endpoint-card"
                        to={CORE_ROUTE_PATHS.endpointDetail(item.id)}
                        state={returnTo ? { ...location.state, returnTo } : undefined}
                      >
                        <span className="nb-row__icon" aria-hidden="true">
                          {Array.from(name)[0]?.toUpperCase()}
                        </span>
                        <span className="nb-row__main">
                          <span className="nb-row__title">
                            <strong title={name}>{name}</strong>
                            <span
                              className={`nb-badge nb-badge--${item.browse?.state === 'available' ? 'ok' : 'warn'}`}
                            >
                              {item.browse
                                ? t(`browse.endpoint.${item.browse.state}`)
                                : t('common.unknown')}
                            </span>
                          </span>
                          <span className="nb-row__sub nb-mono" title={item.base_url}>
                            {item.base_url}
                          </span>
                          <span className="nb-row__facts">
                            <span>
                              <ConnectorLabel value={item.connector_type} /> ·{' '}
                              {item.origin.kind === 'mainstream'
                                ? t('filters.mainstream')
                                : t('filters.custom')}
                            </span>
                            <span>{t('browse.keyCount', { count: item.key_count })}</span>
                            <span>
                              {t('browse.modelCount', { count: item.browse?.model_count ?? '—' })}
                            </span>
                          </span>
                        </span>
                        <span className="nb-row__end">
                          <span className="nb-btn nb-btn--secondary nb-btn--sm">
                            {t('endpoints.manage')}
                          </span>
                        </span>
                      </Link>
                    </li>
                  );
                })}
              </ul>
            )}
            <PagePagination
              metadata={pageData.pagination}
              requestedPage={pager.page}
              busy={endpoints.isFetching}
              onPageChange={setPage}
              onPageSizeChange={setPageSize}
            />
          </>
        ) : (
          <CoreLoading />
        )}
      </section>
    </div>
  );
}

export function EndpointsPage() {
  const { endpointId } = useParams<{ endpointId?: string }>();
  return (
    <CoreUserGate>
      {(user) =>
        endpointId ? (
          <EndpointDetail
            key={`${user.id}:${endpointId}`}
            accountId={user.id}
            endpointId={endpointId}
          />
        ) : (
          <EndpointList key={user.id} user={user} />
        )
      }
    </CoreUserGate>
  );
}

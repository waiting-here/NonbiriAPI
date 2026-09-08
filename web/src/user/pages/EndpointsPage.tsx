import { useState } from 'react';
import { Link, useLocation, useParams } from 'react-router';
import { PageHeader } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import {
  ConnectorLabel,
  CoreEmpty,
  CoreErrorPanel,
  CoreLoading,
  CoreTime,
  CoreUserGate,
  SafeCopyValue,
} from '../features/core/components';
import { EndpointDetail } from '../features/core/EndpointDetail';
import { EndpointBrowseSummary } from '../features/core/ResourceBrowse';
import { EndpointWizard } from '../features/core/EndpointWizard';
import { useCoreCopy } from '../features/core/copy';
import { CORE_ROUTE_PATHS } from '../features/core/descriptors';
import { useNumberedEndpoints } from '../features/core/numberedQueries';
import type { UserProfile } from '../features/core/types';
import '../features/core/core.css';

function EndpointList({ user }: { user: UserProfile }) {
  const { t } = useCoreCopy();
  const location = useLocation();
  const pager = useUrlPagePager({
    station: 'user',
    listType: 'endpoints',
    scopeKey: user.id,
    scopeReady: true,
  });
  const { page, pageSize, setPage, setPageSize } = pager;
  const endpoints = useNumberedEndpoints(
    user.id,
    {
      page,
      pageSize,
    },
    Boolean(user.id),
  );
  const [creating, setCreating] = useState(false);

  const pageData = endpoints.data;
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

  return (
    <div className="page core-page core-stack">
      <PageHeader
        icon="resources"
        title={t('endpoints.title')}
        description={t('endpoints.description')}
        actions={
          <button type="button" className="btn btn-primary" onClick={() => setCreating(true)}>
            {t('endpoints.create')}
          </button>
        }
      />

      {creating ? (
        <EndpointWizard
          accountId={user.id}
          onClose={() => setCreating(false)}
          onCreated={() => void endpoints.refetch()}
        />
      ) : null}

      {endpoints.isPending && !pageData ? (
        <CoreLoading />
      ) : endpoints.error && !pageData ? (
        <CoreErrorPanel error={endpoints.error} onRetry={() => void endpoints.refetch()} />
      ) : pageData ? (
        <section className="core-card" aria-busy={endpoints.isFetching}>
          {endpoints.error ? (
            <CoreErrorPanel
              compact
              error={endpoints.error}
              onRetry={() => void endpoints.refetch()}
            />
          ) : null}
          {pageData.data.length === 0 ? (
            <CoreEmpty
              title={t('endpoints.emptyTitle')}
              body={t('endpoints.emptyBody')}
              action={
                <button type="button" className="btn btn-primary" onClick={() => setCreating(true)}>
                  {t('endpoints.create')}
                </button>
              }
            />
          ) : (
            <ul className="core-endpoint-list">
              {pageData.data.map((endpoint) => (
                <li key={endpoint.id} className="core-endpoint-card">
                  <div className="core-endpoint-card__top">
                    <div>
                      <strong>
                        {endpoint.origin.kind === 'mainstream' ? (
                          t('endpoints.originMainstream', { name: endpoint.origin.name })
                        ) : (
                          <ConnectorLabel value={endpoint.connector_type} />
                        )}
                      </strong>
                    </div>
                  </div>
                  <EndpointBrowseSummary endpoint={endpoint} />
                  <dl className="core-detail-list">
                    {endpoint.origin.kind === 'mainstream' ? (
                      <div>
                        <dt>{t('endpoints.connector')}</dt>
                        <dd>
                          <ConnectorLabel value={endpoint.connector_type} />
                        </dd>
                      </div>
                    ) : null}
                    <div>
                      <dt>{t('endpoints.baseUrl')}</dt>
                      <dd>
                        <SafeCopyValue value={endpoint.base_url} label={t('endpoints.baseUrl')} />
                      </dd>
                    </div>
                    <div>
                      <dt>{t('endpoints.note')}</dt>
                      <dd>{endpoint.note || t('common.notSet')}</dd>
                    </div>
                    <div>
                      <dt>{t('common.updated')}</dt>
                      <dd>
                        <CoreTime value={endpoint.updated_at} />
                      </dd>
                    </div>
                  </dl>
                  <div className="core-row-actions">
                    <span />
                    <Link
                      className="btn btn-secondary"
                      to={CORE_ROUTE_PATHS.endpointDetail(endpoint.id)}
                      state={returnTo ? { returnTo } : undefined}
                    >
                      {t('endpoints.manage')}
                    </Link>
                  </div>
                </li>
              ))}
            </ul>
          )}
          <PagePagination
            metadata={pageData.pagination}
            requestedPage={pager.page}
            busy={endpoints.isFetching}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        </section>
      ) : (
        <CoreLoading />
      )}
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

import { useId, useState } from 'react';
import { Link, useLocation } from 'react-router';
import { useSearchState } from '@shared/operations/useSearchState';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { PageHeader } from '@shared/components/States';
import { Sheet } from '@shared/components/ui/Sheet';
import { Tabs } from '@shared/components/ui/Tabs';
import { Note } from '@shared/components/ui';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { EndpointDetail } from '../EndpointDetail';
import { getEndpoint } from '../api';
import { coreKeys } from '../queries';
import { formatCount } from '@shared/utils/formatNumber';
import { CoreErrorPanel, CoreLoading } from '../components';
import type { UserProfile } from '../types';
import { ConnectionResourceList, connectionKeys } from './ResourceList';
import { connectionAPI } from './api';
import { ServiceCreateForm } from './ServiceCreateForm';
import { ConnectionEditor } from './ConnectionEditor';
import { ConnectionSummary } from './ConnectionSummary';
import { CredentialsPanel } from './CredentialsPanel';
import { DiscoverySourceDetail } from './DiscoverySourceDetail';
import { SectionTabs } from '../../../components/SectionTabs';

type ConnectionView = 'entries' | 'credentials' | 'settings';

function clearDetailScope(params: URLSearchParams) {
  for (const field of [
    'connection_view',
    'endpoint_id',
    'entry_id',
    'key_id',
    'focus_key_id',
    'credential_id',
    'discovery_source_id',
    'quickstart',
  ])
    params.delete(field);
  for (const field of [...params.keys()])
    if (/^(keys_|manual_|routes_|impact_|source_models_|service-endpoint-switch_)/.test(field))
      params.delete(field);
}
function connectionURL(search: string, id: string) {
  const next = new URLSearchParams(search);
  clearDetailScope(next);
  next.set('connection_id', id);
  next.delete('quickstart');
  next.set('connection_view', 'entries');
  return '/endpoints?' + next.toString();
}
export function ConnectionsWorkspace({ user }: { user: UserProfile }) {
  const { t } = useTranslation();
  const location = useLocation();
  const [params, setParams] = useSearchState();
  const selected = params.get('connection_id');
  const [creating, setCreating] = useState(params.get('quickstart') === '1');
  const close = () => {
    setCreating(false);
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      next.delete('quickstart');
      return next;
    });
  };
  if (!selected && params.get('discovery_source_id'))
    return (
      <DiscoverySourceDetail
        accountId={user.id}
        id={params.get('discovery_source_id')!}
        onBack={() =>
          setParams((previous) => {
            const next = new URLSearchParams(previous);
            next.delete('discovery_source_id');
            return next;
          })
        }
      />
    );
  // Opening a service from the finished form ends the create task, so returning to the
  // list does not reopen the sheet.
  if (selected && creating) setCreating(false);
  if (selected)
    return <ConnectionDetail key={user.id + ':' + selected} accountId={user.id} id={selected} />;
  const createButton = (
    <button type="button" className="nb-btn nb-btn--primary" onClick={() => setCreating(true)}>
      {t('user.workflows.addService')}
    </button>
  );
  return (
    <div className="page core-page core-stack services-page">
      <PageHeader
        icon="resources"
        title={t('common.connections.title')}
        description={t('common.connections.intro')}
        actions={createButton}
      />
      <SectionTabs group="own" />
      <Sheet
        open={creating}
        size="lg"
        onClose={close}
        title={t('user.workflows.addService')}
        closeLabel={t('common.connections.close')}
      >
        <ServiceCreateForm accountId={user.id} />
      </Sheet>
      <ConnectionResourceList
        restoreScroll
        accountId={user.id}
        scope="all"
        listType="connections"
        title={t('common.connections.yourConnections')}
        searchPlaceholder={t('common.connections.connectionSearch')}
        load={connectionAPI.list}
        rowKey={(row) => row.id}
        emptyAction={createButton}
        columns={[
          {
            key: 'name',
            header: t('common.connections.name'),
            cell: 'title',
            render: (row) => (
              <ConnectionSummary
                connection={row}
                to={connectionURL(location.search, row.id)}
                showEntryCount={false}
              />
            ),
          },
          {
            key: 'entries',
            header: t('common.connections.entries'),
            mobileLabel: t('common.connections.entries'),
            cell: 'meta',
            align: 'num',
            render: (row) => formatCount(row.entry_count).display,
          },
          {
            key: 'credentials',
            header: t('common.connections.credentials'),
            mobileLabel: t('common.connections.credentials'),
            cell: 'meta',
            align: 'num',
            render: (row) => formatCount(row.credential_count).display,
          },
          {
            key: 'manage',
            header: t('common.connections.actions'),
            cell: 'action',
            render: (row) => (
              <Link
                className="nb-btn nb-btn--secondary"
                to={connectionURL(location.search, row.id)}
              >
                {t('common.connections.manage')}
              </Link>
            ),
          },
        ]}
      />
    </div>
  );
}

function ConnectionDetail({ accountId, id }: { accountId: string; id: string }) {
  const { t } = useTranslation();
  const [params, setParams] = useSearchState();
  const [adding, setAdding] = useState(false);
  const tabsID = useId();
  const requestedView = params.get('connection_view');
  // An open key detail (credential_id) keeps the key tab selected unless settings were chosen.
  const view: ConnectionView =
    requestedView === 'settings'
      ? 'settings'
      : requestedView === 'credentials' || params.get('credential_id')
        ? 'credentials'
        : 'entries';
  const setView = (next: ConnectionView) =>
    setParams((previous) => {
      const updated = new URLSearchParams(previous);
      updated.set('connection_view', next);
      if (next !== 'credentials') updated.delete('credential_id');
      return updated;
    });
  const connection = useQuery({
    queryKey: [...connectionKeys.root(accountId), 'connection', id],
    queryFn: ({ signal }) => connectionAPI.get(id, signal),
  });
  const backParams = new URLSearchParams(params);
  backParams.delete('connection_id');
  clearDetailScope(backParams);
  const back = '/endpoints' + (backParams.size ? '?' + backParams.toString() : '');
  if (connection.isPending)
    return (
      <div className="page core-page core-stack">
        <PageHeader
          title={t('common.connections.title')}
          back={<Link to={back}>{t('common.connections.backToConnections')}</Link>}
        />
        <CoreLoading />
      </div>
    );
  if (connection.error || !connection.data)
    return (
      <div className="page core-page core-stack">
        <PageHeader
          title={t('common.connections.title')}
          back={<Link to={back}>{t('common.connections.backToConnections')}</Link>}
        />
        <CoreErrorPanel error={connection.error} onRetry={() => void connection.refetch()} />
      </div>
    );
  const tabs = [
    { value: 'entries', label: t('common.connections.entries') },
    { value: 'credentials', label: t('common.connections.credentials') },
    { value: 'settings', label: t('common.connections.editConnection') },
  ] as const;
  return (
    <div className="page core-page core-stack services-detail">
      <PageHeader
        title={connection.data.display_name}
        description={connection.data.note}
        back={<Link to={back}>{t('common.connections.backToConnections')}</Link>}
        actions={
          <button type="button" className="nb-btn nb-btn--primary" onClick={() => setAdding(true)}>
            {t('common.connections.addEntry')}
          </button>
        }
      />
      <Tabs
        label={connection.data.display_name}
        value={view}
        onChange={setView}
        tabs={tabs.map((tab) => ({
          ...tab,
          id: tabsID + '-' + tab.value,
          panelId: tabsID + '-' + tab.value + '-panel',
        }))}
      />
      <div
        className="services-detail__panel"
        role="tabpanel"
        id={tabsID + '-' + view + '-panel'}
        aria-labelledby={tabsID + '-' + view}
      >
        {view === 'settings' ? (
          <section className="nb-panel services-settings">
            <ConnectionEditor
              accountId={accountId}
              connection={connection.data}
              onSaved={() => setView('entries')}
              onDeleted={() => setParams(backParams)}
            />
          </section>
        ) : view === 'credentials' ? (
          <div className="nb-stack">
            <Note>{t('user.services.serviceKeysHelp')}</Note>
            <CredentialsPanel accountId={accountId} connectionId={id} />
          </div>
        ) : (
          <ConnectionEndpoints accountId={accountId} connectionId={id} />
        )}
      </div>
      <Sheet
        open={adding}
        size="lg"
        onClose={() => setAdding(false)}
        title={t('common.connections.addEntry')}
        closeLabel={t('common.connections.close')}
      >
        <ServiceCreateForm accountId={accountId} connection={connection.data} />
      </Sheet>
    </div>
  );
}

function ConnectionEndpoints({
  accountId,
  connectionId,
}: {
  accountId: string;
  connectionId: string;
}) {
  const [modelsLocked, setModelsLocked] = useState(false);
  const { t } = useTranslation();
  const [params, setParams] = useSearchState();
  const pager = useUrlPagePager({
    station: 'user',
    listType: 'service-endpoint-switch',
    scopeKey: accountId + ':' + connectionId,
    pageParam: 'service-endpoint-switch_page',
    pageSizeParam: 'service-endpoint-switch_page_size',
  });
  const query = useQuery({
    queryKey: [
      ...connectionKeys.root(accountId),
      connectionId,
      'endpoint-switch',
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) => connectionAPI.entries(connectionId, pager, '', signal),
  });
  const requested = params.get('endpoint_id') || params.get('entry_id');
  const requestedEntry = useQuery({
    queryKey: coreKeys.endpoint(accountId, requested || ''),
    queryFn: ({ signal }) => getEndpoint(requested!, signal),
    enabled: Boolean(requested) && !query.data?.data.some((row) => row.id === requested),
  });
  const selected = requested
    ? (query.data?.data.find((row) => row.id === requested) ?? requestedEntry.data)
    : query.data?.data[0];
  const endpointId = selected?.connection_id === connectionId ? selected.id : undefined;
  return (
    <div className="nb-stack">
      {query.isPending ? (
        <CoreLoading />
      ) : query.error ? (
        <CoreErrorPanel error={query.error} onRetry={() => void query.refetch()} />
      ) : query.data ? (
        <>
          {query.data.pagination.total_items !== '1' && (query.data.data.length > 0 || selected) ? (
            <>
              <div
                className="services-entry-list"
                role="group"
                aria-label={t('common.connections.entries')}
              >
                {/* An entry opened from a link stays listed even when it is on another page. */}
                {[
                  ...(selected && !query.data.data.some((row) => row.id === selected.id)
                    ? [selected]
                    : []),
                  ...query.data.data,
                ].map((row) => (
                  <button
                    key={row.id}
                    type="button"
                    className="services-entry"
                    data-entry-id={row.id}
                    aria-pressed={row.id === selected?.id}
                    disabled={modelsLocked}
                    onClick={() =>
                      setParams((previous) => {
                        const next = new URLSearchParams(previous);
                        next.set('endpoint_id', row.id);
                        next.delete('key_id');
                        next.delete('focus_key_id');
                        for (const field of [...next.keys()])
                          if (/^(keys_|manual_|routes_)/.test(field)) next.delete(field);
                        return next;
                      })
                    }
                  >
                    <strong>{row.note || row.base_url}</strong>
                    <span className="mono">{row.base_url}</span>
                  </button>
                ))}
              </div>
              <PagePagination
                metadata={query.data.pagination}
                requestedPage={pager.page}
                busy={modelsLocked || query.isFetching}
                onPageChange={pager.setPage}
                onPageSizeChange={pager.setPageSize}
              />
            </>
          ) : null}
          {endpointId ? (
            <EndpointDetail
              key={endpointId}
              accountId={accountId}
              endpointId={endpointId}
              embedded
              onModelsLockChange={setModelsLocked}
            />
          ) : requestedEntry.isFetching ? (
            <CoreLoading />
          ) : requestedEntry.error ? (
            <CoreErrorPanel
              error={requestedEntry.error}
              onRetry={() => void requestedEntry.refetch()}
            />
          ) : (
            <Note>{t('user.services.noEndpoints')}</Note>
          )}
        </>
      ) : null}
    </div>
  );
}

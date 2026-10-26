import { useState } from 'react';
import { Link, useLocation } from 'react-router';
import { useSearchState } from '@shared/operations/useSearchState';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { PageHeader } from '@shared/components/States';
import { ExpandablePanel } from '@shared/components/ui/ExpandablePanel';
import { Field, Fold, Note } from '@shared/components/ui';
import { PagePagination } from '@shared/operations/PagePagination';
import { usePagePager } from '@shared/operations/usePagePager';
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
    if (/^(keys_|manual_|routes_|impact_|source_models_)/.test(field)) params.delete(field);
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
      {creating ? (
        <ExpandablePanel
          open
          onClose={close}
          title={t('user.workflows.addService')}
          closeLabel={t('common.connections.close')}
        >
          <ServiceCreateForm accountId={user.id} />
        </ExpandablePanel>
      ) : null}
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
  const [editing, setEditing] = useState(false);
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
  return (
    <div className="page core-page core-stack services-detail">
      <PageHeader
        title={connection.data.display_name}
        description={connection.data.note}
        back={<Link to={back}>{t('common.connections.backToConnections')}</Link>}
        actions={
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            onClick={() => setEditing((value) => !value)}
          >
            {t('common.connections.editConnection')}
          </button>
        }
      />
      {editing ? (
        <ExpandablePanel
          open
          onClose={() => setEditing(false)}
          title={t('common.connections.editConnection')}
          closeLabel={t('common.connections.close')}
        >
          <ConnectionEditor
            accountId={accountId}
            connection={connection.data}
            onSaved={() => setEditing(false)}
            onDeleted={() => setParams(backParams)}
          />
        </ExpandablePanel>
      ) : null}
      <div className="nb-actions">
        <button type="button" className="nb-btn nb-btn--secondary" onClick={() => setAdding(true)}>
          {t('common.connections.addEntry')}
        </button>
      </div>
      {adding ? (
        <ExpandablePanel
          open
          onClose={() => setAdding(false)}
          title={t('common.connections.addEntry')}
          closeLabel={t('common.connections.close')}
        >
          <ServiceCreateForm accountId={accountId} connection={connection.data} />
        </ExpandablePanel>
      ) : null}
      <ConnectionEndpoints accountId={accountId} connectionId={id} />
      <Fold
        lazy
        title={t('user.services.manageServiceKeys')}
        defaultOpen={
          params.get('connection_view') === 'credentials' || Boolean(params.get('credential_id'))
        }
      >
        <Note>{t('user.services.serviceKeysHelp')}</Note>
        <CredentialsPanel accountId={accountId} connectionId={id} />
      </Fold>
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
  const pager = usePagePager({
    station: 'user',
    listType: 'service-endpoint-switch',
    scopeKey: accountId + ':' + connectionId,
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
          {query.data.pagination.total_items !== '1' ? (
            <>
              <Field label={t('common.connections.entry')}>
                {(props) => (
                  <select
                    {...props}
                    disabled={modelsLocked}
                    value={selected?.id ?? ''}
                    onChange={(event) =>
                      setParams((previous) => {
                        const next = new URLSearchParams(previous);
                        next.set('endpoint_id', event.target.value);
                        next.delete('key_id');
                        next.delete('focus_key_id');
                        return next;
                      })
                    }
                  >
                    {selected && !query.data.data.some((row) => row.id === selected.id) ? (
                      <option value={selected.id}>
                        {selected.note || selected.base_url} · {selected.base_url}
                      </option>
                    ) : null}
                    {query.data.data.map((row) => (
                      <option key={row.id} value={row.id}>
                        {row.note || row.base_url} · {row.base_url}
                      </option>
                    ))}
                  </select>
                )}
              </Field>
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

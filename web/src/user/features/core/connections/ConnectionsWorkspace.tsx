import { useState } from 'react';
import { Link, useLocation, useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { PageHeader } from '@shared/components/States';
import { ExpandablePanel } from '@shared/components/ui/ExpandablePanel';
import { Tabs } from '@shared/components/ui';
import { formatCount } from '@shared/utils/formatNumber';
import { ConnectorLabel, CoreErrorPanel, CoreLoading } from '../components';
import type { UserProfile } from '../types';
import { ConnectionResourceList, connectionKeys } from './ResourceList';
import { connectionAPI } from './api';
import { ConnectionCreateForm } from './ConnectionForm';
import { ConnectionEditor } from './ConnectionEditor';
import { EntryForm } from './EntryForm';
import { CredentialsPanel } from './CredentialsPanel';
import { DiscoveryPanel } from './DiscoveryPanel';
import { DiscoverySourceDetail } from './DiscoverySourceDetail';

type View = 'entries' | 'credentials' | 'discovery';
function connectionURL(search: string, id: string) {
  const next = new URLSearchParams(search);
  next.set('connection_id', id);
  next.delete('quickstart');
  next.set('connection_view', 'entries');
  return '/endpoints?' + next.toString();
}
export function ConnectionsWorkspace({ user }: { user: UserProfile }) {
  const { t } = useTranslation();
  const location = useLocation();
  const [params, setParams] = useSearchParams();
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
      {t('common.connections.addConnection')}
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
          title={t('common.connections.addConnection')}
          closeLabel={t('common.connections.close')}
        >
          <ConnectionCreateForm
            accountId={user.id}
            onSaved={(value) => {
              setCreating(false);
              setParams(
                new URLSearchParams(connectionURL(location.search, value.id).split('?')[1]),
              );
            }}
          />
        </ExpandablePanel>
      ) : null}
      <ConnectionResourceList
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
              <Link to={connectionURL(location.search, row.id)}>
                <strong>{row.name}</strong>
              </Link>
            ),
          },
          {
            key: 'entries',
            header: t('common.connections.entries'),
            cell: 'meta',
            align: 'num',
            render: (row) => formatCount(row.entry_count).display,
          },
          {
            key: 'credentials',
            header: t('common.connections.credentials'),
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
  const client = useQueryClient();
  const { t } = useTranslation();
  const location = useLocation();
  const [params, setParams] = useSearchParams();
  const requestedView = params.get('connection_view');
  const view: View =
    requestedView === 'credentials' || requestedView === 'discovery' ? requestedView : 'entries';
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState(false);
  const connection = useQuery({
    queryKey: [...connectionKeys.root(accountId), 'connection', id],
    queryFn: ({ signal }) => connectionAPI.get(id, signal),
  });
  const backParams = new URLSearchParams(params);
  backParams.delete('connection_id');
  backParams.delete('connection_view');
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
        title={connection.data.name}
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
      <Tabs
        label={t('common.connections.sections')}
        value={view}
        tabs={[
          { value: 'entries', label: t('common.connections.entries') },
          { value: 'credentials', label: t('common.connections.credentials') },
          { value: 'discovery', label: t('common.connections.discoverySources') },
        ]}
        onChange={(value) => {
          setAdding(false);
          setParams((previous) => {
            const next = new URLSearchParams(previous);
            next.set('connection_view', value);
            return next;
          });
        }}
      />
      {view === 'entries' ? (
        <>
          {adding ? (
            <ExpandablePanel
              open
              onClose={() => {
                setAdding(false);
                void client.invalidateQueries({ queryKey: connectionKeys.root(accountId) });
              }}
              title={t('common.connections.addEntry')}
              closeLabel={t('common.connections.close')}
            >
              <EntryForm accountId={accountId} connectionId={id} />
            </ExpandablePanel>
          ) : null}
          <ConnectionResourceList
            accountId={accountId}
            scope={id}
            listType="connection-entries"
            title={t('common.connections.entries')}
            actions={
              <button
                type="button"
                className="nb-btn nb-btn--primary"
                onClick={() => setAdding(true)}
              >
                {t('common.connections.addEntry')}
              </button>
            }
            emptyAction={
              <button
                type="button"
                className="nb-btn nb-btn--secondary"
                onClick={() => setAdding(true)}
              >
                {t('common.connections.addEntry')}
              </button>
            }
            searchPlaceholder={t('common.connections.entrySearch')}
            load={(window, q, signal) => connectionAPI.entries(id, window, q, signal)}
            rowKey={(row) => row.id}
            columns={[
              {
                key: 'name',
                header: t('common.connections.entry'),
                cell: 'title',
                render: (row) => (
                  <div className="nb-stack nb-stack--sm">
                    <strong>{row.note || row.base_url}</strong>
                    <small className="nb-mono" title={row.base_url}>
                      {row.base_url}
                    </small>
                  </div>
                ),
              },
              {
                key: 'protocol',
                header: t('common.connections.protocol'),
                cell: 'meta',
                render: (row) => (
                  <ConnectorLabel value={row.configuration_type ?? row.connector_type} />
                ),
              },
              {
                key: 'state',
                header: t('common.connections.status'),
                cell: 'status',
                render: (row) => (
                  <span className={'nb-badge nb-badge--' + (row.enabled ? 'ok' : 'warn')}>
                    {row.enabled
                      ? t('common.connections.enabled')
                      : t('common.connections.disabled')}
                  </span>
                ),
              },
              {
                key: 'manage',
                header: t('common.connections.actions'),
                cell: 'action',
                render: (row) => (
                  <Link
                    className="nb-btn nb-btn--secondary"
                    to={'/endpoints/' + encodeURIComponent(row.id)}
                    state={{ returnTo: location.pathname + location.search }}
                  >
                    {t('common.connections.manageEntry')}
                  </Link>
                ),
              },
            ]}
          />
        </>
      ) : view === 'credentials' ? (
        <CredentialsPanel accountId={accountId} connectionId={id} />
      ) : (
        <DiscoveryPanel accountId={accountId} connectionId={id} />
      )}
    </div>
  );
}

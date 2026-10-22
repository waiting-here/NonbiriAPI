import { useState } from 'react';
import { Link, useLocation, useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import { ExpandablePanel } from '@shared/components/ui/ExpandablePanel';
import { connectionAPI } from './api';
import { ConnectionResourceList } from './ResourceList';
import { SourceForm } from './SourceForm';
import { DiscoverySourceDetail } from './DiscoverySourceDetail';

export function DiscoveryPanel({
  accountId,
  connectionId,
}: {
  accountId: string;
  connectionId: string;
}) {
  const { t } = useTranslation();
  const location = useLocation();
  const [params, setParams] = useSearchParams();
  const [adding, setAdding] = useState(false);
  const selected = params.get('discovery_source_id');
  const select = (id: string) => {
    const next = new URLSearchParams(params);
    next.set('discovery_source_id', id);
    return location.pathname + '?' + next.toString();
  };
  if (selected)
    return (
      <DiscoverySourceDetail
        accountId={accountId}
        id={selected}
        onBack={() =>
          setParams((previous) => {
            const next = new URLSearchParams(previous);
            next.delete('discovery_source_id');
            return next;
          })
        }
      />
    );
  return (
    <div className="nb-stack">
      {adding ? (
        <ExpandablePanel
          open
          title={t('common.connections.addSource')}
          closeLabel={t('common.connections.close')}
          onClose={() => setAdding(false)}
        >
          <SourceForm
            accountId={accountId}
            connectionId={connectionId}
            onSaved={(source) => {
              setAdding(false);
              setParams((previous) => {
                const next = new URLSearchParams(previous);
                next.set('discovery_source_id', source.id);
                return next;
              });
            }}
          />
        </ExpandablePanel>
      ) : null}
      <ConnectionResourceList
        accountId={accountId}
        scope={connectionId}
        listType="discovery-sources"
        title={t('common.connections.discoverySources')}
        searchPlaceholder={t('common.connections.sourceSearch')}
        load={(window, q, signal) => connectionAPI.sources(connectionId, window, q, signal)}
        rowKey={(row) => row.id}
        actions={
          <button type="button" className="nb-btn nb-btn--primary" onClick={() => setAdding(true)}>
            {t('common.connections.addSource')}
          </button>
        }
        columns={[
          {
            key: 'name',
            header: t('common.connections.name'),
            cell: 'title',
            render: (row) => (
              <Link to={select(row.id)}>
                <strong>{row.name || row.base_url}</strong>
              </Link>
            ),
          },
          {
            key: 'url',
            header: t('common.connections.modelsURL'),
            cell: 'meta',
            render: (row) => (
              <span className="nb-mono" title={row.models_url}>
                {row.models_url}
              </span>
            ),
          },
          {
            key: 'state',
            header: t('common.connections.status'),
            cell: 'status',
            render: (row) =>
              row.credential_available === false
                ? t('common.connections.credentialUnavailable')
                : t(`common.connections.sourceStates.${row.state}`),
          },
          {
            key: 'manage',
            header: t('common.connections.actions'),
            cell: 'action',
            render: (row) => (
              <Link className="nb-btn nb-btn--secondary" to={select(row.id)}>
                {t('common.connections.manage')}
              </Link>
            ),
          },
        ]}
      />
    </div>
  );
}

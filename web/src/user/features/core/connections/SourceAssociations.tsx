import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Fold, Note } from '@shared/components/ui';
import { NativeServicesSummary } from '@shared/components/ServiceRoutesSummary';
import { listEndpointKeysPage } from '../pageApi';
import { readEndpointKey } from '../resourceOperation';
import type { Endpoint, EndpointKey } from '../types';
import { ConnectionResourceList } from './ResourceList';
import { ConnectionSummary } from './ConnectionSummary';
import { connectionAPI, resourcePath } from './api';
import { useMetadataOperation } from './useMetadataOperation';
import { ConnectionOperationNotice } from './OperationNotice';
import type { DiscoverySource } from './types';

export function SourceAssociations({
  accountId,
  source,
}: {
  accountId: string;
  source: DiscoverySource;
}) {
  const { t } = useTranslation();
  const [entry, setEntry] = useState<Endpoint | null>(null);
  const [connectionId, setConnectionId] = useState(source.connection_id ?? '');
  return (
    <div className="nb-stack">
      <Note>{t('common.connections.sourceAssociationNote')}</Note>
      <Fold title={t('common.connections.chooseOtherConnection')} defaultOpen={!connectionId}>
        <ConnectionResourceList
          embedded
          accountId={accountId}
          scope="all"
          listType="source-connections"
          title={t('common.connections.title')}
          searchPlaceholder={t('common.connections.connectionSearch')}
          load={connectionAPI.list}
          rowKey={(row) => row.id}
          columns={[
            {
              key: 'name',
              cell: 'title',
              header: t('common.connections.name'),
              render: (row) => <ConnectionSummary connection={row} />,
            },
            {
              key: 'select',
              header: t('common.connections.actions'),
              cell: 'action',
              render: (row) => (
                <button
                  type="button"
                  className="nb-btn nb-btn--secondary"
                  onClick={() => {
                    setConnectionId(row.id);
                    setEntry(null);
                  }}
                >
                  {t('common.connections.select')}
                </button>
              ),
            },
          ]}
        />
      </Fold>
      {!entry && connectionId ? (
        <ConnectionResourceList
          embedded
          accountId={accountId}
          scope={connectionId}
          listType="source-entries"
          title={t('common.connections.chooseEntry')}
          searchPlaceholder={t('common.connections.entrySearch')}
          load={(window, q, signal) => connectionAPI.entries(connectionId, window, q, signal)}
          rowKey={(row) => row.id}
          columns={[
            {
              key: 'name',
              header: t('common.connections.entry'),
              cell: 'title',
              render: (row) => row.note || row.base_url,
            },
            {
              key: 'pick',
              header: t('common.connections.actions'),
              cell: 'action',
              render: (row) => (
                <button
                  type="button"
                  className="nb-btn nb-btn--secondary"
                  onClick={() => setEntry(row)}
                >
                  {t('common.connections.select')}
                </button>
              ),
            },
          ]}
        />
      ) : entry ? (
        <>
          <button type="button" className="nb-btn nb-btn--secondary" onClick={() => setEntry(null)}>
            {t('common.connections.changeEntry')}
          </button>
          <ConnectionResourceList
            embedded
            accountId={accountId}
            scope={entry.id}
            listType="source-entry-keys"
            title={entry.note || entry.base_url}
            searchPlaceholder={t('common.connections.credentialSearch')}
            load={(window, q, signal) => listEndpointKeysPage(entry.id, window, signal, q)}
            rowKey={(row) => row.id}
            columns={[
              {
                key: 'name',
                header: t('common.connections.credential'),
                cell: 'title',
                render: (row) => (
                  <div>
                    <strong>{row.note || row.display_head + '…' + row.display_tail}</strong>
                    <NativeServicesSummary value={row.native_services ?? []} />
                  </div>
                ),
              },
              {
                key: 'associate',
                header: t('common.connections.actions'),
                cell: 'action',
                render: (row) => (
                  <AssociateKey accountId={accountId} source={source} entryKey={row} />
                ),
              },
            ]}
          />
        </>
      ) : null}
    </div>
  );
}
function AssociateKey({
  accountId,
  source,
  entryKey,
}: {
  accountId: string;
  source: DiscoverySource;
  entryKey: EndpointKey;
}) {
  const { t } = useTranslation();
  const operation = useMetadataOperation(
    accountId,
    '/api/endpoints/' + resourcePath(entryKey.endpoint_id) + '/keys/' + resourcePath(entryKey.id),
    'PATCH',
    (signal) => readEndpointKey(entryKey.endpoint_id, entryKey.id, signal),
    () => undefined,
  );
  const compatible =
    source.enabled &&
    source.credential_available !== false &&
    entryKey.credential_available !== false &&
    entryKey.credential_id === source.credential_id &&
    entryKey.credential_version === source.credential_version;
  const associated = entryKey.discovery_source_id === source.id;
  return (
    <div className="nb-stack nb-stack--sm">
      <button
        type="button"
        className="nb-btn nb-btn--secondary"
        disabled={
          (!associated && !compatible) || operation.isPending || operation.outcome === 'unknown'
        }
        onClick={() =>
          void operation
            .mutateAsync({
              discovery_source_id: associated ? null : source.id,
              expected_revision: entryKey.revision,
            })
            .catch(() => undefined)
        }
      >
        {associated
          ? t('common.connections.detachSource')
          : !compatible
            ? t('common.connections.differentVersion')
            : t('common.connections.useSource')}
      </button>
      <ConnectionOperationNotice operation={operation} />
    </div>
  );
}

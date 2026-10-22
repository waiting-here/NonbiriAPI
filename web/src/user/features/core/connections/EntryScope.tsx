import { useState } from 'react';
import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Fold, Note } from '@shared/components/ui';
import { NativeServicesField } from '@shared/components/NativeServicesField';
import { NativeServicesSummary } from '@shared/components/ServiceRoutesSummary';
import { useEndpointCreateOptions } from '../queries';
import { getEndpoint } from '../api';
import { readEndpointKey } from '../resourceOperation';
import type { Endpoint, EndpointKey } from '../types';
import type { NativeService, RerankFormat } from '@shared/nativeServices';
import { CredentialPicker, credentialLabel } from './CredentialPicker';
import { connectionAPI, resourcePath } from './api';
import { useMetadataOperation } from './useMetadataOperation';
import { ConnectionOperationNotice } from './OperationNotice';
import { ConnectionResourceList } from './ResourceList';
import type { Credential, DiscoverySource } from './types';

export function EntryScope(props: { accountId: string; endpoint: Endpoint }) {
  const { t } = useTranslation();
  return (
    <Fold lazy title={t('common.connections.entryScope')}>
      <EntryScopeEditor {...props} />
    </Fold>
  );
}
function EntryScopeEditor({ accountId, endpoint }: { accountId: string; endpoint: Endpoint }) {
  const { t } = useTranslation();
  const options = useEndpointCreateOptions(accountId);
  const allowed = options.data?.native_service_options?.find(
    (row) => row.connector_type === endpoint.connector_type,
  );
  const [services, setServices] = useState<NativeService[]>(endpoint.native_services ?? []);
  const [format, setFormat] = useState<RerankFormat | null>(endpoint.rerank_format || null);
  const [group, setGroup] = useState<string | null>(null);
  const [expectedRevision, setExpectedRevision] = useState(endpoint.revision);
  const operation = useMetadataOperation(
    accountId,
    '/api/endpoints/' + resourcePath(endpoint.id),
    'PATCH',
    (signal) => getEndpoint(endpoint.id, signal),
    () => undefined,
    (saved) => {
      setExpectedRevision(saved.revision);
      setServices(saved.native_services ?? []);
      setFormat(saved.rerank_format || null);
      setGroup(null);
    },
  );
  const locked =
    operation.isPending ||
    operation.outcome === 'unknown' ||
    operation.outcome === 'refresh-failed';
  return (
    <div className="nb-stack">
      <form
        className="nb-form"
        onSubmit={(event) => {
          event.preventDefault();
          void operation
            .mutateAsync({
              native_services: services,
              rerank_format: format ?? '',
              ...(group ? { connection_id: group } : {}),
              expected_revision: expectedRevision,
            })
            .catch(() => undefined);
        }}
      >
        <NativeServicesSummary value={endpoint.native_services ?? []} />
        {endpoint.connection_id ? (
          <Link to={'/endpoints?connection_id=' + encodeURIComponent(endpoint.connection_id)}>
            {t('common.connections.backToConnection')}
          </Link>
        ) : null}
        <NativeServicesField
          options={allowed?.native_services ?? []}
          value={services}
          onChange={setServices}
          rerankFormats={allowed?.rerank_formats}
          rerankFormat={format}
          onRerankFormatChange={setFormat}
          disabled={locked}
        />
        <Fold lazy title={t('common.connections.moveConnection')}>
          <ConnectionResourceList
            embedded
            accountId={accountId}
            scope={endpoint.id}
            listType="move-connection"
            title={t('common.connections.yourConnections')}
            searchPlaceholder={t('common.connections.connectionSearch')}
            load={connectionAPI.list}
            rowKey={(row) => row.id}
            columns={[
              {
                key: 'name',
                header: t('common.connections.name'),
                cell: 'title',
                render: (row) => row.name,
              },
              {
                key: 'select',
                header: t('common.connections.actions'),
                cell: 'action',
                render: (row) => (
                  <button
                    type="button"
                    className="nb-btn nb-btn--secondary"
                    disabled={locked || group === row.id || endpoint.connection_id === row.id}
                    onClick={() => setGroup(row.id)}
                  >
                    {group === row.id
                      ? t('common.connections.selected')
                      : t('common.connections.select')}
                  </button>
                ),
              },
            ]}
          />
        </Fold>
        <ConnectionOperationNotice operation={operation} />
        {operation.outcome === 'conflict' ? (
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            onClick={() => {
              setExpectedRevision(endpoint.revision);
              operation.reset();
            }}
          >
            {t('common.connections.useCurrentRevision')}
          </button>
        ) : null}
        <button
          type="submit"
          className="nb-btn nb-btn--primary"
          disabled={locked || !services.length || operation.outcome === 'conflict'}
        >
          {t('common.connections.save')}
        </button>
      </form>
    </div>
  );
}
export function KeyCredentialScope({
  accountId,
  endpoint,
  entryKey,
}: {
  accountId: string;
  endpoint: Endpoint;
  entryKey: EndpointKey;
}) {
  const { t } = useTranslation();
  const [credential, setCredential] = useState<Credential | null>(null);
  const [services, setServices] = useState(entryKey.native_services ?? []);
  const [source, setSource] = useState<DiscoverySource | null>(null);
  const [sourceChanged, setSourceChanged] = useState(false);
  const [expectedRevision, setExpectedRevision] = useState(entryKey.revision);
  const operation = useMetadataOperation(
    accountId,
    '/api/endpoints/' + resourcePath(endpoint.id) + '/keys/' + resourcePath(entryKey.id),
    'PATCH',
    (signal) => readEndpointKey(endpoint.id, entryKey.id, signal),
    () => undefined,
    (saved) => {
      setExpectedRevision(saved.revision);
      setServices(saved.native_services ?? []);
      setCredential(null);
      setSource(null);
      setSourceChanged(false);
    },
  );
  const locked =
    operation.isPending ||
    operation.outcome === 'unknown' ||
    operation.outcome === 'refresh-failed';
  const credentialId = credential?.id ?? entryKey.credential_id;
  const version = credential?.credential_version ?? entryKey.credential_version;
  return (
    <Fold lazy title={t('common.connections.credentialAndServices')}>
      <form
        className="nb-form"
        onSubmit={(event) => {
          event.preventDefault();
          void operation
            .mutateAsync({
              native_services: services,
              ...(credential ? { credential_id: credential.id } : {}),
              ...(sourceChanged ? { discovery_source_id: source?.id ?? null } : {}),
              expected_revision: expectedRevision,
            })
            .catch(() => undefined);
        }}
      >
        <Note>
          {entryKey.credential_available === false ? (
            <p>{t('common.connections.credentialUnavailable')}</p>
          ) : null}
          <p>
            {credential
              ? credentialLabel(credential)
              : entryKey.display_head + '…' + entryKey.display_tail}{' '}
            · {t('common.connections.versionNumber', { version })}
          </p>
          <p>
            {t('common.connections.grantRevision', { revision: entryKey.grant_revision ?? '—' })}
          </p>
        </Note>
        <NativeServicesField
          options={endpoint.native_services ?? []}
          value={services}
          onChange={setServices}
          disabled={locked}
        />
        {endpoint.connection_id ? (
          <Fold lazy title={t('common.connections.changeCredential')}>
            <CredentialPicker
              accountId={accountId}
              connectionId={endpoint.connection_id}
              onPick={(value) => {
                setCredential(value);
                setSource(null);
                setSourceChanged(true);
              }}
            />
          </Fold>
        ) : null}
        <Fold lazy title={t('common.connections.discoveryOptional')}>
          <p>
            {sourceChanged
              ? source?.name || source?.models_url || t('common.connections.noDiscovery')
              : entryKey.discovery_source_id
                ? t('common.connections.sourceLinked')
                : t('common.connections.noDiscovery')}
          </p>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={locked}
            onClick={() => {
              setSource(null);
              setSourceChanged(true);
            }}
          >
            {t('common.connections.noDiscovery')}
          </button>
          {endpoint.connection_id ? (
            <ConnectionResourceList
              embedded
              accountId={accountId}
              scope={endpoint.id}
              listType="key-sources"
              title={t('common.connections.discoverySources')}
              searchPlaceholder={t('common.connections.sourceSearch')}
              load={(window, q, signal) =>
                connectionAPI.sources(endpoint.connection_id!, window, q, signal)
              }
              rowKey={(row) => row.id}
              columns={[
                {
                  key: 'name',
                  header: t('common.connections.name'),
                  cell: 'title',
                  render: (row) => row.name || row.models_url,
                },
                {
                  key: 'select',
                  header: t('common.connections.actions'),
                  cell: 'action',
                  render: (row) => (
                    <button
                      type="button"
                      className="nb-btn nb-btn--secondary"
                      disabled={
                        locked ||
                        !row.enabled ||
                        row.credential_id !== credentialId ||
                        row.credential_version !== version
                      }
                      onClick={() => {
                        setSource(row);
                        setSourceChanged(true);
                      }}
                    >
                      {t('common.connections.useSource')}
                    </button>
                  ),
                },
              ]}
            />
          ) : null}
        </Fold>
        <ConnectionOperationNotice operation={operation} />
        {operation.outcome === 'conflict' ? (
          <Note tone="warn">
            <p>{t('common.connections.conflictDraft')}</p>
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              onClick={() => {
                setExpectedRevision(entryKey.revision);
                setCredential(null);
                setSource(null);
                setSourceChanged(false);
                operation.reset();
              }}
            >
              {t('common.connections.useCurrentRevision')}
            </button>
          </Note>
        ) : null}
        <button
          type="submit"
          className="nb-btn nb-btn--primary"
          disabled={locked || !services.length || operation.outcome === 'conflict'}
        >
          {t('common.connections.save')}
        </button>
      </form>
    </Fold>
  );
}

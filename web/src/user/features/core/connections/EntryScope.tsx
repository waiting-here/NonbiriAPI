import { useState } from 'react';
import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Field, Fold, Note } from '@shared/components/ui';
import { ProtocolRerankField } from '@shared/components/ProtocolRerankField';
import { useEndpointCreateOptions } from '../queries';
import { getEndpoint } from '../api';
import { readEndpointKey } from '../resourceOperation';
import type { Endpoint, EndpointKey } from '../types';
import type { RerankFormat } from '@shared/nativeServices';
import { CredentialPicker, credentialLabel } from './CredentialPicker';
import { connectionAPI, resourcePath } from './api';
import { useMetadataOperation } from './useMetadataOperation';
import { ConnectionOperationNotice } from './OperationNotice';
import { ConnectionCreateForm } from './ConnectionForm';
import { ExpandablePanel } from '@shared/components/ui/ExpandablePanel';
import { ConnectionResourceList } from './ResourceList';
import { ConnectionSummary } from './ConnectionSummary';
import type { Credential, DiscoverySource } from './types';

export function EntryScope(props: {
  accountId: string;
  endpoint: Endpoint;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  return (
    <Fold
      lazy
      title={t('user.workflows.editService')}
      open={props.open}
      onOpenChange={props.onOpenChange}
    >
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
  const services = allowed?.native_services ?? [];
  const [note, setNote] = useState(endpoint.note);
  const [format, setFormat] = useState<RerankFormat | null>(endpoint.rerank_format || null);
  const selectedFormat = format ?? (allowed?.rerank_formats.includes('generic') ? 'generic' : '');
  const [group, setGroup] = useState<string | null>(null);
  const [creatingConnection, setCreatingConnection] = useState(false);
  const [expectedRevision, setExpectedRevision] = useState(endpoint.revision);
  const [baseline, setBaseline] = useState(endpoint);
  const operation = useMetadataOperation(
    accountId,
    '/api/endpoints/' + resourcePath(endpoint.id),
    'PATCH',
    (signal) => getEndpoint(endpoint.id, signal),
    () => undefined,
    (saved) => {
      setExpectedRevision(saved.revision);
      setBaseline(saved);
      setNote(saved.note);
      setFormat(saved.rerank_format || null);
      setGroup(null);
    },
  );
  const locked =
    operation.isPending ||
    operation.outcome === 'unknown' ||
    operation.outcome === 'refresh-failed';
  const dirty =
    note !== baseline.note ||
    Boolean(group) ||
    JSON.stringify(services) !== JSON.stringify(baseline.native_services ?? []) ||
    selectedFormat !== (baseline.rerank_format ?? '');
  return (
    <div className="nb-stack">
      <form
        className="nb-form"
        onSubmit={(event) => {
          event.preventDefault();
          void operation
            .mutateAsync({
              ...(note !== baseline.note ? { note } : {}),
              native_services: services,
              ...(selectedFormat !== (baseline.rerank_format ?? '')
                ? { rerank_format: selectedFormat }
                : {}),
              ...(group ? { connection_id: group } : {}),
              expected_revision: expectedRevision,
            })
            .catch(() => undefined);
        }}
      >
        <Field label={t('user.workflows.serviceName')}>
          {(props) => (
            <input
              {...props}
              value={note}
              maxLength={2048}
              disabled={locked}
              onChange={(event) => setNote(event.target.value)}
            />
          )}
        </Field>
        {endpoint.connection_id ? (
          <Link to={'/endpoints?connection_id=' + encodeURIComponent(endpoint.connection_id)}>
            {t('common.connections.backToConnection')}
          </Link>
        ) : null}
        <ProtocolRerankField
          formats={allowed?.rerank_formats}
          value={format}
          onChange={setFormat}
          disabled={locked}
        />
        <Fold
          lazy
          title={t('common.connections.moveConnection')}
          defaultOpen={!endpoint.connection_id}
        >
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={locked}
            onClick={() => setCreatingConnection(true)}
          >
            {t('common.connections.addConnection')}
          </button>
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
              setBaseline(endpoint);
              operation.reset();
            }}
          >
            {t('common.connections.useCurrentRevision')}
          </button>
        ) : null}
        <button
          type="submit"
          className="nb-btn nb-btn--primary"
          disabled={locked || !dirty || !services.length || operation.outcome === 'conflict'}
        >
          {t('common.connections.save')}
        </button>
      </form>
      <ExpandablePanel
        open={creatingConnection}
        onClose={() => setCreatingConnection(false)}
        title={t('common.connections.addConnection')}
        closeLabel={t('common.connections.close')}
      >
        {creatingConnection ? (
          <ConnectionCreateForm
            accountId={accountId}
            onSaved={(value) => {
              setGroup(value.id);
              setCreatingConnection(false);
            }}
          />
        ) : null}
      </ExpandablePanel>
    </div>
  );
}
export function KeyCredentialScope({
  accountId,
  endpoint,
  entryKey,
  defaultOpen = false,
  revealCatalog = 0,
}: {
  accountId: string;
  endpoint: Endpoint;
  entryKey: EndpointKey;
  defaultOpen?: boolean;
  revealCatalog?: number;
}) {
  const { t } = useTranslation();
  const options = useEndpointCreateOptions(accountId);
  const services =
    options.data?.native_service_options?.find(
      (row) => row.connector_type === endpoint.connector_type,
    )?.native_services ?? [];
  const [expanded, setExpanded] = useState({
    request: revealCatalog,
    scope: defaultOpen,
    catalog: defaultOpen,
  });
  if (expanded.request !== revealCatalog) {
    setExpanded({ request: revealCatalog, scope: true, catalog: true });
  }
  const [credential, setCredential] = useState<Credential | null>(null);
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
    <Fold
      lazy
      title={t('common.connections.credentialAndServices')}
      open={expanded.scope}
      onOpenChange={(scope) => setExpanded((current) => ({ ...current, scope }))}
    >
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
        <Fold
          lazy
          title={t('common.connections.discoveryOptional')}
          open={expanded.catalog}
          onOpenChange={(catalog) => setExpanded((current) => ({ ...current, catalog }))}
        >
          {endpoint.connection_id ? (
            <Link
              className="nb-btn nb-btn--secondary"
              to={
                '/endpoints?connection_id=' +
                encodeURIComponent(endpoint.connection_id) +
                '&connection_view=discovery'
              }
            >
              {t('common.connections.discoverySources')}
            </Link>
          ) : null}
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

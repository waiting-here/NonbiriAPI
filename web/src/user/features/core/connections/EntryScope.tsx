import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { CoreErrorPanel, CoreLoading } from '../components';
import { CredentialDetail } from './CredentialsPanel';
import { SourceForm } from './SourceForm';
import { connectionKeys } from './ResourceList';
import type { DiscoverySource } from './types';
import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Field, Fold, Note } from '@shared/components/ui';
import { ProtocolRerankField } from '@shared/components/ProtocolRerankField';
import { useEndpointCreateOptions } from '../queries';
import { getEndpoint } from '../api';
import { readEndpointKey } from '../resourceOperation';
import type { Endpoint, EndpointKey } from '../types';
import type { RerankFormat } from '@shared/nativeServices';
import { connectionAPI, resourcePath } from './api';
import { useMetadataOperation } from './useMetadataOperation';
import { ConnectionOperationNotice } from './OperationNotice';

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
      title={t('user.services.endpointSettings')}
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
    },
  );
  const locked =
    operation.isPending ||
    operation.outcome === 'unknown' ||
    operation.outcome === 'refresh-failed';
  const dirty =
    note !== baseline.note ||
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
              expected_revision: expectedRevision,
            })
            .catch(() => undefined);
        }}
      >
        <Field label={t('user.services.endpointName')}>
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
  return (
    <div className="nb-stack">
      <Fold lazy title={t('user.services.manageServiceKey')}>
        <Note>{t('user.services.serviceKeyImpact')}</Note>
        {entryKey.credential_id ? (
          <CredentialDetail
            accountId={accountId}
            id={entryKey.credential_id}
            onBack={() => undefined}
          />
        ) : null}
      </Fold>
      <Fold lazy title={t('user.services.protocolAccess')}>
        <KeyProtocolAccess accountId={accountId} endpoint={endpoint} entryKey={entryKey} />
      </Fold>
      <Fold
        key={revealCatalog}
        lazy
        title={t('user.services.modelListSettings')}
        defaultOpen={defaultOpen || revealCatalog > 0}
      >
        <Note>{t('user.services.pairListSettings')}</Note>
        <PairListSettings accountId={accountId} endpoint={endpoint} entryKey={entryKey} />
      </Fold>
    </div>
  );
}
function PairListSettings({
  accountId,
  endpoint,
  entryKey,
}: {
  accountId: string;
  endpoint: Endpoint;
  entryKey: EndpointKey;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [created, setCreated] = useState<DiscoverySource | null>(null);
  const [expected, setExpected] = useState(entryKey.revision);
  const sourceId = entryKey.discovery_source_id;
  const source = useQuery({
    queryKey: [...connectionKeys.root(accountId), 'pair-list-settings', sourceId],
    queryFn: ({ signal }) => connectionAPI.source(sourceId!, signal),
    enabled: Boolean(sourceId),
  });
  const link = useMetadataOperation(
    accountId,
    '/api/endpoints/' + resourcePath(endpoint.id) + '/keys/' + resourcePath(entryKey.id),
    'PATCH',
    (signal) => readEndpointKey(endpoint.id, entryKey.id, signal),
    () => {
      setCreated(null);
    },
    (saved) => setExpected(saved.revision),
  );
  const attach = (saved: DiscoverySource) => {
    setCreated(saved);
    void link
      .mutateAsync({ discovery_source_id: saved.id, expected_revision: expected })
      .catch(() => undefined);
  };
  if (created)
    return (
      <div className="nb-stack">
        <ConnectionOperationNotice operation={link} />
        {link.outcome === 'conflict' ? (
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            onClick={() => {
              setExpected(entryKey.revision);
              link.reset();
            }}
          >
            {t('common.connections.useCurrentRevision')}
          </button>
        ) : null}
        <button
          type="button"
          className="nb-btn nb-btn--primary"
          disabled={
            link.isPending ||
            link.outcome === 'unknown' ||
            link.outcome === 'refresh-failed' ||
            link.outcome === 'conflict'
          }
          onClick={() => attach(created)}
        >
          {t('user.services.saveListSettings')}
        </button>
      </div>
    );
  return sourceId && source.isPending ? (
    <CoreLoading />
  ) : sourceId && (source.error || !source.data) ? (
    <CoreErrorPanel error={source.error} onRetry={() => void source.refetch()} />
  ) : (
    <SourceForm
      key={source.data?.revision ?? 'new'}
      accountId={accountId}
      connectionId={endpoint.connection_id ?? ''}
      source={source.data}
      preset={{ base_url: endpoint.base_url, list_format: 'openai' }}
      pairSettings
      fixedCredentialId={entryKey.credential_id ?? undefined}
      onSaved={(saved) =>
        sourceId
          ? void client.invalidateQueries({ queryKey: connectionKeys.root(accountId) })
          : attach(saved)
      }
    />
  );
}

function KeyProtocolAccess({
  accountId,
  endpoint,
  entryKey,
}: {
  accountId: string;
  endpoint: Endpoint;
  entryKey: EndpointKey;
}) {
  const { t } = useTranslation();
  const options = useEndpointCreateOptions(accountId);
  const services =
    options.data?.native_service_options?.find(
      (row) => row.connector_type === endpoint.connector_type,
    )?.native_services ?? [];
  const [expectedRevision, setExpectedRevision] = useState(entryKey.revision);
  const operation = useMetadataOperation(
    accountId,
    '/api/endpoints/' + resourcePath(endpoint.id) + '/keys/' + resourcePath(entryKey.id),
    'PATCH',
    (signal) => readEndpointKey(endpoint.id, entryKey.id, signal),
    () => undefined,
    (saved) => setExpectedRevision(saved.revision),
  );
  const locked =
    operation.isPending ||
    operation.outcome === 'unknown' ||
    operation.outcome === 'refresh-failed';
  return (
    <form
      className="nb-form"
      onSubmit={(event) => {
        event.preventDefault();
        void operation
          .mutateAsync({ native_services: services, expected_revision: expectedRevision })
          .catch(() => undefined);
      }}
    >
      <p className="nb-small nb-muted">{t('user.services.protocolAccessHelp')}</p>
      <ConnectionOperationNotice operation={operation} />
      {operation.outcome === 'conflict' ? (
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          onClick={() => {
            setExpectedRevision(entryKey.revision);
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
  );
}

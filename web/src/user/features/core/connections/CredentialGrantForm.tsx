import { supportsChatStoragePolicy } from '@shared/operations/connectorPolicy';
import { useCoreCopy } from '../copy';
import { useState } from 'react';
import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Field, Fold, Note, Segmented } from '@shared/components/ui';
import { NativeServicesField } from '@shared/components/NativeServicesField';
import { KeyLimitFields, KeyLimitSummary } from '@shared/components/KeyRoutingLimits';
import type { NativeService } from '@shared/nativeServices';
import type { Endpoint, EndpointKey } from '../types';
import { CredentialCreateForm } from './CredentialForm';
import { credentialLabel, CredentialPicker } from './CredentialPicker';
import { connectionAPI } from './api';
import { ConnectionResourceList } from './ResourceList';
import { ConnectionOperationNotice } from './OperationNotice';
import { useConnectionOperation } from './useConnectionOperation';
import type { Credential, DiscoverySource } from './types';

export function CredentialGrantForm({
  accountId,
  connectionId,
  endpoint,
  onSaved,
}: {
  accountId: string;
  connectionId: string;
  endpoint: Endpoint;
  onSaved?: (key: EndpointKey) => void;
}) {
  const { t } = useTranslation();
  const { t: coreText } = useCoreCopy();
  const [forceStoreFalse, setForceStoreFalse] = useState(false);
  const [mode, setMode] = useState<'existing' | 'new'>('existing');
  const [credential, setCredential] = useState<Credential | null>(null);
  const [source, setSource] = useState<DiscoverySource | null>(null);
  const [services, setServices] = useState<NativeService[]>(
    endpoint.native_services?.slice() ?? [],
  );
  const [concurrency, setConcurrency] = useState('0');
  const [rpm, setRPM] = useState('0');
  const [note, setNote] = useState('');
  const [saved, setSaved] = useState<EndpointKey | null>(null);
  const operation = useConnectionOperation(accountId, (result) => {
    if (result.kind === 'key') {
      setSaved(result.key);
      onSaved?.(result.key);
    }
  });
  const locked = operation.isPending || operation.outcome === 'unknown';
  const pick = (value: Credential) => {
    setCredential(value);
    setSource(null);
    setMode('existing');
  };
  if (saved)
    return (
      <Note tone="ok">
        <p>{t('common.connections.grantSaved')}</p>
        <KeyLimitSummary concurrency={saved.max_concurrency} rpm={saved.max_rpm} />
        <Link
          className="nb-btn nb-btn--secondary"
          to={'/endpoints/' + encodeURIComponent(endpoint.id)}
        >
          {t('common.connections.manageEntry')}
        </Link>
      </Note>
    );
  return (
    <div className="nb-stack">
      <Segmented
        label={t('common.connections.credential')}
        value={mode}
        options={[
          { value: 'existing', label: t('common.connections.existingCredential') },
          { value: 'new', label: t('common.connections.newCredential') },
        ]}
        onChange={setMode}
        disabled={locked}
      />
      {mode === 'new' ? (
        <CredentialCreateForm accountId={accountId} connectionId={connectionId} onSaved={pick} />
      ) : !credential ? (
        <CredentialPicker accountId={accountId} connectionId={connectionId} onPick={pick} />
      ) : (
        <Note>
          <p>
            {credentialLabel(credential)} ·{' '}
            {t('common.connections.versionNumber', { version: credential.credential_version })}
          </p>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={locked}
            onClick={() => {
              setCredential(null);
              setSource(null);
            }}
          >
            {t('common.connections.changeCredential')}
          </button>
        </Note>
      )}
      {credential ? (
        <form
          className="nb-form"
          onSubmit={(event) => {
            event.preventDefault();
            void operation
              .run({
                kind: 'key',
                endpointId: endpoint.id,
                input: {
                  credential_id: credential.id,
                  native_services: services,
                  discovery_source_id: source?.id ?? null,
                  note: note.trim(),
                  enabled: true,
                  force_store_false:
                    supportsChatStoragePolicy(endpoint.connector_type) && forceStoreFalse,
                  ownership_confirmed: true,
                  max_concurrency: Number(concurrency),
                  max_rpm: Number(rpm),
                },
              })
              .catch(() => undefined);
          }}
        >
          <NativeServicesField
            options={endpoint.native_services ?? []}
            value={services}
            onChange={setServices}
            disabled={locked}
          />
          <Field label={t('common.connections.note')} optional={t('common.connections.optional')}>
            {(props) => (
              <input
                {...props}
                value={note}
                maxLength={1024}
                disabled={locked}
                onChange={(event) => setNote(event.target.value)}
              />
            )}
          </Field>
          {supportsChatStoragePolicy(endpoint.connector_type) ? (
            <label className="core-checkbox">
              <input
                type="checkbox"
                checked={forceStoreFalse}
                disabled={locked}
                onChange={(event) => setForceStoreFalse(event.target.checked)}
              />
              <span>{coreText('endpoints.storePolicy')}</span>
            </label>
          ) : null}
          <KeyLimitFields
            concurrency={concurrency}
            rpm={rpm}
            onConcurrency={setConcurrency}
            onRPM={setRPM}
            disabled={locked}
          />
          <Note>{t('common.connections.separateLimits')}</Note>
          <Fold title={t('common.connections.discoveryOptional')}>
            {source ? (
              <Note>
                <p>{source.name || source.base_url}</p>
                <p>{source.base_url}</p>
                <button
                  type="button"
                  className="nb-btn nb-btn--secondary"
                  disabled={locked}
                  onClick={() => setSource(null)}
                >
                  {t('common.connections.noDiscovery')}
                </button>
              </Note>
            ) : (
              <p className="nb-sub">{t('common.connections.noDiscovery')}</p>
            )}
            <ConnectionResourceList
              embedded
              accountId={accountId}
              scope={connectionId}
              listType="grant-source"
              title={t('common.connections.discoverySources')}
              searchPlaceholder={t('common.connections.sourceSearch')}
              load={(window, q, signal) => connectionAPI.sources(connectionId, window, q, signal)}
              rowKey={(row) => row.id}
              columns={[
                {
                  key: 'name',
                  header: t('common.connections.name'),
                  cell: 'title',
                  render: (row) => (
                    <div className="nb-stack nb-stack--sm">
                      <strong>{row.name || row.base_url}</strong>
                      <small className="nb-mono" title={row.base_url}>
                        {row.base_url}
                      </small>
                    </div>
                  ),
                },
                {
                  key: 'select',
                  header: t('common.connections.actions'),
                  cell: 'action',
                  render: (row) => {
                    const compatible =
                      row.enabled &&
                      row.credential_available !== false &&
                      row.credential_id === credential.id &&
                      row.credential_version === credential.credential_version;
                    return (
                      <button
                        type="button"
                        className="nb-btn nb-btn--secondary"
                        disabled={locked || !compatible}
                        onClick={() => setSource(row)}
                      >
                        {compatible
                          ? t('common.connections.useSource')
                          : t('common.connections.differentVersion')}
                      </button>
                    );
                  },
                },
              ]}
            />
          </Fold>
          <ConnectionOperationNotice operation={operation} />
          <div className="nb-actions">
            <button
              type="submit"
              className="nb-btn nb-btn--primary"
              disabled={locked || !services.length}
            >
              {t('common.connections.authorizeEntry')}
            </button>
          </div>
        </form>
      ) : null}
    </div>
  );
}

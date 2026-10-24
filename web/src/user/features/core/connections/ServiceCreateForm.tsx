import { useId, useRef, useState } from 'react';
import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Field, Fold, Note, Segmented } from '@shared/components/ui';
import { NativeServicesField } from '@shared/components/NativeServicesField';
import type { NativeService, RerankFormat } from '@shared/nativeServices';
import { DISCOVERY_FORMATS, type DiscoveryPreset } from '@shared/connections';
import { useEndpointCreateOptions } from '../queries';
import { CoreErrorPanel, CoreLoading } from '../components';
import type { ConnectorType, Endpoint, EndpointCreateInput, EndpointKey } from '../types';
import type { Connection, Credential, DiscoverySource } from './types';
import type { ConnectionIntent, ConnectionResult } from './resourceOperation';
import { CredentialPicker, credentialLabel } from './CredentialPicker';
import { ConnectionOperationNotice } from './OperationNotice';
import { useConnectionOperation } from './useConnectionOperation';
import { PresetModels } from './PresetModels';
import { ServiceModelSetup } from './ServiceModelSetup';
import { CONNECTOR_COPY_KEYS, useCoreCopy } from '../copy';
import { defaultDiscovery } from './discoveryDefaults';

function setupProgress(connection?: Connection) {
  return {
    connection: connection ?? null,
    credential: null as Credential | null,
    entries: {} as Record<string, Endpoint>,
    sources: {} as Record<string, DiscoverySource>,
    grants: {} as Record<string, EndpointKey>,
  };
}

export function ServiceCreateForm({
  accountId,
  connection,
}: {
  accountId: string;
  connection?: Connection;
}) {
  const { t } = useTranslation();
  const { t: coreText } = useCoreCopy();
  const formID = useId();
  const options = useEndpointCreateOptions(accountId);
  const [mode, setMode] = useState<'custom' | 'mainstream'>('custom');
  const [channelId, setChannelId] = useState('');
  const [connector, setConnector] = useState('');
  const [address, setAddress] = useState('');
  const [name, setName] = useState<string | null>(null);
  const [services, setServices] = useState<NativeService[]>([]);
  const [format, setFormat] = useState<RerankFormat | null>(null);
  const [discoveryOverride, setDiscoveryOverride] = useState<DiscoveryPreset | null | undefined>();
  const [selected, setSelected] = useState<Record<string, NativeService[]>>({});
  const [skipCatalog, setSkipCatalog] = useState<Record<string, boolean>>({});
  const [credentialMode, setCredentialMode] = useState<'new' | 'existing'>(
    connection && connection.credential_count !== '0' ? 'existing' : 'new',
  );
  const [chosenCredential, setChosenCredential] = useState<Credential | null>(null);
  const [secret, setSecret] = useState('');
  const [owned, setOwned] = useState(false);
  const [busy, setBusy] = useState(false);
  const [useModels, setUseModels] = useState(false);
  const [saved, setSaved] = useState(() => setupProgress(connection));
  const progress = useRef(setupProgress(connection));
  const activeRow = useRef('');
  const accept = (result: ConnectionResult) => {
    const saved = progress.current;
    switch (result.kind) {
      case 'connection':
        saved.connection = result.connection;
        break;
      case 'credential':
        saved.credential = result.credential;
        break;
      case 'endpoint':
        saved.entries[activeRow.current] = result.endpoint;
        break;
      case 'discovery_source':
        saved.sources[activeRow.current] = result.source;
        break;
      case 'key':
        saved.grants[activeRow.current] = result.key;
        break;
    }
    setSaved({
      ...saved,
      entries: { ...saved.entries },
      sources: { ...saved.sources },
      grants: { ...saved.grants },
    });
  };
  const operation = useConnectionOperation(accountId, accept, () => setSecret(''));
  const channel = options.data?.mainstream_channels.find((row) => row.id === channelId);
  const nativeOptions = options.data?.native_service_options ?? [];
  const chosenProtocol = nativeOptions.find((row) => row.connector_type === connector);
  const customDiscovery =
    discoveryOverride === undefined
      ? defaultDiscovery(chosenProtocol?.configuration_type, address)
      : discoveryOverride;
  const suggestedName =
    channel?.name ||
    (() => {
      try {
        return new URL(address).hostname;
      } catch {
        return t('user.workflows.defaultServiceName');
      }
    })();
  const serviceName = name ?? suggestedName;
  const inputs: { row: string; input: EndpointCreateInput; discovery?: DiscoveryPreset | null }[] =
    mode === 'custom'
      ? [
          {
            row: 'custom',
            discovery: customDiscovery,
            input: {
              source: 'custom',
              connector_type: connector as ConnectorType,
              base_url: address.trim(),
              note: serviceName.trim(),
              enabled: true,
              native_services: services,
              rerank_format: format ?? '',
            },
          },
        ]
      : (channel?.entries ?? [])
          .filter((entry) => entry.id && selected[entry.id])
          .map((entry) => ({
            row: entry.id!,
            discovery: skipCatalog[entry.id!] ? null : channel?.discovery_source,
            input: {
              source: 'mainstream',
              channel_id: channel!.id,
              channel_entry_id: entry.id,
              channel_revision: channel!.revision,
              native_services: selected[entry.id!],
              rerank_format: entry.rerank_format,
              note: entry.name,
              enabled: true,
            },
          }));
  const complete = inputs.length > 0 && inputs.every(({ row }) => Boolean(saved.grants[row]));
  const locked =
    busy ||
    operation.isPending ||
    operation.outcome === 'unknown' ||
    operation.outcome === 'refresh-failed';
  const started = Object.keys(saved.entries).length > 0;
  const ready = Boolean(
    serviceName.trim() &&
    inputs.length &&
    (mode === 'mainstream' || (chosenProtocol && address.trim() && services.length)) &&
    inputs.every(({ input }) => input.native_services?.length) &&
    (saved.credential || (credentialMode === 'existing' ? chosenCredential : secret)) &&
    (saved.credential || owned),
  );
  const run = async (row: string, intent: ConnectionIntent, requestSecret?: string) => {
    activeRow.current = row;
    await operation.run(intent, requestSecret);
  };
  const save = async () => {
    if (locked || !ready) return;
    setBusy(true);
    const requestSecret = secret;
    const saved = progress.current;
    try {
      if (!saved.connection)
        await run('', {
          kind: 'connection',
          input: { name: serviceName.trim(), note: '' },
        });
      for (const item of inputs) {
        if (!saved.entries[item.row])
          await run(item.row, {
            kind: 'endpoint',
            input: { ...item.input, connection_id: saved.connection!.id },
          });
      }
      if (!saved.credential) {
        if (credentialMode === 'existing')
          accept({ kind: 'credential', credential: chosenCredential! });
        else
          await run(
            '',
            {
              kind: 'credential',
              connectionId: saved.connection!.id,
              input: { note: '', ownership_confirmed: true },
            },
            requestSecret,
          );
      }
      for (const item of inputs) {
        const entry = saved.entries[item.row]!;
        if (item.discovery && !saved.sources[item.row]) {
          const sharedRow = inputs.find(
            (other) =>
              other.discovery?.base_url === item.discovery!.base_url &&
              other.discovery?.list_format === item.discovery!.list_format &&
              saved.sources[other.row]?.credential_id === saved.credential!.id &&
              saved.sources[other.row]?.credential_version === saved.credential!.credential_version,
          );
          const shared = sharedRow ? saved.sources[sharedRow.row] : undefined;
          if (shared) {
            activeRow.current = item.row;
            accept({ kind: 'discovery_source', source: shared });
          } else
            await run(item.row, {
              kind: 'discovery_source',
              connectionId: saved.connection!.id,
              suggestedCredentialVersion: saved.credential!.credential_version,
              input: { ...item.discovery, credential_id: saved.credential!.id, enabled: true },
            });
        }
        if (!saved.grants[item.row])
          await run(item.row, {
            kind: 'key',
            endpointId: entry.id,
            input: {
              credential_id: saved.credential!.id,
              native_services: entry.native_services,
              discovery_source_id: saved.sources[item.row]?.id ?? null,
              note: '',
              enabled: true,
              ownership_confirmed: true,
              force_store_false: false,
              max_concurrency: 0,
              max_rpm: 0,
            },
          });
      }
    } catch {
      // Completed resources remain available when the next stage is retried.
    } finally {
      setBusy(false);
    }
  };
  if (options.isPending) return <CoreLoading />;
  if (options.error)
    return <CoreErrorPanel error={options.error} onRetry={() => void options.refetch()} />;
  if (complete)
    return (
      <div className="nb-stack">
        <Note tone="ok">{t('user.workflows.serviceReady')}</Note>
        <div className="nb-actions">
          <button
            type="button"
            className="nb-btn nb-btn--primary"
            onClick={() => setUseModels(true)}
          >
            {t('user.workflows.useModels')}
          </button>
          <Link
            className="nb-btn nb-btn--secondary"
            to={
              '/charity?tab=donate&endpoint_id=' +
              encodeURIComponent(Object.values(saved.entries)[0]!.id)
            }
          >
            {t('user.workflows.donateService')}
          </Link>
          <Link
            className="nb-btn nb-btn--ghost"
            to={'/endpoints?connection_id=' + saved.connection!.id}
          >
            {t('common.connections.manage')}
          </Link>
        </div>
        {useModels ? (
          channel?.model_mappings?.length ? (
            <PresetModels
              accountId={accountId}
              mappings={channel.model_mappings}
              entries={Object.fromEntries(inputs.map(({ row }) => [row, saved.entries[row]!]))}
              grants={Object.fromEntries(
                inputs.map(({ row }) => [saved.entries[row]!.id, saved.grants[row]!]),
              )}
            />
          ) : (
            inputs.map(({ row }) => (
              <ServiceModelSetup
                key={row}
                accountId={accountId}
                entry={saved.entries[row]!}
                entryKey={saved.grants[row]!}
                source={saved.sources[row]}
              />
            ))
          )
        ) : null}
      </div>
    );
  return (
    <div className="nb-stack">
      <div className="nb-form">
        <Segmented
          label={t('common.connections.entrySource')}
          value={mode}
          options={[
            { value: 'custom', label: t('common.connections.custom') },
            { value: 'mainstream', label: t('common.connections.mainstream') },
          ]}
          onChange={setMode}
          disabled={locked || started}
        />
        {mode === 'mainstream' ? (
          <>
            <Field label={t('common.connections.mainstream')}>
              {(props) => (
                <select
                  {...props}
                  form={formID}
                  value={channelId}
                  disabled={locked || started}
                  required
                  onChange={(event) => {
                    const value = options.data?.mainstream_channels.find(
                      (row) => row.id === event.target.value,
                    );
                    setChannelId(event.target.value);
                    setSkipCatalog({});
                    setSelected(
                      Object.fromEntries(
                        (value?.entries ?? [])
                          .filter((row) => row.id && row.enabled)
                          .map((row) => [row.id!, row.native_services.slice()]),
                      ),
                    );
                  }}
                >
                  <option value="">{t('common.connections.select')}</option>
                  {options.data?.mainstream_channels.map((row) => (
                    <option key={row.id} value={row.id}>
                      {row.name}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            {channel ? (
              <Fold title={t('common.connections.entries')}>
                {channel.entries
                  ?.filter((entry) => entry.enabled && entry.id)
                  .map((entry) => (
                    <div key={entry.id} className="nb-stack nb-stack--sm">
                      <Field label={entry.name}>
                        {(props) => (
                          <input
                            {...props}
                            type="checkbox"
                            checked={Boolean(selected[entry.id!])}
                            disabled={locked || started}
                            onChange={(event) =>
                              setSelected((previous) => {
                                const next = { ...previous };
                                if (event.target.checked)
                                  next[entry.id!] = entry.native_services.slice();
                                else delete next[entry.id!];
                                return next;
                              })
                            }
                          />
                        )}
                      </Field>
                      {selected[entry.id!] ? (
                        <NativeServicesField
                          options={entry.native_services}
                          value={selected[entry.id!]!}
                          disabled={locked || started}
                          onChange={(value) =>
                            setSelected((previous) => ({ ...previous, [entry.id!]: value }))
                          }
                        />
                      ) : null}
                      {selected[entry.id!] && channel.discovery_source ? (
                        <Field label={t('common.connections.useSuggestedCatalog')}>
                          {(props) => (
                            <input
                              {...props}
                              type="checkbox"
                              checked={!skipCatalog[entry.id!]}
                              disabled={locked || started}
                              onChange={(event) =>
                                setSkipCatalog((previous) => ({
                                  ...previous,
                                  [entry.id!]: !event.target.checked,
                                }))
                              }
                            />
                          )}
                        </Field>
                      ) : null}
                    </div>
                  ))}
              </Fold>
            ) : null}
          </>
        ) : (
          <>
            <Field label={t('common.connections.protocol')}>
              {(props) => (
                <select
                  {...props}
                  form={formID}
                  value={connector}
                  disabled={locked || started}
                  required
                  onChange={(event) => {
                    const option = nativeOptions.find(
                      (row) => row.connector_type === event.target.value,
                    );
                    setConnector(event.target.value);
                    setServices(option?.default_native_services.slice() ?? []);
                    setFormat(null);
                    setDiscoveryOverride(undefined);
                  }}
                >
                  <option value="">{t('common.connections.selectProtocol')}</option>
                  {nativeOptions
                    .filter(
                      (row, index, rows) =>
                        rows.findIndex(
                          (other) => other.configuration_type === row.configuration_type,
                        ) === index,
                    )
                    .map((row) => (
                      <option key={row.connector_type} value={row.connector_type}>
                        {coreText(CONNECTOR_COPY_KEYS[row.configuration_type as ConnectorType])}
                      </option>
                    ))}
                </select>
              )}
            </Field>
            <Field label={t('common.connections.entryURL')}>
              {(props) => (
                <input
                  {...props}
                  form={formID}
                  value={address}
                  type="url"
                  required
                  disabled={locked || started}
                  placeholder="https://api.example.com/v1"
                  onChange={(event) => setAddress(event.target.value)}
                />
              )}
            </Field>
            <Fold title={t('common.connections.nativeServices')}>
              <NativeServicesField
                options={chosenProtocol?.native_services ?? []}
                value={services}
                rerankFormats={chosenProtocol?.rerank_formats}
                rerankFormat={format}
                onRerankFormatChange={setFormat}
                onChange={setServices}
                disabled={locked || started}
              />
            </Fold>
            <Fold title={t('common.connections.discoveryOptional')}>
              <Field label={t('user.workflows.discoverModels')}>
                {(props) => (
                  <input
                    {...props}
                    type="checkbox"
                    checked={Boolean(customDiscovery)}
                    disabled={locked || started}
                    onChange={(event) =>
                      setDiscoveryOverride(
                        event.target.checked
                          ? (defaultDiscovery(chosenProtocol?.configuration_type, address) ?? {
                              base_url: address.trim(),
                              list_format: 'openai',
                            })
                          : null,
                      )
                    }
                  />
                )}
              </Field>
              {customDiscovery ? (
                <>
                  <Field
                    label={t('common.connections.discoveryBaseURL')}
                    help={t('common.connections.independentDiscovery')}
                  >
                    {(props) => (
                      <input
                        {...props}
                        form={formID}
                        type="url"
                        required
                        value={customDiscovery.base_url}
                        disabled={locked || started}
                        onChange={(event) =>
                          setDiscoveryOverride({ ...customDiscovery, base_url: event.target.value })
                        }
                      />
                    )}
                  </Field>
                  <Field label={t('common.connections.listFormat')}>
                    {(props) => (
                      <select
                        {...props}
                        value={customDiscovery.list_format}
                        disabled={locked || started}
                        onChange={(event) =>
                          setDiscoveryOverride({
                            ...customDiscovery,
                            list_format: event.target.value as DiscoveryPreset['list_format'],
                          })
                        }
                      >
                        {DISCOVERY_FORMATS.map((value) => (
                          <option key={value} value={value}>
                            {t(`common.connections.discoveryFormats.${value}`)}
                          </option>
                        ))}
                      </select>
                    )}
                  </Field>
                </>
              ) : null}
            </Fold>
          </>
        )}
        <Field label={t('user.workflows.serviceName')}>
          {(props) => (
            <input
              {...props}
              form={formID}
              value={serviceName}
              required
              maxLength={128}
              disabled={locked || started || (!connection && Boolean(saved.connection))}
              onChange={(event) => setName(event.target.value)}
            />
          )}
        </Field>
        {saved.credential ? (
          <Note>{credentialLabel(saved.credential)}</Note>
        ) : (
          <>
            <Segmented
              label={t('common.connections.credential')}
              value={credentialMode}
              options={[
                { value: 'new', label: t('common.connections.newCredential') },
                { value: 'existing', label: t('common.connections.existingCredential') },
              ]}
              onChange={(value) => {
                setCredentialMode(value);
                setSecret('');
              }}
              disabled={locked}
            />
            {credentialMode === 'new' ? (
              <Field
                label={t('common.connections.secret')}
                help={t('common.connections.secretHelp')}
              >
                {(props) => (
                  <input
                    {...props}
                    form={formID}
                    type="password"
                    autoComplete="new-password"
                    value={secret}
                    required
                    disabled={locked}
                    onChange={(event) => setSecret(event.target.value)}
                  />
                )}
              </Field>
            ) : chosenCredential ? (
              <Note>
                {credentialLabel(chosenCredential)}
                <button
                  type="button"
                  className="nb-btn nb-btn--ghost"
                  disabled={locked}
                  onClick={() => setChosenCredential(null)}
                >
                  {t('common.connections.changeCredential')}
                </button>
              </Note>
            ) : (
              <CredentialPicker
                accountId={accountId}
                connectionId={connection?.id ?? ''}
                onPick={setChosenCredential}
              />
            )}
            <Field label={t('common.connections.ownership')}>
              {(props) => (
                <input
                  {...props}
                  form={formID}
                  type="checkbox"
                  checked={owned}
                  required
                  disabled={locked}
                  onChange={(event) => setOwned(event.target.checked)}
                />
              )}
            </Field>
          </>
        )}
        <ConnectionOperationNotice operation={operation} />
        {saved.connection && !complete ? <Note>{t('user.workflows.continueSetup')}</Note> : null}
        <form
          id={formID}
          onSubmit={(event) => {
            event.preventDefault();
            void save();
          }}
        >
          <button type="submit" className="nb-btn nb-btn--primary" disabled={locked || !ready}>
            {busy
              ? t('common.working')
              : saved.connection
                ? t('user.workflows.continueService')
                : t('user.workflows.addService')}
          </button>
        </form>
      </div>
    </div>
  );
}

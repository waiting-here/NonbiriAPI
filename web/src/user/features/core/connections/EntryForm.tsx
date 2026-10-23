import { Link } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { SourceForm } from './SourceForm';
import { PresetModels } from './PresetModels';
import type { DiscoverySource } from './types';
import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { NativeServicesField } from '@shared/components/NativeServicesField';
import { Field, Fold, Note, Segmented } from '@shared/components/ui';
import type { NativeService, RerankFormat } from '@shared/nativeServices';
import type { NativeServiceOption } from '@shared/connections';
import { ConnectorLabel, CoreErrorPanel, CoreLoading } from '../components';
import { CONNECTOR_COPY_KEYS, useCoreCopy } from '../copy';
import { useEndpointCreateOptions } from '../queries';
import { coreKeys } from '../queries';
import type { ConnectorType, Endpoint, EndpointKey, EndpointCreateInput } from '../types';
import { ConnectionOperationNotice } from './OperationNotice';
import { useConnectionOperation } from './useConnectionOperation';
import { CredentialGrantForm } from './CredentialGrantForm';
import { MainstreamMatches } from './MainstreamMatches';
import { useMainstreamMatchingCopy } from './mainstreamMatchingCopy';
import { useMainstreamMatches } from './useMainstreamMatches';

export function EntryForm({
  accountId,
  connectionId,
  onChanged,
}: {
  accountId: string;
  connectionId: string;
  onChanged?: () => void;
}) {
  const { t } = useTranslation();
  const { t: coreText } = useCoreCopy();
  const copy = useMainstreamMatchingCopy();
  const client = useQueryClient();
  const options = useEndpointCreateOptions(accountId);
  const [mode, setMode] = useState<'custom' | 'mainstream'>('custom');
  const [connector, setConnector] = useState('');
  const [baseURL, setBaseURL] = useState('');
  const [note, setNote] = useState('');
  const [services, setServices] = useState<NativeService[]>([]);
  const [format, setFormat] = useState<RerankFormat | null>(null);
  const [channelId, setChannelId] = useState('');
  const [selected, setSelected] = useState<Record<string, NativeService[]>>({});
  const [saved, setSaved] = useState<Record<string, Endpoint>>({});
  const [grants, setGrants] = useState<Record<string, EndpointKey>>({});
  const [sources, setSources] = useState<Record<string, DiscoverySource>>({});
  const active = useRef('custom');
  const nativeOptions = options.data?.native_service_options ?? [];
  const configurations = nativeOptions.filter(
    (item, index, items) =>
      items.findIndex((other) => other.configuration_type === item.configuration_type) === index,
  );
  const selectedOption = nativeOptions.find((item) => item.connector_type === connector);
  const channel = options.data?.mainstream_channels.find((item) => item.id === channelId);
  const pendingEntries = (channel?.entries ?? []).filter(
    (entry) => entry.enabled && entry.id && selected[entry.id] && !saved[entry.id],
  );
  const operation = useConnectionOperation(accountId, (result) => {
    if (result.kind === 'endpoint') {
      setSaved((current) => ({ ...current, [active.current]: result.endpoint }));
      onChanged?.();
    }
  });
  const locked = operation.isPending || operation.outcome === 'unknown';
  const suggestions = useMainstreamMatches(
    accountId,
    baseURL,
    mode === 'custom' && !saved.custom && !locked,
  );
  const chooseConnector = (item: NativeServiceOption | undefined) => {
    setConnector(item?.connector_type ?? '');
    setServices(item?.default_native_services.slice() ?? []);
    setFormat(null);
  };
  const save = async () => {
    const inputs: { row: string; input: EndpointCreateInput }[] =
      mode === 'custom'
        ? [
            {
              row: 'custom',
              input: {
                source: 'custom',
                connector_type: connector as ConnectorType,
                base_url: baseURL.trim(),
                note: note.trim(),
                enabled: true,
                connection_id: connectionId,
                native_services: services,
                rerank_format: format ?? '',
              },
            },
          ]
        : pendingEntries.map((entry) => ({
            row: entry.id!,
            input: {
              source: 'mainstream',
              channel_id: channel!.id,
              channel_entry_id: entry.id,
              channel_revision: channel!.revision,
              note: entry.name,
              enabled: true,
              connection_id: connectionId,
              native_services: selected[entry.id!],
              rerank_format: entry.rerank_format,
            },
          }));
    for (const item of inputs) {
      active.current = item.row;
      try {
        await operation.run({ kind: 'endpoint', input: item.input });
      } catch {
        break;
      }
    }
  };
  if (options.isPending) return <CoreLoading />;
  if (options.error)
    return <CoreErrorPanel error={options.error} onRetry={() => void options.refetch()} />;
  const ready =
    mode === 'custom'
      ? Boolean(selectedOption && baseURL.trim() && services.length && !saved.custom)
      : Boolean(
          channel?.revision &&
          pendingEntries.length &&
          pendingEntries.every((entry) => selected[entry.id!]?.length),
        );
  return (
    <div className="nb-stack">
      <Segmented
        label={t('common.connections.entrySource')}
        value={mode}
        options={[
          { value: 'custom', label: t('common.connections.custom') },
          { value: 'mainstream', label: t('common.connections.mainstream') },
        ]}
        onChange={setMode}
        disabled={locked}
      />
      <form
        className="nb-form"
        onSubmit={(event) => {
          event.preventDefault();
          void save();
        }}
      >
        {mode === 'custom' ? (
          <>
            <Field label={t('common.connections.protocol')}>
              {(props) => (
                <select
                  {...props}
                  value={connector}
                  disabled={locked || Boolean(saved.custom)}
                  required
                  onChange={(event) =>
                    chooseConnector(
                      nativeOptions.find((item) => item.connector_type === event.target.value),
                    )
                  }
                >
                  <option value="">{t('common.connections.selectProtocol')}</option>
                  {configurations.map((item) => (
                    <option value={item.connector_type} key={item.configuration_type}>
                      {coreText(CONNECTOR_COPY_KEYS[item.configuration_type as ConnectorType])}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Field label={t('common.connections.entryURL')}>
              {(props) => (
                <input
                  {...props}
                  type="url"
                  value={baseURL}
                  placeholder="https://api.example.com/v1"
                  required
                  disabled={locked || Boolean(saved.custom)}
                  onChange={(event) => setBaseURL(event.target.value)}
                />
              )}
            </Field>
            <Field label={t('common.connections.note')} optional={t('common.connections.optional')}>
              {(props) => (
                <input
                  {...props}
                  value={note}
                  maxLength={1024}
                  disabled={locked || Boolean(saved.custom)}
                  onChange={(event) => setNote(event.target.value)}
                />
              )}
            </Field>
            {selectedOption ? (
              <NativeServicesField
                options={selectedOption.native_services}
                value={services}
                onChange={setServices}
                disabled={locked || Boolean(saved.custom)}
                rerankFormats={selectedOption.rerank_formats}
                rerankFormat={format}
                onRerankFormatChange={setFormat}
              />
            ) : null}
            <MainstreamMatches
              options={suggestions}
              disabled={locked}
              onAdopt={(snapshot, matchingChannel, entry) => {
                client.setQueryData(coreKeys.endpointCreateOptions(accountId), snapshot);
                setChannelId(matchingChannel.id);
                setSelected({
                  [entry.id!]: services.filter((service) =>
                    entry.native_services.includes(service),
                  ),
                });
                setMode('mainstream');
              }}
            />
          </>
        ) : (
          <>
            <Field label={t('common.connections.mainstream')}>
              {(props) => (
                <select
                  {...props}
                  value={channelId}
                  disabled={locked}
                  onChange={(event) => {
                    setChannelId(event.target.value);
                    setSelected({});
                  }}
                >
                  <option value="">{t('common.connections.selectPreset')}</option>
                  {options.data?.mainstream_channels.map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.name}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            {channel?.entries?.map((entry) =>
              entry.id && entry.enabled ? (
                <div key={entry.id} className="nb-stack nb-stack--sm">
                  <Field label={entry.name || entry.base_url} help={entry.base_url}>
                    {(props) => (
                      <input
                        {...props}
                        type="checkbox"
                        disabled={locked || Boolean(saved[entry.id!])}
                        checked={Boolean(selected[entry.id!])}
                        onChange={(event) => {
                          const enabled = event.target.checked;
                          setSelected((current) => {
                            const next = { ...current };
                            if (enabled) next[entry.id!] = entry.native_services.slice();
                            else delete next[entry.id!];
                            return next;
                          });
                        }}
                      />
                    )}
                  </Field>
                  {selected[entry.id] ? (
                    <>
                      {selected[entry.id].length === 0 ? <Note>{copy.chooseServices}</Note> : null}
                      <NativeServicesField
                        options={entry.native_services}
                        value={selected[entry.id]}
                        onChange={(value) =>
                          setSelected((current) => ({ ...current, [entry.id!]: value }))
                        }
                        disabled={locked || Boolean(saved[entry.id])}
                      />
                    </>
                  ) : null}
                </div>
              ) : null,
            )}
            <Note>{t('common.connections.presetAuthorization')}</Note>
          </>
        )}
        <ConnectionOperationNotice operation={operation} />
        <div className="nb-actions">
          <button type="submit" className="nb-btn nb-btn--primary" disabled={locked || !ready}>
            {t('common.connections.saveEntries')}
          </button>
        </div>
      </form>
      {Object.entries(saved).map(([key, endpoint]) => (
        <section className="nb-stack" key={endpoint.id}>
          <h3>{endpoint.note || endpoint.base_url}</h3>
          <p className="nb-sub">
            <ConnectorLabel value={endpoint.configuration_type ?? endpoint.connector_type} /> ·{' '}
            {t('common.connections.entrySaved')}
          </p>
          <CredentialGrantForm
            accountId={accountId}
            connectionId={connectionId}
            endpoint={endpoint}
            onSaved={(key) => setGrants((previous) => ({ ...previous, [endpoint.id]: key }))}
          />
          {key === 'custom' ? (
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              onClick={() => {
                setSaved((current) => {
                  const next = { ...current };
                  delete next.custom;
                  return next;
                });
                setBaseURL('');
                setNote('');
              }}
            >
              {t('common.connections.addAnotherEntry')}
            </button>
          ) : null}
        </section>
      ))}
      {mode === 'mainstream' && channel?.discovery_source ? (
        <Fold title={t('common.connections.adoptDiscoverySuggestion')} lazy>
          {sources[channel.id] ? (
            <Note tone="ok">
              <Link
                to={
                  '/endpoints?connection_id=' +
                  encodeURIComponent(connectionId) +
                  '&connection_view=discovery&discovery_source_id=' +
                  encodeURIComponent(sources[channel.id]!.id)
                }
              >
                {t('common.connections.manageSource')}
              </Link>
            </Note>
          ) : (
            <SourceForm
              key={channel.id}
              accountId={accountId}
              connectionId={connectionId}
              preset={channel.discovery_source}
              onSaved={(source) =>
                setSources((previous) => ({ ...previous, [channel.id]: source }))
              }
            />
          )}
        </Fold>
      ) : null}
      {mode === 'mainstream' && channel?.model_mappings?.length ? (
        <PresetModels
          accountId={accountId}
          mappings={channel.model_mappings}
          entries={saved}
          grants={grants}
        />
      ) : null}
    </div>
  );
}

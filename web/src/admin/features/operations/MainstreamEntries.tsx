import { useTranslation } from 'react-i18next';
import { Field, Fold, Note } from '@shared/components/ui';
import { NativeServicesField } from '@shared/components/NativeServicesField';
import { ProtocolRerankField } from '@shared/components/ProtocolRerankField';
import {
  DISCOVERY_FORMATS,
  type DiscoveryPreset,
  type MainstreamEntry,
  type ModelMapping,
  type NativeServiceOption,
} from '@shared/connections';

export interface MainstreamLayout {
  entries: MainstreamEntry[];
  discovery_source: DiscoveryPreset | null;
  model_mappings: ModelMapping[];
}
export function MainstreamEntries({
  value,
  onChange,
  options,
  disabled,
  connectorLabels,
}: {
  value: MainstreamLayout;
  onChange: (value: MainstreamLayout) => void;
  options: NativeServiceOption[];
  disabled: boolean;
  connectorLabels: Record<string, string>;
}) {
  const { t } = useTranslation();
  const protocols = options.filter(
    (row, index, rows) =>
      rows.findIndex((other) => other.configuration_type === row.configuration_type) === index,
  );
  const patch = (index: number, change: Partial<MainstreamEntry>) => {
    const entry = value.entries[index]!;
    const protocol = options.find(
      (row) => row.connector_type === (change.connector_type ?? entry.connector_type),
    );
    onChange({
      ...value,
      entries: value.entries.map((row, i) =>
        i === index
          ? {
              ...row,
              ...change,
              native_services: protocol?.native_services.slice() ?? row.native_services,
              rerank_format:
                change.rerank_format ??
                (entry.rerank_format ||
                  (protocol?.rerank_formats.includes('generic') ? 'generic' : '')),
            }
          : row,
      ),
    });
  };
  return (
    <div className="nb-stack">
      <Note>{t('common.connections.presetNoAutoGrant')}</Note>
      {value.entries.map((entry, index) => {
        const metadata = options.find((row) => row.connector_type === entry.connector_type);
        return (
          <fieldset key={entry.id ?? 'new:' + index} className="nb-stack" disabled={disabled}>
            <legend>{t('common.connections.presetEntry', { number: index + 1 })}</legend>
            <Field label={t('common.connections.name')}>
              {(props) => (
                <input
                  {...props}
                  value={entry.name}
                  onChange={(event) => patch(index, { name: event.target.value })}
                />
              )}
            </Field>
            <Field label={t('common.connections.protocol')}>
              {(props) => (
                <select
                  {...props}
                  value={entry.connector_type}
                  onChange={(event) => {
                    const choice = options.find((row) => row.connector_type === event.target.value);
                    if (choice)
                      patch(index, {
                        connector_type: choice.connector_type,
                        native_services: choice.native_services.slice(),
                        rerank_format: choice.rerank_formats.includes('generic') ? 'generic' : '',
                      });
                  }}
                >
                  {!protocols.some((row) => row.connector_type === entry.connector_type) ? (
                    <option value={entry.connector_type}>
                      {t(connectorLabels[entry.connector_type])}
                    </option>
                  ) : null}
                  {protocols.map((row) => (
                    <option key={row.connector_type} value={row.connector_type}>
                      {t(connectorLabels[row.configuration_type])}
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
                  value={entry.base_url}
                  onChange={(event) => patch(index, { base_url: event.target.value })}
                />
              )}
            </Field>
            <ProtocolRerankField
              formats={metadata?.rerank_formats}
              value={entry.rerank_format || null}
              onChange={(format) => patch(index, { rerank_format: format })}
              disabled={disabled}
            />
            <Field label={t('common.connections.enabled')}>
              {(props) => (
                <input
                  {...props}
                  type="checkbox"
                  checked={entry.enabled}
                  onChange={(event) => patch(index, { enabled: event.target.checked })}
                />
              )}
            </Field>
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              onClick={() =>
                onChange({
                  ...value,
                  entries: value.entries.filter((_, i) => i !== index),
                  model_mappings: entry.id
                    ? value.model_mappings
                        .map((row) => ({
                          ...row,
                          entries: row.entries.filter((item) => item.entry_id !== entry.id),
                        }))
                        .filter((row) => row.entries.length)
                    : value.model_mappings,
                })
              }
            >
              {t('common.connections.removeEntry')}
            </button>
          </fieldset>
        );
      })}
      <button
        type="button"
        className="nb-btn nb-btn--secondary"
        disabled={disabled || !protocols.length}
        onClick={() => {
          const first = protocols[0]!;
          onChange({
            ...value,
            entries: [
              ...value.entries,
              {
                name: '',
                connector_type: first.connector_type,
                base_url: '',
                native_services: first.native_services.slice(),
                rerank_format: first.rerank_formats.includes('generic') ? 'generic' : '',
                enabled: true,
              },
            ],
          });
        }}
      >
        {t('common.connections.addEntry')}
      </button>
      <Fold title={t('common.connections.discoverySuggestion')}>
        <Field label={t('common.connections.addDiscoverySuggestion')}>
          {(props) => (
            <input
              {...props}
              type="checkbox"
              checked={value.discovery_source !== null}
              disabled={disabled}
              onChange={(event) =>
                onChange({
                  ...value,
                  discovery_source: event.target.checked
                    ? { base_url: '', list_format: DISCOVERY_FORMATS[0] }
                    : null,
                })
              }
            />
          )}
        </Field>
        {value.discovery_source ? (
          <>
            <Field label={t('common.connections.discoveryBaseURL')}>
              {(props) => (
                <input
                  {...props}
                  type="url"
                  value={value.discovery_source!.base_url}
                  disabled={disabled}
                  onChange={(event) =>
                    onChange({
                      ...value,
                      discovery_source: {
                        ...value.discovery_source!,
                        base_url: event.target.value,
                      },
                    })
                  }
                />
              )}
            </Field>
            <Field label={t('common.connections.listFormat')}>
              {(props) => (
                <select
                  {...props}
                  value={value.discovery_source!.list_format}
                  disabled={disabled}
                  onChange={(event) =>
                    onChange({
                      ...value,
                      discovery_source: {
                        ...value.discovery_source!,
                        list_format: event.target.value as DiscoveryPreset['list_format'],
                      },
                    })
                  }
                >
                  {DISCOVERY_FORMATS.map((format) => (
                    <option key={format} value={format}>
                      {t(`common.connections.discoveryFormats.${format}`)}
                    </option>
                  ))}
                </select>
              )}
            </Field>
          </>
        ) : null}
      </Fold>
      <Mappings value={value} onChange={onChange} disabled={disabled} />
    </div>
  );
}
function Mappings({
  value,
  onChange,
  disabled,
}: {
  value: MainstreamLayout;
  onChange: (value: MainstreamLayout) => void;
  disabled: boolean;
}) {
  const { t } = useTranslation();
  const saved = value.entries.filter((row) => row.id);
  const change = (index: number, patch: Partial<ModelMapping>) =>
    onChange({
      ...value,
      model_mappings: value.model_mappings.map((row, i) =>
        i === index ? { ...row, ...patch } : row,
      ),
    });
  return (
    <Fold title={t('common.connections.modelMappings')}>
      <Note>{t('common.connections.saveEntriesBeforeMappings')}</Note>
      {value.model_mappings.map((row, index) => (
        <fieldset key={index} className="nb-stack" disabled={disabled}>
          <legend>{t('common.connections.modelMapping', { number: index + 1 })}</legend>
          <Field label={t('common.connections.catalogModel')}>
            {(props) => (
              <input
                {...props}
                value={row.model_id}
                onChange={(event) => change(index, { model_id: event.target.value })}
              />
            )}
          </Field>
          {saved.map((entry) => {
            const mapped = row.entries.find((item) => item.entry_id === entry.id);
            const set = (patch: Partial<ModelMapping['entries'][number]>) =>
              change(index, {
                entries: row.entries.map((item) =>
                  item.entry_id === entry.id ? { ...item, ...patch } : item,
                ),
              });
            return (
              <div key={entry.id} className="nb-stack">
                <Field label={entry.name || entry.base_url}>
                  {(props) => (
                    <input
                      {...props}
                      type="checkbox"
                      checked={Boolean(mapped)}
                      onChange={(event) =>
                        change(index, {
                          entries: event.target.checked
                            ? [
                                ...row.entries,
                                {
                                  entry_id: entry.id!,
                                  upstream_model_id: row.model_id,
                                  native_services: entry.native_services.slice(),
                                },
                              ]
                            : row.entries.filter((item) => item.entry_id !== entry.id),
                        })
                      }
                    />
                  )}
                </Field>
                {mapped ? (
                  <>
                    <Field label={t('common.connections.upstreamModel')}>
                      {(props) => (
                        <input
                          {...props}
                          value={mapped.upstream_model_id}
                          onChange={(event) => set({ upstream_model_id: event.target.value })}
                        />
                      )}
                    </Field>
                    <NativeServicesField
                      options={entry.native_services}
                      value={mapped.native_services}
                      onChange={(native_services) => set({ native_services })}
                    />
                  </>
                ) : null}
              </div>
            );
          })}
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            onClick={() =>
              onChange({
                ...value,
                model_mappings: value.model_mappings.filter((_, i) => i !== index),
              })
            }
          >
            {t('common.connections.removeMapping')}
          </button>
        </fieldset>
      ))}
      <button
        type="button"
        className="nb-btn nb-btn--secondary"
        disabled={disabled || !saved.length || value.entries.some((row) => !row.id)}
        onClick={() =>
          onChange({
            ...value,
            model_mappings: [...value.model_mappings, { model_id: '', entries: [] }],
          })
        }
      >
        {t('common.connections.addMapping')}
      </button>
    </Fold>
  );
}

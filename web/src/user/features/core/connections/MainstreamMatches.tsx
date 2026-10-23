import { useTranslation } from 'react-i18next';
import type { MainstreamEntry } from '@shared/connections';
import { NATIVE_SERVICE_LABEL_KEYS } from '@shared/nativeServices';
import { Fold, Note } from '@shared/components/ui';
import { ConnectorLabel } from '../components';
import type { ConnectorType, EndpointCreateOptions, MainstreamChannelOption } from '../types';
import { useMainstreamMatchingCopy } from './mainstreamMatchingCopy';

export function MainstreamMatches({
  options,
  disabled,
  onAdopt,
}: {
  options: EndpointCreateOptions | undefined;
  disabled: boolean;
  onAdopt: (
    options: EndpointCreateOptions,
    channel: MainstreamChannelOption,
    entry: MainstreamEntry,
  ) => void;
}) {
  const { t } = useTranslation();
  const copy = useMainstreamMatchingCopy();
  const matches = options?.matching_mainstream_entries ?? [];
  if (!options || matches.length === 0) return null;
  return (
    <section className="nb-stack nb-stack--sm" aria-label={copy.title}>
      <Note title={copy.title}>{copy.help}</Note>
      {matches.map((match) => {
        const channel = options.mainstream_channels.find((item) => item.id === match.channel_id);
        const entry = channel?.entries?.find((item) => item.id === match.entry_id);
        if (!channel || !entry) return null;
        const configuration = options.native_service_options?.find(
          (option) => option.connector_type === entry.connector_type,
        )?.configuration_type;
        const mappings =
          channel.model_mappings?.flatMap((model) =>
            model.entries
              .filter((item) => item.entry_id === entry.id)
              .map((item) => ({ model: model.model_id, ...item })),
          ) ?? [];
        const title = channel.name + (entry.name ? ' · ' + entry.name : '');
        return (
          <Fold key={channel.id + ':' + entry.id} title={title} defaultOpen={matches.length <= 3}>
            <dl className="nb-facts connection-match-facts">
              <div>
                <dt>{t('common.connections.protocol')}</dt>
                <dd>
                  <ConnectorLabel
                    value={(configuration ?? entry.connector_type) as ConnectorType}
                  />
                </dd>
              </div>
              <div>
                <dt>{t('common.connections.entryURL')}</dt>
                <dd className="mono">{entry.base_url}</dd>
              </div>
              <div>
                <dt>{copy.services}</dt>
                <dd>
                  {entry.native_services
                    .map((service) => t(NATIVE_SERVICE_LABEL_KEYS[service]))
                    .join(' · ')}
                </dd>
              </div>
              {channel.discovery_source ? (
                <div>
                  <dt>{copy.discovery}</dt>
                  <dd>
                    <span className="mono">{channel.discovery_source.base_url}</span>
                    <br />
                    {t(
                      `common.connections.discoveryFormats.${channel.discovery_source.list_format}`,
                    )}
                  </dd>
                </div>
              ) : null}
              {mappings.length ? (
                <div>
                  <dt>{copy.models}</dt>
                  <dd>
                    <ul>
                      {mappings.map((mapping) => (
                        <li key={mapping.model}>
                          <span className="mono">
                            {mapping.model} → {mapping.upstream_model_id}
                          </span>
                          <br />
                          {mapping.native_services
                            .map((service) => t(NATIVE_SERVICE_LABEL_KEYS[service]))
                            .join(' · ')}
                        </li>
                      ))}
                    </ul>
                  </dd>
                </div>
              ) : null}
            </dl>
            <div className="nb-actions">
              <button
                type="button"
                className="nb-btn nb-btn--secondary"
                disabled={disabled}
                aria-label={copy.usePreset + ': ' + title}
                onClick={() => onAdopt(options, channel, entry)}
              >
                {copy.usePreset}
              </button>
            </div>
          </Fold>
        );
      })}
    </section>
  );
}

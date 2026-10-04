import { useRegisteredCopy } from '@shared/i18n/useRegisteredCopy';
import { FilterBar } from '@shared/components/ui';
import { useEffect, useState } from 'react';
import { useCoreCopy } from './copy';
import { CoreEmpty, ConnectorLabel } from './components';
import { useEndpointCreateOptions } from './queries';
import type { ResourceFilters } from './resourceFilters';
import type { ResourceFilterControl } from './useResourceFilters';

const pageCopyKeys = {
  'user.services.filters': 'user.services.filters',
  'user.services.removeFilter': 'user.services.removeFilter',
} as const;

function ConnectorFilter({ control }: { control: ResourceFilterControl }) {
  const { t } = useCoreCopy();
  const options = useEndpointCreateOptions(control.accountId);
  const values = [
    ...new Set([
      ...(options.data?.base_connector_types ?? []),
      ...(options.data?.mainstream_channels.map((channel) => channel.connector_type) ?? []),
    ]),
  ];
  return (
    <label>
      <span>{t('endpoints.connector')}</span>
      <select
        value={control.filters.connector_type ?? ''}
        onChange={(event) =>
          control.update((current) => ({ ...current, connector_type: event.target.value }))
        }
      >
        <option value="">{t('filters.all')}</option>
        {values.map((value) => (
          <option key={value} value={value}>
            <ConnectorLabel value={value} />
          </option>
        ))}
      </select>
    </label>
  );
}

export function ResourceFilterBar({ control }: { control: ResourceFilterControl }) {
  return (
    <ResourceFilterForm
      key={control.kind === 'models' ? `${control.scope}:${control.identity}` : control.scope}
      control={control}
    />
  );
}

function ResourceFilterForm({ control }: { control: ResourceFilterControl }) {
  const { t } = useCoreCopy();
  const { t: ui } = useRegisteredCopy(pageCopyKeys);
  const [query, setQuery] = useState(control.filters.q ?? '');
  const [provider, setProvider] = useState(control.filters.provider ?? '');
  const [invalid, setInvalid] = useState(false);
  useEffect(() => {
    if (control.kind !== 'models') setQuery(control.filters.q ?? '');
  }, [control.identity, control.kind, control.filters.q]);
  const select = (
    field: keyof ResourceFilters,
    label: string,
    values: readonly (readonly [string, string])[],
  ) => (
    <label>
      <span>{label}</span>
      <select
        value={control.filters[field] ?? ''}
        onChange={(event) =>
          control.update((current) => ({ ...current, [field]: event.target.value }))
        }
      >
        <option value="">{t('filters.all')}</option>
        {values.map(([value, text]) => (
          <option key={value} value={value}>
            {text}
          </option>
        ))}
      </select>
    </label>
  );
  if (control.kind !== 'models') {
    const labels: Partial<Record<keyof ResourceFilters, string>> = {
      q: t('common.search'),
      connector_type: t('endpoints.connector'),
      source: t('filters.source'),
      state: t('filters.state'),
      enabled: t('filters.enabled'),
      donated: t('filters.donated'),
      suspension_state: t('filters.security'),
    };
    const valueLabels: Record<string, string> = {
      mainstream: t('filters.mainstream'),
      custom: t('filters.custom'),
      available: t('common.available'),
      endpoint_disabled: t('common.disabled'),
      no_keys: t('filters.noKeys'),
      no_usable_key: t('filters.noUsableKeys'),
      none: t('filters.securityNone'),
      security_processing: t('filters.securityProcessing'),
      'openai-compatible': t('connector.openai'),
      'anthropic-compatible': t('connector.anthropic'),
      'ai-sdk-gateway-v3': t('connector.gateway'),
    };
    const chips = Object.entries(control.filters)
      .filter(([, value]) => Boolean(value))
      .map(([field, value]) => {
        const key = field as keyof ResourceFilters;
        const shown =
          key === 'enabled'
            ? value === 'true'
              ? t('common.enabled')
              : t('common.disabled')
            : key === 'donated'
              ? value === 'true'
                ? t('common.yes')
                : t('common.no')
              : (valueLabels[value] ?? value);
        return {
          key,
          label: `${labels[key]}: ${shown}`,
          removeLabel: ui('user.services.removeFilter', { label: labels[key] ?? key }),
          onRemove: () => control.update((current) => ({ ...current, [key]: '' })),
        };
      });
    return (
      <>
        <FilterBar
          ariaLabel={t('filters.title')}
          persistKey={`user-resource-filters:${control.kind}:${control.scope}`}
          secondaryLabel={ui('user.services.filters')}
          activeCount={chips.length}
          search={
            <>
              <label>
                <span className="nb-sr">{t('common.search')}</span>
                <input
                  className="nb-input"
                  type="search"
                  value={query}
                  aria-invalid={invalid}
                  onChange={(event) => setQuery(event.target.value)}
                />
              </label>
              <button className="nb-btn nb-btn--secondary" type="submit">
                {t('common.search')}
              </button>
            </>
          }
          secondary={
            control.kind === 'endpoints' ? (
              <>
                <ConnectorFilter control={control} />
                {select('source', t('filters.source'), [
                  ['mainstream', t('filters.mainstream')],
                  ['custom', t('filters.custom')],
                ])}
                {select('state', t('filters.state'), [
                  ['available', t('common.available')],
                  ['endpoint_disabled', t('common.disabled')],
                  ['no_keys', t('filters.noKeys')],
                  ['no_usable_key', t('filters.noUsableKeys')],
                ])}
              </>
            ) : (
              <>
                {select('enabled', t('filters.enabled'), [
                  ['true', t('common.enabled')],
                  ['false', t('common.disabled')],
                ])}
                {select('donated', t('filters.donated'), [
                  ['true', t('common.yes')],
                  ['false', t('common.no')],
                ])}
                {select('suspension_state', t('filters.security'), [
                  ['none', t('filters.securityNone')],
                  ['security_processing', t('filters.securityProcessing')],
                ])}
              </>
            )
          }
          chips={chips}
          onClearAll={control.clear}
          clearAllLabel={t('filters.clear')}
          onSubmit={() => {
            try {
              control.update((current) => ({ ...current, q: query }));
              setInvalid(false);
            } catch {
              setInvalid(true);
            }
          }}
        />
        {invalid ? (
          <p role="alert" className="field-error">
            {t('filters.invalid')}
          </p>
        ) : null}
      </>
    );
  }
  const labels = {
    q: t('common.search'),
    provider: t('models.provider'),
    route_strategy: t('models.strategy'),
    connection_state: t('filters.connection'),
  };
  const choices = {
    ordered: t('models.ordered'),
    random: t('models.random'),
    available: t('common.available'),
    unavailable: t('filters.unavailable'),
    unconfigured: t('filters.unconfigured'),
  };
  const chips = Object.entries(control.filters)
    .filter(([, value]) => Boolean(value))
    .map(([field, value]) => ({
      key: field,
      label: `${labels[field as keyof typeof labels]}: ${choices[value as keyof typeof choices] ?? value}`,
      removeLabel: ui('user.services.removeFilter', {
        label: labels[field as keyof typeof labels],
      }),
      onRemove: () => control.update((current) => ({ ...current, [field]: '' })),
    }));
  return (
    <>
      <FilterBar
        ariaLabel={t('filters.title')}
        persistKey={`user-resource-filters:models:${control.scope}`}
        secondaryLabel={ui('user.services.filters')}
        activeCount={chips.length}
        chips={chips}
        onClearAll={control.clear}
        clearAllLabel={t('filters.clear')}
        search={
          <>
            <label>
              <span className="nb-sr">{t('common.search')}</span>
              <input
                value={query}
                type="search"
                aria-invalid={invalid}
                onChange={(event) => setQuery(event.target.value)}
              />
            </label>
            <button type="submit" className="btn btn-secondary">
              {t('common.search')}
            </button>
          </>
        }
        secondary={
          <>
            <label>
              <span>{t('models.provider')}</span>
              <input
                value={provider}
                aria-invalid={invalid}
                onChange={(event) => setProvider(event.target.value)}
              />
            </label>
            {select('route_strategy', t('models.strategy'), [
              ['ordered', t('models.ordered')],
              ['random', t('models.random')],
            ])}
            {select('connection_state', t('filters.connection'), [
              ['available', t('common.available')],
              ['unavailable', t('filters.unavailable')],
              ['unconfigured', t('filters.unconfigured')],
            ])}
          </>
        }
        onSubmit={() => {
          try {
            control.update((current) => ({ ...current, q: query, provider }));
            setInvalid(false);
          } catch {
            setInvalid(true);
          }
        }}
      />
      {invalid ? (
        <p role="alert" className="field-error">
          {t('filters.invalid')}
        </p>
      ) : null}
    </>
  );
}

export function FilteredResourceEmpty({ control }: { control: ResourceFilterControl }) {
  const { t } = useCoreCopy();
  return (
    <CoreEmpty
      title={t('filters.empty')}
      body={t('filters.emptyBody')}
      action={
        <button type="button" className="btn btn-secondary" onClick={control.clear}>
          {t('filters.clear')}
        </button>
      }
    />
  );
}

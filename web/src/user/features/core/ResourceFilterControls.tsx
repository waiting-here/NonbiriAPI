import { useState } from 'react';
import { useCoreCopy } from './copy';
import { CoreEmpty, ConnectorLabel } from './components';
import { useEndpointCreateOptions } from './queries';
import type { ResourceFilters } from './resourceFilters';
import type { ResourceFilterControl } from './useResourceFilters';

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
  return <ResourceFilterForm key={`${control.scope}:${control.identity}`} control={control} />;
}

function ResourceFilterForm({ control }: { control: ResourceFilterControl }) {
  const { t } = useCoreCopy();
  const [query, setQuery] = useState(control.filters.q ?? '');
  const [provider, setProvider] = useState(control.filters.provider ?? '');
  const [invalid, setInvalid] = useState(false);
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
  return (
    <form
      className="core-resource-filters"
      aria-label={t('filters.title')}
      onSubmit={(event) => {
        event.preventDefault();
        try {
          control.update((current) => ({
            ...current,
            q: query,
            ...(control.kind === 'models' ? { provider } : {}),
          }));
          setInvalid(false);
        } catch {
          setInvalid(true);
        }
      }}
    >
      <label>
        <span>{t('common.search')}</span>
        <input
          value={query}
          type="search"
          aria-invalid={invalid}
          onChange={(event) => setQuery(event.target.value)}
        />
      </label>
      {control.kind === 'endpoints' ? (
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
      ) : control.kind === 'keys' ? (
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
      ) : (
        <>
          <label>
            <span>{t('models.provider')}</span>
            <input
              value={provider}
              onChange={(event) => setProvider(event.target.value)}
              aria-invalid={invalid}
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
      )}
      <div className="core-row-actions">
        <button type="submit" className="btn btn-secondary">
          {t('common.search')}
        </button>
        <button type="button" className="btn btn-quiet" onClick={control.clear}>
          {t('filters.clear')}
        </button>
      </div>
      {invalid ? (
        <p role="alert" className="field-error">
          {t('filters.invalid')}
        </p>
      ) : null}
    </form>
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

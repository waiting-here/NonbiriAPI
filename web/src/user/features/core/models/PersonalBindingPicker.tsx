import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { Field, FilterBar, Fold, Note, Segmented } from '@shared/components/ui';
import { ConnectionUnavailable } from '@shared/components/ConnectionUnavailable';
import { PagePagination } from '@shared/operations/PagePagination';
import { usePagePager } from '@shared/operations/usePagePager';
import { ConnectorLabel, CoreErrorPanel, CoreLoading } from '../components';
import { connectionAPI } from '../connections/api';
import { connectionKeys } from '../connections/ResourceList';
import type { Connection } from '../connections/types';
import { useNumberedBindingCandidates } from '../modelNumberedQueries';
import { useNumberedEndpointKeys } from '../numberedQueries';
import { listEndpointsPage } from '../pageApi';
import { coreKeys } from '../queries';
import type { BindingCandidate, CatalogSourceType, Endpoint, EndpointKey } from '../types';
import { candidateIdentity } from './bindingIdentity';
import { useBindingCopy } from './bindingCopy';
import { isAccessLoss } from './feedback';
import { ManualSource } from './ManualSource';
import { useModelText } from './copy';
import './binding-picker.css';

export function PersonalBindingPicker({
  accountId,
  modelId,
  selected,
  bound,
  onToggle,
  locked: externalLocked = false,
  focusSearch = false,
  onSearchFocused,
  onAccessLoss,
}: {
  accountId: string;
  modelId: string;
  selected: ReadonlySet<string>;
  bound: ReadonlySet<string>;
  onToggle: (candidate: BindingCandidate) => void;
  locked?: boolean;
  focusSearch?: boolean;
  onSearchFocused?: () => void;
  onAccessLoss?: (error: Error) => void;
}) {
  const copy = useBindingCopy();
  const text = useModelText();
  const [manualLocked, setManualLocked] = useState(false);
  const [manualSaved, setManualSaved] = useState(false);
  const locked = externalLocked || manualLocked;
  const [service, setService] = useState<Connection | null>(null);
  const [serviceQuery, setServiceQuery] = useState('');
  const [endpoint, setEndpoint] = useState<Endpoint | null>(null);
  const [endpointQuery, setEndpointQuery] = useState('');
  const [entryKey, setEntryKey] = useState<EndpointKey | null>(null);
  const [keyQuery, setKeyQuery] = useState('');
  const [queryDraft, setQueryDraft] = useState('');
  const [query, setQuery] = useState('');
  const [source, setSource] = useState<CatalogSourceType | ''>('');
  const [step, setStep] = useState<'endpoints' | 'keys' | 'models'>('endpoints');
  const searchInput = useRef<HTMLInputElement>(null);
  const servicePager = usePagePager({
    station: 'user',
    listType: 'binding-services',
    scopeKey: accountId,
    resetKey: serviceQuery,
  });
  const endpointPager = usePagePager({
    station: 'user',
    listType: 'models-binding-endpoints',
    scopeKey: accountId,
    resetKey: `${service?.id ?? ''}:${endpointQuery}`,
  });
  const keyPager = usePagePager({
    station: 'user',
    listType: 'models-binding-keys',
    scopeKey: accountId,
    resetKey: `${endpoint?.id ?? ''}:${keyQuery}`,
  });
  const modelPager = usePagePager({
    station: 'user',
    listType: 'models-binding-candidates',
    scopeKey: `${accountId}:${modelId}`,
    resetKey: `${entryKey?.id ?? ''}:${query}:${source}`,
  });
  const services = useQuery({
    queryKey: [
      ...connectionKeys.root(accountId),
      'binding-services',
      serviceQuery,
      servicePager.page,
      servicePager.pageSize,
    ],
    queryFn: ({ signal }) => connectionAPI.list(servicePager, serviceQuery.trim(), signal),
  });
  const endpoints = useQuery({
    queryKey: [
      ...coreKeys.endpointsRoot(accountId),
      'binding-picker',
      service?.id ?? '',
      endpointQuery,
      endpointPager.page,
      endpointPager.pageSize,
    ],
    queryFn: ({ signal }) =>
      service
        ? connectionAPI.entries(service.id, endpointPager, endpointQuery.trim(), signal)
        : listEndpointsPage(
            { page: endpointPager.page, pageSize: endpointPager.pageSize },
            signal,
            { q: endpointQuery.trim() },
          ),
  });
  const keys = useNumberedEndpointKeys(
    accountId,
    endpoint?.id,
    { page: keyPager.page, pageSize: keyPager.pageSize },
    Boolean(endpoint),
    {
      q: keyQuery.trim(),
    },
  );
  const candidates = useNumberedBindingCandidates(
    accountId,
    modelId,
    { endpointId: endpoint?.id, keyId: entryKey?.id, query, source: source || undefined },
    { page: modelPager.page, pageSize: modelPager.pageSize },
    Boolean(endpoint && entryKey),
  );
  const accessError = [services.error, endpoints.error, keys.error, candidates.error].find(
    isAccessLoss,
  );
  useEffect(() => {
    if (accessError) onAccessLoss?.(accessError);
  }, [accessError, onAccessLoss]);
  useEffect(() => {
    const timer = window.setTimeout(() => setQuery(queryDraft.trim()), 250);
    return () => window.clearTimeout(timer);
  }, [queryDraft]);
  useEffect(() => {
    if (!focusSearch || locked) return;
    searchInput.current?.focus();
    onSearchFocused?.();
  }, [focusSearch, locked, onSearchFocused]);
  const chooseEndpoint = (next: Endpoint) => {
    setManualSaved(false);
    setEndpoint(next);
    setEntryKey(null);
    setKeyQuery('');
    setQueryDraft('');
    setQuery('');
    setStep('keys');
  };
  const chooseKey = (next: EndpointKey) => {
    setManualSaved(false);
    setEntryKey(next);
    setQueryDraft('');
    setQuery('');
    setStep('models');
  };
  // A single available branch is already a complete choice; avoid empty pickers.
  useEffect(() => {
    const rows = endpoints.data?.data;
    if (
      endpoint ||
      endpointQuery ||
      endpoints.data?.pagination.total_items !== '1' ||
      !rows?.[0]?.enabled
    )
      return;
    let active = true;
    queueMicrotask(() => {
      if (active) chooseEndpoint(rows[0]!);
    });
    return () => {
      active = false;
    };
  }, [endpoint, endpointQuery, endpoints.data]);
  useEffect(() => {
    const rows = keys.data?.data;
    if (
      entryKey ||
      keyQuery ||
      keys.data?.pagination.total_items !== '1' ||
      !rows?.[0]?.enabled ||
      rows[0].suspension_state !== 'none' ||
      rows[0].credential_available === false
    )
      return;
    let active = true;
    queueMicrotask(() => {
      if (active) chooseKey(rows[0]!);
    });
    return () => {
      active = false;
    };
  }, [entryKey, keyQuery, keys.data]);

  const keyLabel = (key: EndpointKey) =>
    `${key.credential_note || key.note ? (key.credential_note || key.note) + ' · ' : ''}${key.display_head}…${key.display_tail}`;
  const renderError = (result: { error: Error | null; refetch: () => unknown }) =>
    result.error ? (
      <CoreErrorPanel compact error={result.error} onRetry={() => void result.refetch()} />
    ) : null;
  return (
    <div className="personal-binding-picker nb-stack">
      <FilterBar
        as="div"
        ariaLabel={copy.filterService}
        activeCount={service ? 1 : 0}
        secondaryAlwaysVisible
        onSubmit={() => undefined}
        onReset={() => setServiceQuery('')}
        search={
          <Field label={copy.searchServices}>
            {(props) => (
              <input
                {...props}
                ref={searchInput}
                type="search"
                placeholder={copy.searchServices}
                value={serviceQuery}
                maxLength={128}
                disabled={locked}
                onChange={(event) => setServiceQuery(event.target.value)}
              />
            )}
          </Field>
        }
        secondary={
          <Field label={copy.filterService}>
            {(props) => (
              <select
                {...props}
                value={service?.id ?? ''}
                disabled={locked || services.isFetching}
                onChange={(event) => {
                  const next =
                    services.data?.data.find((row) => row.id === event.target.value) ?? null;
                  setService(next);
                  setManualSaved(false);
                  setEndpoint(null);
                  setEntryKey(null);
                  setEndpointQuery('');
                  setKeyQuery('');
                  setQueryDraft('');
                  setQuery('');
                  setStep('endpoints');
                }}
              >
                <option value="">{copy.allServices}</option>
                {service && !services.data?.data.some((row) => row.id === service.id) ? (
                  <option value={service.id}>{service.display_name}</option>
                ) : null}
                {services.data?.data.map((row) => (
                  <option key={row.id} value={row.id}>
                    {row.display_name}
                  </option>
                ))}
              </select>
            )}
          </Field>
        }
      />
      {renderError(services)}
      {services.data && services.data.pagination.total_pages !== '1' ? (
        <PagePagination
          metadata={services.data.pagination}
          requestedPage={servicePager.page}
          busy={services.isFetching || locked}
          onPageChange={servicePager.setPage}
          onPageSizeChange={servicePager.setPageSize}
        />
      ) : null}
      <div className="personal-binding-picker__path nb-actions" aria-label={copy.addSources}>
        <button
          type="button"
          className="nb-btn nb-btn--ghost"
          disabled={locked}
          onClick={() => setStep('endpoints')}
        >
          {copy.endpoints}
          {endpoint ? ` · ${endpoint.note || endpoint.base_url}` : ''}
        </button>
        {endpoint ? (
          <button
            type="button"
            className="nb-btn nb-btn--ghost"
            disabled={locked}
            onClick={() => setStep('keys')}
          >
            {copy.keys}
            {entryKey ? ` · ${keyLabel(entryKey)}` : ''}
          </button>
        ) : null}
        {entryKey ? (
          <button
            type="button"
            className="nb-btn nb-btn--ghost"
            disabled={locked}
            onClick={() => setStep('models')}
          >
            {copy.models}
          </button>
        ) : null}
      </div>
      <div className="personal-binding-picker__columns">
        <section
          className="personal-binding-picker__browse"
          aria-label={step === 'endpoints' ? copy.endpoints : copy.keys}
        >
          {step === 'endpoints' || !endpoint ? (
            <>
              <Field label={copy.endpoints}>
                {(props) => (
                  <input
                    {...props}
                    type="search"
                    placeholder={copy.searchEndpoints}
                    value={endpointQuery}
                    maxLength={128}
                    disabled={locked}
                    onChange={(event) => setEndpointQuery(event.target.value)}
                  />
                )}
              </Field>
              {endpoints.isPending ? (
                <CoreLoading compact />
              ) : endpoints.error ? (
                renderError(endpoints)
              ) : (
                <>
                  {!endpoints.data?.data.length ? (
                    <Note>{copy.emptyEndpoints}</Note>
                  ) : (
                    <ul className="personal-binding-picker__list">
                      {endpoints.data.data.map((row) => (
                        <li key={row.id}>
                          <button
                            type="button"
                            className="personal-binding-picker__choice"
                            aria-pressed={endpoint?.id === row.id}
                            disabled={locked || !row.enabled}
                            onClick={() => chooseEndpoint(row)}
                          >
                            <strong>
                              {row.connection_name ? `${row.connection_name} · ` : ''}
                              {row.note || row.base_url}
                            </strong>{' '}
                            <span className="nb-small nb-muted">{row.base_url}</span>
                            <ConnectorLabel value={row.configuration_type ?? row.connector_type} />
                          </button>
                        </li>
                      ))}
                    </ul>
                  )}
                  {endpoints.data ? (
                    <PagePagination
                      metadata={endpoints.data.pagination}
                      requestedPage={endpointPager.page}
                      busy={endpoints.isFetching || locked}
                      onPageChange={endpointPager.setPage}
                      onPageSizeChange={endpointPager.setPageSize}
                    />
                  ) : null}
                </>
              )}
            </>
          ) : (
            <>
              <div className="nb-actions">
                <button
                  type="button"
                  className="nb-btn nb-btn--ghost"
                  disabled={locked}
                  onClick={() => setStep('endpoints')}
                >
                  {copy.changeEndpoint}
                </button>
              </div>
              <Field label={copy.keys}>
                {(props) => (
                  <input
                    {...props}
                    type="search"
                    placeholder={copy.searchKeys}
                    value={keyQuery}
                    maxLength={128}
                    disabled={locked}
                    onChange={(event) => setKeyQuery(event.target.value)}
                  />
                )}
              </Field>
              {keys.isPending ? (
                <CoreLoading compact />
              ) : keys.error ? (
                renderError(keys)
              ) : (
                <>
                  {!keys.data?.data.length ? (
                    <Note>{copy.emptyKeys}</Note>
                  ) : (
                    <ul className="personal-binding-picker__list">
                      {keys.data.data.map((row) => (
                        <li key={row.id}>
                          <button
                            type="button"
                            className="personal-binding-picker__choice"
                            aria-pressed={entryKey?.id === row.id}
                            disabled={
                              locked ||
                              !row.enabled ||
                              row.suspension_state !== 'none' ||
                              row.credential_available === false
                            }
                            onClick={() => chooseKey(row)}
                          >
                            <strong>{keyLabel(row)}</strong>
                            {!row.enabled || row.credential_available === false ? (
                              <span className="nb-small">{copy.unavailable}</span>
                            ) : null}
                          </button>
                        </li>
                      ))}
                    </ul>
                  )}
                  {keys.data ? (
                    <PagePagination
                      metadata={keys.data.pagination}
                      requestedPage={keyPager.page}
                      busy={keys.isFetching || locked}
                      onPageChange={keyPager.setPage}
                      onPageSizeChange={keyPager.setPageSize}
                    />
                  ) : null}
                </>
              )}
            </>
          )}
        </section>
        <section
          className="personal-binding-picker__models nb-stack"
          aria-label={copy.models}
          aria-busy={candidates.isFetching || queryDraft.trim() !== query}
        >
          {!endpoint ? (
            <Note>{copy.chooseEndpoint}</Note>
          ) : !entryKey ? (
            <Note>{copy.chooseKey}</Note>
          ) : (
            <>
              <div className="nb-stack nb-stack--sm">
                <strong>
                  {endpoint.connection_name ? `${endpoint.connection_name} · ` : ''}
                  {endpoint.note || endpoint.base_url} · {keyLabel(entryKey)}
                </strong>
                <span className="nb-small nb-muted">{endpoint.base_url}</span>
              </div>
              <FilterBar
                as="div"
                ariaLabel={copy.models}
                activeCount={source ? 1 : 0}
                onSubmit={() => setQuery(queryDraft.trim())}
                onReset={() => {
                  setQueryDraft('');
                  setQuery('');
                  setSource('');
                }}
                search={
                  <Field label={copy.searchModels}>
                    {(props) => (
                      <input
                        {...props}
                        type="search"
                        data-testid="model-source-search"
                        placeholder={copy.searchModels}
                        maxLength={256}
                        disabled={locked}
                        value={queryDraft}
                        onChange={(event) => setQueryDraft(event.target.value)}
                      />
                    )}
                  </Field>
                }
              />
              <Segmented
                label={copy.source}
                value={source}
                onChange={setSource}
                disabled={locked}
                options={[
                  { value: '', label: copy.allModels },
                  { value: 'automatic', label: copy.automatic },
                  { value: 'manual', label: copy.manual },
                ]}
              />
              {candidates.isPending ? (
                <CoreLoading compact />
              ) : candidates.error ? (
                renderError(candidates)
              ) : !candidates.data?.data.length ? (
                <Note>{copy.emptyModels}</Note>
              ) : (
                <ul className="personal-binding-picker__list">
                  {candidates.data.data.map((row) => {
                    const identity = candidateIdentity(row);
                    const alreadyBound = bound.has(identity);
                    return (
                      <li key={identity} className="personal-binding-picker__model">
                        <Field
                          label={
                            <span className="nb-stack nb-stack--sm">
                              <strong>{row.upstream_model_id}</strong>{' '}
                              <span className="nb-small nb-muted">
                                {alreadyBound
                                  ? copy.bound
                                  : row.source_types
                                      .map((type) =>
                                        type === 'automatic' ? copy.automatic : copy.manual,
                                      )
                                      .join(' + ')}
                              </span>
                            </span>
                          }
                        >
                          {(props) => (
                            <input
                              {...props}
                              type="checkbox"
                              checked={selected.has(identity)}
                              disabled={
                                locked ||
                                candidates.isFetching ||
                                alreadyBound ||
                                row.capabilities?.can_bind === false
                              }
                              onChange={() =>
                                onToggle({
                                  ...row,
                                  endpoint_note:
                                    endpoint.connection_name &&
                                    endpoint.connection_name !== row.endpoint_note
                                      ? `${endpoint.connection_name} · ${row.endpoint_note || endpoint.base_url}`
                                      : row.endpoint_note,
                                })
                              }
                            />
                          )}
                        </Field>
                        {row.capabilities?.can_bind === false &&
                        row.capabilities.unavailable_reason ? (
                          <ConnectionUnavailable reason={row.capabilities.unavailable_reason} />
                        ) : null}
                      </li>
                    );
                  })}
                </ul>
              )}
              {candidates.data ? (
                <PagePagination
                  metadata={candidates.data.pagination}
                  requestedPage={modelPager.page}
                  busy={candidates.isFetching || locked}
                  onPageChange={modelPager.setPage}
                  onPageSizeChange={modelPager.setPageSize}
                />
              ) : null}
              <Fold title={copy.addManual} lazy>
                <ManualSource
                  key={`${accountId}:${endpoint.id}:${entryKey.id}`}
                  accountId={accountId}
                  endpointId={endpoint.id}
                  keyId={entryKey.id}
                  onLock={setManualLocked}
                  onEdit={() => setManualSaved(false)}
                  onCreated={(upstream) => {
                    setQueryDraft(upstream);
                    setManualSaved(true);
                  }}
                />
                {manualSaved ? <p role="status">{text('manualSaved')}</p> : null}
              </Fold>
              <Link
                className="nb-btn nb-btn--ghost"
                aria-disabled={locked || undefined}
                onClick={(event) => {
                  if (locked) event.preventDefault();
                }}
                to={`/endpoints?${new URLSearchParams({ ...(endpoint.connection_id ? { connection_id: endpoint.connection_id } : {}), endpoint_id: endpoint.id, key_id: entryKey.id })}`}
              >
                {copy.managePair}
              </Link>
            </>
          )}
        </section>
      </div>
    </div>
  );
}

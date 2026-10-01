import { useEffect, useRef, useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import { useOperation } from '@shared/operations/useOperation';
import { PagePagination } from '@shared/operations/PagePagination';
import { usePagePager } from '@shared/operations/usePagePager';
import { isConflict, isOutcomeUnknown } from './request';
import { getBindings, getEndpoint, getModel } from './api';
import { listModelsPage } from './pageApi';
import { coreKeys, invalidateResourceDependents, useEndpointCreateOptions } from './queries';
import {
  useNumberedCatalog,
  useNumberedEndpointKeys,
  useNumberedEndpoints,
} from './numberedQueries';
import {
  canonicalBaseURLPreview,
  validateEndpointSecret,
  validateLogicalName,
  validatePersonalProviderName,
} from './normalizers';
import {
  ConnectorLabel,
  CoreErrorPanel,
  CoreLoading,
  DiscoveryStatus,
  SafeCopyValue,
} from './components';
import { useCoreCopy } from './copy';
import { useQuickstartCopy } from './quickstartCopy';
import {
  executeResource,
  readResourceResult,
  resourceStatus,
  type ResourceIntent,
  type ResourceResult,
  type ResourceStatus,
} from './resourceOperation';
import type { CatalogEntry, ConnectorType, Endpoint, Model } from './types';
import './quickstart.css';

interface ModelChoice {
  id: string;
  upstream: string;
  provider: string;
  name: string;
  selected: boolean;
  checked: boolean;
  existing?: Model;
  decision: 'append' | 'rename' | '';
  model?: Model;
  complete: boolean;
  error?: unknown;
}

export function Quickstart({ accountId, onClose }: { accountId: string; onClose: () => void }) {
  const client = useQueryClient(),
    { t: core } = useCoreCopy(),
    { t: text } = useQuickstartCopy();
  const [stage, setStage] = useState(0),
    [stopped, setStopped] = useState(false);
  const flow = useRef(new AbortController()),
    mounted = useRef(true);
  const [source, setSource] = useState<'new' | 'existing'>('new');
  const [serviceQuery, setServiceQuery] = useState(''),
    [keyQuery, setKeyQuery] = useState('');
  const [channel, setChannel] = useState(''),
    [connector, setConnector] = useState<ConnectorType>('openai-compatible');
  const [address, setAddress] = useState(''),
    [note, setNote] = useState('');
  const [endpoint, setEndpoint] = useState<Endpoint | null>(null),
    [keyId, setKeyId] = useState('');
  const [secret, setSecret] = useState(''),
    [ownership, setOwnership] = useState(false);
  const [keyNote, setKeyNote] = useState(''),
    [store, setStore] = useState(false);
  const [keyMode, setKeyMode] = useState<'new' | 'existing'>('new');
  const [manualName, setManualName] = useState(''),
    [manualProvider, setManualProvider] = useState('my-service');
  const [rows, setRows] = useState<ModelChoice[]>([]),
    [error, setError] = useState<unknown>(null);
  const [receipt, setReceipt] = useState<ResourceStatus | null>(null),
    [reading, setReading] = useState(false),
    [connecting, setConnecting] = useState(false);
  const callNames = useRef<Record<string, HTMLInputElement | null>>({});
  const receiptRef = useRef<ResourceStatus | null>(null);
  const saveReceipt = (value: ResourceStatus | null) => {
    receiptRef.current = value;
    setReceipt(value);
  };
  const options = useEndpointCreateOptions(accountId);
  const endpointPager = usePagePager({
    station: 'user',
    listType: 'quickstart-services',
    scopeKey: accountId,
    resetKey: serviceQuery,
  });
  const keyPager = usePagePager({
    station: 'user',
    listType: 'quickstart-keys',
    scopeKey: accountId,
    resetKey: `${endpoint?.id}:${keyQuery}`,
  });
  const catalogPager = usePagePager({
    station: 'user',
    listType: 'quickstart-catalog',
    scopeKey: accountId,
    resetKey: `${endpoint?.id}:${keyId}`,
  });
  const endpoints = useNumberedEndpoints(
    accountId,
    { page: endpointPager.page, pageSize: endpointPager.pageSize },
    source === 'existing',
    { q: serviceQuery },
  );
  const keys = useNumberedEndpointKeys(
    accountId,
    endpoint?.id,
    { page: keyPager.page, pageSize: keyPager.pageSize },
    keyMode === 'existing',
    { q: keyQuery },
  );
  const catalog = useNumberedCatalog(
    accountId,
    endpoint?.id,
    keyId || undefined,
    undefined,
    { page: catalogPager.page, pageSize: catalogPager.pageSize },
    stage === 2 && !stopped,
  );

  const clearSecrets = () => {
    setSecret('');
    setOwnership(false);
    setKeyNote('');
    setStore(false);
  };
  const applyResult = (result: ResourceResult) => {
    saveReceipt(null);
    setError(null);
    switch (result.kind) {
      case 'endpoint':
        setEndpoint(result.endpoint);
        if (!flow.current.signal.aborted) setStage(1);
        break;
      case 'key':
        setKeyId(result.keyId);
        if (!flow.current.signal.aborted) setStage(2);
        break;
      case 'refresh':
      case 'manual':
        if (!flow.current.signal.aborted) setStage(2);
        break;
      case 'model':
        setRows((previous) =>
          previous.map((row) =>
            row.id === result.row ? { ...row, model: result.model, error: undefined } : row,
          ),
        );
        break;
      case 'binding':
        setRows((previous) =>
          previous.map((row) =>
            row.id === result.row
              ? { ...row, model: result.model, complete: true, error: undefined }
              : row,
          ),
        );
        break;
    }
  };
  const operation = useOperation<ResourceIntent, string, ResourceResult>({
    authorityRoot: ['user', 'core'],
    clearSecrets,
    execute: async (intent, secretValue, key, context) => {
      if (intent.kind === 'key') context.commit(() => setSecret(''));
      const result = await executeResource(intent, secretValue, key, context.signal);
      context.commit(() => applyResult(result));
      return result;
    },
    reconcile: async (intent, failure, context) => {
      let confirmed = false;
      if (failure && isOutcomeUnknown(failure)) {
        if (!context.operationKey) return;
        const status = await resourceStatus(context.operationKey, context.signal);
        context.commit(() => saveReceipt(status));
        const result = await readResourceResult(intent, status, context.signal);
        context.assertCurrent();
        if (result) {
          context.commit(() => applyResult(result));
          confirmed = true;
        } else return;
      }
      await invalidateResourceDependents(client, accountId);
      context.assertCurrent();
      if (intent.kind === 'manual' || intent.kind === 'refresh')
        await client.invalidateQueries({
          queryKey: coreKeys.catalogRoot(accountId, intent.endpointId, intent.keyId),
        });
      context.assertCurrent();
      if (confirmed) return { operationConfirmed: true };
    },
  });
  const locked = operation.isPending || operation.outcome === 'unknown' || connecting || reading;

  useEffect(() => {
    mounted.current = true;
    if (flow.current.signal.aborted) flow.current = new AbortController();
    return () => {
      mounted.current = false;
      flow.current.abort();
    };
  }, []);

  const run = async (intent: ResourceIntent, secretValue?: string) => {
    const controller = flow.current;
    if (controller.signal.aborted) return null;
    setError(null);
    saveReceipt(null);
    try {
      const result = await operation.run(intent, secretValue);
      return controller.signal.aborted ? null : result;
    } catch (failure) {
      if (
        !controller.signal.aborted &&
        mounted.current &&
        !isOutcomeUnknown(failure) &&
        !isConflict(failure)
      )
        setError(failure);
      return null;
    }
  };
  const stop = () => {
    flow.current.abort();
    setStopped(true);
    operation.cancel();
    setReading(false);
    setConnecting(false);
    clearSecrets();
  };
  const leave = () => {
    stop();
    onClose();
  };
  const resume = async () => {
    flow.current = new AbortController();
    setStopped(false);
    setError(null);
    if (operation.outcome === 'unknown') await operation.check();
  };
  const createService = async (event: FormEvent) => {
    event.preventDefault();
    const selected = options.data?.mainstream_channels.find((c) => c.id === channel);
    try {
      const input = selected
        ? { source: 'mainstream' as const, channel_id: selected.id, note, enabled: true }
        : {
            source: 'custom' as const,
            connector_type: connector,
            base_url: canonicalBaseURLPreview(address),
            note,
            enabled: true,
          };
      await run({ kind: 'endpoint', input });
    } catch {
      setError(new Error(text('invalid')));
    }
  };
  const chooseService = async (id: string) => {
    setReading(true);
    setError(null);
    const controller = flow.current;
    try {
      const result = await getEndpoint(id, controller.signal);
      if (!controller.signal.aborted && mounted.current) {
        setEndpoint(result);
        setStage(1);
      }
    } catch (failure) {
      if (!controller.signal.aborted && mounted.current) setError(failure);
    } finally {
      if (mounted.current && flow.current === controller) setReading(false);
    }
  };
  const addKey = async (event: FormEvent) => {
    event.preventDefault();
    if (!endpoint || !ownership) return;
    try {
      validateEndpointSecret(secret);
      await run(
        {
          kind: 'key',
          endpointId: endpoint.id,
          input: {
            note: keyNote,
            enabled: true,
            ownership_confirmed: true,
            force_store_false: endpoint.connector_type === 'openai-compatible' && store,
          },
        },
        secret,
      );
    } catch {
      setError(new Error(text('invalid')));
    }
  };
  const selectEntry = (entry: CatalogEntry) => {
    const id = `${keyId}\u0000${entry.upstream_model_id}`;
    setRows((previous) => {
      const existing = previous.find((row) => row.id === id);
      return existing
        ? previous.map((row) => (row.id === id ? { ...row, selected: !row.selected } : row))
        : [
            ...previous,
            {
              id,
              upstream: entry.upstream_model_id,
              provider: entry.provider || 'my-service',
              name: entry.upstream_model_id,
              selected: true,
              checked: false,
              decision: '',
              complete: false,
            },
          ];
    });
  };
  const updateRow = (id: string, changes: Partial<ModelChoice>) =>
    setRows((previous) => previous.map((row) => (row.id === id ? { ...row, ...changes } : row)));
  const lookupName = async (row: ModelChoice) => {
    const controller = flow.current;
    setError(null);
    try {
      validatePersonalProviderName(row.provider);
      validateLogicalName(row.name);
      let page = '1',
        existing: Model | undefined;
      for (;;) {
        const result = await listModelsPage({ page, pageSize: 50 }, controller.signal, {
          q: `${row.provider}/${row.name}`,
          provider: row.provider,
        });
        existing = result.data.find(
          (model) => model.provider === row.provider && model.model === row.name,
        );
        if (existing || BigInt(result.pagination.page) >= BigInt(result.pagination.total_pages))
          break;
        page = String(BigInt(page) + 1n);
      }
      if (!controller.signal.aborted && mounted.current)
        setRows((previous) =>
          previous.map((current) =>
            current.id === row.id && current.provider === row.provider && current.name === row.name
              ? { ...current, checked: true, existing, decision: '', error: undefined }
              : current,
          ),
        );
      return controller.signal.aborted ? null : { ...row, checked: true, existing };
    } catch (failure) {
      if (!controller.signal.aborted && mounted.current)
        updateRow(row.id, { error: failure, checked: false });
      return null;
    }
  };
  const connect = async () => {
    const controller = flow.current;
    let selected = rows.filter((row) => row.selected && !row.complete);
    if (!keyId || !selected.length) return;
    setConnecting(true);
    try {
      const verified: ModelChoice[] = [];
      for (const row of selected) {
        if (controller.signal.aborted) return;
        const checked = row.model || row.checked ? row : await lookupName(row);
        if (!checked) return;
        verified.push(checked);
      }
      if (verified.some((row) => row.existing && !row.model && row.decision !== 'append')) return;
      selected = verified;
      for (const row of selected) {
        if (controller.signal.aborted) break;
        let model = row.model;
        if (!model && row.existing) {
          try {
            model = await getModel(row.existing.id, controller.signal);
          } catch (failure) {
            if (!controller.signal.aborted) updateRow(row.id, { error: failure });
            break;
          }
        }
        if (controller.signal.aborted) break;
        if (!model) {
          const saved = await run({
            kind: 'model',
            row: row.id,
            input: { provider: row.provider, model: row.name },
          });
          if (!saved || saved.kind !== 'model') break;
          model = saved.model;
        }
        if (controller.signal.aborted) break;
        try {
          const bindings = await getBindings(model.id, controller.signal);
          if (controller.signal.aborted) break;
          if (
            bindings.bindings.some(
              (b) => b.endpoint_key_id === keyId && b.upstream_model_id === row.upstream,
            )
          ) {
            updateRow(row.id, { model, complete: true, error: undefined });
            continue;
          }
          const saved = await run({
            kind: 'binding',
            row: row.id,
            modelId: model.id,
            revision: bindings.binding_revision,
            selection: { endpoint_key_id: keyId, upstream_model_id: row.upstream },
          });
          if (!saved) break;
        } catch (failure) {
          if (!controller.signal.aborted) updateRow(row.id, { model, error: failure });
          break;
        }
      }
    } finally {
      if (mounted.current && flow.current === controller) setConnecting(false);
    }
  };
  const checkResult = async () => {
    const controller = flow.current;
    setReading(true);
    setError(null);
    try {
      await operation.check();
      if (controller.signal.aborted) return;
      const status = receiptRef.current,
        intent = operation.variables;
      if (!stopped && status?.status === 'not_recorded' && intent) {
        if (intent.kind === 'key' && (!secret || !ownership)) return;
        await run(intent, intent.kind === 'key' ? secret : undefined);
      }
    } finally {
      if (mounted.current && flow.current === controller) setReading(false);
    }
  };
  const confirmedRows = rows.filter((row) => row.complete),
    selectedRows = rows.filter((row) => row.selected);
  const finished = selectedRows.length > 0 && selectedRows.every((row) => row.complete);
  const showRecovery =
    operation.outcome === 'unknown' ||
    receipt?.status === 'not_recorded' ||
    receipt?.status === 'expired';

  return (
    <section className="core-card quickstart" aria-labelledby="quickstart-title">
      <div className="core-card__header">
        <div>
          <h2 id="quickstart-title">{text('title')}</h2>
          <p className="core-muted">{text('body')}</p>
        </div>
        <button type="button" className="btn btn-secondary" onClick={leave}>
          {text('leave')}
        </button>
      </div>
      <ol className="quickstart-steps">
        {(['serviceStage', 'keyStage', 'modelsStage'] as const).map((key, index) => (
          <li key={key} aria-current={stage === index ? 'step' : undefined}>
            {text(key)}
          </li>
        ))}
      </ol>
      {endpoint ? (
        <p className="quickstart-fact">
          {text('serviceSaved')}{' '}
          <Link to={`/endpoints/${endpoint.id}`}>{endpoint.note || endpoint.base_url}</Link>
        </p>
      ) : null}
      {keyId && !finished ? (
        <p className="quickstart-fact">
          {text('keySaved')} {keyNote}
        </p>
      ) : null}
      {stopped ? (
        <div className="quickstart-notice" role="status">
          <p>{text('stopped')}</p>
          <button className="btn btn-primary" type="button" onClick={() => void resume()}>
            {text('continue')}
          </button>
        </div>
      ) : null}
      {showRecovery ? (
        <div className="quickstart-notice" role="status">
          <p>
            {text(
              receipt?.status === 'expired'
                ? 'expired'
                : receipt?.status === 'not_recorded'
                  ? 'notRecorded'
                  : 'unknown',
            )}
          </p>
          <div className="core-row-actions">
            <button
              type="button"
              className="btn btn-secondary"
              disabled={operation.isPending || reading}
              onClick={() => void checkResult()}
            >
              {text('checkResult')}
            </button>
          </div>
          {operation.variables?.kind === 'key' && !stopped && receipt?.status === 'not_recorded' ? (
            <>
              <label>
                {text('secretAgain')}
                <input
                  type="password"
                  autoComplete="off"
                  value={secret}
                  onChange={(e) => setSecret(e.target.value)}
                />
              </label>
              {!ownership ? (
                <label className="core-check">
                  <input
                    type="checkbox"
                    checked={ownership}
                    onChange={(e) => setOwnership(e.target.checked)}
                  />
                  {text('ownership')}
                </label>
              ) : null}
            </>
          ) : null}
          {receipt?.status === 'expired' ? (
            <button type="button" className="btn btn-secondary" onClick={leave}>
              {text('useExistingRecovery')}
            </button>
          ) : null}
        </div>
      ) : null}
      {operation.outcome === 'conflict' ? (
        <p className="field-error" role="alert">
          {text('conflict')}
        </p>
      ) : !showRecovery && error ? (
        <p className="field-error" role="alert">
          {error instanceof Error ? error.message : text('invalid')}
        </p>
      ) : null}
      {operation.outcome === 'refresh-failed' ? (
        <p role="status">
          {text('savedRefresh')}{' '}
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => void operation.refresh()}
          >
            {core('common.refresh')}
          </button>
        </p>
      ) : null}
      {!stopped && !showRecovery ? (
        <>
          {stage === 0 ? (
            <>
              <div className="core-row-actions">
                <button
                  type="button"
                  className="btn btn-secondary"
                  aria-pressed={source === 'new'}
                  disabled={locked}
                  onClick={() => setSource('new')}
                >
                  {text('newService')}
                </button>
                <button
                  type="button"
                  className="btn btn-secondary"
                  aria-pressed={source === 'existing'}
                  disabled={locked}
                  onClick={() => setSource('existing')}
                >
                  {text('existingService')}
                </button>
              </div>
              {source === 'new' ? (
                options.isPending ? (
                  <CoreLoading compact />
                ) : options.error ? (
                  <CoreErrorPanel
                    compact
                    error={options.error}
                    onRetry={() => void options.refetch()}
                  />
                ) : (
                  <form className="quickstart-form" onSubmit={(e) => void createService(e)}>
                    <label>
                      {text('channel')}
                      <select
                        aria-label={text('channel')}
                        disabled={locked}
                        value={channel}
                        onChange={(e) => setChannel(e.target.value)}
                      >
                        <option value="">{text('custom')}</option>
                        {options.data?.mainstream_channels.map((c) => (
                          <option key={c.id} value={c.id}>
                            {c.name}
                          </option>
                        ))}
                      </select>
                    </label>
                    {!channel ? (
                      <>
                        <label>
                          {text('connector')}
                          <select
                            aria-label={text('connector')}
                            disabled={locked}
                            value={connector}
                            onChange={(e) => setConnector(e.target.value as ConnectorType)}
                          >
                            {options.data?.base_connector_types.map((type) => (
                              <option key={type} value={type}>
                                {type === 'openai-compatible'
                                  ? core('connector.openai')
                                  : type === 'anthropic-compatible'
                                    ? core('connector.anthropic')
                                    : core('connector.gateway')}
                              </option>
                            ))}
                          </select>
                        </label>
                        <label>
                          {text('address')}
                          <input
                            aria-label={text('address')}
                            required
                            type="url"
                            placeholder="https://api.example.com/v1"
                            value={address}
                            disabled={locked}
                            onChange={(e) => setAddress(e.target.value)}
                          />
                          <small>{text('addressHelp')}</small>
                        </label>
                      </>
                    ) : null}
                    <label>
                      {text('serviceNote')}
                      <input
                        aria-label={text('serviceNote')}
                        value={note}
                        maxLength={1024}
                        disabled={locked}
                        onChange={(e) => setNote(e.target.value)}
                      />
                      <small>{text('noteHelp')}</small>
                    </label>
                    <button type="submit" className="btn btn-primary" disabled={locked}>
                      {operation.isPending ? core('common.working') : text('createService')}
                    </button>
                  </form>
                )
              ) : (
                <>
                  <label>
                    {text('serviceSearch')}
                    <input
                      aria-label={text('serviceSearch')}
                      type="search"
                      maxLength={128}
                      value={serviceQuery}
                      disabled={locked}
                      onChange={(e) => setServiceQuery(e.target.value)}
                    />
                    <small>{text('searchHelp')}</small>
                  </label>
                  {endpoints.isPending ? (
                    <CoreLoading compact />
                  ) : endpoints.error ? (
                    <CoreErrorPanel
                      compact
                      error={endpoints.error}
                      onRetry={() => void endpoints.refetch()}
                    />
                  ) : (
                    <>
                      <ul className="quickstart-choice-list">
                        {endpoints.data.data.map((item) => (
                          <li key={item.id}>
                            <strong>{item.note || item.base_url}</strong>
                            <span>{item.base_url}</span>
                            <ConnectorLabel value={item.connector_type} />
                            <button
                              type="button"
                              className="btn btn-secondary"
                              disabled={locked || reading || !item.enabled}
                              onClick={() => void chooseService(item.id)}
                            >
                              {text('useService')}
                            </button>
                          </li>
                        ))}
                      </ul>
                      {endpoints.data.data.length === 0 ? <p>{text('none')}</p> : null}
                      <PagePagination
                        metadata={endpoints.data.pagination}
                        requestedPage={endpointPager.page}
                        busy={endpoints.isFetching}
                        onPageChange={endpointPager.setPage}
                        onPageSizeChange={endpointPager.setPageSize}
                      />
                    </>
                  )}
                </>
              )}
            </>
          ) : null}
          {stage === 1 && endpoint ? (
            <>
              <div className="core-row-actions">
                <button
                  type="button"
                  className="btn btn-secondary"
                  disabled={locked}
                  aria-pressed={keyMode === 'new'}
                  onClick={() => setKeyMode('new')}
                >
                  {text('newKey')}
                </button>
                <button
                  type="button"
                  className="btn btn-secondary"
                  disabled={locked}
                  aria-pressed={keyMode === 'existing'}
                  onClick={() => {
                    setSecret('');
                    setKeyMode('existing');
                  }}
                >
                  {text('existingKey')}
                </button>
              </div>
              {keyMode === 'new' ? (
                <form className="quickstart-form" onSubmit={(e) => void addKey(e)}>
                  <label>
                    {text('secret')}
                    <input
                      aria-label={text('secret')}
                      required
                      type="password"
                      autoComplete="off"
                      value={secret}
                      disabled={locked}
                      onChange={(e) => setSecret(e.target.value)}
                    />
                    <small>{text('secretHelp')}</small>
                  </label>
                  <label>
                    {text('keyNote')}
                    <input
                      value={keyNote}
                      maxLength={1024}
                      disabled={locked}
                      onChange={(e) => setKeyNote(e.target.value)}
                    />
                  </label>
                  <label className="core-check">
                    <input
                      type="checkbox"
                      checked={ownership}
                      disabled={locked}
                      onChange={(e) => setOwnership(e.target.checked)}
                    />
                    {text('ownership')}
                  </label>
                  {endpoint.connector_type === 'openai-compatible' ? (
                    <>
                      <label className="core-check">
                        <input
                          type="checkbox"
                          checked={store}
                          disabled={locked}
                          onChange={(e) => setStore(e.target.checked)}
                        />
                        {core('endpoints.storePolicy')}
                      </label>
                      <p className="core-muted">{text('storeHelp')}</p>
                    </>
                  ) : null}
                  <button
                    type="submit"
                    className="btn btn-primary"
                    disabled={locked || !ownership || !secret}
                  >
                    {text('addKey')}
                  </button>
                </form>
              ) : (
                <>
                  <label>
                    {text('keySearch')}
                    <input
                      aria-label={text('keySearch')}
                      type="search"
                      maxLength={128}
                      value={keyQuery}
                      onChange={(e) => setKeyQuery(e.target.value)}
                    />
                    <small>{text('searchHelp')}</small>
                  </label>
                  {keys.isPending ? (
                    <CoreLoading compact />
                  ) : keys.error ? (
                    <CoreErrorPanel
                      compact
                      error={keys.error}
                      onRetry={() => void keys.refetch()}
                    />
                  ) : keys.data ? (
                    <>
                      <ul className="quickstart-choice-list">
                        {keys.data.data.map((item) => (
                          <li key={item.id}>
                            <strong>
                              {item.note || `${item.display_head}…${item.display_tail}`}
                            </strong>
                            <button
                              type="button"
                              className="btn btn-secondary"
                              disabled={locked || !item.enabled || item.suspension_state !== 'none'}
                              onClick={() => {
                                setKeyId(item.id);
                                setKeyNote(item.note);
                                setStage(2);
                              }}
                            >
                              {text('existingKey')}
                            </button>
                          </li>
                        ))}
                      </ul>
                      <PagePagination
                        metadata={keys.data.pagination}
                        requestedPage={keyPager.page}
                        busy={keys.isFetching}
                        onPageChange={keyPager.setPage}
                        onPageSizeChange={keyPager.setPageSize}
                      />
                    </>
                  ) : null}
                </>
              )}
            </>
          ) : null}
          {stage === 2 && endpoint && keyId ? (
            <>
              {finished ? (
                <div className="quickstart-complete" role="status">
                  <h3>{text('finished')}</h3>
                  <p>{text('summary')}</p>
                  {confirmedRows.map((row) => (
                    <SafeCopyValue
                      key={row.id}
                      value={row.model!.full_name}
                      label={text('callName')}
                    />
                  ))}
                  <button
                    type="button"
                    className="btn btn-secondary"
                    onClick={() => {
                      setRows([]);
                      operation.reset();
                    }}
                  >
                    {text('moreModels')}
                  </button>
                  <Link className="btn btn-primary" to="/keys">
                    {text('apiUse')}
                  </Link>
                </div>
              ) : (
                <>
                  <div className="core-row-actions">
                    <button
                      type="button"
                      className="btn btn-secondary"
                      disabled={locked || catalog.data?.evidence.state === 'checking'}
                      onClick={() => void run({ kind: 'refresh', endpointId: endpoint.id, keyId })}
                    >
                      {text('check')}
                    </button>
                    <button
                      type="button"
                      className="btn btn-secondary"
                      disabled={locked}
                      onClick={() => {
                        setKeyId('');
                        setKeyNote('');
                        setStore(false);
                        setRows([]);
                        setStage(1);
                        setSecret('');
                      }}
                    >
                      {text('another')}
                    </button>
                  </div>
                  <p className="core-muted">{text('checkHelp')}</p>
                  {catalog.data ? <DiscoveryStatus evidence={catalog.data.evidence} /> : null}
                  {catalog.data?.evidence.state === 'checking' ? (
                    <p role="status">{text('checking')}</p>
                  ) : null}
                  {catalog.data?.evidence.state === 'failed' ? <p>{text('checkFailed')}</p> : null}
                  {catalog.isPending ? (
                    <CoreLoading compact />
                  ) : catalog.error ? (
                    <CoreErrorPanel
                      compact
                      error={catalog.error}
                      onRetry={() => void catalog.refetch()}
                    />
                  ) : catalog.data ? (
                    <>
                      <p>{text('selectHelp')}</p>
                      <ul className="quickstart-catalog">
                        {[...catalog.data.automatic_entries, ...catalog.data.manual_entries].map(
                          (entry) => (
                            <li key={entry.id}>
                              <label className="core-check">
                                <input
                                  type="checkbox"
                                  checked={rows.some(
                                    (row) =>
                                      row.id === `${keyId}\u0000${entry.upstream_model_id}` &&
                                      row.selected,
                                  )}
                                  disabled={locked}
                                  onChange={() => selectEntry(entry)}
                                />
                                <span>{entry.upstream_model_id}</span>
                              </label>
                              <small>
                                {core(
                                  entry.source_type === 'manual'
                                    ? 'models.manual'
                                    : 'models.automatic',
                                )}
                              </small>
                            </li>
                          ),
                        )}
                      </ul>
                      {catalog.data.pagination.total_items === '0' ? <p>{text('empty')}</p> : null}
                      <PagePagination
                        metadata={catalog.data.pagination}
                        requestedPage={catalogPager.page}
                        busy={catalog.isFetching}
                        onPageChange={catalogPager.setPage}
                        onPageSizeChange={catalogPager.setPageSize}
                      />
                    </>
                  ) : null}
                  <details>
                    <summary>{text('manual')}</summary>
                    <form
                      className="quickstart-form"
                      onSubmit={(event) => {
                        event.preventDefault();
                        void run({
                          kind: 'manual',
                          endpointId: endpoint.id,
                          keyId,
                          entries: [{ upstream_model_id: manualName, provider: manualProvider }],
                        });
                      }}
                    >
                      <p>{text('manualHelp')}</p>
                      <label>
                        {text('upstream')}
                        <input
                          required
                          maxLength={512}
                          value={manualName}
                          disabled={locked}
                          onChange={(e) => setManualName(e.target.value)}
                        />
                      </label>
                      <label>
                        {text('provider')}
                        <input
                          aria-label={text('provider')}
                          required
                          maxLength={128}
                          value={manualProvider}
                          disabled={locked}
                          onChange={(e) => setManualProvider(e.target.value)}
                        />
                      </label>
                      <button type="submit" className="btn btn-secondary" disabled={locked}>
                        {text('manual')}
                      </button>
                    </form>
                  </details>
                  <h3>{text('selected', { count: selectedRows.length })}</h3>
                  <div className="quickstart-models">
                    {selectedRows.map((row, index) => (
                      <section className="quickstart-model" key={row.id}>
                        <h4>{row.upstream}</h4>
                        {row.complete ? (
                          <p role="status">
                            {text('connected')}{' '}
                            <SafeCopyValue
                              value={row.model?.full_name ?? `${row.provider}/${row.name}`}
                              label={text('callName')}
                            />
                          </p>
                        ) : (
                          <>
                            <label>
                              {text('provider')}
                              <input
                                aria-label={text('provider')}
                                value={row.provider}
                                maxLength={64}
                                disabled={locked || Boolean(row.model)}
                                onChange={(e) =>
                                  updateRow(row.id, {
                                    provider: e.target.value,
                                    checked: false,
                                    existing: undefined,
                                    decision: '',
                                  })
                                }
                              />
                              <small>{text('providerHelp')}</small>
                            </label>
                            <label>
                              {text('callName')}
                              <input
                                aria-label={text('callName')}
                                ref={(input) => {
                                  callNames.current[row.id] = input;
                                }}
                                value={row.name}
                                maxLength={64}
                                disabled={locked || Boolean(row.model)}
                                onChange={(e) =>
                                  updateRow(row.id, {
                                    name: e.target.value,
                                    checked: false,
                                    existing: undefined,
                                    decision: '',
                                  })
                                }
                              />
                              <small>{text('nameHelp')}</small>
                            </label>
                            <code>
                              {row.provider}/{row.name}
                            </code>
                            {row.model ? <p>{text('modelSaved')}</p> : null}
                            {row.checked && row.existing ? (
                              <fieldset>
                                <legend>{text('sameName')}</legend>
                                <label className="core-check">
                                  <input
                                    type="radio"
                                    name={`call-choice-${index}`}
                                    checked={row.decision === 'append'}
                                    disabled={locked}
                                    onChange={() => updateRow(row.id, { decision: 'append' })}
                                  />
                                  {text('append')}
                                </label>
                                <label className="core-check">
                                  <input
                                    type="radio"
                                    name={`call-choice-${index}`}
                                    checked={row.decision === 'rename'}
                                    disabled={locked}
                                    onChange={() => {
                                      updateRow(row.id, { decision: 'rename' });
                                      callNames.current[row.id]?.focus();
                                    }}
                                  />
                                  {text('rename')}
                                </label>
                              </fieldset>
                            ) : row.checked ? (
                              <p>{text('ready')}</p>
                            ) : null}
                            {row.error ? (
                              <p className="field-error" role="alert">
                                {text('modelFailed')}
                              </p>
                            ) : null}
                            <button
                              type="button"
                              className="btn btn-secondary"
                              disabled={locked || Boolean(row.model)}
                              onClick={() => updateRow(row.id, { selected: false })}
                            >
                              {core('common.cancel')}
                            </button>
                          </>
                        )}
                      </section>
                    ))}
                  </div>
                  {confirmedRows.length > 0 && !finished ? (
                    <p role="status">{text('partial')}</p>
                  ) : null}
                  <button
                    type="button"
                    className="btn btn-primary"
                    disabled={locked || selectedRows.length === 0}
                    onClick={() => void connect()}
                  >
                    {text('saveModels')}
                  </button>
                </>
              )}
            </>
          ) : null}
          {!finished ? (
            <button type="button" className="btn btn-secondary" onClick={stop}>
              {text('stop')}
            </button>
          ) : null}
        </>
      ) : null}
      <nav className="quickstart-links">
        <Link to="/charity">{text('community')}</Link>
        <Link to="/endpoints">{text('viewResources')}</Link>
        <Link to="/models">{text('viewModels')}</Link>
        <Link to={endpoint ? `/endpoints/${endpoint.id}` : '/endpoints'}>{text('advanced')}</Link>
      </nav>
    </section>
  );
}

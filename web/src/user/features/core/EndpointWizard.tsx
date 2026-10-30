import { Field, Fields, FormActions, OptionCards, Toggle } from '@shared/components/ui';
import { supportsChatStoragePolicy } from '@shared/operations/connectorPolicy';
import { EndpointTransportNotice } from '@shared/components/EndpointTransportNotice';
import { useEffect, useReducer, useRef, useState } from 'react';
import { useOperation } from '@shared/operations/useOperation';
import { useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import { KeyLimitFields } from '@shared/components/KeyRoutingLimits';
import {
  executeResource,
  readResourceResult,
  resourceStatus,
  type ResourceIntent,
  type ResourceResult,
  type ResourceStatus,
} from './resourceOperation';
import { useQuickstartCopy } from './quickstartCopy';
import { ConnectorLabel, CoreErrorPanel, CoreLoading, DiscoveryStatus } from './components';
import { useCoreCopy } from './copy';
import { CORE_ROUTE_PATHS } from './descriptors';
import { canonicalBaseURLPreview, validateEndpointSecret } from './normalizers';
import {
  coreKeys,
  invalidateResourceDependents,
  useCatalog,
  useEndpointCreateOptions,
} from './queries';
import { createOperationIdentity, isOutcomeUnknown } from './request';
import { endpointSecretDraftReducer, initialEndpointSecretDraftState } from './stateMachines';
import {
  type CatalogView,
  type ConnectorType,
  type DiscoveryAccepted,
  type Endpoint,
  type EndpointCreateInput,
  type EndpointKey,
  type EndpointSource,
} from './types';

type Step = 0 | 1 | 2 | 3 | 4;

function instanceId(): string {
  return createOperationIdentity().actionId;
}

export function EndpointWizard({
  accountId,
  onClose,
  onCreated,
}: {
  accountId: string;
  onClose: () => void;
  onCreated: (endpoint: Endpoint) => void;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const endpointOptionsQuery = useEndpointCreateOptions(accountId);
  const [pageInstanceId] = useState(instanceId);
  const [step, setStep] = useState<Step>(0);
  const [source, setSource] = useState<EndpointSource | null>(null);
  const [channelId, setChannelId] = useState('');
  const [connector, setConnector] = useState<ConnectorType>('openai-compatible');
  const [baseURL, setBaseURL] = useState('');
  const [endpointNote, setEndpointNote] = useState('');
  const [keyNote, setKeyNote] = useState('');
  const [forceStoreFalse, setForceStoreFalse] = useState(false);
  const [maxConcurrency, setMaxConcurrency] = useState('0');
  const [maxRPM, setMaxRPM] = useState('0');
  const [endpoint, setEndpoint] = useState<Endpoint | null>(null);
  const [endpointKey, setEndpointKey] = useState<EndpointKey | null>(null);
  const [accepted, setAccepted] = useState<DiscoveryAccepted | null>(null);
  const [catalog, setCatalog] = useState<CatalogView | null>(null);
  const liveCatalog = useCatalog(accountId, endpoint?.id, endpointKey?.id, undefined, step === 4);
  const [addedKeys, setAddedKeys] = useState<EndpointKey[]>([]);
  const [localError, setLocalError] = useState<unknown>(null);
  const copy = useQuickstartCopy();
  const receipt = useRef<ResourceStatus | null>(null);
  const saved = useRef<ResourceResult | null>(null);
  const [secret, dispatchSecret] = useReducer(endpointSecretDraftReducer, undefined, () =>
    initialEndpointSecretDraftState(accountId, pageInstanceId),
  );

  const apply = (result: ResourceResult) => {
    if (result.kind === 'endpoint') {
      setEndpoint(result.endpoint);
      queryClient.setQueryData(coreKeys.endpoint(accountId, result.endpoint.id), result.endpoint);
      onCreated(result.endpoint);
      setStep(2);
    } else if (result.kind === 'key') {
      dispatchSecret({ type: 'clear-secret', accountId, pageInstanceId });
      setEndpointKey(result.key);
      setAddedKeys((current) => [...current.filter((key) => key.id !== result.key.id), result.key]);
      setStep(3);
    } else if (result.kind === 'refresh') {
      setAccepted(result.accepted ?? null);
      setCatalog(result.catalog);
      setStep(4);
    }
  };
  const operation = useOperation<ResourceIntent, string, ResourceResult>({
    authorityRoot: ['user', 'core'],
    clearSecrets: () => dispatchSecret({ type: 'cancel', accountId, pageInstanceId }),
    execute: async (intent, keySecret, key, context) => {
      context.commit(() => {
        saved.current = null;
        receipt.current = null;
        if (intent.kind === 'key')
          dispatchSecret({ type: 'clear-secret', accountId, pageInstanceId });
      });
      const result = await executeResource(intent, keySecret, key, context.signal);
      context.commit(() => {
        saved.current = result;
      });
      return result;
    },
    reconcile: async (intent, error, context) => {
      let result = saved.current;
      if (error && isOutcomeUnknown(error) && context.operationKey) {
        const status = await resourceStatus(context.operationKey, context.signal);
        context.commit(() => {
          receipt.current = status;
        });
        result = await readResourceResult(intent, status, context.signal);
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: coreKeys.endpointsRoot(accountId) }),
        intent.kind === 'key' || intent.kind === 'refresh'
          ? queryClient.invalidateQueries({
              queryKey: coreKeys.endpointKeysRoot(accountId, intent.endpointId),
            })
          : Promise.resolve(),
        intent.kind === 'refresh'
          ? queryClient.invalidateQueries({
              queryKey: coreKeys.catalogRoot(accountId, intent.endpointId, intent.keyId),
            })
          : Promise.resolve(),
        invalidateResourceDependents(queryClient, accountId),
      ]);
      context.assertCurrent();
      if (result) {
        context.commit(() => apply(result));
        if (error) return { operationConfirmed: true };
      }
    },
  });
  const busy = operation.isPending;
  const hasAttempt = busy || operation.outcome === 'unknown';
  const hasEndpointAttempt = hasAttempt && operation.variables?.kind === 'endpoint';
  const hasKeyAttempt = hasAttempt && operation.variables?.kind === 'key';
  const hasDiscoveryAttempt = hasAttempt && operation.variables?.kind === 'refresh';
  const outcome = operation.outcome;
  const error = localError ?? operation.error ?? operation.refreshError;
  useEffect(() => {
    dispatchSecret({ type: 'boundary', accountId, pageInstanceId });
  }, [accountId, pageInstanceId]);
  const close = () => {
    operation.cancel();
    onClose();
  };
  const recover = async (): Promise<boolean> => {
    if (operation.outcome !== 'unknown') return false;
    await operation.check();
    return receipt.current?.status !== 'not_recorded';
  };

  const availableConnectors = endpointOptionsQuery.data?.base_connector_types ?? [];
  const mainstreamChannels = endpointOptionsQuery.data?.mainstream_channels ?? [];
  const selectedSource: EndpointSource =
    source ?? (mainstreamChannels.length > 0 ? 'mainstream' : 'custom');
  const selectedChannel =
    mainstreamChannels.find((channel) => channel.id === channelId) ?? mainstreamChannels[0];
  const selectedConnector = availableConnectors.includes(connector)
    ? connector
    : (availableConnectors[0] ?? connector);
  const formBaseURL = selectedSource === 'mainstream' ? (selectedChannel?.base_url ?? '') : baseURL;

  let preview = '';
  let previewError = false;
  if (formBaseURL) {
    try {
      preview = canonicalBaseURLPreview(formBaseURL);
    } catch {
      previewError = true;
    }
  }

  const createEndpointStep = async () => {
    setLocalError(null);
    if (operation.isSuccess && operation.outcome === 'refresh-failed') {
      await operation.refresh();
      return;
    }
    if (await recover()) return;
    if (
      !preview ||
      previewError ||
      (selectedSource === 'mainstream' && !selectedChannel) ||
      (selectedSource === 'custom' && !availableConnectors.includes(selectedConnector))
    ) {
      setLocalError(new Error(t('common.errorBody')));
      return;
    }
    const input: EndpointCreateInput =
      selectedSource === 'mainstream'
        ? {
            source: 'mainstream',
            channel_id: selectedChannel!.id,
            note: endpointNote,
            enabled: true,
          }
        : {
            source: 'custom',
            connector_type: selectedConnector,
            base_url: preview,
            note: endpointNote,
            enabled: true,
          };
    await operation
      .run(
        operation.outcome === 'unknown' && operation.variables
          ? operation.variables
          : { kind: 'endpoint', input },
      )
      .catch(() => undefined);
  };
  const createKeyStep = async () => {
    if (!endpoint || busy) return;
    setLocalError(null);
    if (operation.isSuccess && operation.outcome === 'refresh-failed') {
      await operation.refresh();
      return;
    }
    if (await recover()) return;
    try {
      validateEndpointSecret(secret.secret);
      if (!secret.ownershipConfirmed) throw new Error('ownership');
    } catch {
      dispatchSecret({
        type: 'local-error',
        accountId,
        pageInstanceId,
        message: t('endpoints.secretRequired'),
      });
      return;
    }
    const intent: ResourceIntent = {
      kind: 'key',
      endpointId: endpoint.id,
      input: {
        note: keyNote,
        enabled: true,
        ownership_confirmed: true,
        force_store_false: supportsChatStoragePolicy(endpoint.connector_type) && forceStoreFalse,
        max_concurrency: Number(maxConcurrency),
        max_rpm: Number(maxRPM),
      },
    };
    await operation
      .run(
        operation.outcome === 'unknown' && operation.variables ? operation.variables : intent,
        secret.secret,
      )
      .catch(() => undefined);
  };
  const checkModels = async () => {
    if (!endpoint || !endpointKey || busy) return;
    setLocalError(null);
    if (operation.isSuccess && operation.outcome === 'refresh-failed') {
      await operation.refresh();
      return;
    }
    if (await recover()) return;
    await operation
      .run(
        operation.outcome === 'unknown' && operation.variables
          ? operation.variables
          : { kind: 'refresh', endpointId: endpoint.id, keyId: endpointKey.id },
      )
      .catch(() => undefined);
  };

  const stepLabels = [
    t('endpoints.wizardConnector'),
    t('endpoints.wizardUrl'),
    t('endpoints.wizardKey'),
    t('endpoints.wizardDiscovery'),
    t('endpoints.wizardBinding'),
  ];

  if (!endpointOptionsQuery.data && endpointOptionsQuery.isPending) {
    return (
      <section className="core-card core-wizard" aria-labelledby="endpoint-wizard-title">
        <div className="core-card__header">
          <h2 id="endpoint-wizard-title">{t('endpoints.wizardTitle')}</h2>
          <button type="button" className="nb-btn nb-btn--secondary" onClick={close}>
            {t('common.cancel')}
          </button>
        </div>
        <CoreLoading />
      </section>
    );
  }

  if (!endpointOptionsQuery.data && endpointOptionsQuery.error) {
    return (
      <section className="core-card core-wizard" aria-labelledby="endpoint-wizard-title">
        <div className="core-card__header">
          <h2 id="endpoint-wizard-title">{t('endpoints.wizardTitle')}</h2>
          <button type="button" className="nb-btn nb-btn--secondary" onClick={close}>
            {t('common.cancel')}
          </button>
        </div>
        <CoreErrorPanel
          error={endpointOptionsQuery.error}
          onRetry={() => void endpointOptionsQuery.refetch()}
        />
      </section>
    );
  }

  return (
    <section className="core-card core-wizard" aria-labelledby="endpoint-wizard-title">
      <div className="core-card__header">
        <h2 id="endpoint-wizard-title">{t('endpoints.wizardTitle')}</h2>
        <button type="button" className="nb-btn nb-btn--secondary" onClick={close}>
          {endpoint ? t('endpoints.finishLater') : t('common.cancel')}
        </button>
      </div>
      <ol className="core-steps">
        {stepLabels.map((label, index) => (
          <li key={label} className={step === index ? 'is-current' : ''}>
            {label}
          </li>
        ))}
      </ol>

      {step === 0 ? (
        <div className="core-form">
          <OptionCards
            value={selectedSource}
            legend={t('filters.source')}
            options={[
              {
                value: 'mainstream',
                title: t('endpoints.mainstream'),
                disabled: mainstreamChannels.length === 0 || hasEndpointAttempt,
              },
              { value: 'custom', title: t('endpoints.custom'), disabled: hasEndpointAttempt },
            ]}
            onChange={(value) => {
              setSource(value);
              if (value === 'mainstream' && !selectedChannel)
                setChannelId(mainstreamChannels[0]?.id ?? '');
            }}
          />

          {selectedSource === 'mainstream' ? (
            <Fields>
              <Field label={t('endpoints.channel')}>
                {(props) => (
                  <select
                    {...props}
                    aria-label={t('endpoints.channel')}
                    value={selectedChannel?.id ?? ''}
                    disabled={hasEndpointAttempt || mainstreamChannels.length === 0}
                    onChange={(event) => setChannelId(event.target.value)}
                  >
                    {mainstreamChannels.map((channel) => (
                      <option key={channel.id} value={channel.id}>
                        {channel.name}
                      </option>
                    ))}
                  </select>
                )}
              </Field>
              <Field label={t('endpoints.baseUrl')} size="long">
                {(props) => (
                  <input
                    {...props}
                    aria-label={t('endpoints.baseUrl')}
                    value={selectedChannel?.base_url ?? ''}
                    readOnly
                    aria-readonly="true"
                    disabled={hasEndpointAttempt}
                  />
                )}
              </Field>
            </Fields>
          ) : (
            <OptionCards
              legend={t('endpoints.connector')}
              value={selectedConnector}
              options={availableConnectors.map((value) => ({
                value,
                title: <ConnectorLabel value={value} />,
                disabled: hasEndpointAttempt,
              }))}
              onChange={setConnector}
            />
          )}

          <FormActions>
            <button
              type="button"
              className="nb-btn nb-btn--primary"
              disabled={
                hasEndpointAttempt ||
                (selectedSource === 'mainstream'
                  ? !selectedChannel
                  : !availableConnectors.includes(selectedConnector))
              }
              onClick={() => setStep(1)}
            >
              {t('common.next')}
            </button>
          </FormActions>
        </div>
      ) : null}

      {step === 1 ? (
        <form
          className="core-form"
          onSubmit={(event) => {
            event.preventDefault();
            void createEndpointStep();
          }}
        >
          <Fields>
            <Field label={t('endpoints.baseUrl')} size="long">
              {(props) => (
                <input
                  {...props}
                  value={formBaseURL}
                  maxLength={4096}
                  disabled={hasEndpointAttempt}
                  readOnly={selectedSource === 'mainstream'}
                  aria-readonly={selectedSource === 'mainstream' ? 'true' : undefined}
                  onChange={(event) => {
                    if (selectedSource === 'custom') setBaseURL(event.target.value);
                  }}
                  required
                />
              )}
            </Field>
            <Field label={t('endpoints.note')} size="long">
              {(props) => (
                <input
                  {...props}
                  value={endpointNote}
                  maxLength={2048}
                  disabled={hasEndpointAttempt}
                  onChange={(event) => setEndpointNote(event.target.value)}
                />
              )}
            </Field>
          </Fields>
          <EndpointTransportNotice url={formBaseURL} />
          {formBaseURL ? (
            <div className={previewError ? 'core-inline-error' : 'core-inline-success'}>
              <strong>{t('endpoints.preview')}</strong>
              <div className="core-mono">{previewError ? t('common.errorBody') : preview}</div>
              {!previewError ? <small>{t('endpoints.previewAuthority')}</small> : null}
            </div>
          ) : null}
          {error ? <CoreErrorPanel error={error} compact /> : null}
          {outcome === 'conflict' ? (
            <p className="core-inline-warning">{t('common.conflict')}</p>
          ) : null}
          {outcome === 'unknown' ? (
            <p className="core-inline-warning">{t('common.outcomeUnknown')}</p>
          ) : null}
          <FormActions>
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={busy || hasEndpointAttempt}
              onClick={() => setStep(0)}
            >
              {t('common.back')}
            </button>
            <button
              type="submit"
              className="nb-btn nb-btn--primary"
              disabled={busy || !preview || previewError}
            >
              {busy
                ? t('common.working')
                : hasEndpointAttempt
                  ? copy.t('checkResult')
                  : t('endpoints.createEndpointStep')}
            </button>
          </FormActions>
        </form>
      ) : null}

      {step >= 2 && endpoint ? (
        <div className="core-stack">
          <EndpointTransportNotice url={endpoint.base_url} />
          <p>{t('endpoints.multipleKeysHint')}</p>
          {addedKeys.length > 0 ? (
            <ul className="core-added-keys">
              {addedKeys.map((key) => (
                <li key={key.id}>
                  {key.note || t('endpoints.key')}{' '}
                  <span className="core-muted core-mono">
                    {key.display_head}…{key.display_tail}
                  </span>
                </li>
              ))}
            </ul>
          ) : null}
          {step >= 3 ? (
            <div className="core-row-actions">
              <button
                type="button"
                className="nb-btn nb-btn--secondary"
                disabled={busy || hasDiscoveryAttempt}
                onClick={() => {
                  dispatchSecret({ type: 'clear-secret', accountId, pageInstanceId });
                  operation.reset();
                  setForceStoreFalse(false);
                  setKeyNote('');
                  setMaxConcurrency('0');
                  setMaxRPM('0');
                  setEndpointKey(null);
                  setCatalog(null);
                  setAccepted(null);
                  setLocalError(null);

                  setStep(2);
                }}
              >
                {t('endpoints.addAnotherKey')}
              </button>
              {step === 3 ? (
                <button
                  type="button"
                  className="nb-btn nb-btn--secondary"
                  disabled={busy || hasDiscoveryAttempt}
                  onClick={close}
                >
                  {t('endpoints.finish')}
                </button>
              ) : null}
            </div>
          ) : null}
        </div>
      ) : null}

      {step === 2 && endpoint ? (
        <form
          className="core-form"
          onSubmit={(event) => {
            event.preventDefault();
            void createKeyStep();
          }}
        >
          <div className="core-inline-success">
            <span>{t('endpoints.savedForLater')}</span>
            <span className="core-mono">{endpoint.base_url}</span>
          </div>
          <Fields>
            <Field label={t('endpoints.secret')} size="long">
              {(props) => (
                <input
                  {...props}
                  type="password"
                  autoComplete="new-password"
                  value={secret.secret}
                  maxLength={65536}
                  disabled={busy}
                  onChange={(event) =>
                    dispatchSecret({
                      type: 'change',
                      accountId,
                      pageInstanceId,
                      secret: event.target.value,
                    })
                  }
                  required={outcome !== 'unknown'}
                />
              )}
            </Field>
            <Field label={t('endpoints.keyNote')} size="long">
              {(props) => (
                <input
                  {...props}
                  value={keyNote}
                  maxLength={2048}
                  disabled={hasKeyAttempt}
                  onChange={(event) => setKeyNote(event.target.value)}
                />
              )}
            </Field>
          </Fields>
          <KeyLimitFields
            concurrency={maxConcurrency}
            rpm={maxRPM}
            onConcurrency={setMaxConcurrency}
            onRPM={setMaxRPM}
            disabled={hasKeyAttempt}
          />
          <Field label={t('endpoints.ownership')}>
            {(props) => (
              <input
                {...props}
                type="checkbox"
                checked={secret.ownershipConfirmed}
                disabled={hasKeyAttempt}
                onChange={(event) =>
                  dispatchSecret({
                    type: 'ownership',
                    accountId,
                    pageInstanceId,
                    confirmed: event.target.checked,
                  })
                }
              />
            )}
          </Field>
          {supportsChatStoragePolicy(endpoint.connector_type) ? (
            <Toggle
              label={t('endpoints.storePolicy')}
              checked={forceStoreFalse}
              disabled={hasKeyAttempt}
              onChange={setForceStoreFalse}
            />
          ) : null}
          <p className="core-inline-warning">{t('endpoints.costWarning')}</p>
          {secret.message ? (
            <p className="core-inline-error" role="alert">
              {secret.message}
            </p>
          ) : null}
          {error ? <CoreErrorPanel error={error} compact /> : null}
          <FormActions>
            <button type="submit" className="nb-btn nb-btn--primary" disabled={busy}>
              {busy
                ? t('common.working')
                : hasKeyAttempt
                  ? copy.t('checkResult')
                  : t('endpoints.addKeyStep')}
            </button>
          </FormActions>
        </form>
      ) : null}

      {step === 3 && endpoint && endpointKey ? (
        <div className="core-form">
          <p className="core-inline-warning">{t('endpoints.costWarning')}</p>
          {error ? <CoreErrorPanel error={error} compact /> : null}
          <FormActions>
            <button
              type="button"
              className="nb-btn nb-btn--primary"
              disabled={busy}
              onClick={() => void checkModels()}
            >
              {busy
                ? t('common.working')
                : hasDiscoveryAttempt
                  ? copy.t('checkResult')
                  : t('endpoints.refreshDiscovery')}
            </button>
          </FormActions>
        </div>
      ) : null}

      {step === 4 && (accepted || catalog) ? (
        <div className="core-form">
          <DiscoveryStatus
            evidence={liveCatalog.data?.evidence ?? catalog?.evidence ?? accepted!.evidence}
          />
          {liveCatalog.error ? (
            <CoreErrorPanel
              compact
              error={liveCatalog.error}
              onRetry={() => void liveCatalog.refetch()}
            />
          ) : null}
          <FormActions>
            {(liveCatalog.data?.evidence ?? catalog?.evidence ?? accepted?.evidence)?.state ===
            'failed' ? (
              <>
                <button
                  type="button"
                  className="nb-btn nb-btn--secondary"
                  disabled={busy}
                  onClick={() => void checkModels()}
                >
                  {t('endpoints.refreshDiscovery')}
                </button>
                {endpoint ? (
                  <Link
                    className="nb-btn nb-btn--secondary"
                    to={CORE_ROUTE_PATHS.endpointDetail(endpoint.id)}
                  >
                    {t('endpoints.manualTitle')}
                  </Link>
                ) : null}
              </>
            ) : null}
            <button type="button" className="nb-btn nb-btn--secondary" onClick={close}>
              {t('endpoints.finish')}
            </button>
            <Link
              className="nb-btn nb-btn--primary"
              to={CORE_ROUTE_PATHS.models}
              onClick={() => dispatchSecret({ type: 'leave', accountId, pageInstanceId })}
            >
              {t('endpoints.openBindings')}
            </Link>
          </FormActions>
        </div>
      ) : null}

      {busy ? <CoreLoading compact /> : null}
    </section>
  );
}

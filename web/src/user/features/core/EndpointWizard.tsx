import { useEffect, useReducer, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import { KeyLimitFields } from '@shared/components/KeyRoutingLimits';
import {
  createEndpoint,
  createEndpointKey,
  getCatalog,
  listEndpointKeys,
  listEndpoints,
  refreshDiscovery,
} from './api';
import { ConnectorLabel, CoreErrorPanel, CoreLoading, DiscoveryStatus } from './components';
import { useCoreCopy } from './copy';
import { CORE_ROUTE_PATHS } from './descriptors';
import { canonicalBaseURLPreview, validateEndpointSecret } from './normalizers';
import {
  coreKeys,
  coreSessionMatchesAccount,
  invalidateResourceDependents,
  useCatalog,
  useEndpointCreateOptions,
} from './queries';
import { createOperationIdentity, isConflict, isOutcomeUnknown } from './request';
import { endpointSecretDraftReducer, initialEndpointSecretDraftState } from './stateMachines';
import {
  type CatalogView,
  type ConnectorType,
  type DiscoveryAccepted,
  type Endpoint,
  type EndpointCreateInput,
  type EndpointKey,
  type EndpointKeyCreateInput,
  type EndpointSource,
  type OperationIdentity,
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
  const abortRef = useRef<AbortController | null>(null);
  const endpointAttemptRef = useRef<{
    input: EndpointCreateInput;
    operation: OperationIdentity;
  } | null>(null);
  const keyAttemptRef = useRef<{
    endpointId: string;
    input: EndpointKeyCreateInput;
    operation: OperationIdentity;
  } | null>(null);
  const discoveryAttemptRef = useRef<{
    endpointId: string;
    keyId: string;
    operation: OperationIdentity;
  } | null>(null);
  const [hasEndpointAttempt, setHasEndpointAttempt] = useState(false);
  const [hasKeyAttempt, setHasKeyAttempt] = useState(false);
  const [hasDiscoveryAttempt, setHasDiscoveryAttempt] = useState(false);
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
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [outcome, setOutcome] = useState<'conflict' | 'unknown' | null>(null);
  const [secret, dispatchSecret] = useReducer(endpointSecretDraftReducer, undefined, () =>
    initialEndpointSecretDraftState(accountId, pageInstanceId),
  );

  const discardStaleMutation = () => {
    abortRef.current?.abort();
    abortRef.current = null;
    endpointAttemptRef.current = null;
    keyAttemptRef.current = null;
    discoveryAttemptRef.current = null;
    setHasEndpointAttempt(false);
    setHasKeyAttempt(false);
    setHasDiscoveryAttempt(false);
    dispatchSecret({ type: 'cancel', accountId, pageInstanceId });
    setEndpoint(null);
    setEndpointKey(null);
    setAddedKeys([]);
    setAccepted(null);
    setCatalog(null);
    setBusy(false);
  };

  useEffect(() => {
    dispatchSecret({ type: 'boundary', accountId, pageInstanceId });
    abortRef.current?.abort();
    abortRef.current = null;
    endpointAttemptRef.current = null;
    keyAttemptRef.current = null;
    discoveryAttemptRef.current = null;
    let active = true;
    queueMicrotask(() => {
      if (!active) return;
      setHasEndpointAttempt(false);
      setHasKeyAttempt(false);
      setHasDiscoveryAttempt(false);
      setStep(0);
      setSource(null);
      setChannelId('');
      setConnector('openai-compatible');
      setBaseURL('');
      setEndpointNote('');
      setKeyNote('');
      setForceStoreFalse(false);
      setMaxConcurrency('0');
      setMaxRPM('0');
      setEndpoint(null);
      setEndpointKey(null);
      setAddedKeys([]);
      setAccepted(null);
      setCatalog(null);
      setBusy(false);
      setError(null);
      setOutcome(null);
    });
    return () => {
      active = false;
    };
  }, [accountId, pageInstanceId]);

  useEffect(() => () => abortRef.current?.abort(), []);

  const close = () => {
    abortRef.current?.abort();
    endpointAttemptRef.current = null;
    keyAttemptRef.current = null;
    discoveryAttemptRef.current = null;
    setHasEndpointAttempt(false);
    setHasKeyAttempt(false);
    setHasDiscoveryAttempt(false);
    dispatchSecret({ type: 'cancel', accountId, pageInstanceId });
    onClose();
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
    setError(null);
    setOutcome(null);
    if (
      !preview ||
      previewError ||
      (selectedSource === 'mainstream' && !selectedChannel) ||
      (selectedSource === 'custom' && !availableConnectors.includes(selectedConnector))
    ) {
      setError(new Error(t('common.errorBody')));
      return;
    }
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    const attempt = endpointAttemptRef.current ?? {
      input:
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
            },
      operation: createOperationIdentity(),
    };
    endpointAttemptRef.current = attempt;
    setHasEndpointAttempt(true);
    const controller = new AbortController();
    abortRef.current?.abort();
    abortRef.current = controller;
    setBusy(true);
    try {
      const created = await createEndpoint(attempt.input, attempt.operation, controller.signal);
      if (controller.signal.aborted || abortRef.current !== controller) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      endpointAttemptRef.current = null;
      setHasEndpointAttempt(false);
      setEndpoint(created);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      queryClient.setQueryData(coreKeys.endpoint(accountId, created.id), created);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: coreKeys.endpointsRoot(accountId) }),
        invalidateResourceDependents(queryClient, accountId, { endpointId: created.id }),
      ]);
      if (controller.signal.aborted || abortRef.current !== controller) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      onCreated(created);
      setStep(2);
    } catch (caught) {
      if (controller.signal.aborted || abortRef.current !== controller) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      if (!isOutcomeUnknown(caught)) {
        endpointAttemptRef.current = null;
        setHasEndpointAttempt(false);
      }
      setOutcome(isConflict(caught) ? 'conflict' : isOutcomeUnknown(caught) ? 'unknown' : null);
      setError(caught);
      if (isConflict(caught)) {
        if (!coreSessionMatchesAccount(queryClient, accountId)) {
          discardStaleMutation();
          return;
        }
        await Promise.all([
          queryClient.invalidateQueries({ queryKey: coreKeys.endpointsRoot(accountId) }),
          invalidateResourceDependents(queryClient, accountId),
        ]);
        if (!coreSessionMatchesAccount(queryClient, accountId)) discardStaleMutation();
      } else if (isOutcomeUnknown(caught)) {
        try {
          const authority = await listEndpoints(undefined, controller.signal);
          if (
            !controller.signal.aborted &&
            abortRef.current === controller &&
            coreSessionMatchesAccount(queryClient, accountId)
          ) {
            queryClient.setQueryData(coreKeys.endpoints(accountId), authority);
            if (!(await invalidateResourceDependents(queryClient, accountId))) {
              discardStaleMutation();
              return;
            }
          } else if (!controller.signal.aborted && abortRef.current === controller) {
            discardStaleMutation();
          }
        } catch {
          if (
            !controller.signal.aborted &&
            abortRef.current === controller &&
            !coreSessionMatchesAccount(queryClient, accountId)
          ) {
            discardStaleMutation();
          }
          // The retained request identity remains available after a failed authority read.
        }
      }
    } finally {
      if (abortRef.current === controller) {
        abortRef.current = null;
        setBusy(false);
      }
    }
  };

  const createKeyStep = async () => {
    if (!endpoint || busy) return;
    setError(null);
    setOutcome(null);
    try {
      validateEndpointSecret(secret.secret);
      if (!secret.ownershipConfirmed) throw new Error(t('endpoints.secretRequired'));
    } catch {
      dispatchSecret({
        type: 'local-error',
        accountId,
        pageInstanceId,
        message: t('endpoints.secretRequired'),
      });
      return;
    }
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    const attempt = keyAttemptRef.current ?? {
      endpointId: endpoint.id,
      input: {
        secret: secret.secret,
        note: keyNote,
        enabled: true,
        force_store_false: endpoint.connector_type === 'openai-compatible' && forceStoreFalse,
        ownership_confirmed: true,
        max_concurrency: Number(maxConcurrency),
        max_rpm: Number(maxRPM),
      },
      operation: createOperationIdentity(),
    };
    keyAttemptRef.current = attempt;
    setHasKeyAttempt(true);
    const controller = new AbortController();
    abortRef.current?.abort();
    abortRef.current = controller;
    setBusy(true);
    dispatchSecret({ type: 'submit', accountId, pageInstanceId });
    try {
      const created = await createEndpointKey(
        attempt.endpointId,
        attempt.input,
        attempt.operation,
        controller.signal,
      );
      if (controller.signal.aborted || abortRef.current !== controller) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      keyAttemptRef.current = null;
      setHasKeyAttempt(false);
      dispatchSecret({ type: 'success', accountId, pageInstanceId });
      setEndpointKey(created);
      setAddedKeys((current) => [...current.filter((key) => key.id !== created.id), created]);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: coreKeys.endpointKeysRoot(accountId, endpoint.id),
        }),
        queryClient.invalidateQueries({ queryKey: coreKeys.endpoint(accountId, endpoint.id) }),
        queryClient.invalidateQueries({ queryKey: coreKeys.endpointsRoot(accountId) }),
        invalidateResourceDependents(queryClient, accountId, { endpointId: endpoint.id }),
      ]);
      if (controller.signal.aborted || abortRef.current !== controller) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      setStep(3);
    } catch (caught) {
      if (controller.signal.aborted || abortRef.current !== controller) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      if (!isOutcomeUnknown(caught)) {
        keyAttemptRef.current = null;
        setHasKeyAttempt(false);
      }
      const message = isOutcomeUnknown(caught) ? t('common.outcomeUnknown') : t('common.errorBody');
      dispatchSecret({ type: 'request-error', accountId, pageInstanceId, message });
      setOutcome(isConflict(caught) ? 'conflict' : isOutcomeUnknown(caught) ? 'unknown' : null);
      setError(caught);
      if (isConflict(caught)) {
        if (!coreSessionMatchesAccount(queryClient, accountId)) {
          discardStaleMutation();
          return;
        }
        await Promise.all([
          queryClient.invalidateQueries({
            queryKey: coreKeys.endpointKeysRoot(accountId, endpoint.id),
          }),
          queryClient.invalidateQueries({ queryKey: coreKeys.endpoint(accountId, endpoint.id) }),
          invalidateResourceDependents(queryClient, accountId, { endpointId: endpoint.id }),
        ]);
        if (!coreSessionMatchesAccount(queryClient, accountId)) discardStaleMutation();
      } else if (isOutcomeUnknown(caught)) {
        try {
          const authority = await listEndpointKeys(endpoint.id, undefined, controller.signal);
          if (
            !controller.signal.aborted &&
            abortRef.current === controller &&
            coreSessionMatchesAccount(queryClient, accountId)
          ) {
            queryClient.setQueryData(coreKeys.endpointKeys(accountId, endpoint.id), authority);
            if (
              !(await invalidateResourceDependents(queryClient, accountId, {
                endpointId: endpoint.id,
              }))
            ) {
              discardStaleMutation();
              return;
            }
          } else if (!controller.signal.aborted && abortRef.current === controller) {
            discardStaleMutation();
          }
        } catch {
          if (
            !controller.signal.aborted &&
            abortRef.current === controller &&
            !coreSessionMatchesAccount(queryClient, accountId)
          ) {
            discardStaleMutation();
          }
          // The secret-bearing request remains retained for an explicit exact replay.
        }
      }
    } finally {
      if (abortRef.current === controller) {
        abortRef.current = null;
        setBusy(false);
      }
    }
  };

  const checkModels = async () => {
    if (!endpoint || !endpointKey || busy) return;
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    setError(null);
    setOutcome(null);
    const attempt = discoveryAttemptRef.current ?? {
      endpointId: endpoint.id,
      keyId: endpointKey.id,
      operation: createOperationIdentity(),
    };
    discoveryAttemptRef.current = attempt;
    setHasDiscoveryAttempt(true);
    const controller = new AbortController();
    abortRef.current?.abort();
    abortRef.current = controller;
    setBusy(true);
    try {
      const result = await refreshDiscovery(
        attempt.endpointId,
        attempt.keyId,
        attempt.operation,
        controller.signal,
      );
      if (controller.signal.aborted || abortRef.current !== controller) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      discoveryAttemptRef.current = null;
      setHasDiscoveryAttempt(false);
      setAccepted(result);
      setCatalog(null);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      await queryClient.invalidateQueries({
        queryKey: coreKeys.catalogRoot(accountId, endpoint.id, endpointKey.id),
      });
      await invalidateResourceDependents(queryClient, accountId, { endpointId: endpoint.id });
      if (controller.signal.aborted || abortRef.current !== controller) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      try {
        const authoritative = await getCatalog(
          endpoint.id,
          endpointKey.id,
          undefined,
          controller.signal,
        );
        if (
          !controller.signal.aborted &&
          abortRef.current === controller &&
          coreSessionMatchesAccount(queryClient, accountId) &&
          BigInt(authoritative.evidence.revision) >= BigInt(result.evidence.revision)
        ) {
          setCatalog(authoritative);
        }
      } catch {
        // The accepted checking evidence remains authoritative even if the
        // immediate follow-up read fails.
      }
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      setStep(4);
    } catch (caught) {
      if (controller.signal.aborted || abortRef.current !== controller) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      if (!isOutcomeUnknown(caught)) {
        discoveryAttemptRef.current = null;
        setHasDiscoveryAttempt(false);
      }
      setOutcome(isConflict(caught) ? 'conflict' : isOutcomeUnknown(caught) ? 'unknown' : null);
      setError(caught);
      if (isConflict(caught)) {
        if (!coreSessionMatchesAccount(queryClient, accountId)) {
          discardStaleMutation();
          return;
        }
        await queryClient.invalidateQueries({
          queryKey: coreKeys.catalogRoot(accountId, endpoint.id, endpointKey.id),
        });
        await invalidateResourceDependents(queryClient, accountId, {
          endpointId: endpoint.id,
        });
        if (!coreSessionMatchesAccount(queryClient, accountId)) discardStaleMutation();
      } else if (isOutcomeUnknown(caught)) {
        try {
          const authoritative = await getCatalog(
            endpoint.id,
            endpointKey.id,
            undefined,
            controller.signal,
          );
          if (controller.signal.aborted || abortRef.current !== controller) return;
          if (!coreSessionMatchesAccount(queryClient, accountId)) {
            discardStaleMutation();
            return;
          }
          setCatalog(authoritative);
          if (
            !(await invalidateResourceDependents(queryClient, accountId, {
              endpointId: endpoint.id,
            }))
          ) {
            discardStaleMutation();
            return;
          }
          if (authoritative.evidence.state !== 'unknown') {
            discoveryAttemptRef.current = null;
            setHasDiscoveryAttempt(false);
            setOutcome(null);
            setError(null);
            setStep(4);
          }
        } catch {
          if (
            !controller.signal.aborted &&
            abortRef.current === controller &&
            !coreSessionMatchesAccount(queryClient, accountId)
          ) {
            discardStaleMutation();
          }
          // Keep the exact replay identity until a later authoritative read succeeds.
        }
      }
    } finally {
      if (abortRef.current === controller) {
        abortRef.current = null;
        setBusy(false);
      }
    }
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
          <button type="button" className="btn btn-secondary" onClick={close}>
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
          <button type="button" className="btn btn-secondary" onClick={close}>
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
        <button type="button" className="btn btn-secondary" onClick={close}>
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
          <div className="core-choice-grid">
            <button
              type="button"
              className={`core-choice${selectedSource === 'mainstream' ? ' is-selected' : ''}`}
              aria-pressed={selectedSource === 'mainstream'}
              disabled={mainstreamChannels.length === 0 || hasEndpointAttempt}
              onClick={() => {
                setSource('mainstream');
                if (!selectedChannel) setChannelId(mainstreamChannels[0]?.id ?? '');
              }}
            >
              <strong>{t('endpoints.mainstream')}</strong>
            </button>
            <button
              type="button"
              className={`core-choice${selectedSource === 'custom' ? ' is-selected' : ''}`}
              aria-pressed={selectedSource === 'custom'}
              disabled={hasEndpointAttempt}
              onClick={() => setSource('custom')}
            >
              <strong>{t('endpoints.custom')}</strong>
            </button>
          </div>

          {selectedSource === 'mainstream' ? (
            <div className="core-field-grid">
              <label>
                <span>{t('endpoints.channel')}</span>
                <select
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
              </label>
              <label>
                <span>{t('endpoints.baseUrl')}</span>
                <input
                  aria-label={t('endpoints.baseUrl')}
                  value={selectedChannel?.base_url ?? ''}
                  readOnly
                  aria-readonly="true"
                  disabled={hasEndpointAttempt}
                />
              </label>
            </div>
          ) : (
            <div className="core-choice-grid">
              {availableConnectors.map((value) => (
                <button
                  key={value}
                  type="button"
                  className={`core-choice${selectedConnector === value ? ' is-selected' : ''}`}
                  aria-pressed={selectedConnector === value}
                  disabled={hasEndpointAttempt}
                  onClick={() => setConnector(value)}
                >
                  <strong>
                    <ConnectorLabel value={value} />
                  </strong>
                </button>
              ))}
            </div>
          )}

          <div className="core-form-actions">
            <span />
            <button
              type="button"
              className="btn btn-primary"
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
          </div>
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
          <div className="core-field-grid">
            <label>
              <span>{t('endpoints.baseUrl')}</span>
              <input
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
            </label>
            <label>
              <span>{t('endpoints.note')}</span>
              <input
                value={endpointNote}
                maxLength={2048}
                disabled={hasEndpointAttempt}
                onChange={(event) => setEndpointNote(event.target.value)}
              />
            </label>
          </div>
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
          <div className="core-form-actions">
            <button
              type="button"
              className="btn btn-secondary"
              disabled={busy || hasEndpointAttempt}
              onClick={() => setStep(0)}
            >
              {t('common.back')}
            </button>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={busy || !preview || previewError}
            >
              {busy
                ? t('common.working')
                : hasEndpointAttempt
                  ? t('common.retrySame')
                  : t('endpoints.createEndpointStep')}
            </button>
          </div>
        </form>
      ) : null}

      {step >= 2 && endpoint ? (
        <div className="core-stack">
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
                className="btn btn-secondary"
                disabled={busy || hasDiscoveryAttempt}
                onClick={() => {
                  dispatchSecret({ type: 'boundary', accountId, pageInstanceId });
                  setKeyNote('');
                  setMaxConcurrency('0');
                  setMaxRPM('0');
                  setEndpointKey(null);
                  setCatalog(null);
                  setAccepted(null);
                  setError(null);
                  setOutcome(null);
                  setStep(2);
                }}
              >
                {t('endpoints.addAnotherKey')}
              </button>
              {step === 3 ? (
                <button
                  type="button"
                  className="btn btn-secondary"
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
          <div className="core-field-grid">
            <label>
              <span>{t('endpoints.secret')}</span>
              <input
                type="password"
                autoComplete="new-password"
                value={secret.secret}
                maxLength={65536}
                disabled={hasKeyAttempt}
                onChange={(event) =>
                  dispatchSecret({
                    type: 'change',
                    accountId,
                    pageInstanceId,
                    secret: event.target.value,
                  })
                }
                required
              />
            </label>
            <label>
              <span>{t('endpoints.keyNote')}</span>
              <input
                value={keyNote}
                maxLength={2048}
                disabled={hasKeyAttempt}
                onChange={(event) => setKeyNote(event.target.value)}
              />
            </label>
          </div>
          <KeyLimitFields
            concurrency={maxConcurrency}
            rpm={maxRPM}
            onConcurrency={setMaxConcurrency}
            onRPM={setMaxRPM}
            disabled={hasKeyAttempt}
          />
          <label className="core-checkbox">
            <input
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
            <span>{t('endpoints.ownership')}</span>
          </label>
          {endpoint.connector_type === 'openai-compatible' ? (
            <label className="core-checkbox">
              <input
                type="checkbox"
                checked={forceStoreFalse}
                disabled={hasKeyAttempt}
                onChange={(event) => setForceStoreFalse(event.target.checked)}
              />
              <span>{t('endpoints.storePolicy')}</span>
            </label>
          ) : null}
          <p className="core-inline-warning">{t('endpoints.costWarning')}</p>
          {secret.message ? (
            <p className="core-inline-error" role="alert">
              {secret.message}
            </p>
          ) : null}
          {error ? <CoreErrorPanel error={error} compact /> : null}
          <div className="core-form-actions">
            <span />
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {busy
                ? t('common.working')
                : hasKeyAttempt
                  ? t('common.retrySame')
                  : t('endpoints.addKeyStep')}
            </button>
          </div>
        </form>
      ) : null}

      {step === 3 && endpoint && endpointKey ? (
        <div className="core-form">
          <p className="core-inline-warning">{t('endpoints.costWarning')}</p>
          {error ? <CoreErrorPanel error={error} compact /> : null}
          <div className="core-form-actions">
            <span />
            <button
              type="button"
              className="btn btn-primary"
              disabled={busy}
              onClick={() => void checkModels()}
            >
              {busy
                ? t('common.working')
                : hasDiscoveryAttempt
                  ? t('common.retrySame')
                  : t('endpoints.refreshDiscovery')}
            </button>
          </div>
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
          <div className="core-form-actions">
            <button type="button" className="btn btn-secondary" onClick={close}>
              {t('endpoints.finish')}
            </button>
            <Link
              className="btn btn-primary"
              to={CORE_ROUTE_PATHS.models}
              onClick={() => dispatchSecret({ type: 'leave', accountId, pageInstanceId })}
            >
              {t('endpoints.openBindings')}
            </Link>
          </div>
        </div>
      ) : null}

      {busy ? <CoreLoading compact /> : null}
    </section>
  );
}

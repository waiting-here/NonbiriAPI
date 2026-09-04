import { useEffect, useReducer, useRef, useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { PageHeader } from '@shared/components/States';
import { isNotFoundError } from '@shared/query/http';
import {
  createEndpointKey,
  createManualEntries,
  deleteEndpoint,
  deleteEndpointKey,
  deleteManualEntry,
  patchEndpoint,
  patchEndpointKey,
  refreshDiscovery,
  updateManualEntry,
} from './api';
import {
  ConnectorLabel,
  CoreEmpty,
  CoreErrorPanel,
  CoreLoading,
  CoreTime,
  DiscoveryStatus,
  SafeCopyValue,
  StatusPill,
} from './components';
import { useCoreCopy } from './copy';
import { CORE_ROUTE_PATHS } from './descriptors';
import {
  applyManualUpdateToCache,
  coreKeys,
  coreSessionMatchesAccount,
  invalidateResourceDependents,
  useCatalog,
  useEndpoint,
  useEndpointKeysPage,
  useEndpointRoutingProjection,
} from './queries';
import { createOperationIdentity, isConflict, isOutcomeUnknown } from './request';
import { endpointSecretDraftReducer, initialEndpointSecretDraftState } from './stateMachines';
import { validateEndpointSecret, validateManualValue } from './normalizers';
import type {
  BindingReplacement,
  CatalogEntry,
  Endpoint,
  EndpointPatchInput,
  EndpointKey,
  EndpointKeyCreateInput,
  EndpointKeyPatchInput,
  OperationIdentity,
} from './types';

type ActionOutcome = 'conflict' | 'unknown' | 'error' | null;

function actionOutcome(error: unknown): ActionOutcome {
  if (isConflict(error)) return 'conflict';
  if (isOutcomeUnknown(error)) return 'unknown';
  return 'error';
}

function OutcomeNotice({ outcome }: { outcome: ActionOutcome }) {
  const { t } = useCoreCopy();
  if (!outcome) return null;
  return (
    <p className={outcome === 'error' ? 'core-inline-error' : 'core-inline-warning'} role="alert">
      {outcome === 'conflict'
        ? t('common.conflict')
        : outcome === 'unknown'
          ? t('common.outcomeUnknown')
          : t('common.errorBody')}
    </p>
  );
}

function AddEndpointKeyForm({
  accountId,
  endpoint,
  onClose,
}: {
  accountId: string;
  endpoint: Endpoint;
  onClose: () => void;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const [instance] = useState(() => createOperationIdentity().actionId);
  const abortRef = useRef<AbortController | null>(null);
  const attemptRef = useRef<{ input: EndpointKeyCreateInput; operation: OperationIdentity } | null>(
    null,
  );
  const [hasAttempt, setHasAttempt] = useState(false);
  const [draft, dispatch] = useReducer(endpointSecretDraftReducer, undefined, () =>
    initialEndpointSecretDraftState(accountId, instance),
  );
  const [note, setNote] = useState('');
  const [forceStoreFalse, setForceStoreFalse] = useState(false);
  const [outcome, setOutcome] = useState<ActionOutcome>(null);

  const discardStaleMutation = () => {
    abortRef.current?.abort();
    abortRef.current = null;
    attemptRef.current = null;
    setHasAttempt(false);
    setOutcome(null);
    dispatch({ type: 'cancel', accountId, pageInstanceId: instance });
  };

  useEffect(() => () => abortRef.current?.abort(), []);

  const close = () => {
    abortRef.current?.abort();
    attemptRef.current = null;
    setHasAttempt(false);
    dispatch({ type: 'cancel', accountId, pageInstanceId: instance });
    onClose();
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setOutcome(null);
    try {
      validateEndpointSecret(draft.secret);
      if (!draft.ownershipConfirmed) throw new Error('ownership');
    } catch {
      dispatch({
        type: 'local-error',
        accountId,
        pageInstanceId: instance,
        message: t('endpoints.secretRequired'),
      });
      return;
    }
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    const attempt = attemptRef.current ?? {
      input: {
        secret: draft.secret,
        note,
        enabled: true,
        force_store_false: endpoint.connector_type === 'openai-compatible' && forceStoreFalse,
        ownership_confirmed: true,
      },
      operation: createOperationIdentity(),
    };
    attemptRef.current = attempt;
    setHasAttempt(true);
    const controller = new AbortController();
    abortRef.current?.abort();
    abortRef.current = controller;
    dispatch({ type: 'submit', accountId, pageInstanceId: instance });
    try {
      await createEndpointKey(endpoint.id, attempt.input, attempt.operation, controller.signal);
      if (controller.signal.aborted || abortRef.current !== controller) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      attemptRef.current = null;
      setHasAttempt(false);
      dispatch({ type: 'success', accountId, pageInstanceId: instance });
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
      close();
    } catch (error) {
      if (controller.signal.aborted || abortRef.current !== controller) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      const nextOutcome = actionOutcome(error);
      if (nextOutcome !== 'unknown') {
        attemptRef.current = null;
        setHasAttempt(false);
      }
      setOutcome(nextOutcome);
      dispatch({
        type: 'request-error',
        accountId,
        pageInstanceId: instance,
        message: nextOutcome === 'unknown' ? t('common.outcomeUnknown') : t('common.errorBody'),
      });
      if (nextOutcome === 'conflict') {
        if (!coreSessionMatchesAccount(queryClient, accountId)) {
          discardStaleMutation();
          return;
        }
        await queryClient.invalidateQueries({
          queryKey: coreKeys.endpointKeysRoot(accountId, endpoint.id),
        });
        await invalidateResourceDependents(queryClient, accountId, {
          endpointId: endpoint.id,
        });
      } else if (nextOutcome === 'unknown') {
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
      }
      if (!coreSessionMatchesAccount(queryClient, accountId)) discardStaleMutation();
    } finally {
      if (abortRef.current === controller) abortRef.current = null;
    }
  };

  return (
    <form className="core-card core-wizard core-form" onSubmit={(event) => void submit(event)}>
      <div className="core-card__header">
        <h2>{t('endpoints.addKey')}</h2>
        <button type="button" className="btn btn-secondary" onClick={close}>
          {t('common.cancel')}
        </button>
      </div>
      <div className="core-field-grid">
        <label>
          <span>{t('endpoints.secret')}</span>
          <input
            type="password"
            autoComplete="new-password"
            maxLength={65536}
            disabled={hasAttempt}
            value={draft.secret}
            onChange={(event) =>
              dispatch({
                type: 'change',
                accountId,
                pageInstanceId: instance,
                secret: event.target.value,
              })
            }
          />
        </label>
        <label>
          <span>{t('endpoints.keyNote')}</span>
          <input
            maxLength={2048}
            disabled={hasAttempt}
            value={note}
            onChange={(event) => setNote(event.target.value)}
          />
        </label>
      </div>
      <label className="core-checkbox">
        <input
          type="checkbox"
          checked={draft.ownershipConfirmed}
          disabled={hasAttempt}
          onChange={(event) =>
            dispatch({
              type: 'ownership',
              accountId,
              pageInstanceId: instance,
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
            disabled={hasAttempt}
            onChange={(event) => setForceStoreFalse(event.target.checked)}
          />
          <span>{t('endpoints.storePolicy')}</span>
        </label>
      ) : null}
      <p className="core-inline-warning">{t('endpoints.costWarning')}</p>
      {draft.message ? <p className="core-inline-error">{draft.message}</p> : null}
      <OutcomeNotice outcome={outcome} />
      <div className="core-form-actions">
        <span />
        <button type="submit" className="btn btn-primary" disabled={draft.status === 'submitting'}>
          {draft.status === 'submitting'
            ? t('common.working')
            : hasAttempt
              ? t('common.retrySame')
              : t('endpoints.addKeyStep')}
        </button>
      </div>
    </form>
  );
}

function ManualEntryRow({
  accountId,
  endpointId,
  keyId,
  entry,
  impacts,
  impactsKnown,
  onChanged,
}: {
  accountId: string;
  endpointId: string;
  keyId: string;
  entry: CatalogEntry;
  impacts: Array<{ bindingId: string; modelId: string; modelName: string }>;
  impactsKnown: boolean;
  onChanged: () => Promise<boolean>;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [model, setModel] = useState(entry.upstream_model_id);
  const [provider, setProvider] = useState(entry.provider);
  const [replacements, setReplacements] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<ActionOutcome>(null);
  const updateAttemptRef = useRef<{
    input: {
      upstream_model_id: string;
      provider: string;
      expected_pair_revision: string;
      replacements: BindingReplacement[];
    };
    operation: OperationIdentity;
  } | null>(null);
  const deleteAttemptRef = useRef<{
    expectedPairRevision: string;
    replacements: BindingReplacement[];
    operation: OperationIdentity;
  } | null>(null);
  const [attemptKind, setAttemptKind] = useState<'update' | 'delete' | null>(null);

  const discardStaleMutation = () => {
    updateAttemptRef.current = null;
    deleteAttemptRef.current = null;
    setAttemptKind(null);
    setBusy(false);
    setOutcome(null);
    setEditing(false);
  };

  const replacementPayload = (): BindingReplacement[] =>
    impacts.map((impact) => ({
      binding_id: impact.bindingId,
      replacement_upstream_model_id: replacements[impact.bindingId] ?? '',
    }));
  const replacementsReady = impacts.every((impact) => {
    try {
      return Boolean(validateManualValue(replacements[impact.bindingId] ?? '', 512, false));
    } catch {
      return false;
    }
  });
  const updateNeedsReplacements = impacts.length > 0 && model !== entry.upstream_model_id;
  const updateImpactUnknown = !impactsKnown && model !== entry.upstream_model_id;

  useEffect(() => {
    let active = true;
    queueMicrotask(() => {
      if (!active) return;
      const attempt = updateAttemptRef.current;
      if (
        attempt &&
        BigInt(entry.pair_revision) > BigInt(attempt.input.expected_pair_revision) &&
        entry.upstream_model_id === attempt.input.upstream_model_id &&
        entry.provider === attempt.input.provider
      ) {
        updateAttemptRef.current = null;
        setAttemptKind(null);
        setOutcome(null);
      }
      setModel(entry.upstream_model_id);
      setProvider(entry.provider);
    });
    return () => {
      active = false;
    };
  }, [entry.pair_revision, entry.provider, entry.upstream_model_id]);

  const update = async () => {
    if (busy || deleteAttemptRef.current) return;
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    const attempt = updateAttemptRef.current ?? {
      input: {
        upstream_model_id: model,
        provider,
        expected_pair_revision: entry.pair_revision,
        replacements: updateNeedsReplacements ? replacementPayload() : [],
      },
      operation: createOperationIdentity(),
    };
    updateAttemptRef.current = attempt;
    setAttemptKind('update');
    setBusy(true);
    setOutcome(null);
    try {
      const response = await updateManualEntry(
        endpointId,
        keyId,
        entry.id,
        attempt.input,
        attempt.operation,
      );
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      updateAttemptRef.current = null;
      setAttemptKind(null);
      if (!applyManualUpdateToCache(queryClient, accountId, response)) {
        discardStaleMutation();
        return;
      }
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      if (!(await onChanged())) {
        discardStaleMutation();
        return;
      }
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      setEditing(false);
    } catch (error) {
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      const nextOutcome = actionOutcome(error);
      if (nextOutcome !== 'unknown') {
        updateAttemptRef.current = null;
        setAttemptKind(null);
      }
      setOutcome(nextOutcome);
      if (isConflict(error) || isOutcomeUnknown(error)) {
        if (!coreSessionMatchesAccount(queryClient, accountId)) {
          discardStaleMutation();
          return;
        }
        if (
          !(await invalidateResourceDependents(queryClient, accountId, {
            endpointId,
            modelIds: 'all',
          }))
        ) {
          discardStaleMutation();
          return;
        }
        if (!(await onChanged())) {
          discardStaleMutation();
          return;
        }
        if (!coreSessionMatchesAccount(queryClient, accountId)) discardStaleMutation();
      }
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (busy || updateAttemptRef.current) return;
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    const attempt = deleteAttemptRef.current ?? {
      expectedPairRevision: entry.pair_revision,
      replacements: replacementPayload(),
      operation: createOperationIdentity(),
    };
    deleteAttemptRef.current = attempt;
    setAttemptKind('delete');
    setBusy(true);
    setOutcome(null);
    try {
      await deleteManualEntry(
        endpointId,
        keyId,
        entry.id,
        attempt.expectedPairRevision,
        attempt.replacements,
        attempt.operation,
      );
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      deleteAttemptRef.current = null;
      setAttemptKind(null);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      if (
        !(await invalidateResourceDependents(queryClient, accountId, {
          endpointId,
          modelIds: impacts.map((impact) => impact.modelId),
        }))
      ) {
        discardStaleMutation();
        return;
      }
      if (!(await onChanged())) {
        discardStaleMutation();
        return;
      }
    } catch (error) {
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      const nextOutcome = actionOutcome(error);
      if (nextOutcome !== 'unknown') {
        deleteAttemptRef.current = null;
        setAttemptKind(null);
      }
      setOutcome(nextOutcome);
      if (isConflict(error) || isOutcomeUnknown(error)) {
        if (!coreSessionMatchesAccount(queryClient, accountId)) {
          discardStaleMutation();
          return;
        }
        if (
          !(await invalidateResourceDependents(queryClient, accountId, {
            endpointId,
            modelIds: 'all',
          }))
        ) {
          discardStaleMutation();
          return;
        }
        if (!(await onChanged())) {
          discardStaleMutation();
          return;
        }
        if (!coreSessionMatchesAccount(queryClient, accountId)) discardStaleMutation();
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <li className="core-manual-row">
      <div className="core-binding-row__top">
        <div>
          <strong className="core-mono">{entry.upstream_model_id}</strong>
          <div className="core-muted">{entry.provider || t('common.notSet')}</div>
        </div>
        <button
          type="button"
          className="btn btn-secondary"
          disabled={busy || Boolean(attemptKind)}
          onClick={() => setEditing((value) => !value)}
        >
          {editing ? t('common.close') : t('common.edit')}
        </button>
      </div>
      {impacts.length > 0 ? (
        <div className="core-inline-warning">
          <p>{t('endpoints.manualImpact', { count: impacts.length })}</p>
          <ul className="core-impact-list">
            {impacts.map((impact) => (
              <li key={impact.bindingId}>{impact.modelName}</li>
            ))}
          </ul>
        </div>
      ) : null}
      {!impactsKnown ? (
        <p className="core-inline-warning" role="alert">
          {t('endpoints.manualImpactUnknown')}
        </p>
      ) : null}
      {editing ? (
        <div className="core-form">
          <div className="core-field-grid">
            <label>
              <span>{t('endpoints.manualModel')}</span>
              <input
                value={model}
                maxLength={1024}
                disabled={Boolean(attemptKind)}
                onChange={(event) => setModel(event.target.value)}
              />
            </label>
            <label>
              <span>{t('endpoints.manualProvider')}</span>
              <input
                value={provider}
                maxLength={256}
                disabled={Boolean(attemptKind)}
                onChange={(event) => setProvider(event.target.value)}
              />
            </label>
            {impacts.map((impact) => (
              <label key={impact.bindingId}>
                <span>
                  {t('endpoints.replacement', { id: impact.bindingId, model: impact.modelName })}
                </span>
                <input
                  className="core-mono"
                  maxLength={1024}
                  disabled={Boolean(attemptKind)}
                  value={replacements[impact.bindingId] ?? ''}
                  onChange={(event) =>
                    setReplacements((current) => ({
                      ...current,
                      [impact.bindingId]: event.target.value,
                    }))
                  }
                />
              </label>
            ))}
          </div>
          <OutcomeNotice outcome={outcome} />
          <div className="core-form-actions">
            <button
              type="button"
              className="btn btn-danger"
              disabled={
                busy ||
                attemptKind === 'update' ||
                !impactsKnown ||
                (attemptKind !== 'delete' && impacts.length > 0 && !replacementsReady)
              }
              onClick={() => void remove()}
            >
              {attemptKind === 'delete' ? t('common.retrySame') : t('endpoints.deleteManual')}
            </button>
            <button
              type="button"
              className="btn btn-primary"
              disabled={
                busy ||
                attemptKind === 'delete' ||
                updateImpactUnknown ||
                (attemptKind !== 'update' && updateNeedsReplacements && !replacementsReady)
              }
              onClick={() => void update()}
            >
              {busy
                ? t('common.working')
                : attemptKind === 'update'
                  ? t('common.retrySame')
                  : t('endpoints.updateManual')}
            </button>
          </div>
        </div>
      ) : null}
    </li>
  );
}

function ManualCatalog({
  accountId,
  endpointId,
  keyId,
  routingEntries,
  routingKnown,
}: {
  accountId: string;
  endpointId: string;
  keyId: string;
  routingEntries: Array<{
    model: { id: string; full_name: string };
    binding: { id: string; upstream_model_id: string };
  }>;
  routingKnown: boolean;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const [cursorStack, setCursorStack] = useState<Array<string | undefined>>([undefined]);
  const cursor = cursorStack.at(-1);
  const catalog = useCatalog(accountId, endpointId, keyId, cursor);
  const [upstreamModel, setUpstreamModel] = useState('');
  const [provider, setProvider] = useState('');
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<ActionOutcome>(null);
  const attemptRef = useRef<{
    input: { upstream_model_id: string; provider: string };
    operation: OperationIdentity;
  } | null>(null);
  const [hasAttempt, setHasAttempt] = useState(false);

  const discardStaleMutation = () => {
    attemptRef.current = null;
    setHasAttempt(false);
    setUpstreamModel('');
    setProvider('');
    setBusy(false);
    setOutcome(null);
  };

  useEffect(() => {
    let active = true;
    queueMicrotask(() => {
      if (!active) return;
      const attempt = attemptRef.current;
      if (!attempt || !catalog.data) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      if (
        catalog.data.manual_entries.some(
          (entry) =>
            entry.upstream_model_id === attempt.input.upstream_model_id &&
            entry.provider === attempt.input.provider,
        )
      ) {
        attemptRef.current = null;
        setHasAttempt(false);
        setUpstreamModel('');
        setProvider('');
        setOutcome(null);
      }
    });
    return () => {
      active = false;
    };
  }, [accountId, catalog.data, queryClient]);

  const refresh = async (): Promise<boolean> => {
    if (!coreSessionMatchesAccount(queryClient, accountId)) return false;
    await Promise.all([
      queryClient.invalidateQueries({
        queryKey: coreKeys.catalogRoot(accountId, endpointId, keyId),
      }),
      queryClient.invalidateQueries({
        queryKey: coreKeys.endpointRoutingRoot(accountId, endpointId),
        exact: false,
      }),
      invalidateResourceDependents(queryClient, accountId, { endpointId }),
    ]);
    return coreSessionMatchesAccount(queryClient, accountId);
  };

  const create = async (event: FormEvent) => {
    event.preventDefault();
    if (busy) return;
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    const attempt = attemptRef.current ?? {
      input: { upstream_model_id: upstreamModel, provider },
      operation: createOperationIdentity(),
    };
    attemptRef.current = attempt;
    setHasAttempt(true);
    setBusy(true);
    setOutcome(null);
    try {
      await createManualEntries(endpointId, keyId, [attempt.input], attempt.operation);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      attemptRef.current = null;
      setHasAttempt(false);
      setUpstreamModel('');
      setProvider('');
      if (!(await refresh())) discardStaleMutation();
    } catch (error) {
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      const nextOutcome = actionOutcome(error);
      if (nextOutcome !== 'unknown') {
        attemptRef.current = null;
        setHasAttempt(false);
      }
      setOutcome(nextOutcome);
      if (isConflict(error) || isOutcomeUnknown(error)) {
        if (!(await refresh())) discardStaleMutation();
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="core-card">
      <div className="core-card__header">
        <div>
          <h3>{t('endpoints.manualTitle')}</h3>
          <p className="core-muted">{t('endpoints.manualAlways')}</p>
        </div>
      </div>
      <form className="core-form" onSubmit={(event) => void create(event)}>
        <div className="core-field-grid">
          <label>
            <span>{t('endpoints.manualModel')}</span>
            <input
              className="core-mono"
              value={upstreamModel}
              maxLength={1024}
              disabled={hasAttempt}
              onChange={(event) => setUpstreamModel(event.target.value)}
            />
          </label>
          <label>
            <span>{t('endpoints.manualProvider')}</span>
            <input
              value={provider}
              maxLength={256}
              disabled={hasAttempt}
              onChange={(event) => setProvider(event.target.value)}
            />
          </label>
        </div>
        <OutcomeNotice outcome={outcome} />
        <div className="core-form-actions">
          <span />
          <button
            type="submit"
            className="btn btn-primary"
            disabled={busy || (!hasAttempt && !upstreamModel)}
          >
            {busy
              ? t('common.working')
              : hasAttempt
                ? t('common.retrySame')
                : t('endpoints.manualAdd')}
          </button>
        </div>
      </form>
      {catalog.isPending && !catalog.data ? (
        <CoreLoading compact />
      ) : !catalog.data ? (
        <CoreErrorPanel
          compact
          error={catalog.error ?? new Error('The manually added model list is unavailable.')}
          onRetry={() => void catalog.refetch()}
        />
      ) : (
        <>
          {catalog.error ? (
            <CoreErrorPanel compact error={catalog.error} onRetry={() => void catalog.refetch()} />
          ) : null}
          {catalog.data.manual_entries.length === 0 ? (
            <p className="core-muted">{t('endpoints.manualEmpty')}</p>
          ) : (
            <ul className="core-manual-list">
              {catalog.data.manual_entries.map((entry) => (
                <ManualEntryRow
                  key={entry.id}
                  accountId={accountId}
                  endpointId={endpointId}
                  keyId={keyId}
                  entry={entry}
                  impactsKnown={routingKnown}
                  impacts={routingEntries
                    .filter(
                      (projection) =>
                        projection.binding.upstream_model_id === entry.upstream_model_id,
                    )
                    .map((projection) => ({
                      bindingId: projection.binding.id,
                      modelId: projection.model.id,
                      modelName: projection.model.full_name,
                    }))}
                  onChanged={refresh}
                />
              ))}
            </ul>
          )}
          {cursorStack.length > 1 || catalog.data.next_cursor ? (
            <div className="core-pagination">
              <button
                type="button"
                className="btn btn-secondary"
                disabled={cursorStack.length <= 1}
                onClick={() => setCursorStack((current) => current.slice(0, -1))}
              >
                {t('common.previous')}
              </button>
              <button
                type="button"
                className="btn btn-secondary"
                disabled={!catalog.data.next_cursor}
                onClick={() =>
                  catalog.data.next_cursor &&
                  setCursorStack((current) => [...current, catalog.data.next_cursor ?? undefined])
                }
              >
                {t('common.next')}
              </button>
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}

function EndpointKeyCard({
  accountId,
  endpoint,
  keyData,
  routingEntries,
  routingPending,
  routingError,
  onRetryRouting,
}: {
  accountId: string;
  endpoint: Endpoint;
  keyData: EndpointKey;
  routingEntries: Array<{
    model: { id: string; full_name: string };
    binding: { id: string; upstream_model_id: string };
  }>;
  routingPending: boolean;
  routingError: unknown;
  onRetryRouting: () => void;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const catalog = useCatalog(accountId, endpoint.id, keyData.id);
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<ActionOutcome>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [note, setNote] = useState(keyData.note);
  const [reconciliationRequired, setReconciliationRequired] = useState(false);
  const [replayAttempt, setReplayAttempt] = useState<
    | { kind: 'refresh'; evidenceRevision: string; operation: OperationIdentity }
    | { kind: 'patch'; input: EndpointKeyPatchInput; operation: OperationIdentity }
    | { kind: 'delete'; expectedRevision: string; operation: OperationIdentity }
    | null
  >(null);

  const discardStaleMutation = () => {
    setReplayAttempt(null);
    setBusy(false);
    setOutcome(null);
    setReconciliationRequired(false);
    setDeleteOpen(false);
    setEditing(false);
  };

  useEffect(() => {
    let active = true;
    queueMicrotask(() => {
      if (active) setNote(keyData.note);
    });
    return () => {
      active = false;
    };
  }, [keyData.note, keyData.revision]);

  useEffect(() => {
    if (!replayAttempt) return;
    let active = true;
    queueMicrotask(() => {
      if (!active) return;
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      const confirmed =
        replayAttempt.kind === 'refresh'
          ? Boolean(
              catalog.data &&
              BigInt(catalog.data.evidence.revision) > BigInt(replayAttempt.evidenceRevision),
            )
          : replayAttempt.kind === 'patch'
            ? BigInt(keyData.revision) > BigInt(replayAttempt.input.expected_revision) &&
              (replayAttempt.input.note === undefined ||
                keyData.note === replayAttempt.input.note) &&
              (replayAttempt.input.enabled === undefined ||
                keyData.enabled === replayAttempt.input.enabled) &&
              (replayAttempt.input.force_store_false === undefined ||
                keyData.force_store_false === replayAttempt.input.force_store_false)
            : false;
      if (!confirmed) return;
      setReplayAttempt(null);
      setOutcome(null);
      if (replayAttempt.kind === 'patch' && replayAttempt.input.note !== undefined)
        setEditing(false);
    });
    return () => {
      active = false;
    };
  }, [
    catalog.data,
    keyData.enabled,
    keyData.force_store_false,
    keyData.note,
    keyData.revision,
    accountId,
    queryClient,
    replayAttempt,
  ]);

  const reconcile = async (deleted = replayAttempt?.kind === 'delete') => {
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return false;
    }
    await Promise.all([
      queryClient.invalidateQueries({
        queryKey: coreKeys.endpointKeysRoot(accountId, endpoint.id),
      }),
      queryClient.invalidateQueries({ queryKey: coreKeys.endpoint(accountId, endpoint.id) }),
      ...(deleted
        ? [queryClient.invalidateQueries({ queryKey: coreKeys.endpointsRoot(accountId) })]
        : []),
      queryClient.invalidateQueries({
        queryKey: coreKeys.catalogRoot(accountId, endpoint.id, keyData.id),
      }),
      queryClient.invalidateQueries({ queryKey: coreKeys.modelsRoot(accountId) }),
      invalidateResourceDependents(queryClient, accountId, {
        endpointId: endpoint.id,
        ...(deleted ? { modelIds: 'all' as const, charity: true } : {}),
      }),
    ]);
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return false;
    }
    const roots = [
      coreKeys.endpointKeysRoot(accountId, endpoint.id),
      coreKeys.endpoint(accountId, endpoint.id),
      coreKeys.catalogRoot(accountId, endpoint.id, keyData.id),
    ];
    const ready = roots.every((root) =>
      queryClient
        .getQueryCache()
        .findAll({ queryKey: root, exact: false })
        .every((query) => query.state.status !== 'error'),
    );
    setReconciliationRequired(!ready);
    return ready;
  };

  const run = async (attempt: NonNullable<typeof replayAttempt>) => {
    if (reconciliationRequired) return;
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleMutation();
      return;
    }
    setBusy(true);
    setOutcome(null);
    try {
      if (attempt.kind === 'refresh') {
        await refreshDiscovery(endpoint.id, keyData.id, attempt.operation);
      } else if (attempt.kind === 'patch') {
        await patchEndpointKey(endpoint.id, keyData.id, attempt.input, attempt.operation);
      } else {
        await deleteEndpointKey(
          endpoint.id,
          keyData.id,
          attempt.expectedRevision,
          attempt.operation,
        );
      }
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      setReplayAttempt(null);
      if (attempt.kind === 'patch' && attempt.input.note !== undefined) setEditing(false);
      if (attempt.kind === 'delete') setDeleteOpen(false);
      await reconcile(attempt.kind === 'delete');
    } catch (error) {
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleMutation();
        return;
      }
      const nextOutcome = actionOutcome(error);
      setReplayAttempt(nextOutcome === 'unknown' ? attempt : null);
      setOutcome(nextOutcome);
      if (isConflict(error) || isOutcomeUnknown(error)) await reconcile(attempt.kind === 'delete');
    } finally {
      setBusy(false);
    }
  };

  const physicalAvailable =
    endpoint.enabled && keyData.enabled && keyData.suspension_state === 'none';
  const display =
    `${keyData.display_head}${keyData.display_head && keyData.display_tail ? '…' : ''}${keyData.display_tail}` ||
    t('common.notSet');
  const projectedRoutes = routingEntries.map((projection) => {
    if (!physicalAvailable) return { ...projection, status: 'blocked' as const };
    if (catalog.isPending || catalog.error) return { ...projection, status: 'unknown' as const };
    const manuallySupported = catalog.data.manual_entries.some(
      (entry) => entry.upstream_model_id === projection.binding.upstream_model_id,
    );
    const automaticallySupported =
      catalog.data.evidence.state === 'succeeded' &&
      catalog.data.automatic_entries.some(
        (entry) =>
          entry.upstream_model_id === projection.binding.upstream_model_id &&
          entry.source_revision === catalog.data.evidence.revision,
      );
    if (manuallySupported || automaticallySupported)
      return { ...projection, status: 'available' as const };
    return {
      ...projection,
      status: catalog.data.next_cursor ? ('unknown' as const) : ('blocked' as const),
    };
  });
  const availableRoutes = projectedRoutes.filter(
    (projection) => projection.status === 'available',
  ).length;
  const hasUnknownRoute = projectedRoutes.some((projection) => projection.status === 'unknown');

  return (
    <li className="core-key-card">
      <div className="core-key-card__top">
        <div>
          <strong>{t('endpoints.key')}</strong>
          <div>
            <SafeCopyValue value={display} label={t('endpoints.key')} />
          </div>
        </div>
        {keyData.suspension_state === 'security_processing' ? (
          <StatusPill tone="danger">{t('endpoints.securityProcessing')}</StatusPill>
        ) : (
          <StatusPill tone={keyData.enabled ? 'success' : 'neutral'}>
            {keyData.enabled ? t('common.enabled') : t('common.disabled')}
          </StatusPill>
        )}
      </div>
      <dl className="core-detail-list">
        <div>
          <dt>{t('endpoints.keyNote')}</dt>
          <dd>{keyData.note || t('common.notSet')}</dd>
        </div>
        <div>
          <dt>{t('endpoints.storePolicy')}</dt>
          <dd>{keyData.force_store_false ? t('common.yes') : t('common.no')}</dd>
        </div>
        <div>
          <dt>{t('common.updated')}</dt>
          <dd>
            <CoreTime value={keyData.updated_at} />
          </dd>
        </div>
      </dl>

      <section className="core-card">
        <div className="core-card__header">
          <h3>{t('endpoints.discovery')}</h3>
        </div>
        {catalog.isPending ? (
          <CoreLoading compact />
        ) : catalog.error ? (
          <CoreErrorPanel compact error={catalog.error} onRetry={() => void catalog.refetch()} />
        ) : (
          <>
            <DiscoveryStatus evidence={catalog.data.evidence} />
            {catalog.data.evidence.observed_at !== null ? (
              <p>
                <span className="core-muted">{t('endpoints.observedAt')}: </span>
                <CoreTime value={catalog.data.evidence.observed_at} />
              </p>
            ) : null}
          </>
        )}
        <button
          type="button"
          className="btn btn-secondary"
          disabled={busy || reconciliationRequired || !physicalAvailable || Boolean(replayAttempt)}
          onClick={() =>
            void run({
              kind: 'refresh',
              evidenceRevision: catalog.data?.evidence.revision ?? '0',
              operation: createOperationIdentity(),
            })
          }
        >
          {busy ? t('common.working') : t('endpoints.refreshDiscovery')}
        </button>
      </section>

      <section className="core-card">
        <div className="core-card__header">
          <h3>{t('endpoints.routing')}</h3>
        </div>
        {routingPending ? (
          <CoreLoading compact />
        ) : routingError ? (
          <CoreErrorPanel compact error={routingError} onRetry={onRetryRouting} />
        ) : routingEntries.length === 0 ? (
          <p className="core-muted">{t('endpoints.routingNone')}</p>
        ) : (
          <>
            <p
              className={
                availableRoutes > 0 && !hasUnknownRoute
                  ? 'core-inline-success'
                  : 'core-inline-warning'
              }
            >
              {hasUnknownRoute
                ? t('endpoints.routingUnknown')
                : availableRoutes > 0
                  ? t('endpoints.routingAvailable', { count: availableRoutes })
                  : t('endpoints.routingBlocked')}
            </p>
            <ul className="core-routing-list">
              {projectedRoutes.map((projection) => (
                <li key={projection.binding.id}>
                  <span className="core-mono">
                    {projection.model.full_name} → {projection.binding.upstream_model_id}
                  </span>
                  <StatusPill
                    tone={
                      projection.status === 'available'
                        ? 'success'
                        : projection.status === 'blocked'
                          ? 'warning'
                          : 'neutral'
                    }
                  >
                    {projection.status === 'available'
                      ? t('common.available')
                      : projection.status === 'blocked'
                        ? t('common.blocked')
                        : t('common.unknown')}
                  </StatusPill>
                </li>
              ))}
            </ul>
          </>
        )}
      </section>

      <ManualCatalog
        accountId={accountId}
        endpointId={endpoint.id}
        keyId={keyData.id}
        routingEntries={routingEntries}
        routingKnown={!routingPending && !routingError}
      />
      <OutcomeNotice outcome={outcome} />
      {reconciliationRequired ? (
        <button
          type="button"
          className="btn btn-secondary"
          disabled={busy}
          onClick={() => void reconcile()}
        >
          {t('common.reconcile')}
        </button>
      ) : null}
      {replayAttempt && (replayAttempt.kind !== 'delete' || !deleteOpen) ? (
        <button
          type="button"
          className="btn btn-secondary"
          disabled={busy || reconciliationRequired}
          onClick={() => void run(replayAttempt)}
        >
          {t('common.retrySame')}
        </button>
      ) : null}
      {editing ? (
        <form
          className="core-form"
          onSubmit={(event) => {
            event.preventDefault();
            void run({
              kind: 'patch',
              input: { note, expected_revision: keyData.revision },
              operation: createOperationIdentity(),
            });
          }}
        >
          <div className="core-field-grid">
            <label>
              <span>{t('endpoints.keyNote')}</span>
              <input
                value={note}
                maxLength={2048}
                disabled={Boolean(replayAttempt)}
                onChange={(event) => setNote(event.target.value)}
              />
            </label>
          </div>
          <div className="core-form-actions">
            <button
              type="button"
              className="btn btn-secondary"
              disabled={busy || Boolean(replayAttempt)}
              onClick={() => {
                setNote(keyData.note);
                setEditing(false);
              }}
            >
              {t('common.cancel')}
            </button>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={
                busy || reconciliationRequired || Boolean(replayAttempt) || note === keyData.note
              }
            >
              {t('common.save')}
            </button>
          </div>
        </form>
      ) : null}
      <div className="core-row-actions">
        <button
          type="button"
          className="btn btn-secondary"
          disabled={
            busy ||
            reconciliationRequired ||
            Boolean(replayAttempt) ||
            keyData.suspension_state !== 'none'
          }
          onClick={() => setEditing((value) => !value)}
        >
          {t('common.edit')}
        </button>
        <button
          type="button"
          className="btn btn-secondary"
          disabled={
            busy ||
            reconciliationRequired ||
            Boolean(replayAttempt) ||
            keyData.suspension_state !== 'none'
          }
          onClick={() =>
            void run({
              kind: 'patch',
              input: { enabled: !keyData.enabled, expected_revision: keyData.revision },
              operation: createOperationIdentity(),
            })
          }
        >
          {keyData.enabled ? t('endpoints.keyToggleOff') : t('endpoints.keyToggleOn')}
        </button>
        {endpoint.connector_type === 'openai-compatible' ? (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={
              busy ||
              reconciliationRequired ||
              Boolean(replayAttempt) ||
              keyData.suspension_state !== 'none'
            }
            onClick={() =>
              void run({
                kind: 'patch',
                input: {
                  force_store_false: !keyData.force_store_false,
                  expected_revision: keyData.revision,
                },
                operation: createOperationIdentity(),
              })
            }
          >
            {keyData.force_store_false
              ? t('endpoints.storePolicyOff')
              : t('endpoints.storePolicyOn')}
          </button>
        ) : null}
        <button
          type="button"
          className="btn btn-danger"
          disabled={
            busy ||
            reconciliationRequired ||
            Boolean(replayAttempt) ||
            keyData.suspension_state !== 'none'
          }
          onClick={() => setDeleteOpen(true)}
        >
          {t('endpoints.deleteKey')}
        </button>
      </div>
      <ConfirmDialog
        open={deleteOpen}
        title={t('endpoints.deleteKeyTitle')}
        description={t('endpoints.deleteKeyBody')}
        confirmLabel={
          replayAttempt?.kind === 'delete' ? t('common.retrySame') : t('endpoints.deleteKey')
        }
        danger
        busy={busy}
        onCancel={() => {
          if (!busy) setDeleteOpen(false);
        }}
        onConfirm={() =>
          void run(
            replayAttempt?.kind === 'delete'
              ? replayAttempt
              : {
                  kind: 'delete',
                  expectedRevision: keyData.revision,
                  operation: createOperationIdentity(),
                },
          )
        }
      />
    </li>
  );
}

export function EndpointDetail({
  accountId,
  endpointId,
}: {
  accountId: string;
  endpointId: string;
}) {
  const { t } = useCoreCopy();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [keyCursors, setKeyCursors] = useState<Array<string | undefined>>([undefined]);
  const endpoint = useEndpoint(accountId, endpointId);
  const keys = useEndpointKeysPage(accountId, endpointId, keyCursors.at(-1));
  const keyIds = keys.data?.data.map((key) => key.id) ?? [];
  const routing = useEndpointRoutingProjection(accountId, endpointId, keyIds, Boolean(keys.data));
  const [addingKey, setAddingKey] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<ActionOutcome>(null);
  const [editing, setEditing] = useState(false);
  const [endpointNote, setEndpointNote] = useState('');
  const [reconciliationRequired, setReconciliationRequired] = useState(false);
  const [replayAttempt, setReplayAttempt] = useState<
    | { kind: 'patch'; input: EndpointPatchInput; operation: OperationIdentity }
    | { kind: 'delete'; expectedRevision: string; operation: OperationIdentity }
    | null
  >(null);

  const discardStaleEndpointMutation = () => {
    setReplayAttempt(null);
    setBusy(false);
    setOutcome(null);
    setReconciliationRequired(false);
    setDeleteOpen(false);
    setEditing(false);
    setAddingKey(false);
  };

  useEffect(() => {
    if (!endpoint.data) return;
    let active = true;
    queueMicrotask(() => {
      if (active) setEndpointNote(endpoint.data?.note ?? '');
    });
    return () => {
      active = false;
    };
  }, [endpoint.data]);

  const reconcile = async (attempt = replayAttempt) => {
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleEndpointMutation();
      return false;
    }
    const [endpointResult, keysResult] = await Promise.all([
      endpoint.refetch(),
      keys.refetch(),
      queryClient.invalidateQueries({ queryKey: coreKeys.endpointsRoot(accountId) }),
      invalidateResourceDependents(queryClient, accountId, {
        endpointId,
        ...(attempt?.kind === 'delete' ? { modelIds: 'all' as const, charity: true } : {}),
      }),
    ]);
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleEndpointMutation();
      return false;
    }
    if (
      attempt?.kind === 'delete' &&
      endpointResult.error &&
      isNotFoundError(endpointResult.error)
    ) {
      setReplayAttempt(null);
      setOutcome(null);
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleEndpointMutation();
        return false;
      }
      queryClient.removeQueries({ queryKey: coreKeys.endpoint(accountId, endpointId) });
      navigate(CORE_ROUTE_PATHS.endpoints);
      return true;
    }
    const ready =
      !endpointResult.error &&
      !keysResult.error &&
      [
        ...queryClient
          .getQueryCache()
          .findAll({ queryKey: coreKeys.endpoint(accountId, endpointId), exact: false }),
        ...queryClient
          .getQueryCache()
          .findAll({ queryKey: coreKeys.endpointKeysRoot(accountId, endpointId), exact: false }),
      ].every((query) => query.state.status !== 'error');
    setReconciliationRequired(!ready);
    if (ready && attempt?.kind === 'patch' && endpointResult.data) {
      const confirmed =
        BigInt(endpointResult.data.revision) > BigInt(attempt.input.expected_revision) &&
        (attempt.input.note === undefined || endpointResult.data.note === attempt.input.note) &&
        (attempt.input.enabled === undefined ||
          endpointResult.data.enabled === attempt.input.enabled);
      if (confirmed) {
        setReplayAttempt(null);
        setOutcome(null);
        if (attempt.input.note !== undefined) setEditing(false);
      }
    }
    return ready;
  };

  if (endpoint.isPending && !endpoint.data)
    return (
      <div className="page core-page">
        <CoreLoading />
      </div>
    );
  if (!endpoint.data)
    return (
      <div className="page core-page">
        <CoreErrorPanel
          error={endpoint.error ?? new Error('The endpoint details are unavailable.')}
          onRetry={() => void endpoint.refetch()}
        />
      </div>
    );

  const runEndpointAction = async (attempt: NonNullable<typeof replayAttempt>) => {
    if (reconciliationRequired) return;
    if (!coreSessionMatchesAccount(queryClient, accountId)) {
      discardStaleEndpointMutation();
      return;
    }
    setBusy(true);
    setOutcome(null);
    try {
      if (attempt.kind === 'patch') {
        await patchEndpoint(endpoint.data.id, attempt.input, attempt.operation);
      } else {
        await deleteEndpoint(endpoint.data.id, attempt.expectedRevision, attempt.operation);
      }
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleEndpointMutation();
        return;
      }
      setReplayAttempt(null);
      if (attempt.kind === 'delete') {
        const [dependentsCurrent] = await Promise.all([
          invalidateResourceDependents(queryClient, accountId, {
            endpointId: endpoint.data.id,
            modelIds: 'all',
            charity: true,
          }),
          queryClient.invalidateQueries({ queryKey: coreKeys.endpointsRoot(accountId) }),
        ]);
        if (!dependentsCurrent) {
          discardStaleEndpointMutation();
          return;
        }
        if (!coreSessionMatchesAccount(queryClient, accountId)) {
          discardStaleEndpointMutation();
          return;
        }
        queryClient.removeQueries({ queryKey: coreKeys.endpoint(accountId, endpoint.data.id) });
        navigate(CORE_ROUTE_PATHS.endpoints);
      } else {
        if (attempt.input.note !== undefined) setEditing(false);
        await reconcile();
      }
    } catch (error) {
      if (!coreSessionMatchesAccount(queryClient, accountId)) {
        discardStaleEndpointMutation();
        return;
      }
      const nextOutcome = actionOutcome(error);
      setReplayAttempt(nextOutcome === 'unknown' ? attempt : null);
      setOutcome(nextOutcome);
      if (isConflict(error) || isOutcomeUnknown(error)) {
        await reconcile(nextOutcome === 'unknown' ? attempt : null);
      }
    } finally {
      setBusy(false);
    }
  };

  const removeEndpoint = async () => {
    if (busy || reconciliationRequired || replayAttempt?.kind === 'patch') return;
    await runEndpointAction(
      replayAttempt?.kind === 'delete'
        ? replayAttempt
        : {
            kind: 'delete',
            expectedRevision: endpoint.data.revision,
            operation: createOperationIdentity(),
          },
    );
  };

  return (
    <div className="page core-page core-stack">
      <PageHeader
        icon="endpoints"
        title={t('endpoints.detailsTitle')}
        description={t('endpoints.detailsDescription')}
        back={<Link to={CORE_ROUTE_PATHS.endpoints}>{t('common.back')}</Link>}
        actions={
          <button
            type="button"
            className="btn btn-primary"
            disabled={reconciliationRequired || Boolean(replayAttempt)}
            onClick={() => setAddingKey(true)}
          >
            {t('endpoints.addKey')}
          </button>
        }
      />
      <section className="core-card">
        <div className="core-card__header">
          <h2>
            <ConnectorLabel value={endpoint.data.connector_type} />
          </h2>
          <StatusPill tone={endpoint.data.enabled ? 'success' : 'neutral'}>
            {endpoint.data.enabled ? t('common.enabled') : t('common.disabled')}
          </StatusPill>
        </div>
        <dl className="core-detail-list">
          <div>
            <dt>{t('endpoints.baseUrl')}</dt>
            <dd>
              <SafeCopyValue value={endpoint.data.base_url} label={t('endpoints.baseUrl')} />
            </dd>
          </div>
          <div>
            <dt>{t('endpoints.origin')}</dt>
            <dd>
              {endpoint.data.origin.kind === 'mainstream'
                ? t('endpoints.originMainstream', { name: endpoint.data.origin.name })
                : t('endpoints.originCustom')}
            </dd>
          </div>
          <div>
            <dt>{t('endpoints.note')}</dt>
            <dd>{endpoint.data.note || t('common.notSet')}</dd>
          </div>
          <div>
            <dt>{t('endpoints.keyCount')}</dt>
            <dd className="core-number">{endpoint.data.key_count}</dd>
          </div>
        </dl>
        <div className="core-row-actions">
          <button
            type="button"
            className="btn btn-secondary"
            disabled={busy || reconciliationRequired || Boolean(replayAttempt)}
            onClick={() =>
              void runEndpointAction({
                kind: 'patch',
                input: {
                  enabled: !endpoint.data.enabled,
                  expected_revision: endpoint.data.revision,
                },
                operation: createOperationIdentity(),
              })
            }
          >
            {endpoint.data.enabled ? t('endpoints.toggleOff') : t('endpoints.toggleOn')}
          </button>
        </div>
        <OutcomeNotice outcome={outcome} />
        {reconciliationRequired ? (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={busy}
            onClick={() => void reconcile()}
          >
            {t('common.reconcile')}
          </button>
        ) : null}
        {replayAttempt && (replayAttempt.kind !== 'delete' || !deleteOpen) ? (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={busy || reconciliationRequired}
            onClick={() => void runEndpointAction(replayAttempt)}
          >
            {t('common.retrySame')}
          </button>
        ) : null}
        {editing ? (
          <form
            className="core-form"
            onSubmit={(event) => {
              event.preventDefault();
              void runEndpointAction({
                kind: 'patch',
                input: { note: endpointNote, expected_revision: endpoint.data.revision },
                operation: createOperationIdentity(),
              });
            }}
          >
            <div className="core-field-grid">
              <label>
                <span>{t('endpoints.note')}</span>
                <input
                  value={endpointNote}
                  maxLength={2048}
                  disabled={Boolean(replayAttempt)}
                  onChange={(event) => setEndpointNote(event.target.value)}
                />
              </label>
            </div>
            <div className="core-form-actions">
              <button
                type="button"
                className="btn btn-secondary"
                disabled={busy || Boolean(replayAttempt)}
                onClick={() => {
                  setEndpointNote(endpoint.data.note);
                  setEditing(false);
                }}
              >
                {t('common.cancel')}
              </button>
              <button
                type="submit"
                className="btn btn-primary"
                disabled={
                  busy ||
                  reconciliationRequired ||
                  Boolean(replayAttempt) ||
                  endpointNote === endpoint.data.note
                }
              >
                {t('common.save')}
              </button>
            </div>
          </form>
        ) : (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={busy || reconciliationRequired || Boolean(replayAttempt)}
            onClick={() => setEditing(true)}
          >
            {t('common.edit')}
          </button>
        )}
      </section>

      {addingKey ? (
        <AddEndpointKeyForm
          accountId={accountId}
          endpoint={endpoint.data}
          onClose={() => setAddingKey(false)}
        />
      ) : null}

      <section className="core-card">
        <div className="core-card__header">
          <h2>{t('endpoints.key')}</h2>
        </div>
        {keys.error && keys.data ? (
          <CoreErrorPanel compact error={keys.error} onRetry={() => void keys.refetch()} />
        ) : null}
        {keys.isPending && !keys.data ? (
          <CoreLoading />
        ) : !keys.data ? (
          <CoreErrorPanel
            error={keys.error ?? new Error('The key details are unavailable.')}
            onRetry={() => void keys.refetch()}
          />
        ) : keys.data.data.length === 0 ? (
          <CoreEmpty
            title={t('endpoints.noKeysTitle')}
            body={t('endpoints.noKeysBody')}
            action={
              <button type="button" className="btn btn-primary" onClick={() => setAddingKey(true)}>
                {t('endpoints.addKey')}
              </button>
            }
          />
        ) : (
          <ul className="core-key-list">
            {keys.data.data.map((keyData) => (
              <EndpointKeyCard
                key={keyData.id}
                accountId={accountId}
                endpoint={endpoint.data}
                keyData={keyData}
                routingEntries={routing.data?.byKey[keyData.id] ?? []}
                routingPending={routing.isPending}
                routingError={routing.error}
                onRetryRouting={() => void routing.refetch()}
              />
            ))}
          </ul>
        )}
        {keys.data && (keyCursors.length > 1 || keys.data.next_cursor) ? (
          <nav className="core-pagination" aria-label={t('endpoints.key')}>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={keyCursors.length <= 1}
              onClick={() => setKeyCursors((current) => current.slice(0, -1))}
            >
              {t('common.previous')}
            </button>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={!keys.data.next_cursor}
              onClick={() =>
                keys.data.next_cursor &&
                setKeyCursors((current) => [...current, keys.data.next_cursor ?? undefined])
              }
            >
              {t('common.next')}
            </button>
          </nav>
        ) : null}
      </section>

      <section className="core-card core-danger-zone">
        <div className="core-card__header">
          <h2>{t('endpoints.dangerTitle')}</h2>
        </div>
        <p>{t('endpoints.deleteEndpointBody')}</p>
        <div className="core-row-actions">
          <span />
          <button
            type="button"
            className="btn btn-danger"
            disabled={busy || reconciliationRequired || Boolean(replayAttempt)}
            onClick={() => setDeleteOpen(true)}
          >
            {t('endpoints.deleteEndpoint')}
          </button>
        </div>
      </section>
      <ConfirmDialog
        open={deleteOpen}
        title={t('endpoints.deleteEndpointTitle')}
        description={t('endpoints.deleteEndpointBody')}
        confirmLabel={
          replayAttempt?.kind === 'delete' ? t('common.retrySame') : t('endpoints.deleteEndpoint')
        }
        danger
        busy={busy}
        onCancel={() => {
          if (!busy) setDeleteOpen(false);
        }}
        onConfirm={() => void removeEndpoint()}
      />
    </div>
  );
}

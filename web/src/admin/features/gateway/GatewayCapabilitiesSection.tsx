import { useEffect, useId, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { OperationFeedback } from '@shared/components/OperationFeedback';
import { Card, EmptyState, ErrorState, LoadingState } from '@shared/components/States';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { GatewayCapabilitySummary } from '@shared/gateway/GatewayCapabilitySummary';
import { gatewayAdapterLabels, gatewayEffortLabels } from '@shared/gateway/copy';
import {
  GATEWAY_ADAPTERS,
  gatewayAdapterEfforts,
  type GatewayAdapter,
} from '@shared/gateway/capabilities';
import {
  gatewayCapabilityKeys,
  getGatewayCapabilities,
  writeGatewayCapability,
  type GatewayCapabilityEntry,
  type GatewayCapabilityDeleted,
  type GatewayCapabilityIntent,
  type GatewayCapabilityRecord,
} from './api';

function entryFor(row?: GatewayCapabilityRecord): GatewayCapabilityEntry {
  return {
    base_url: row?.base_url ?? '',
    model: row?.model ?? '',
    adapter: row?.adapter ?? 'openai_chat',
    efforts: [...(row?.efforts ?? [])],
    max_output_tokens: row?.max_output_tokens ?? 0,
    storage: row?.storage ?? 'reject',
    cache: row?.cache ?? 'reject',
  };
}

export default function GatewayCapabilitiesSection() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const prefix = useId();
  const [editor, setEditor] = useState<GatewayCapabilityRecord | null | undefined>();
  const [draft, setDraft] = useState<GatewayCapabilityEntry>(() => entryFor());
  const [outputText, setOutputText] = useState('0');
  const [deleteTarget, setDeleteTarget] = useState<GatewayCapabilityRecord | null>(null);
  const advanced = useRef<HTMLDetailsElement>(null);
  const hasAdvanced = draft.storage !== 'reject' || draft.cache !== 'reject';
  useEffect(() => {
    if (hasAdvanced && advanced.current) advanced.current.open = true;
  }, [hasAdvanced, editor]);

  const list = useQuery({
    queryKey: gatewayCapabilityKeys.root,
    queryFn: ({ signal }) => getGatewayCapabilities(signal),
    retry: false,
  });
  const operation = useRetainedOperation(
    (intent: GatewayCapabilityIntent, key, context) =>
      writeGatewayCapability(intent, key, context.signal),
    async (_intent, _error, context) => {
      const rows = await getGatewayCapabilities(context.signal);
      context.commit(() => client.setQueryData(gatewayCapabilityKeys.root, rows));
    },
    gatewayCapabilityKeys.root,
  );
  const change = (patch: Partial<GatewayCapabilityEntry>) => {
    operation.reset();
    setDraft((value) => ({ ...value, ...patch }));
  };
  const open = (row: GatewayCapabilityRecord | null) => {
    operation.reset();
    setEditor(row);
    setDraft(entryFor(row ?? undefined));
    setOutputText(String(row?.max_output_tokens ?? 0));
  };
  const complete = (result: GatewayCapabilityRecord | GatewayCapabilityDeleted) => {
    if ('deleted' in result) {
      setDeleteTarget(null);
      if (editor?.id === result.id) setEditor(undefined);
      return;
    }
    setEditor(result);
    setDraft(entryFor(result));
    setOutputText(String(result.max_output_tokens));
  };
  const save = () => {
    const entry = { ...draft, max_output_tokens: Number(outputText) };
    const intent: GatewayCapabilityIntent = editor
      ? { action: 'update', id: editor.id, expected_revision: editor.revision, entry }
      : { action: 'create', expected_revision: '0', entry };
    operation.mutate(intent, { onSuccess: complete });
  };
  const anthropic = draft.adapter.startsWith('anthropic_');
  const feedback = (
    <OperationFeedback
      outcome={operation.outcome}
      message={operation.outcome === 'failed' ? operation.error?.message : undefined}
      detail={
        operation.outcome === 'unknown' || operation.outcome === 'conflict'
          ? operation.error?.message
          : undefined
      }
      onCheck={() => {
        if (operation.outcome === 'unknown' && operation.variables) {
          operation.mutate(operation.variables, { onSuccess: complete });
        } else {
          void (operation.outcome === 'refresh-failed' ? operation.refresh() : operation.check());
        }
      }}
    />
  );
  return (
    <Card className="ops-stack">
      <div className="card-title-row ops-toolbar">
        <h2>{t('gatewayCapabilities.title')}</h2>
        <button
          className="btn btn-secondary"
          type="button"
          disabled={operation.isPending || list.isPending || Boolean(list.error)}
          onClick={() => open(null)}
        >
          {t('gatewayCapabilities.add')}
        </button>
      </div>
      <p className="muted">{t('gatewayCapabilities.description')}</p>
      {list.isPending ? (
        <LoadingState />
      ) : list.error ? (
        <ErrorState error={list.error} onRetry={() => void list.refetch()} />
      ) : list.data?.length ? (
        <div className="ops-stack">
          {list.data.map((row) => (
            <div className="ops-subcard" key={row.id}>
              <div className="card-title-row ops-toolbar">
                <div className="gateway-capability-target">
                  <h3 className="ops-wrap">{row.model}</h3>
                  <p className="muted ops-wrap">{row.base_url}</p>
                </div>
                <div className="ops-actions">
                  <button
                    className="btn btn-secondary"
                    type="button"
                    disabled={operation.isPending}
                    onClick={() => open(row)}
                    aria-label={t('gatewayCapabilities.editTarget', { model: row.model })}
                  >
                    {t('common.edit')}
                  </button>
                  <button
                    className="btn btn-danger"
                    type="button"
                    disabled={operation.isPending}
                    onClick={() => {
                      operation.reset();
                      setDeleteTarget(row);
                    }}
                    aria-label={t('gatewayCapabilities.deleteTarget', { model: row.model })}
                  >
                    {t('common.remove')}
                  </button>
                </div>
              </div>
              <GatewayCapabilitySummary entry={row} />
            </div>
          ))}
        </div>
      ) : (
        <EmptyState
          title={t('gatewayCapabilities.emptyTitle')}
          body={t('gatewayCapabilities.emptyBody')}
        />
      )}
      {editor !== undefined ? (
        <form
          className="ops-subcard"
          onSubmit={(event) => {
            event.preventDefault();
            save();
          }}
        >
          <h3>{t(editor ? 'gatewayCapabilities.edit' : 'gatewayCapabilities.add')}</h3>
          <fieldset className="ops-field-grid" disabled={operation.isPending}>
            <legend>{t('gatewayCapabilities.target')}</legend>
            <label>
              <span>{t('gatewayCapabilities.baseUrl')}</span>
              <input
                required
                type="url"
                value={draft.base_url}
                maxLength={8192}
                aria-describedby={`${prefix}-target-hint`}
                onChange={(event) => change({ base_url: event.target.value })}
              />
            </label>
            <label>
              <span>{t('gatewayCapabilities.model')}</span>
              <input
                required
                value={draft.model}
                maxLength={512}
                aria-describedby={`${prefix}-target-hint`}
                onChange={(event) => change({ model: event.target.value })}
              />
            </label>
          </fieldset>
          <p className="muted" id={`${prefix}-target-hint`}>
            {t('gatewayCapabilities.targetHint')}
          </p>
          <label className="ops-form-field">
            <span>{t('gatewayCapabilities.adapter')}</span>
            <select
              value={draft.adapter}
              aria-label={t('gatewayCapabilities.adapter')}
              disabled={operation.isPending}
              aria-describedby={`${prefix}-adapter-hint`}
              onChange={(event) => {
                const adapter = event.target.value as GatewayAdapter;
                const efforts = draft.efforts.filter((value) =>
                  gatewayAdapterEfforts(adapter).includes(value),
                );
                change({
                  adapter,
                  efforts,
                  storage:
                    adapter.startsWith('anthropic_') && draft.storage === 'openai'
                      ? 'reject'
                      : draft.storage,
                  cache: adapter.startsWith('openai_') ? 'reject' : draft.cache,
                });
              }}
            >
              {GATEWAY_ADAPTERS.map((adapter) => (
                <option key={adapter} value={adapter}>
                  {t(gatewayAdapterLabels[adapter])}
                </option>
              ))}
            </select>
            <small className="muted" id={`${prefix}-adapter-hint`}>
              {t('gatewayCapabilities.adapterHint')}
            </small>
          </label>
          <fieldset
            className="ops-field-grid"
            disabled={operation.isPending}
            aria-describedby={`${prefix}-effort-hint`}
          >
            <legend>{t('gatewayCapabilities.efforts')}</legend>
            {gatewayAdapterEfforts(draft.adapter).map((effort) => (
              <label className="checkbox-label" key={effort}>
                <input
                  type="checkbox"
                  checked={draft.efforts.includes(effort)}
                  onChange={(event) =>
                    change({
                      efforts: event.target.checked
                        ? [...draft.efforts, effort]
                        : draft.efforts.filter((value) => value !== effort),
                    })
                  }
                />
                <span>{t(gatewayEffortLabels[effort])}</span>
              </label>
            ))}
          </fieldset>
          <p className="muted" id={`${prefix}-effort-hint`}>
            {t('gatewayCapabilities.effortsHint')}
          </p>
          <label className="ops-form-field">
            <span>{t('gatewayCapabilities.output')}</span>
            <input
              required
              type="number"
              min={anthropic ? 1 : 0}
              max={2147483647}
              step={1}
              value={outputText}
              aria-label={t('gatewayCapabilities.output')}
              disabled={operation.isPending}
              aria-describedby={`${prefix}-output-hint`}
              onChange={(event) => {
                operation.reset();
                setOutputText(event.target.value);
              }}
            />
            <small className="muted" id={`${prefix}-output-hint`}>
              {t(
                anthropic
                  ? 'gatewayCapabilities.outputAnthropicHint'
                  : 'gatewayCapabilities.outputHint',
              )}
            </small>
          </label>
          <details className="ops-advanced" ref={advanced}>
            <summary>{t('gatewayCapabilities.advanced')}</summary>
            <fieldset className="ops-field-grid" disabled={operation.isPending}>
              <label>
                <span>{t('gatewayCapabilities.storage')}</span>
                <select
                  value={draft.storage}
                  aria-label={t('gatewayCapabilities.storage')}
                  aria-describedby={`${prefix}-storage-hint`}
                  onChange={(event) =>
                    change({ storage: event.target.value as GatewayCapabilityEntry['storage'] })
                  }
                >
                  <option value="reject">{t('gatewayCapabilities.storageOptions.reject')}</option>
                  {!anthropic ? (
                    <option value="openai">{t('gatewayCapabilities.storageOptions.openai')}</option>
                  ) : null}
                  <option value="omit_false">
                    {t('gatewayCapabilities.storageOptions.omit_false')}
                  </option>
                </select>
                <small className="muted" id={`${prefix}-storage-hint`}>
                  {t('gatewayCapabilities.storageHint')}
                </small>
                {draft.storage === 'omit_false' ? (
                  <p className="inline-notice">{t('gatewayCapabilities.omitWarning')}</p>
                ) : null}
              </label>
              <label>
                <span>{t('gatewayCapabilities.cache')}</span>
                <select
                  value={draft.cache}
                  aria-label={t('gatewayCapabilities.cache')}
                  aria-describedby={`${prefix}-cache-hint`}
                  onChange={(event) =>
                    change({ cache: event.target.value as GatewayCapabilityEntry['cache'] })
                  }
                >
                  <option value="reject">{t('gatewayCapabilities.cacheOptions.reject')}</option>
                  {anthropic ? (
                    <>
                      <option value="anthropic">
                        {t('gatewayCapabilities.cacheOptions.anthropic')}
                      </option>
                      <option value="anthropic_explicit">
                        {t('gatewayCapabilities.cacheOptions.anthropic_explicit')}
                      </option>
                    </>
                  ) : null}
                </select>
                <small className="muted" id={`${prefix}-cache-hint`}>
                  {t('gatewayCapabilities.cacheHint')}
                </small>
              </label>
            </fieldset>
          </details>
          {!deleteTarget ? feedback : null}
          <div className="ops-actions">
            <button className="btn btn-primary" type="submit" disabled={operation.isPending}>
              {t(operation.isPending ? 'common.working' : 'common.save')}
            </button>
            <button
              className="btn btn-secondary"
              type="button"
              disabled={operation.isPending}
              onClick={() => {
                setEditor(undefined);
                operation.reset();
              }}
            >
              {t('common.cancel')}
            </button>
          </div>
        </form>
      ) : !deleteTarget ? (
        feedback
      ) : null}
      {deleteTarget ? (
        <ConfirmDialog
          open
          danger
          busy={operation.isPending}
          title={t('gatewayCapabilities.deleteTitle')}
          description={t('gatewayCapabilities.deleteDescription', {
            model: deleteTarget.model,
            baseUrl: deleteTarget.base_url,
          })}
          confirmLabel={t('common.remove')}
          onCancel={() => {
            setDeleteTarget(null);
            operation.reset();
          }}
          onConfirm={() => {
            const target = deleteTarget;
            operation.mutate(
              { action: 'delete', id: target.id, expected_revision: target.revision },
              { onSuccess: complete },
            );
          }}
        >
          {feedback}
        </ConfirmDialog>
      ) : null}
    </Card>
  );
}

import { Link, useLocation } from 'react-router';
import { getModel } from '../api';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { ModelMapping } from '@shared/connections';
import type { NativeService } from '@shared/nativeServices';
import type { ModelType } from '@shared/modelTypes';
import { ModelTypesField } from '@shared/components/ModelTypesField';
import { NativeServicesField } from '@shared/components/NativeServicesField';
import { Field, Fold, Note } from '@shared/components/ui';
import { listModelsPage } from '../pageApi';
import { ConnectionResourceList } from './ResourceList';
import type { Endpoint, EndpointKey, Model } from '../types';
import { useConnectionOperation } from './useConnectionOperation';
import { ConnectionOperationNotice } from './OperationNotice';
export function PresetModels({
  accountId,
  mappings,
  entries,
  grants,
}: {
  accountId: string;
  mappings: ModelMapping[];
  entries: Record<string, Endpoint>;
  grants: Record<string, EndpointKey>;
}) {
  const { t } = useTranslation();
  return (
    <Fold title={t('common.connections.adoptModelSuggestions')} lazy>
      <Note>{t('common.connections.suggestionsNeedSelection')}</Note>
      {mappings.map((mapping) => (
        <Fold key={mapping.model_id} title={mapping.model_id} lazy>
          <PresetModelRow
            accountId={accountId}
            mapping={mapping}
            entries={entries}
            grants={grants}
          />
        </Fold>
      ))}
    </Fold>
  );
}
export interface MappingSaveState {
  ready: boolean;
  complete: boolean;
  locked: boolean;
}
export function PresetModelRow({
  accountId,
  mapping,
  entries,
  grants,
  rowId,
  bulk,
  editableUpstream = false,
  grantsByEntry = false,
  initialProvider = '',
  onState,
  register,
}: {
  accountId: string;
  rowId?: string;
  bulk?: { provider: string; types: ModelType[]; generation: number };
  editableUpstream?: boolean;
  grantsByEntry?: boolean;
  initialProvider?: string;
  onState?: (id: string, state: MappingSaveState) => void;
  register?: (id: string, save: () => Promise<boolean>) => void;
  mapping: ModelMapping;
  entries: Record<string, Endpoint>;
  grants: Record<string, EndpointKey>;
}) {
  const { t } = useTranslation();
  const location = useLocation();
  const [existing, setExisting] = useState(false);
  const [names, setNames] = useState<Record<string, string>>({});
  const [edited, setEdited] = useState(false);
  const [generation, setGeneration] = useState(bulk?.generation ?? 0);
  const [services, setServices] = useState<Record<string, NativeService[] | null>>({});
  const [provider, setProvider] = useState(initialProvider);
  const [name, setName] = useState(mapping.model_id);
  const [types, setTypes] = useState<ModelType[]>([]);
  const [model, setModel] = useState<Model | null>(null);
  const [catalogs, setCatalogs] = useState<Record<string, boolean>>({});
  const active = useRef('');
  const [complete, setComplete] = useState(false);
  if (bulk && generation !== bulk.generation) {
    setGeneration(bulk.generation);
    if (!edited && !model) {
      setProvider(bulk.provider);
      setTypes(bulk.types);
    }
  }
  const operation = useConnectionOperation(accountId, (result) => {
    if (result.kind === 'model') setModel(result.model);
    if (result.kind === 'manual')
      setCatalogs((previous) => ({ ...previous, [active.current]: true }));
    if (result.kind === 'binding') setComplete(true);
  });
  const locked =
    operation.isPending ||
    operation.outcome === 'unknown' ||
    operation.outcome === 'refresh-failed';
  const selectedServices = (entry: ModelMapping['entries'][number]) => {
    if (entry.entry_id in services) return services[entry.entry_id] ?? undefined;
    const endpoint = entries[entry.entry_id];
    const key = endpoint ? grants[grantsByEntry ? entry.entry_id : endpoint.id] : undefined;
    const known = entry.native_services.filter((service) =>
      key?.native_services?.includes(service),
    );
    return known.length ? known : undefined;
  };
  const targets = mapping.entries.flatMap((entry) => {
    const endpoint = entries[entry.entry_id];
    const key = endpoint ? grants[grantsByEntry ? entry.entry_id : endpoint.id] : undefined;
    const selected = selectedServices(entry);
    return endpoint && key && selected?.length
      ? [{ entry, endpoint, key, services: selected }]
      : [];
  });
  const save = async () => {
    try {
      for (const target of targets) {
        const identity =
          target.key.id +
          '\u0000' +
          (names[target.entry.entry_id] ?? target.entry.upstream_model_id);
        if (!catalogs[identity]) {
          active.current = identity;
          await operation.run({
            kind: 'manual',
            reuseExisting: true,
            endpointId: target.endpoint.id,
            keyId: target.key.id,
            entries: [
              {
                upstream_model_id: names[target.entry.entry_id] ?? target.entry.upstream_model_id,
                provider: provider.trim(),
              },
            ],
          });
        }
      }
      let saved = model;
      if (!saved) {
        const result = await operation.run({
          kind: 'model',
          row: mapping.model_id,
          input: { provider: provider.trim(), model: name.trim(), model_types: types },
        });
        if (result.kind === 'model') saved = result.model;
      }
      if (!saved) return false;
      const input = targets.map((target) => ({
        endpoint_key_id: target.key.id,
        upstream_model_id: names[target.entry.entry_id] ?? target.entry.upstream_model_id,
        native_services: target.services,
      }));
      await operation.run({
        kind: 'binding',
        row: mapping.model_id,
        modelId: saved.id,
        revision: saved.binding_revision,
        selection: { ...input[0]!, targets: input },
      });
      return true;
    } catch {
      return false;
    }
  };
  const ready =
    targets.length > 0 &&
    targets.every(
      (target) =>
        (names[target.entry.entry_id] ?? target.entry.upstream_model_id).trim().length > 0,
    ) &&
    (Boolean(model) || Boolean(provider.trim() && name.trim() && types.length));
  const identity = rowId ?? mapping.model_id;
  useEffect(() => {
    onState?.(identity, { ready, complete, locked });
  }, [onState, identity, ready, complete, locked]);
  useEffect(() => {
    register?.(identity, save);
  });
  if (complete)
    return (
      <div className="nb-stack">
        <Note tone="ok">{t('common.connections.mappingSaved')}</Note>
        {model ? (
          <Link
            className="nb-btn nb-btn--secondary connection-model-link"
            to={'/models?model_id=' + encodeURIComponent(model.id)}
            state={{ returnTo: location.pathname + location.search }}
          >
            {model.full_name}
          </Link>
        ) : null}
      </div>
    );
  return (
    <div className="nb-stack">
      {mapping.entries.map((entry) => {
        const endpoint = entries[entry.entry_id];
        const key = endpoint ? grants[grantsByEntry ? entry.entry_id : endpoint.id] : undefined;
        const selected = selectedServices(entry);
        return (
          <div key={entry.entry_id}>
            <Field
              label={
                (endpoint?.note || endpoint?.base_url || t('common.connections.entryNotCreated')) +
                ' · ' +
                entry.upstream_model_id
              }
            >
              {(props) => (
                <input
                  {...props}
                  type="checkbox"
                  checked={Boolean(selected)}
                  disabled={locked || !key}
                  onChange={(event) =>
                    setServices((previous) => {
                      const next = { ...previous };
                      if (event.target.checked)
                        next[entry.entry_id] = entry.native_services.filter((service) =>
                          key?.native_services?.includes(service),
                        );
                      else next[entry.entry_id] = null;
                      return next;
                    })
                  }
                />
              )}
            </Field>
            {editableUpstream && selected ? (
              <Field
                label={
                  t('common.connections.upstreamModel') +
                  ' · ' +
                  (endpoint?.note || endpoint?.base_url || entry.entry_id)
                }
              >
                {(props) => (
                  <input
                    {...props}
                    value={names[entry.entry_id] ?? entry.upstream_model_id}
                    disabled={locked}
                    maxLength={512}
                    onChange={(event) =>
                      setNames((previous) => ({
                        ...previous,
                        [entry.entry_id]: event.target.value,
                      }))
                    }
                  />
                )}
              </Field>
            ) : null}
            {key && selected ? (
              <NativeServicesField
                options={key.native_services ?? []}
                value={selected}
                onChange={(value) =>
                  setServices((previous) => ({ ...previous, [entry.entry_id]: value }))
                }
                disabled={locked}
              />
            ) : !key ? (
              <p className="nb-sub">{t('common.connections.authorizeEntryFirst')}</p>
            ) : null}
          </div>
        );
      })}
      {!model ? (
        <>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={locked}
            onClick={() => setExisting((value) => !value)}
          >
            {t('common.connections.existingModel')}
          </button>
          {existing ? (
            <ConnectionResourceList
              embedded
              accountId={accountId}
              scope={identity}
              listType="mapping-existing-models"
              title={t('common.connections.existingModel')}
              searchPlaceholder={t('common.connections.modelSearch')}
              load={(window, q, signal) => listModelsPage(window, signal, { q })}
              rowKey={(row) => row.id}
              columns={[
                {
                  key: 'name',
                  cell: 'title',
                  header: t('common.connections.model'),
                  render: (row) => row.full_name,
                },
                {
                  key: 'select',
                  cell: 'action',
                  header: t('common.connections.actions'),
                  render: (row) => (
                    <button
                      type="button"
                      className="nb-btn nb-btn--secondary"
                      disabled={locked}
                      onClick={() => setModel(row)}
                    >
                      {t('common.connections.select')}
                    </button>
                  ),
                },
              ]}
            />
          ) : null}
          <Field label={t('common.connections.provider')}>
            {(props) => (
              <input
                {...props}
                value={provider}
                disabled={locked}
                onChange={(event) => {
                  setEdited(true);
                  setProvider(event.target.value);
                }}
              />
            )}
          </Field>
          <Field label={t('common.connections.model')}>
            {(props) => (
              <input
                {...props}
                value={name}
                disabled={locked}
                onChange={(event) => {
                  setEdited(true);
                  setName(event.target.value);
                }}
              />
            )}
          </Field>
          <ModelTypesField
            value={types}
            onChange={(value) => {
              setEdited(true);
              setTypes(value);
            }}
            disabled={locked}
          />
        </>
      ) : (
        <Note tone="ok">
          {t('common.connections.modelSaved')} · {model.full_name}
        </Note>
      )}
      <ConnectionOperationNotice operation={operation} />
      {operation.outcome === 'conflict' && model ? (
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          onClick={() =>
            void getModel(model.id).then((current) => {
              setModel(current);
              operation.reset();
            })
          }
        >
          {t('common.connections.useCurrentRevision')}
        </button>
      ) : null}
      <button
        type="button"
        className="nb-btn nb-btn--primary"
        disabled={locked || !ready}
        onClick={() => void save()}
      >
        {t('common.connections.saveMapping')}
      </button>
    </div>
  );
}

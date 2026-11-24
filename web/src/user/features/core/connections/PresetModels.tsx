import { defaultBindingServices } from '@shared/nativeServices';
import { CoreErrorPanel } from '../components';
import type { DiscoveryModel } from './types';
import { Link, useLocation } from 'react-router';
import { getModel } from '../api';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { ModelMapping } from '@shared/connections';
import type { NativeService } from '@shared/nativeServices';
import { MODEL_TYPES, type ModelType } from '@shared/modelTypes';
import { ModelTypesField } from '@shared/components/ModelTypesField';
import { NativeServicesField } from '@shared/components/NativeServicesField';
import { Field, Fold, Note } from '@shared/components/ui';
import { getCatalogPage, listModelsPage } from '../pageApi';
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
    <Fold plain title={t('common.connections.adoptModelSuggestions')} lazy>
      <Note>{t('common.connections.suggestionsNeedSelection')}</Note>
      {mappings.map((mapping) => (
        <Fold plain key={mapping.model_id} title={mapping.model_id} lazy>
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
  missing: string;
}
export function PresetModelRow({
  accountId,
  mapping,
  entries,
  grants,
  rowId,
  bulk,
  collapsed = false,
  editableUpstream = false,
  grantsByEntry = false,
  initialProvider = '',
  catalogEntry,
  onState,
  register,
}: {
  accountId: string;
  rowId?: string;
  bulk?: { provider: string; types: ModelType[]; services: NativeService[]; generation: number };
  collapsed?: boolean;
  editableUpstream?: boolean;
  grantsByEntry?: boolean;
  initialProvider?: string;
  catalogEntry?: DiscoveryModel['catalogEntry'];
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
  const [typeOverride, setTypes] = useState<ModelType[]>();
  const types =
    typeOverride ??
    MODEL_TYPES.filter((type) =>
      mapping.entries.some((entry) => {
        const endpoint = entries[entry.entry_id];
        const allowed = endpoint
          ? (grants[grantsByEntry ? entry.entry_id : endpoint.id]?.native_services ?? [])
          : [];
        const defaults =
          editableUpstream && endpoint?.connector_type === 'openai-compatible'
            ? defaultBindingServices(
                endpoint.connector_type,
                entry.native_services.length ? entry.native_services : allowed,
              )
            : entry.native_services;
        return defaults.includes(type);
      }),
    );
  const [model, setModel] = useState<Model | null>(null);
  const [catalogs, setCatalogs] = useState<Record<string, boolean>>({});
  const active = useRef('');
  const [complete, setComplete] = useState(false);
  const [catalogProof, setCatalogProof] = useState(catalogEntry);
  const [missingSource, setMissingSource] = useState(false);
  const [readError, setReadError] = useState<Error>();
  if (bulk && generation !== bulk.generation) {
    setGeneration(bulk.generation);
    if (!edited && !model) {
      if (bulk.provider.trim()) setProvider(bulk.provider.trim());
      if (bulk.types.length) setTypes(bulk.types);
      if (bulk.services.length) {
        const next = { ...services };
        for (const entry of mapping.entries) {
          const endpoint = entries[entry.entry_id];
          const key = endpoint && grants[grantsByEntry ? entry.entry_id : endpoint.id];
          const allowed = (key?.native_services ?? []).filter(
            (service) =>
              bulk.services.includes(service) &&
              (!entry.native_services.length || entry.native_services.includes(service)),
          );
          if (allowed.length && next[entry.entry_id] !== null) next[entry.entry_id] = allowed;
        }
        setServices(next);
      }
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
    const known = key?.native_services ?? [];
    if (!known.length) return undefined;
    const declared = entry.native_services.filter((service) => known.includes(service));
    if (editableUpstream && endpoint?.connector_type === 'openai-compatible')
      return defaultBindingServices(
        endpoint.connector_type,
        entry.native_services.length ? declared : known,
      );
    return entry.native_services.length ? declared : known.length === 1 ? known : [];
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
        if (
          (!catalogProof ||
            (names[target.entry.entry_id] ?? target.entry.upstream_model_id) !==
              mapping.model_id) &&
          !catalogs[identity]
        ) {
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
        ...((names[target.entry.entry_id] ?? target.entry.upstream_model_id) === mapping.model_id &&
        catalogProof
          ? {
              catalog_entry_id: catalogProof.catalog_entry_id,
              expected_pair_revision: catalogProof.expected_pair_revision,
            }
          : {}),
        endpoint_key_id: target.key.id,
        upstream_model_id: names[target.entry.entry_id] ?? target.entry.upstream_model_id,
        native_services: target.services,
      }));
      await operation.run({
        kind: 'binding',
        row: mapping.model_id,
        modelId: saved.id,
        revision: saved.binding_revision,
        selection: {
          endpoint_key_id: input[0]!.endpoint_key_id,
          upstream_model_id: input[0]!.upstream_model_id,
          targets: input,
        },
      });
      return true;
    } catch {
      return false;
    }
  };
  const reviewCurrent = async () => {
    if (!model) return;
    setReadError(undefined);
    try {
      const current = await getModel(model.id);
      const target = targets[0];
      if (
        catalogProof &&
        target &&
        (names[target.entry.entry_id] ?? target.entry.upstream_model_id) === mapping.model_id
      ) {
        const catalog = await getCatalogPage(
          target.endpoint.id,
          target.key.id,
          { page: '1', pageSize: 10 },
          catalogProof.source,
          undefined,
          mapping.model_id,
          true,
        );
        const candidates = [...catalog.automatic_entries, ...catalog.manual_entries];
        const row =
          candidates.find((item) => item.id === catalogProof.catalog_entry_id) ?? candidates[0];
        if (!row) {
          setMissingSource(true);
          return;
        }
        setCatalogProof({
          ...catalogProof,
          catalog_entry_id: row.id,
          expected_pair_revision: row.pair_revision,
        });
        setMissingSource(false);
      }
      setModel(current);
      operation.reset();
    } catch (error) {
      setReadError(error as Error);
    }
  };
  const missing = [
    missingSource &&
      targets.some(
        (target) =>
          (names[target.entry.entry_id] ?? target.entry.upstream_model_id) === mapping.model_id,
      ) &&
      t('common.connections.upstreamModel'),
    !targets.length && t('common.nativeServices.title'),
    targets.some(
      (target) => !(names[target.entry.entry_id] ?? target.entry.upstream_model_id).trim(),
    ) && t('common.connections.upstreamModel'),
    !model && !provider.trim() && t('common.connections.provider'),
    !model && !name.trim() && t('common.connections.model'),
    !model && !types.length && t('common.modelTypes.title'),
  ]
    .filter(Boolean)
    .join(' · ');
  const ready = !missing;
  const identity = rowId ?? mapping.model_id;
  useEffect(() => {
    onState?.(identity, { ready, complete, locked, missing });
  }, [onState, identity, ready, complete, locked, missing]);
  useEffect(() => {
    register?.(identity, save);
  });
  if (collapsed) return null;
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
          <div key={entry.entry_id} className="nb-stack nb-stack--sm">
            <label className="core-checkbox">
              <input
                type="checkbox"
                checked={Boolean(selected)}
                disabled={locked || !key}
                onChange={(event) => {
                  setEdited(true);
                  setServices((previous) => {
                    const next = { ...previous };
                    if (event.target.checked)
                      next[entry.entry_id] = defaultBindingServices(
                        endpoint?.connector_type,
                        key?.native_services ?? [],
                      );
                    else next[entry.entry_id] = null;
                    return next;
                  });
                }}
              />
              <span>
                {(endpoint?.note || endpoint?.base_url || t('common.connections.entryNotCreated')) +
                  ' · ' +
                  entry.upstream_model_id}
              </span>
            </label>
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
                    onChange={(event) => {
                      setEdited(true);
                      setNames((previous) => ({
                        ...previous,
                        [entry.entry_id]: event.target.value,
                      }));
                    }}
                  />
                )}
              </Field>
            ) : null}
            {key && selected ? (
              <NativeServicesField
                options={key.native_services ?? []}
                value={selected}
                onChange={(value) => {
                  setEdited(true);
                  setServices((previous) => ({ ...previous, [entry.entry_id]: value }));
                  if (!types.length) setTypes(MODEL_TYPES.filter((type) => value.includes(type)));
                }}
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
              emptyDescription={t('common.connections.noExistingModel')}
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
      {readError ? (
        <CoreErrorPanel compact error={readError} onRetry={() => void reviewCurrent()} />
      ) : null}
      {missingSource ? <Note tone="warn">{t('user.services.noMatchingModels')}</Note> : null}
      <ConnectionOperationNotice operation={operation} />
      {operation.outcome === 'conflict' && model && catalogEntry ? (
        <Note tone="warn">{t('user.services.selectionChanged')}</Note>
      ) : null}
      {operation.outcome === 'conflict' && model ? (
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          onClick={() => void reviewCurrent()}
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

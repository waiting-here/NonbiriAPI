import { NativeServicesField } from '@shared/components/NativeServicesField';
import type { NativeService } from '@shared/nativeServices';
import { Link } from 'react-router';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Field, Fold, Note } from '@shared/components/ui';
import { ModelTypesField } from '@shared/components/ModelTypesField';
import type { ModelType } from '@shared/modelTypes';
import type { Endpoint, EndpointKey } from '../types';
import type { DiscoveryModel } from './types';
import { ManualCatalog } from './ManualCatalog';
import { PresetModelRow, type MappingSaveState } from './PresetModels';
export function DirectoryBatch({
  accountId,
  rows,
  initialTarget,
  onLockChange,
  onRemove,
}: {
  onLockChange?: (locked: boolean) => void;
  onRemove?: (id: string) => void;
  accountId: string;
  rows: DiscoveryModel[];
  initialTarget?: { entry: Endpoint; key: EndpointKey };
}) {
  const { t } = useTranslation();
  const [entries, setEntries] = useState<Record<string, Endpoint>>(() =>
    initialTarget ? { [initialTarget.key.id]: initialTarget.entry } : {},
  );
  const [grants, setGrants] = useState<Record<string, EndpointKey>>(() =>
    initialTarget ? { [initialTarget.key.id]: initialTarget.key } : {},
  );
  const [states, setStates] = useState<Record<string, MappingSaveState>>({});
  const saves = useRef<Record<string, () => Promise<boolean>>>({});
  const [busy, setBusy] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const [provider, setProvider] = useState('');
  const [types, setTypes] = useState<ModelType[]>([]);
  const [services, setServices] = useState<NativeService[]>([]);
  const availableServices = [
    ...new Set(Object.values(grants).flatMap((key) => key.native_services ?? [])),
  ];
  const [bulk, setBulk] = useState({
    provider: '',
    types: [] as ModelType[],
    services: [] as NativeService[],
    generation: 0,
  });
  const onState = useCallback((id: string, state: MappingSaveState) => {
    setStates((previous) => {
      const old = previous[id];
      return old?.ready === state.ready &&
        old?.complete === state.complete &&
        old?.locked === state.locked &&
        old.missing === state.missing
        ? previous
        : { ...previous, [id]: state };
    });
  }, []);
  const register = useCallback((id: string, save: () => Promise<boolean>) => {
    saves.current[id] = save;
  }, []);
  const [expansions, setExpansions] = useState<Record<string, boolean>>({});
  const remaining = rows.filter((row) => !states[row.id]?.complete);
  const incomplete = remaining.filter((row) => !states[row.id]?.ready);
  const locked = busy || rows.some((row) => states[row.id]?.locked);
  useEffect(() => {
    onLockChange?.(locked);
    return () => onLockChange?.(false);
  }, [locked, onLockChange]);

  return (
    <div className="nb-stack">
      {!initialTarget ? (
        <Fold title={t('common.connections.chooseEntry')}>
          <Note>{t('common.connections.batchTargetHelp')}</Note>
          <ManualCatalog
            disabled={locked}
            accountId={accountId}
            onPick={(entry, key) => {
              setEntries((previous) => ({ ...previous, [key.id]: entry }));
              setGrants((previous) => ({ ...previous, [key.id]: key }));
            }}
          />
        </Fold>
      ) : null}
      {rows.length > 1 ? (
        <Fold title={t('common.connections.bulkModelSettings')}>
          <Field label={t('common.connections.provider')}>
            {(props) => (
              <input
                {...props}
                value={provider}
                disabled={locked}
                onChange={(event) => setProvider(event.target.value)}
              />
            )}
          </Field>
          <ModelTypesField value={types} onChange={setTypes} disabled={locked} />
          {availableServices.length > 0 && (
            <NativeServicesField
              options={availableServices}
              value={services}
              onChange={setServices}
              disabled={locked}
            />
          )}
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={locked || (!provider.trim() && !types.length && !services.length)}
            onClick={() =>
              setBulk((previous) => ({
                provider,
                types,
                services,
                generation: previous.generation + 1,
              }))
            }
          >
            {t('common.connections.applyToUnedited')}
          </button>
        </Fold>
      ) : null}
      <fieldset disabled={busy} className="nb-stack connection-model-batch">
        {rows.map((row) => (
          <div key={row.id} id={'mapping-' + row.id}>
            <Fold
              title={row.upstream_model_id}
              open={expansions[row.id] ?? rows.length <= 5}
              onOpenChange={(open) => setExpansions((current) => ({ ...current, [row.id]: open }))}
              meta={
                states[row.id]?.complete && !(expansions[row.id] ?? rows.length <= 5)
                  ? t('common.connections.mappingSaved')
                  : states[row.id]?.missing && !states[row.id]?.complete
                    ? `${t('user.services.mappingIncomplete')} · ${states[row.id].missing}`
                    : undefined
              }
            >
              <PresetModelRow
                accountId={accountId}
                collapsed={!(expansions[row.id] ?? rows.length <= 5)}
                rowId={row.id}
                initialProvider={row.provider || (initialTarget ? 'my' : '')}
                bulk={bulk}
                catalogEntry={row.catalogEntry}
                editableUpstream
                grantsByEntry
                onState={onState}
                register={register}
                mapping={{
                  model_id: row.upstream_model_id,
                  entries: Object.keys(grants).map((id) => ({
                    entry_id: id,
                    upstream_model_id: row.upstream_model_id,
                    native_services: row.needs_service_selection ? [] : row.native_services,
                  })),
                }}
                entries={entries}
                grants={grants}
              />
              {onRemove && (expansions[row.id] ?? rows.length <= 5) ? (
                <button
                  type="button"
                  className="nb-btn nb-btn--ghost"
                  disabled={locked}
                  onClick={() => onRemove(row.id)}
                >
                  {t('common.remove')}
                </button>
              ) : null}
            </Fold>
          </div>
        ))}
      </fieldset>
      {attempted ? (
        <p role="status">
          {t('user.services.batchProgress', {
            saved: rows.length - remaining.length,
            total: rows.length,
          })}
        </p>
      ) : null}
      {remaining.length ? (
        <>
          <Note>{t('common.connections.batchMappingHelp')}</Note>
          {incomplete.length > 0 && (
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              onClick={() => {
                const id = incomplete[0]!.id;
                setExpansions((previous) => ({ ...previous, [id]: true }));
                requestAnimationFrame(() => {
                  const row = document.getElementById('mapping-' + id);
                  row?.querySelector('summary')?.focus();
                  row?.scrollIntoView?.({ block: 'nearest' });
                });
              }}
            >
              {t('user.services.reviewIncompleteModels', { count: incomplete.length })}
            </button>
          )}
          <button
            type="button"
            className="nb-btn nb-btn--primary"
            disabled={locked || remaining.some((row) => !states[row.id]?.ready)}
            onClick={() => {
              setAttempted(true);
              setBusy(true);
              void (async () => {
                try {
                  for (const row of remaining) {
                    if (!(await saves.current[row.id]?.())) {
                      setExpansions((current) => ({ ...current, [row.id]: true }));
                      break;
                    }
                  }
                } finally {
                  setBusy(false);
                }
              })();
            }}
          >
            {t(
              attempted
                ? 'common.connections.retryUnfinishedModels'
                : 'common.connections.saveSelectedModels',
              { count: remaining.length },
            )}
          </button>
        </>
      ) : rows.length ? (
        <div className="nb-actions">
          <Link to="/models" className="nb-btn nb-btn--secondary">
            {t('user.services.viewModels')}
          </Link>
          <Link to="/keys#client" className="nb-btn nb-btn--secondary">
            {t('user.services.apiKeyLink')}
          </Link>
        </div>
      ) : null}
    </div>
  );
}

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
  const [bulk, setBulk] = useState({ provider: '', types: [] as ModelType[], generation: 0 });
  const onState = useCallback((id: string, state: MappingSaveState) => {
    setStates((previous) => {
      const old = previous[id];
      return old?.ready === state.ready &&
        old?.complete === state.complete &&
        old?.locked === state.locked
        ? previous
        : { ...previous, [id]: state };
    });
  }, []);
  const register = useCallback((id: string, save: () => Promise<boolean>) => {
    saves.current[id] = save;
  }, []);
  const [expansions, setExpansions] = useState<Record<string, boolean>>({});
  const remaining = rows.filter((row) => !states[row.id]?.complete);
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
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={locked}
            onClick={() =>
              setBulk((previous) => ({ provider, types, generation: previous.generation + 1 }))
            }
          >
            {t('common.connections.applyToUnedited')}
          </button>
        </Fold>
      ) : null}
      <fieldset disabled={busy} className="nb-stack connection-model-batch">
        {rows.map((row) => (
          <Fold
            key={row.id}
            title={row.upstream_model_id}
            open={expansions[row.id] ?? rows.length <= 5}
            onOpenChange={(open) => setExpansions((current) => ({ ...current, [row.id]: open }))}
            meta={
              states[row.id]?.complete && !(expansions[row.id] ?? rows.length <= 5)
                ? t('common.connections.mappingSaved')
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

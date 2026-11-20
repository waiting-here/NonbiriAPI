import { useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ChoiceRow, Field, Fold, Note, Tabs } from '@shared/components/ui';
import { PagePagination } from '@shared/operations/PagePagination';
import { usePagePager } from '@shared/operations/usePagePager';
import type { CatalogEntry, DiscoveryEvidence, Endpoint, EndpointKey } from '../types';
import type { DiscoveryModel, DiscoverySource } from './types';
import { DirectoryBatch } from './DirectoryBatch';
import { useConnectionOperation } from './useConnectionOperation';
import { ConnectionOperationNotice } from './OperationNotice';
import { CoreErrorPanel, CoreLoading, CoreTime, DiscoveryStatus } from '../components';
import { useNumberedCatalog } from '../numberedQueries';
import { coreKeys } from '../queries';
import { getCatalogSelection } from '../pageApi';
import { PairManualCatalog } from '../EndpointDetail';

export function ServiceModelSetup({
  accountId,
  entry,
  entryKey,
  autoRefresh = false,
  onLockChange,
}: {
  onLockChange?: (locked: boolean) => void;
  accountId: string;
  entry: Endpoint;
  entryKey: EndpointKey;
  source?: DiscoverySource;
  autoRefresh?: boolean;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [batchLocked, setBatchLocked] = useState(false);
  useEffect(() => {
    onLockChange?.(batchLocked);
    return () => onLockChange?.(false);
  }, [batchLocked, onLockChange]);
  const [view, setView] = useState<'automatic' | 'manual'>(
    autoRefresh && !entryKey.discovery_source_id ? 'manual' : 'automatic',
  );
  const [selected, setSelected] = useState<DiscoveryModel[]>([]);
  const [searchDraft, setSearchDraft] = useState('');
  const [search, setSearch] = useState('');
  const [selectionError, setSelectionError] = useState<Error>();
  const [selecting, setSelecting] = useState(false);
  const selectionRequest = useRef<AbortController | null>(null);
  useEffect(() => () => selectionRequest.current?.abort(), []);
  const pager = usePagePager({
    station: 'user',
    listType: 'service-key-models',
    scopeKey: accountId + ':' + entry.id + ':' + entryKey.id,
    resetKey: JSON.stringify([view, search]),
  });
  const catalog = useNumberedCatalog(
    accountId,
    entry.id,
    entryKey.id,
    view,
    {
      page: pager.page,
      pageSize: pager.pageSize,
    },
    true,
    search,
  );
  const refresh = useConnectionOperation(accountId, () => {
    void client.invalidateQueries({
      queryKey: coreKeys.catalogRoot(accountId, entry.id, entryKey.id),
    });
  });
  const started = useRef(false);
  useEffect(() => {
    if (
      !autoRefresh ||
      !entryKey.discovery_source_id ||
      !entry.enabled ||
      !entryKey.enabled ||
      started.current
    )
      return;
    started.current = true;
    void refresh
      .run({ kind: 'refresh', endpointId: entry.id, keyId: entryKey.id })
      .catch(() => undefined);
  }, [
    autoRefresh,
    entry.id,
    entry.enabled,
    entryKey.id,
    entryKey.enabled,
    entryKey.discovery_source_id,
    refresh,
  ]);
  const models =
    view === 'automatic' ? catalog.data?.automatic_entries : catalog.data?.manual_entries;
  const locked =
    refresh.isPending || refresh.outcome === 'unknown' || refresh.outcome === 'refresh-failed';
  const evidence = catalog.data?.evidence as
    | (DiscoveryEvidence & {
        last_success_at?: number | null;
        added_count?: string | null;
        removed_count?: string | null;
      })
    | undefined;
  const keyLabel =
    (entryKey as EndpointKey & { credential_note?: string }).credential_note ||
    entryKey.note ||
    entryKey.display_head + '…' + entryKey.display_tail;
  const selectRows = (rows: CatalogEntry[]) => {
    setSelected((previous) => {
      const byName = new Map(previous.map((item) => [item.upstream_model_id, item]));
      for (const row of rows) {
        const known =
          row.native_services?.filter((service) => entryKey.native_services?.includes(service)) ??
          [];
        const available = known.length ? known : (entryKey.native_services ?? []);
        if (!byName.has(row.upstream_model_id))
          byName.set(row.upstream_model_id, {
            id: row.id + ':' + row.pair_revision,
            upstream_model_id: row.upstream_model_id,
            catalogEntry: {
              catalog_entry_id: row.id,
              expected_pair_revision: row.pair_revision,
              source: row.source_type,
            },
            provider: row.provider,
            native_services: available,
            needs_service_selection: !known.length && available.length !== 1,
            service_evidence: known.length ? 'provider' : 'unknown',
          });
      }
      return [...byName.values()];
    });
  };
  const selectMatches = async () => {
    const snapshot = catalog.data;
    if (!snapshot?.selection_revision || selecting) return;
    const request = new AbortController();
    selectionRequest.current = request;
    setSelecting(true);
    setSelectionError(undefined);
    try {
      const rows = await getCatalogSelection(
        entry.id,
        entryKey.id,
        view,
        search,
        snapshot.pagination.total_items,
        snapshot.evidence.revision,
        snapshot.selection_revision,
        request.signal,
      );
      if (!request.signal.aborted) selectRows(rows);
    } catch (error) {
      if (!request.signal.aborted) {
        setSelectionError(error as Error);
        void catalog.refetch();
      }
    } finally {
      if (!request.signal.aborted) setSelecting(false);
    }
  };
  const changeScope = () => {
    selectionRequest.current?.abort();
    setSelecting(false);
    setSelectionError(undefined);
  };
  return (
    <div className="nb-stack service-key-models">
      <div className="nb-stack nb-stack--sm">
        <div className="service-key-models__head">
          <div className="nb-stack nb-stack--xs">
            <h3>{t('user.services.modelsForKey', { key: keyLabel })}</h3>
            <p className="nb-small nb-muted">
              {entry.note && entry.note !== entry.base_url ? <>{entry.note} · </> : null}
              <span className="mono">{entry.base_url}</span>
            </p>
          </div>
          {entryKey.discovery_source_id ? (
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={locked || catalog.data?.evidence.state === 'checking' || !entryKey.enabled}
              onClick={() =>
                void refresh
                  .run({ kind: 'refresh', endpointId: entry.id, keyId: entryKey.id })
                  .catch(() => undefined)
              }
            >
              {t('user.services.fetchModels')}
            </button>
          ) : null}
        </div>
        <ConnectionOperationNotice operation={refresh} />
        {catalog.data ? <DiscoveryStatus evidence={catalog.data.evidence} /> : null}
        {evidence?.last_success_at ? (
          <p className="nb-small nb-muted">
            {t('user.services.lastSuccessfulFetch')} · <CoreTime value={evidence.last_success_at} />
          </p>
        ) : null}
        {evidence?.state === 'succeeded' &&
        evidence.added_count != null &&
        evidence.removed_count != null ? (
          <Note>
            {t('user.services.fetchChanges', {
              added: evidence.added_count,
              removed: evidence.removed_count,
            })}
          </Note>
        ) : null}
        {catalog.data?.evidence.state === 'failed' ? (
          <Note tone="warn">{t('user.services.retainedCatalog')}</Note>
        ) : null}
      </div>
      <Tabs
        label={t('user.services.modelLists')}
        value={view}
        tabs={[
          { value: 'automatic', label: t('user.services.pulledModels') },
          { value: 'manual', label: t('user.services.manualModels') },
        ]}
        onChange={(next) => {
          changeScope();
          setView(next);
        }}
      >
        <form
          className="nb-actions"
          onSubmit={(event) => {
            event.preventDefault();
            changeScope();
            setSearch(searchDraft.trim());
          }}
        >
          <Field label={t('user.charity.searchModels')}>
            {(props) => (
              <input
                {...props}
                type="search"
                maxLength={512}
                value={searchDraft}
                onChange={(event) => setSearchDraft(event.target.value)}
              />
            )}
          </Field>
          <button type="submit" className="nb-btn nb-btn--secondary">
            {t('common.search')}
          </button>
          {search || searchDraft ? (
            <button
              type="button"
              className="nb-btn nb-btn--ghost"
              onClick={() => {
                changeScope();
                setSearch('');
                setSearchDraft('');
              }}
            >
              {t('common.resetFilter')}
            </button>
          ) : null}
        </form>
        {selectionError ? (
          <CoreErrorPanel compact error={selectionError} onRetry={() => void selectMatches()} />
        ) : null}
        {catalog.isPending ? (
          <CoreLoading compact />
        ) : catalog.error ? (
          <CoreErrorPanel compact error={catalog.error} onRetry={() => void catalog.refetch()} />
        ) : (
          <div className="nb-stack">
            {models?.length ? (
              <>
                <div className="nb-actions">
                  <button
                    type="button"
                    className="nb-btn nb-btn--secondary"
                    disabled={batchLocked || selecting || catalog.isPlaceholderData}
                    onClick={() => selectRows(models)}
                  >
                    {t('user.services.selectPage', { count: models.length })}
                  </button>
                  {catalog.data && Number(catalog.data.pagination.total_items) > models.length ? (
                    catalog.data.selection_revision ? (
                      <button
                        type="button"
                        className="nb-btn nb-btn--secondary"
                        disabled={batchLocked || selecting || catalog.isPlaceholderData}
                        onClick={() => void selectMatches()}
                      >
                        {t('user.services.selectMatches', {
                          count: catalog.data.pagination.total_items,
                        })}
                      </button>
                    ) : (
                      <span className="nb-small">{t('user.services.narrowMatches')}</span>
                    )
                  ) : null}
                </div>
                <div className="service-model-grid">
                  {models.map((row) => (
                    <ChoiceRow
                      key={row.id}
                      label={<span className="core-mono">{row.upstream_model_id}</span>}
                      checked={selected.some(
                        (item) => item.upstream_model_id === row.upstream_model_id,
                      )}
                      disabled={batchLocked || selecting || catalog.isPlaceholderData}
                      onChange={(checked) =>
                        checked
                          ? selectRows([row])
                          : setSelected((current) =>
                              current.filter(
                                (item) => item.upstream_model_id !== row.upstream_model_id,
                              ),
                            )
                      }
                    />
                  ))}
                </div>
              </>
            ) : (
              <Note>
                {t(
                  search
                    ? 'user.services.noMatchingModels'
                    : view === 'manual'
                      ? 'user.services.manualListEmpty'
                      : 'user.services.pulledListEmpty',
                )}
              </Note>
            )}
            {catalog.data ? (
              <PagePagination
                metadata={catalog.data.pagination}
                requestedPage={pager.page}
                busy={catalog.isFetching}
                onPageChange={pager.setPage}
                onPageSizeChange={pager.setPageSize}
              />
            ) : null}
          </div>
        )}
        <Fold
          lazy
          plain
          title={t('user.services.manualModels')}
          defaultOpen={autoRefresh && !entryKey.discovery_source_id}
        >
          <PairManualCatalog
            accountId={accountId}
            endpointId={entry.id}
            keyId={entryKey.id}
            visible
          />
        </Fold>
        {selected.length ? (
          <div className="nb-stack">
            <Note>{t('user.services.selectedModels', { count: selected.length })}</Note>
            <button
              type="button"
              className="nb-btn nb-btn--ghost"
              disabled={batchLocked}
              onClick={() => setSelected([])}
            >
              {t('user.services.clearSelected')}
            </button>
            <DirectoryBatch
              key={entry.id + ':' + entryKey.id}
              onLockChange={setBatchLocked}
              accountId={accountId}
              rows={selected}
              onRemove={(id) => setSelected((current) => current.filter((item) => item.id !== id))}
              initialTarget={{ entry, key: entryKey }}
            />
          </div>
        ) : null}
      </Tabs>
    </div>
  );
}

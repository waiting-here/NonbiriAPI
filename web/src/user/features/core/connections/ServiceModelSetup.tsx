import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Field, Fold, Note, Tabs } from '@shared/components/ui';
import { PagePagination } from '@shared/operations/PagePagination';
import { usePagePager } from '@shared/operations/usePagePager';
import type { DiscoveryEvidence, Endpoint, EndpointKey } from '../types';
import type { DiscoveryModel, DiscoverySource } from './types';
import { DirectoryBatch } from './DirectoryBatch';
import { useConnectionOperation } from './useConnectionOperation';
import { ConnectionOperationNotice } from './OperationNotice';
import { CoreErrorPanel, CoreLoading, DiscoveryStatus } from '../components';
import { useNumberedCatalog } from '../numberedQueries';
import { coreKeys } from '../queries';
import { connectionAPI } from './api';
import { CoreTime } from '../components';
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
  const [view, setView] = useState<'automatic' | 'manual'>('automatic');
  const [selected, setSelected] = useState<DiscoveryModel[]>([]);
  const pager = usePagePager({
    station: 'user',
    listType: 'service-key-models',
    scopeKey: accountId + ':' + entry.id + ':' + entryKey.id,
    resetKey: view,
  });
  const catalog = useNumberedCatalog(accountId, entry.id, entryKey.id, view, {
    page: pager.page,
    pageSize: pager.pageSize,
  });
  const detail = useQuery({
    queryKey: [
      ...coreKeys.catalogRoot(accountId, entry.id, entryKey.id),
      'capabilities',
      pager.page,
      pager.pageSize,
      catalog.data?.evidence.revision,
    ],
    queryFn: ({ signal }) =>
      connectionAPI.models(entryKey.discovery_source_id!, pager, '', undefined, signal),
    enabled: view === 'automatic' && Boolean(entryKey.discovery_source_id),
  });
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
  return (
    <div className="nb-stack service-key-models">
      <div className="nb-stack nb-stack--sm">
        <h3>{t('user.services.modelsForKey', { key: keyLabel })}</h3>
        <p className="nb-small nb-muted nb-mono">
          {entry.note || entry.base_url} · {entry.base_url}
        </p>
        <div className="nb-actions">
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={
              locked ||
              !entryKey.discovery_source_id ||
              catalog.data?.evidence.state === 'checking' ||
              !entryKey.enabled
            }
            onClick={() =>
              void refresh
                .run({ kind: 'refresh', endpointId: entry.id, keyId: entryKey.id })
                .catch(() => undefined)
            }
          >
            {t('user.services.fetchModels')}
          </button>
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
        onChange={setView}
      />
      {catalog.isPending ? (
        <CoreLoading compact />
      ) : catalog.error ? (
        <CoreErrorPanel compact error={catalog.error} onRetry={() => void catalog.refetch()} />
      ) : (
        <div className="nb-stack">
          {models?.length ? (
            models.map((row) => (
              <Field key={row.id} label={row.upstream_model_id}>
                {(props) => (
                  <input
                    {...props}
                    type="checkbox"
                    disabled={batchLocked}
                    checked={selected.some(
                      (item) => item.upstream_model_id === row.upstream_model_id,
                    )}
                    onChange={(event) =>
                      setSelected((previous) =>
                        event.target.checked
                          ? [
                              ...previous.filter(
                                (item) => item.upstream_model_id !== row.upstream_model_id,
                              ),
                              {
                                id: entry.id + ':' + entryKey.id + ':' + row.upstream_model_id,
                                upstream_model_id: row.upstream_model_id,
                                provider: row.provider,
                                native_services:
                                  detail.data?.data.find(
                                    (item) => item.upstream_model_id === row.upstream_model_id,
                                  )?.native_services ??
                                  entryKey.native_services ??
                                  [],
                                service_evidence: 'selected',
                                needs_service_selection:
                                  detail.data?.data.find(
                                    (item) => item.upstream_model_id === row.upstream_model_id,
                                  )?.needs_service_selection ??
                                  entryKey.native_services?.length !== 1,
                              },
                            ]
                          : previous.filter(
                              (item) => item.upstream_model_id !== row.upstream_model_id,
                            ),
                      )
                    }
                  />
                )}
              </Field>
            ))
          ) : (
            <Note>
              {t(
                view === 'manual'
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
      <Fold lazy title={t('user.services.manualModels')}>
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
            initialTarget={{ entry, key: entryKey }}
          />
        </div>
      ) : null}
    </div>
  );
}

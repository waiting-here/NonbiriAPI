import { useMemo, useState, type ReactNode } from 'react';
import { useSearchParams } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Card, EmptyState, ErrorState, LoadingState } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { isPageNumber, isPageSize } from '@shared/operations/pageNumbers';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { riskAPI, type Filters, type RiskRole, type TaskKind, type TaskScan } from './api';
import { scanReasonKey } from './scanReason';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';

const running = (scan?: TaskScan) => scan?.state === 'running' || scan?.state === 'queued';
const options = { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false } as const;

export function TaskScans<T>({
  role,
  scopeKey,
  filters,
  kind,
  signal = '',
  renderItem,
}: {
  role: RiskRole;
  scopeKey: string;
  filters: Filters;
  kind: Exclude<TaskKind, 'client_hits'>;
  signal?: '' | 'rpm' | 'concurrency';
  renderItem: (item: T) => ReactNode;
}) {
  const { t } = useTranslation();
  const formatDateTime = useDateTimeFormatter();
  const [params, setParams] = useSearchParams();
  const prefix =
    kind === 'users' ? 'audit_users_' : kind === 'user_ips' ? 'audit_user_ips_' : 'audit_ips_';
  const id = params.get(prefix + 'scan') ?? '';
  const rawPage = params.get(prefix + 'page');
  const page = isPageNumber(rawPage) ? rawPage : '1';
  const rawSize = Number(params.get(prefix + 'size'));
  const size = isPageSize(rawSize) && rawSize !== 10 ? rawSize : 20;
  const [token, setToken] = useState(() => crypto.randomUUID());
  const client = useQueryClient();
  const key = ['risk', role, scopeKey, 'tasks', kind];
  const recent = useQuery({
    queryKey: [...key, 'recent'],
    queryFn: ({ signal: abort }) => riskAPI(role).recentTasks(abort),
    refetchInterval: (q) => (q.state.data?.some(running) ? 1500 : false),
    ...options,
  });
  const task = useQuery({
    queryKey: [...key, id, page, size],
    enabled: !!id,
    queryFn: ({ signal: abort }) => riskAPI(role).taskResults<T>(id, kind, page, size, abort),
    refetchInterval: (q) => (running(q.state.data?.scan) ? 1500 : false),
    ...options,
  });
  const input = useMemo(
    () => ({
      request_token: token,
      kind,
      call_kind: String(filters.kind ?? 'total'),
      signal: kind === 'users' ? signal : undefined,
      from: filters.from === undefined ? undefined : Number(filters.from),
      to: filters.to === undefined ? undefined : Number(filters.to),
      lookback_hours:
        filters.lookback_hours === undefined ? undefined : Number(filters.lookback_hours),
    }),
    [token, kind, filters.from, filters.to, filters.kind, filters.lookback_hours, signal],
  );
  const select = (scan: TaskScan) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      // A saved scan owns its immutable filters. Restore them with the selected
      // ID so the shared filter controls cannot describe a different query.
      next.set('audit_from', String(scan.from));
      next.set('audit_to', String(scan.to));
      next.delete('audit_lookback_hours');
      next.set('audit_kind', scan.call_kind);
      if (scan.model) next.set('audit_model', scan.model);
      else next.delete('audit_model');
      if (kind === 'users') {
        if (scan.signal) next.set('audit_signal', scan.signal);
        else next.delete('audit_signal');
      }
      next.set(prefix + 'scan', scan.id);
      next.set(prefix + 'page', '1');
      return next;
    });
  const authorityRoot =
    role === 'admin' ? ['admin', 'risk-audit'] : ['user', 'steward', 'risk-audit'];
  const refresh = () => client.invalidateQueries({ queryKey: key }, { throwOnError: true });
  const start = useRetainedOperation<typeof input, TaskScan>(
    async (value, _key, context) => {
      const result = await riskAPI(role).createTask(value, context.signal);
      context.commit(() => {
        setToken(crypto.randomUUID());
        select(result);
      });
      return result;
    },
    refresh,
    authorityRoot,
  );
  const cancel = useRetainedOperation<string, TaskScan>(
    (scanID, _key, context) => riskAPI(role).cancelTask(scanID, context.signal),
    refresh,
    authorityRoot,
  );
  const visible = recent.data?.filter((item) => item.kind === kind) ?? [];
  const scan = task.data?.scan;
  const statuses: Record<TaskScan['state'], string> = {
    queued: t('common.auditScans.queued'),
    running: t('common.auditScans.scanning'),
    completed: t('common.auditScans.completed'),
    cancelled: t('common.auditScans.stoppedResultsIncomplete'),
    limited: t('common.auditScans.scanLimitReachedResultsIncomplete'),
    failed: t('common.auditScans.scanFailedResultsIncomplete'),
  };
  const callKindLabel = (value: string) =>
    ({
      total: t('common.auditScans.allCalls'),
      self: t('common.auditScans.personal'),
      charity: t('common.auditScans.charity'),
      unclassified: t('common.auditScans.unclassified'),
    })[value] ?? value;
  const signalLabel = (value: string) =>
    ({
      '': t('common.auditScans.allUsers'),
      rpm: t('common.auditScans.highRpm'),
      concurrency: t('common.auditScans.highConcurrency'),
    })[value] ?? value;
  const frozenLabel = (value: TaskScan) =>
    `${callKindLabel(value.call_kind)} · ${kind === 'users' ? `${signalLabel(value.signal)} · ` : ''}${formatDateTime(value.from)} – ${formatDateTime(value.to)}`;
  return (
    <div className="ops-stack">
      <Card>
        <h2>
          {kind === 'user_ips'
            ? t('common.audit.userIPsTitle')
            : kind === 'users'
              ? t('common.auditScans.scanUserSummaries')
              : t('common.auditScans.scanSharedIps')}
        </h2>
        <p>{t('common.auditScans.theFiltersAboveStartANewScan')}</p>
        <div className="ops-actions">
          <button
            type="button"
            className="nb-btn nb-btn--primary"
            disabled={
              start.isPending ||
              start.outcome === 'unknown' ||
              recent.isPending ||
              recent.data?.filter(running).length === 2
            }
            onClick={() => start.mutate(input)}
          >
            {start.isPending
              ? t('common.auditScans.starting')
              : t('common.auditScans.startNewScan')}
          </button>
          {running(scan) && (
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={cancel.isPending || cancel.outcome === 'unknown'}
              onClick={() => cancel.mutate(id)}
            >
              {t('common.auditScans.stopScan')}
            </button>
          )}
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            onClick={() => void client.invalidateQueries({ queryKey: key })}
          >
            {t('common.auditScans.refresh')}
          </button>
        </div>
        {start.error && (
          <ErrorState error={start.error} onRetry={() => start.mutate(start.variables ?? input)} />
        )}
        {cancel.error && (
          <ErrorState error={cancel.error} onRetry={() => cancel.mutate(cancel.variables!)} />
        )}
        {start.refreshError ? (
          <ErrorState error={start.refreshError} onRetry={() => void start.refresh()} />
        ) : null}
        {cancel.refreshError ? (
          <ErrorState error={cancel.refreshError} onRetry={() => void cancel.refresh()} />
        ) : null}
        {recent.error && <ErrorState error={recent.error} onRetry={() => void recent.refetch()} />}
        {!!visible.length && (
          <label>
            {t('common.auditScans.recentScans')}
            <select
              value={id}
              onChange={(event) => {
                const selected = visible.find((item) => item.id === event.target.value);
                if (selected) select(selected);
              }}
            >
              <option value="">{t('common.auditScans.chooseAScan')}</option>
              {visible.map((item) => (
                <option key={item.id} value={item.id}>
                  {statuses[item.state]} · {frozenLabel(item)} · {item.matched}
                </option>
              ))}
            </select>
          </label>
        )}
      </Card>
      {id &&
        (task.error ? (
          <ErrorState error={task.error} onRetry={() => void task.refetch()} />
        ) : task.isPending ? (
          <LoadingState />
        ) : null)}
      {scan && task.data && (
        <>
          <Card>
            <strong aria-live="polite">{statuses[scan.state]}</strong>
            <p>
              {t('common.auditScans.examinedCandidates')}: {scan.scanned_candidates} /{' '}
              {scan.candidates} · {t('common.auditScans.publishedResults')}: {scan.matched}
            </p>
            <p>
              {t('common.auditScans.frozenConditionsForThisScan')}: {frozenLabel(scan)} ·{' '}
              {t('common.auditScans.model')}: {scan.model || t('common.auditScans.any')} ·{' '}
              {t('common.auditScans.availableUntil')}: {formatDateTime(scan.expires_at)}
            </p>
            {(running(scan) || scan.coverage !== 'complete') && (
              <p role="status">
                {running(scan)
                  ? t('common.auditScans.resultsAndPageCountsAreProvisionalFor')
                  : t('common.auditScans.resultsCoverOnlyExaminedCandidatesNarrowThe')}
              </p>
            )}
            {scan.changed && (
              <p role="status">{t('common.auditScans.relatedUsersOrSourcesWereRemovedVisible')}</p>
            )}
            {scan.truncated_reason && (
              <p>
                {t('common.auditScans.incompleteReason')}: {t(scanReasonKey(scan.truncated_reason))}
              </p>
            )}
            {scan.last_source_at !== undefined ? (
              <p>
                {t('common.audit.lastSourceTime')}: {formatDateTime(scan.last_source_at)}
              </p>
            ) : null}
            {scan.source_watermark ? (
              <details>
                <summary>{t('common.audit.progressDetails')}</summary>
                <p>
                  {t('common.audit.sourceSnapshot')}: {scan.source_watermark}
                </p>
                {scan.last_source_id ? (
                  <p>
                    {t('common.audit.lastSource')}: {scan.last_source_id}
                  </p>
                ) : null}
              </details>
            ) : null}
          </Card>
          <PagePagination
            metadata={task.data}
            requestedPage={page}
            pageSizes={[20, 50, 100]}
            busy={task.isFetching}
            onPageChange={(next) =>
              setParams((previous) => {
                const p = new URLSearchParams(previous);
                p.set(prefix + 'page', next);
                return p;
              })
            }
            onPageSizeChange={(next) =>
              setParams((previous) => {
                const p = new URLSearchParams(previous);
                p.set(prefix + 'size', String(next));
                p.set(prefix + 'page', '1');
                return p;
              })
            }
          />
          {task.data.items.map(renderItem)}
          {!task.data.items.length && (
            <EmptyState
              title={t('common.auditScans.noResultsYet')}
              body={
                running(scan)
                  ? t('common.auditScans.theScanIsStillRunning')
                  : t('common.auditScans.noRetainedResultsMatchThisScan')
              }
            />
          )}
        </>
      )}
    </div>
  );
}

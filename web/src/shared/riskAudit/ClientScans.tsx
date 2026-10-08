import { useMemo, useState, type ReactNode } from 'react';
import { useSearchParams } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { scanReasonKey } from './scanReason';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { Card, EmptyState, ErrorState, LoadingState } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { isPageNumber, isPageSize } from '@shared/operations/pageNumbers';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { riskAPI, type ClientScan, type Filters, type Request, type RiskRole } from './api';

const running = (scan?: ClientScan) => scan?.state === 'running' || scan?.state === 'queued';
const options = { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false } as const;

export function ClientScans({
  role,
  scopeKey,
  filters,
  renderItem,
}: {
  role: RiskRole;
  scopeKey: string;
  filters: Filters;
  renderItem: (item: Request) => ReactNode;
}) {
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const id = params.get('audit_scan') ?? '';
  const rawPage = params.get('audit_page');
  const page = isPageNumber(rawPage) ? rawPage : '1';
  const rawSize = Number(params.get('audit_size'));
  const size = isPageSize(rawSize) && rawSize !== 10 ? rawSize : 20;
  const client = useQueryClient();
  const [token, setToken] = useState(() => crypto.randomUUID());
  const prefix = ['risk', role, scopeKey, 'scans'];
  const recent = useQuery({
    queryKey: [...prefix, 'recent'],
    queryFn: ({ signal }) => riskAPI(role).recentScans(signal),
    refetchInterval: (q) => (q.state.data?.some(running) ? 1500 : false),
    ...options,
  });
  const query = useQuery({
    queryKey: [...prefix, id, page, size],
    enabled: !!id,
    queryFn: ({ signal }) => riskAPI(role).scanResults(id, page, size, signal),
    refetchInterval: (q) => (running(q.state.data?.scan) ? 1500 : false),
    ...options,
  });
  const scan = query.data?.scan;
  const input = useMemo(
    () => ({
      request_token: token,
      from: filters.from === undefined ? undefined : Number(filters.from),
      to: filters.to === undefined ? undefined : Number(filters.to),
      lookback_hours:
        filters.lookback_hours === undefined ? undefined : Number(filters.lookback_hours),
      kind: filters.kind === undefined ? undefined : String(filters.kind),
      model: filters.model === undefined ? undefined : String(filters.model),
    }),
    [token, filters.from, filters.to, filters.lookback_hours, filters.kind, filters.model],
  );
  const select = (next: ClientScan) => {
    setParams((previous) => {
      const p = new URLSearchParams(previous);
      p.set('audit_tab', 'clients');
      p.set('audit_scan', next.id);
      p.set('audit_page', '1');
      return p;
    });
  };
  const authorityRoot =
    role === 'admin' ? ['admin', 'risk-audit'] : ['user', 'steward', 'risk-audit'];
  const refresh = () => client.invalidateQueries({ queryKey: prefix }, { throwOnError: true });
  const start = useRetainedOperation<typeof input, ClientScan>(
    async (value, _key, context) => {
      const result = await riskAPI(role).createScan(value, context.signal);
      context.commit(() => {
        setToken(crypto.randomUUID());
        select(result);
      });
      return result;
    },
    refresh,
    authorityRoot,
  );
  const cancel = useRetainedOperation<string, ClientScan>(
    (scanID, _key, context) => riskAPI(role).cancelScan(scanID, context.signal),
    refresh,
    authorityRoot,
  );
  const active = recent.data?.find((item) => running(item.id === scan?.id ? scan : item));
  const status: Record<ClientScan['state'], string> = {
    queued: t('common.auditScans.queued'),
    running: t('common.auditScans.scanning'),
    completed: t('common.auditScans.completed'),
    cancelled: t('common.auditScans.stoppedExistingResultsRetained'),
    limited: t('common.auditScans.resultLimitReached'),
    failed: t('common.auditScans.scanIncomplete'),
  };
  return (
    <div className="ops-stack">
      <Card>
        <h2>{t('common.auditScans.scanClientEvidence')}</h2>
        <p>{t('common.auditScans.scanRequestsUsingTheCurrentFiltersAnd')}</p>
        <div className="ops-actions">
          <button
            type="button"
            className="nb-btn nb-btn--primary"
            disabled={
              start.isPending ||
              start.outcome === 'unknown' ||
              !!active ||
              running(scan) ||
              recent.isPending
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
          {active && active.id !== id && (
            <button type="button" className="nb-btn nb-btn--secondary" onClick={() => select(active)}>
              {t('common.auditScans.openActiveScan')}
            </button>
          )}
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            onClick={() => {
              void client.invalidateQueries({ queryKey: prefix });
            }}
          >
            {t('common.auditScans.refresh')}
          </button>
        </div>
        {start.error && (
          <ErrorState error={start.error} onRetry={() => start.mutate(start.variables ?? input)} />
        )}
        {cancel.error && cancel.variables === id && (
          <ErrorState error={cancel.error} onRetry={() => cancel.mutate(cancel.variables!)} />
        )}
        {start.refreshError ? (
          <ErrorState error={start.refreshError} onRetry={() => void start.refresh()} />
        ) : null}
        {cancel.refreshError ? (
          <ErrorState error={cancel.refreshError} onRetry={() => void cancel.refresh()} />
        ) : null}
        {recent.error && <ErrorState error={recent.error} onRetry={() => void recent.refetch()} />}
        {!!recent.data?.length && (
          <label>
            {t('common.auditScans.recentScans')}
            <select
              value={id}
              onChange={(event) => {
                const selected = recent.data?.find((item) => item.id === event.target.value);
                if (selected) select(selected);
              }}
            >
              <option value="">{t('common.auditScans.chooseAScan')}</option>
              {recent.data.map((item) => (
                <option key={item.id} value={item.id}>
                  {status[item.state]} · {formatDateTime(item.from)} — {formatDateTime(item.to)} ·{' '}
                  {item.matched} {t('common.auditScans.matches')}
                </option>
              ))}
            </select>
          </label>
        )}
      </Card>
      {id &&
        (query.error ? (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} />
        ) : query.isPending ? (
          <LoadingState />
        ) : null)}
      {scan && query.data && (
        <>
          <Card>
            <div className="ops-actions">
              <strong aria-live="polite">{status[scan.state]}</strong>
              <span>
                {t('common.auditScans.scanned')}: {scan.scanned} / {scan.candidates} ·{' '}
                {t('common.auditScans.matchesFound')}: {scan.matched} ·{' '}
                {t('common.auditScans.enabledRules')}: {scan.rule_count}
              </span>
            </div>
            <progress
              className="audit-scan-progress"
              aria-label={t('common.auditScans.scanProgress')}
              value={Number((BigInt(scan.scanned) * 1000n) / (BigInt(scan.candidates) || 1n))}
              max={1000}
            />
            <p>{t('common.auditScans.filtersAndRulesAreFrozenNewRequests')}</p>
            <p>
              {formatDateTime(scan.from)} – {formatDateTime(scan.to)} ·{' '}
              {
                {
                  total: t('common.auditScans.allCalls'),
                  self: t('common.auditScans.personal'),
                  charity: t('common.auditScans.charity'),
                  unclassified: t('common.auditScans.unclassified'),
                }[scan.kind]
              }
              {scan.model && (
                <>
                  {' '}
                  · {t('common.auditScans.model')}: {scan.model}
                </>
              )}
              {' · '}
              {t('common.auditScans.availableUntil')}: {formatDateTime(scan.expires_at)}
            </p>
            {running(scan) && (
              <p role="status">
                {t('common.auditScans.pageCountsAndTotalsCoverDiscoveredRetained')}
              </p>
            )}
            {scan.state === 'limited' && <p role="status">{t(scanReasonKey(scan.reason))}</p>}
            {scan.state === 'failed' && (
              <p role="alert">
                {t('common.auditScans.repeatedScanFailuresStoppedTheTaskCommitted')}
              </p>
            )}
            {scan.rule_count === 0 && (
              <p>{t('common.auditScans.noClientRulesAreEnabledSaveAnd')}</p>
            )}
          </Card>
          <PagePagination
            metadata={query.data}
            pageSizes={[20, 50, 100]}
            requestedPage={page}
            busy={query.isPending}
            onPageChange={(next) =>
              setParams((previous) => {
                const p = new URLSearchParams(previous);
                p.set('audit_page', next);
                return p;
              })
            }
            onPageSizeChange={(next) =>
              setParams((previous) => {
                const p = new URLSearchParams(previous);
                p.set('audit_size', String(next));
                p.set('audit_page', '1');
                return p;
              })
            }
          />
          {query.data.items.map(renderItem)}
          {!query.data.items.length && (
            <EmptyState
              title={t('common.auditScans.noMatchingResultsYet')}
              body={
                running(scan)
                  ? t('common.auditScans.theScanIsStillRunning')
                  : t('common.auditScans.thereAreNoRetainedMatchesToDisplay')
              }
            />
          )}
        </>
      )}
    </div>
  );
}

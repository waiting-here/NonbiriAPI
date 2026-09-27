import { useMemo, useState, type ReactNode } from 'react';
import { useSearchParams } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Card, EmptyState, ErrorState, LoadingState } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { isPageNumber, isPageSize } from '@shared/operations/pageNumbers';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { riskAPI, type Filters, type RiskRole, type TaskKind, type TaskScan } from './api';

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
  const { i18n } = useTranslation();
  const t = (zh: string, en: string) => (i18n.language.startsWith('zh') ? zh : en);
  const formatDateTime = useDateTimeFormatter();
  const [params, setParams] = useSearchParams();
  const prefix = kind === 'users' ? 'audit_users_' : 'audit_ips_';
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
  const start = useMutation({
    mutationKey: [...key, 'create'],
    mutationFn: (value: typeof input) => riskAPI(role).createTask(value),
    onSuccess: (scan) => {
      setToken(crypto.randomUUID());
      select(scan);
      void client.invalidateQueries({ queryKey: [...key, 'recent'] });
    },
  });
  const cancel = useMutation({
    mutationKey: [...key, 'cancel'],
    mutationFn: (scanID: string) => riskAPI(role).cancelTask(scanID),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: key });
    },
  });
  const visible = recent.data?.filter((item) => item.kind === kind) ?? [];
  const scan = task.data?.scan;
  const statuses: Record<TaskScan['state'], string> = {
    queued: t('排队中', 'Queued'),
    running: t('扫描中', 'Scanning'),
    completed: t('扫描完成', 'Completed'),
    cancelled: t('已停止，结果不完整', 'Stopped; results incomplete'),
    limited: t('达到扫描上限，结果不完整', 'Scan limit reached; results incomplete'),
    failed: t('扫描失败，结果不完整', 'Scan failed; results incomplete'),
  };
  const callKindLabel = (value: string) =>
    ({
      total: t('全部调用', 'All calls'),
      self: t('自用', 'Personal'),
      charity: t('公益', 'Charity'),
      unclassified: t('未分类', 'Unclassified'),
    })[value] ?? value;
  const signalLabel = (value: string) =>
    ({
      '': t('全部用户', 'All users'),
      rpm: t('高 RPM', 'High RPM'),
      concurrency: t('高并发', 'High concurrency'),
    })[value] ?? value;
  const frozenLabel = (value: TaskScan) =>
    `${callKindLabel(value.call_kind)} · ${kind === 'users' ? `${signalLabel(value.signal)} · ` : ''}${formatDateTime(value.from)} – ${formatDateTime(value.to)} · ${t('修订', 'Revision')} ${value.filter_revision}`;
  return (
    <div className="ops-stack">
      <Card>
        <h2>
          {kind === 'users'
            ? t('扫描用户汇总', 'Scan user summaries')
            : t('扫描共享 IP', 'Scan shared IPs')}
        </h2>
        <p>
          {t(
            '上方筛选用于新扫描；选取历史扫描会恢复其冻结条件。任务和结果保留 24 小时。',
            'The filters above start a new scan; choosing a saved scan restores its frozen conditions. Tasks and results remain available for 24 hours.',
          )}
        </p>
        <div className="ops-actions">
          <button
            type="button"
            className="btn btn-primary"
            disabled={
              start.isPending || recent.isPending || recent.data?.filter(running).length === 2
            }
            onClick={() => start.mutate(input)}
          >
            {start.isPending ? t('正在开始…', 'Starting…') : t('开始新扫描', 'Start new scan')}
          </button>
          {running(scan) && (
            <button
              type="button"
              className="btn btn-secondary"
              disabled={cancel.isPending}
              onClick={() => cancel.mutate(id)}
            >
              {t('停止扫描', 'Stop scan')}
            </button>
          )}
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => void client.invalidateQueries({ queryKey: key })}
          >
            {t('刷新', 'Refresh')}
          </button>
        </div>
        {start.error && (
          <ErrorState error={start.error} onRetry={() => start.mutate(start.variables ?? input)} />
        )}
        {cancel.error && (
          <ErrorState error={cancel.error} onRetry={() => cancel.mutate(cancel.variables!)} />
        )}
        {recent.error && <ErrorState error={recent.error} onRetry={() => void recent.refetch()} />}
        {!!visible.length && (
          <label>
            {t('最近的扫描', 'Recent scans')}
            <select
              value={id}
              onChange={(event) => {
                const selected = visible.find((item) => item.id === event.target.value);
                if (selected) select(selected);
              }}
            >
              <option value="">{t('选择扫描', 'Choose a scan')}</option>
              {visible.map((item) => (
                <option key={item.id} value={item.id}>
                  {statuses[item.state]} · {frozenLabel(item)} · {item.matched} ·{' '}
                  {item.id.slice(-6)}
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
              {t('已检查候选', 'Examined candidates')}: {scan.scanned_candidates} /{' '}
              {scan.candidates} · {t('已发布结果', 'Published results')}: {scan.matched}
            </p>
            <p>
              {t('本次扫描冻结条件', 'Frozen conditions for this scan')}: {frozenLabel(scan)} ·{' '}
              {t('模型', 'Model')}: {scan.model || t('不限', 'Any')} ·{' '}
              {t('保留至', 'Available until')}: {formatDateTime(scan.expires_at)}
            </p>
            {(running(scan) || scan.coverage !== 'complete') && (
              <p role="status">
                {running(scan)
                  ? t(
                      '结果与页数仍是已完成组的暂时统计。',
                      'Results and page counts are provisional for completed groups so far.',
                    )
                  : t(
                      '结果只覆盖已处理的候选。请缩小范围重试。',
                      'Results cover only examined candidates. Narrow the range and retry.',
                    )}
              </p>
            )}
            {scan.changed && (
              <p role="status">
                {t(
                  '有相关用户或来源已删除；显示结果已更新。',
                  'Related users or sources were removed; visible results have been updated.',
                )}
              </p>
            )}
            {scan.truncated_reason && (
              <p>
                {t('未完成原因', 'Incomplete reason')}: {scan.truncated_reason}
              </p>
            )}
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
              title={t('暂无结果', 'No results yet')}
              body={
                running(scan)
                  ? t('扫描仍在进行。', 'The scan is still running.')
                  : t('当前没有可显示的结果。', 'No retained results match this scan.')
              }
            />
          )}
        </>
      )}
    </div>
  );
}

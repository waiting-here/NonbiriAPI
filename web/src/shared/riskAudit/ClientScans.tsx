import { useMemo, useState, type ReactNode } from 'react';
import { useSearchParams } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
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
  const { i18n } = useTranslation();
  const t = (zh: string, en: string) => (i18n.language.startsWith('zh') ? zh : en);
  const [params, setParams] = useSearchParams();
  const id = params.get('audit_scan') ?? '';
  const rawPage = params.get('audit_page');
  const page = isPageNumber(rawPage) ? rawPage : '1';
  const rawSize = Number(params.get('audit_size'));
  const size = isPageSize(rawSize) ? rawSize : 20;
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
  const start = useMutation({
    mutationKey: [...prefix, 'create'],
    mutationFn: (value: typeof input) => riskAPI(role).createScan(value),
    onSuccess: (next) => {
      setToken(crypto.randomUUID());
      select(next);
      void client.invalidateQueries({ queryKey: [...prefix, 'recent'] });
    },
  });
  const cancel = useMutation({
    mutationKey: [...prefix, 'cancel'],
    mutationFn: (scanID: string) => riskAPI(role).cancelScan(scanID),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: prefix });
    },
  });
  const active = recent.data?.find((item) => running(item.id === scan?.id ? scan : item));
  const status: Record<ClientScan['state'], string> = {
    queued: t('排队中', 'Queued'),
    running: t('扫描中', 'Scanning'),
    completed: t('扫描完成', 'Completed'),
    cancelled: t('已停止，保留已有结果', 'Stopped; existing results retained'),
    limited: t('已达到结果上限', 'Result limit reached'),
    failed: t('扫描未完成', 'Scan incomplete'),
  };
  return (
    <div className="ops-stack">
      <Card>
        <h2>{t('扫描客户端线索', 'Scan client evidence')}</h2>
        <p>
          {t(
            '按当前筛选和已启用规则扫描请求。结果按命中请求分页；扫描期间可以查看已发现的结果。任务保留 24 小时。',
            'Scan requests using the current filters and enabled rules. Pages contain matching requests; discovered results remain available while scanning. Tasks are retained for 24 hours.',
          )}
        </p>
        <div className="ops-actions">
          <button
            type="button"
            className="btn btn-primary"
            disabled={start.isPending || !!active || running(scan) || recent.isPending}
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
          {active && active.id !== id && (
            <button type="button" className="btn btn-secondary" onClick={() => select(active)}>
              {t('查看进行中的扫描', 'Open active scan')}
            </button>
          )}
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => {
              void client.invalidateQueries({ queryKey: prefix });
            }}
          >
            {t('刷新', 'Refresh')}
          </button>
        </div>
        {start.error && (
          <ErrorState error={start.error} onRetry={() => start.mutate(start.variables ?? input)} />
        )}
        {cancel.error && cancel.variables === id && (
          <ErrorState error={cancel.error} onRetry={() => cancel.mutate(cancel.variables!)} />
        )}
        {recent.error && <ErrorState error={recent.error} onRetry={() => void recent.refetch()} />}
        {!!recent.data?.length && (
          <label>
            {t('最近的扫描', 'Recent scans')}
            <select
              value={id}
              onChange={(event) => {
                const selected = recent.data?.find((item) => item.id === event.target.value);
                if (selected) select(selected);
              }}
            >
              <option value="">{t('选择扫描', 'Choose a scan')}</option>
              {recent.data.map((item) => (
                <option key={item.id} value={item.id}>
                  {status[item.state]} · {item.matched} {t('次命中', 'matches')} ·{' '}
                  {item.id.slice(-6)}
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
                {t('已扫描', 'Scanned')}: {scan.scanned} / {scan.candidates} ·{' '}
                {t('发现命中', 'Matches found')}: {scan.matched} · {t('启用规则', 'Enabled rules')}:{' '}
                {scan.rule_count}
              </span>
            </div>
            <progress
              className="audit-scan-progress"
              aria-label={t('扫描进度', 'Scan progress')}
              value={Number((BigInt(scan.scanned) * 1000n) / (BigInt(scan.candidates) || 1n))}
              max={1000}
            />
            <p>
              {t(
                '已冻结筛选和规则；新请求或后续规则修改不会加入本次扫描。',
                'Filters and rules are frozen; new requests and later rule edits are excluded.',
              )}
            </p>
            <p>
              {formatDateTime(scan.from)} – {formatDateTime(scan.to)} ·{' '}
              {
                {
                  total: t('全部调用', 'All calls'),
                  self: t('自用', 'Personal'),
                  charity: t('公益', 'Charity'),
                  unclassified: t('未分类', 'Unclassified'),
                }[scan.kind]
              }
              {scan.model && (
                <>
                  {' '}
                  · {t('模型', 'Model')}: {scan.model}
                </>
              )}
              {' · '}
              {t('保留至', 'Available until')}: {formatDateTime(scan.expires_at)}
            </p>
            {running(scan) && (
              <p role="status">
                {t(
                  '以下页数与总数仅对应目前发现且仍保留的结果，扫描完成后才是最终结果。',
                  'Page counts and totals cover discovered, retained results so far. They are provisional until the scan completes.',
                )}
              </p>
            )}
            {scan.state === 'limited' && (
              <p role="status">
                {t(
                  '本次最多保留 100,000 次命中。请缩小时间范围后重新扫描；当前结果不是完整范围。',
                  'This scan reached the 100,000-match limit. Narrow the time range and start again; the current result is incomplete.',
                )}
              </p>
            )}
            {scan.state === 'failed' && (
              <p role="alert">
                {t(
                  '扫描多次失败，已保留成功提交的结果。可开始新扫描；如再次失败请检查服务日志。',
                  'Repeated scan failures stopped the task. Committed results are retained. Start a new scan; check service logs if the failure recurs.',
                )}
              </p>
            )}
            {scan.rule_count === 0 && (
              <p>
                {t(
                  '没有已启用的客户端规则，请先在“客户端规则”中保存并启用规则。',
                  'No client rules are enabled. Save and enable rules in Client rules first.',
                )}
              </p>
            )}
          </Card>
          <PagePagination
            metadata={query.data}
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
              title={t('暂无匹配结果', 'No matching results yet')}
              body={
                running(scan)
                  ? t('扫描仍在进行，请稍候。', 'The scan is still running.')
                  : t(
                      '本次扫描没有可显示的命中记录。',
                      'There are no retained matches to display for this scan.',
                    )
              }
            />
          )}
        </>
      )}
    </div>
  );
}

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { TimeInput } from '@shared/components/TimeInput';
import { TimeContextNotice } from '@shared/components/TimeContext';
import { createTimeDraft, timeDraftValue } from '@shared/time';
import { ApiError } from '@shared/query/http';
import { useAdminSession } from '../data';
import {
  assets,
  displayAmount,
  getChannels,
  getOperations,
  getSeries,
  getSummary,
  siteDateTime,
  type AuditFilter,
  type Metadata,
  type Metrics,
  type Summary,
  type View,
} from '../features/economyaudit/api';
import { assetLabel, channelLabel, useEconomyText, type Text } from '../features/economyaudit/copy';
import { EconomyTrendChart } from '../features/economyaudit/EconomyTrendChart';
import '../features/economyaudit/economy.css';

function time(at: number, offset: number) {
  return siteDateTime(at, offset).replace('T', ' ');
}
function zone(offset: number) {
  return `UTC${offset >= 0 ? '+' : '-'}${String(Math.floor(Math.abs(offset) / 60)).padStart(2, '0')}:${String(Math.abs(offset) % 60).padStart(2, '0')}`;
}
const metricKeys = [
  'issued',
  'reclaimed',
  'user_income',
  'user_expense',
  'internal_transfer',
] as const;
function metricLabel(key: (typeof metricKeys)[number], t: Text) {
  return {
    issued: t('新增发行', 'New issuance'),
    reclaimed: t('永久回收', 'Permanent retirement'),
    user_income: t('用户收入', 'User income'),
    user_expense: t('用户支出', 'User spending'),
    internal_transfer: t('内部转账', 'Internal transfers'),
  }[key];
}

function AuditErrorState({
  error,
  onRetry,
}: {
  readonly error: unknown;
  readonly onRetry: () => void;
}) {
  const t = useEconomyText();
  if (
    !(error instanceof ApiError) ||
    !['invalid_request', 'feature_disabled', 'service_unavailable'].includes(error.code)
  ) {
    return <ErrorState error={error} onRetry={onRetry} />;
  }
  const message = {
    invalid_request: t(
      '请检查资产、时间区间和筛选条件。',
      'Check the asset, time range and filters.',
    ),
    feature_disabled: t(
      '请先配置站点时区，再查看账务审计。',
      'Configure the site time zone before viewing the audit.',
    ),
    service_unavailable: t(
      '账务审计暂时无法加载，请重试。',
      'The accounting audit could not be loaded. Please try again.',
    ),
  }[error.code];
  return (
    <div className="state-panel error-state nb-state nb-state--error" role="alert">
      <div>
        <h2>{t('账务审计无法加载', 'Accounting audit unavailable')}</h2>
        <p>{message}</p>
        <button type="button" className="btn btn-secondary" onClick={onRetry}>
          {t('重试', 'Retry')}
        </button>
      </div>
    </div>
  );
}

function Coverage({ meta }: { readonly meta: Metadata }) {
  const t = useEconomyText();
  const c = meta.coverage;
  return (
    <div className="audit-coverage" role="status">
      <p>
        {t('业务时区', 'Site time zone')} {zone(meta.offset_minutes)} ·{' '}
        {t('账本水位', 'Ledger watermark')} {meta.projected_seq} / {meta.ledger_seq} ·{' '}
        {t('快照', 'Snapshot')} {time(meta.snapshot_at, meta.offset_minutes)}
      </p>
      <p>
        {c.first_occurred_at === null
          ? t('尚无已处理的账本记录。', 'No ledger records have been processed yet.')
          : `${t('统计记录起于', 'Records begin at')} ${time(c.first_occurred_at, meta.offset_minutes)} · #${c.first_ledger_seq}`}
      </p>
      {c.status === 'catching_up' && (
        <p>
          {t(
            '正在按账本重建统计，请稍候。追平前不显示库存对账结果。',
            'Statistics are catching up with the ledger. Inventory reconciliation is withheld until they agree on a watermark.',
          )}
        </p>
      )}
      {!c.opening_known && (
        <p>
          {t(
            '期初未知：不能核对完整存量恒等式。',
            'The opening balance is unknown; a complete stock reconciliation is unavailable.',
          )}
        </p>
      )}
      {c.unclassified_operations !== '0' && (
        <p>
          {t('未分类操作', 'Unclassified operations')}: {c.unclassified_operations} ·{' '}
          {t(
            '金额仍计入，渠道分类存在缺口。',
            'Amounts are included, but channel classification has gaps.',
          )}
        </p>
      )}
    </div>
  );
}

function MetricCards({ metrics }: { readonly metrics: Metrics }) {
  const t = useEconomyText();
  return (
    <dl className="audit-metrics">
      {metricKeys.map((key) => (
        <div key={key}>
          <dt>{metricLabel(key, t)}</dt>
          <dd>{displayAmount(metrics[key])}</dd>
        </div>
      ))}
    </dl>
  );
}

function Stock({ summary }: { readonly summary: Summary }) {
  const t = useEconomyText(),
    s = summary.inventory,
    r = summary.reconciliation;
  if (!s) return null;
  const rows = [
    [t('用户可用正余额', 'Available user balances'), s.user_available],
    [t('冻结与预扣', 'Frozen and reserved'), s.frozen],
    [t('奖池', 'Pools'), s.pools],
    [t('平台持有', 'Platform holdings'), s.platform],
    [t('用户负余额', 'Negative user balances'), s.negative_users],
    [t('冻结账户负余额', 'Negative reserve balances'), s.negative_frozen],
    [t('奖池负余额', 'Negative pool balances'), s.negative_pools],
    [t('平台负余额', 'Negative platform balances'), s.negative_platform],
    [t('净存量', 'Net stock'), s.net],
  ];
  return (
    <section>
      <h2>{t('当前库存', 'Current stock')}</h2>
      <dl className="audit-metrics">
        {rows.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{displayAmount(value)}</dd>
          </div>
        ))}
      </dl>
      <p className={r.status === 'mismatch' ? 'audit-warning' : undefined}>
        {r.status === 'matched'
          ? t(
              '保留账本对账一致：净存量 = 累计发行 − 累计回收。',
              'Retained-ledger reconciliation matches: net stock = issuance − retirement.',
            )
          : r.status === 'mismatch'
            ? t(
                '对账不一致，请检查库存与账本。',
                'Reconciliation does not match. Review inventory and ledger records.',
              )
            : t(
                '对账覆盖不完整，请查看统计说明。',
                'Reconciliation coverage is incomplete; see the coverage details.',
              )}
      </p>
      {r.interval_opening_net !== null &&
        r.interval_closing_net !== null &&
        r.interval_net_change !== null && (
          <p>
            {t('所选币种的区间净存量', 'Interval net stock for the selected asset')}:{' '}
            {displayAmount(r.interval_closing_net)} − {displayAmount(r.interval_opening_net)} ={' '}
            {displayAmount(r.interval_net_change)}
          </p>
        )}
      <p>
        {t(
          '库存对应当前快照；区间收支对应上方时间筛选。渠道筛选不会改变整个币种的库存。',
          'Inventory belongs to the current snapshot; flows belong to the selected interval. A channel filter does not change the stock of the entire asset.',
        )}
      </p>
    </section>
  );
}

function Trend({ filter }: { readonly filter: AuditFilter }) {
  const t = useEconomyText(),
    session = useAdminSession();
  const q = useQuery({
    queryKey: ['admin', session.data?.admin.username, 'economy-series', filter],
    queryFn: ({ signal }) => getSeries(filter, signal),
    retry: false,
    refetchInterval: (q) =>
      q.state.data?.metadata.coverage.status === 'catching_up' ? 1000 : false,
  });
  if (q.isPending) return <LoadingState />;
  if (q.error) return <AuditErrorState error={q.error} onRetry={() => void q.refetch()} />;
  return (
    <>
      <Coverage meta={q.data.metadata} />
      {q.data.metadata.coverage.status !== 'catching_up' && (
        <>
          <EconomyTrendChart series={q.data} />
          <div className="audit-table">
            <table>
              <caption>{t('分时段收支', 'Flows by time bucket')}</caption>
              <thead>
                <tr>
                  <th>{t('区间起点', 'Interval start')}</th>
                  {metricKeys.map((key) => (
                    <th key={key}>{metricLabel(key, t)}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {q.data.data.map((p) => (
                  <tr key={p.start}>
                    <th>{time(p.start, q.data.metadata.offset_minutes)}</th>
                    {metricKeys.map((key) => (
                      <td key={key}>{displayAmount(p.metrics[key])}</td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </>
  );
}

function ChannelDetails({
  filter,
  select,
}: {
  readonly filter: AuditFilter;
  readonly select: (kind: string, channel: string) => void;
}) {
  const t = useEconomyText(),
    session = useAdminSession();
  const q = useQuery({
    queryKey: ['admin', session.data?.admin.username, 'economy-channels', filter],
    queryFn: ({ signal }) => getChannels(filter, signal),
    retry: false,
    refetchInterval: (q) =>
      q.state.data?.metadata.coverage.status === 'catching_up' ? 1000 : false,
  });
  if (q.isPending) return <LoadingState />;
  if (q.error) return <AuditErrorState error={q.error} onRetry={() => void q.refetch()} />;
  return (
    <>
      <Coverage meta={q.data.metadata} />
      <div className="audit-table">
        <table>
          <caption>
            {t('点击渠道查看对应分录', 'Open a channel to inspect its ledger entries')}
          </caption>
          <thead>
            <tr>
              <th>{t('渠道 / 操作', 'Channel / operation')}</th>
              {metricKeys.map((key) => (
                <th key={key}>{metricLabel(key, t)}</th>
              ))}
              <th>{t('操作数', 'Operations')}</th>
            </tr>
          </thead>
          <tbody>
            {q.data.data.map((c) => (
              <tr key={`${c.kind}/${c.source_type}/${c.channel}`}>
                <th>
                  <button className="btn btn-secondary" onClick={() => select(c.kind, c.channel)}>
                    {channelLabel(c.channel, t)}
                  </button>
                  <br />
                  <code>{c.kind}</code>
                  {!c.known && <strong>{t('未分类', 'Unclassified')}</strong>}
                </th>
                {metricKeys.map((key) => (
                  <td key={key}>{displayAmount(c.metrics[key])}</td>
                ))}
                <td>{c.metrics.operations}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {q.data.data.length === 0 && q.data.metadata.coverage.status !== 'catching_up' && (
        <p>{t('该区间没有账本操作。', 'There are no ledger operations in this interval.')}</p>
      )}
    </>
  );
}

function LedgerDetails({ filter }: { readonly filter: AuditFilter }) {
  const t = useEconomyText(),
    session = useAdminSession();
  const [cursors, setCursors] = useState<string[]>([]);
  const cursor = cursors.at(-1);
  const q = useQuery({
    queryKey: ['admin', session.data?.admin.username, 'economy-operations', filter, cursor],
    queryFn: ({ signal }) => getOperations({ ...filter, cursor }, signal),
    retry: false,
  });
  if (q.isPending) return <LoadingState />;
  if (q.error) return <AuditErrorState error={q.error} onRetry={() => void q.refetch()} />;
  return (
    <>
      <Coverage meta={q.data.metadata} />
      <p>
        {t(
          '每页最多100笔；每个币种的分录独立展示。',
          'Up to 100 operations per page; each asset is shown separately.',
        )}{' '}
        {t('本次翻页固定于账本水位', 'Pagination is anchored at ledger watermark')}{' '}
        {q.data.anchor_seq}.
      </p>
      {q.data.data.map((o) => (
        <details className="audit-operation" key={o.id}>
          <summary>
            {time(o.created_at, q.data.metadata.offset_minutes)} ·{' '}
            {channelLabel(o.classification.channel, t)} · <code>{o.kind}</code> · #{o.ledger_seq}
          </summary>
          <p>
            <code>{o.id}</code> · {o.source_type}: <code>{o.source_id}</code>
          </p>
          <div className="audit-table">
            <table>
              <thead>
                <tr>
                  <th>{t('资产', 'Asset')}</th>
                  <th>{t('账户', 'Account')}</th>
                  <th>{t('用户', 'User')}</th>
                  <th>{t('变动', 'Delta')}</th>
                </tr>
              </thead>
              <tbody>
                {o.entries.map((e, i) => (
                  <tr key={i}>
                    <td>{assetLabel(e.asset, t)}</td>
                    <td>{e.account_kind}</td>
                    <td>
                      {e.user_id ??
                        (e.account_kind === 'user' ? t('已去身份', 'Deidentified') : '—')}
                    </td>
                    <td>{displayAmount(e.delta)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </details>
      ))}
      {q.data.data.length === 0 && (
        <p>{t('该区间没有账本操作。', 'There are no ledger operations in this interval.')}</p>
      )}
      <div className="audit-actions">
        <button
          className="btn btn-secondary"
          disabled={cursors.length === 0}
          onClick={() => setCursors((v) => v.slice(0, -1))}
        >
          {t('上一页', 'Previous')}
        </button>
        <button
          className="btn btn-secondary"
          disabled={!q.data.next_cursor}
          onClick={() => {
            if (q.data.next_cursor) setCursors((v) => [...v, q.data.next_cursor!]);
          }}
        >
          {t('下一页', 'Next')}
        </button>
      </div>
    </>
  );
}

export function EconomyAuditPage() {
  const t = useEconomyText(),
    session = useAdminSession();
  const [filter, setFilter] = useState<AuditFilter>(() => {
    const to = Math.floor(Date.now() / 1000) + 1;
    return { asset: 'general', from: to - 86400, to, bucket: 'hour' };
  });
  const [draft, setDraft] = useState(filter),
    [view, setView] = useState<View>('series'),
    [invalid, setInvalid] = useState(false);
  const [fromInput, setFromInput] = useState(() => createTimeDraft(filter.from, 'second'));
  const [toInput, setToInput] = useState(() => createTimeDraft(filter.to, 'second'));
  const summary = useQuery({
    queryKey: ['admin', session.data?.admin.username, 'economy-summary', filter],
    queryFn: ({ signal }) => getSummary(filter, signal),
    retry: false,
    refetchInterval: (q) =>
      q.state.data?.metadata.coverage.status === 'catching_up' ? 1000 : false,
  });
  const from = timeDraftValue(fromInput),
    to = timeDraftValue(toInput);
  const offset = fromInput.siteOffsetMinutes;
  const ready =
    typeof from === 'number' &&
    typeof to === 'number' &&
    offset !== null &&
    toInput.siteOffsetMinutes === offset;
  function apply() {
    if (!ready) return;
    const step = draft.bucket === 'hour' ? 3600 : 86400;
    const count =
      Math.floor((to - 1 + offset * 60) / step) - Math.floor((from + offset * 60) / step) + 1;
    if (to <= from || count > (draft.bucket === 'hour' ? 744 : 366)) {
      setInvalid(true);
      return;
    }
    setInvalid(false);
    setFilter({ ...draft, from, to, kind: undefined, channel: undefined });
  }
  return (
    <div className="economy-audit">
      <PageHeader
        title={t('积分体系审计', 'Credit economy audit')}
        description={t(
          '分别查看发行、回收、用户收支和库存；不同资产不会相加。',
          'Inspect issuance, retirement, user flows and stock separately for each asset.',
        )}
      />
      <Card>
        <TimeContextNotice station="admin" />
        <form
          className="audit-filters"
          onSubmit={(e) => {
            e.preventDefault();
            apply();
          }}
        >
          <label>
            {t('资产', 'Asset')}
            <select
              value={draft.asset}
              onChange={(e) =>
                setDraft((v) => ({ ...v, asset: e.target.value as AuditFilter['asset'] }))
              }
            >
              {assets.map((a) => (
                <option value={a} key={a}>
                  {assetLabel(a, t)}
                </option>
              ))}
            </select>
          </label>
          <label>
            {t('开始', 'From')}
            <TimeInput
              station="admin"
              showZoneHint={false}
              draft={fromInput}
              onChange={setFromInput}
              required
            />
          </label>
          <label>
            {t('结束（不含）', 'To (exclusive)')}
            <TimeInput
              station="admin"
              showZoneHint={false}
              draft={toInput}
              onChange={setToInput}
              required
            />
          </label>
          <label>
            {t('分时粒度', 'Bucket')}
            <select
              value={draft.bucket}
              onChange={(e) =>
                setDraft((v) => ({ ...v, bucket: e.target.value as AuditFilter['bucket'] }))
              }
            >
              <option value="hour">{t('小时（最多744个）', 'Hour (up to 744)')}</option>
              <option value="day">{t('日（最多366个）', 'Day (up to 366)')}</option>
            </select>
          </label>
          <button className="btn btn-primary" type="submit" disabled={!ready}>
            {t('应用', 'Apply')}
          </button>
          <button
            className="btn btn-secondary"
            type="button"
            onClick={() => void summary.refetch()}
          >
            {t('刷新库存', 'Refresh stock')}
          </button>
        </form>
        {invalid && (
          <p role="alert">
            {t(
              '请检查时间区间；较长区间请按日分段查询。',
              'Check the range; use daily buckets and separate ranges for longer periods.',
            )}
          </p>
        )}
      </Card>
      <Card>
        {summary.isPending ? (
          <LoadingState />
        ) : summary.error ? (
          <AuditErrorState error={summary.error} onRetry={() => void summary.refetch()} />
        ) : (
          <>
            <h2>{assetLabel(filter.asset, t)}</h2>
            <Coverage meta={summary.data.metadata} />
            {summary.data.metadata.coverage.status !== 'catching_up' && (
              <>
                <h3>{t('所选区间收支', 'Selected interval flows')}</h3>
                <MetricCards metrics={summary.data.flows} />
                <Stock summary={summary.data} />
              </>
            )}
          </>
        )}
      </Card>
      <Card>
        <div className="audit-actions" role="group" aria-label={t('审计视图', 'Audit view')}>
          {(['series', 'channels', 'operations'] as const).map((v) => (
            <button
              key={v}
              className={`btn ${view === v ? 'btn-primary' : 'btn-secondary'}`}
              aria-pressed={view === v}
              onClick={() => setView(v)}
            >
              {
                {
                  series: t('趋势', 'Trends'),
                  channels: t('渠道', 'Channels'),
                  operations: t('分录', 'Ledger'),
                }[v]
              }
            </button>
          ))}
          {filter.kind && (
            <button
              className="btn btn-secondary"
              onClick={() => setFilter((v) => ({ ...v, kind: undefined, channel: undefined }))}
            >
              {t('清除操作筛选', 'Clear operation filter')}: {filter.kind}
            </button>
          )}
        </div>
        {view === 'series' ? (
          <Trend filter={filter} />
        ) : view === 'channels' ? (
          <ChannelDetails
            filter={filter}
            select={(kind, channel) => {
              setFilter((v) => ({ ...v, kind, channel }));
              setView('operations');
            }}
          />
        ) : (
          <LedgerDetails key={JSON.stringify(filter)} filter={filter} />
        )}
      </Card>
    </div>
  );
}

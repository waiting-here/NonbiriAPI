import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  captureStationSession,
  clearStationSession,
  stationSessionMatches,
  StationSessionChangedError,
} from '@shared/charityManagement';
import {
  getPenalties,
  getPenalty,
  getPenaltyEvidence,
  penaltyKinds,
  penaltyStates,
  ruleFields,
  type Penalty,
  type PenaltyAction,
  type PenaltyFilter,
  type PenaltyRole,
} from '@shared/operations/penalties';
import { usePagePager, type PagePager } from '@shared/operations/usePagePager';
import { PagePagination } from '@shared/operations/PagePagination';
import type { PageMetadata } from '@shared/operations/pageNumbers';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import { HistoryDialog } from './LoanDetails';
import { useLoanText } from './loanCopy';
import { ErrorState, LoadingState } from './States';

type Scope = { role: PenaltyRole; account: string; userID: string };
const names: Record<string, readonly [string, string]> = {
  deduction: ['积分扣除', 'Credit deduction'],
  ban: ['账号封禁', 'Account ban'],
  charity_suspend: ['公益暂停', 'Charity suspension'],
  active: ['生效中', 'Active'],
  ended: ['已结束', 'Ended'],
  applied: ['已执行', 'Applied'],
  extended: ['已延长', 'Extended'],
  expired: ['已到期', 'Expired'],
  released: ['已解除', 'Released'],
  adjusted: ['人工接管', 'Manually adjusted'],
  trigger: ['触发', 'Triggered'],
  extend: ['延长', 'Extended'],
  adjust: ['人工调整', 'Manually adjusted'],
  release: ['人工解除', 'Manually released'],
  expire: ['自动到期', 'Expired'],
  charity_rpm: ['公益请求超过频率限制', 'Charity request rate exceeded'],
  charity_short_content: ['公益内容过短', 'Charity content too short'],
  manual_release: ['人工解除', 'Manually released'],
  manual_adjustment: ['人工调整', 'Manually adjusted'],
  rpm_window: ['频率违规窗口', 'Rate violation window'],
  short_content_direct: ['单次短内容', 'Single short request'],
  short_content_window: ['短内容累计窗口', 'Repeated short requests'],
  short_content_suspend_window: ['公益暂停累计窗口', 'Charity suspension window'],
  short_content_deduction: ['短内容扣分', 'Short-content deduction'],
  rpm_ban_threshold: ['频率违规封禁阈值（次）', 'Rate ban threshold (requests)'],
  rpm_ban_window_seconds: ['频率违规窗口（秒）', 'Rate window (seconds)'],
  rpm_ban_duration_seconds: ['频率违规封禁时长（秒）', 'Rate ban duration (seconds)'],
  charity_min_chars: ['公益内容最少字符', 'Minimum charity characters'],
  charity_violation_deduct_milli: [
    '单次扣分（千分之一积分）',
    'Deduction (thousandths of a credit)',
  ],
  charity_violation_ban_seconds: ['单次封禁时长（秒）', 'Direct ban duration (seconds)'],
  charity_violation_window_seconds: ['短内容累计窗口（秒）', 'Short-content window (seconds)'],
  charity_violation_ban_threshold: [
    '短内容封禁阈值（次）',
    'Short-content ban threshold (requests)',
  ],
  charity_violation_window_ban_seconds: ['累计封禁时长（秒）', 'Window ban duration (seconds)'],
  charity_suspend_window_seconds: ['暂停累计窗口（秒）', 'Suspension window (seconds)'],
  charity_suspend_threshold: ['暂停阈值（次）', 'Suspension threshold (requests)'],
  charity_suspend_duration_seconds: ['暂停时长（秒）', 'Suspension duration (seconds)'],
};
function useName() {
  const text = useLoanText();
  return (key: string) => (names[key] ? text(...names[key]) : key);
}
function useProtectedQuery<T>(
  scope: Scope,
  key: readonly unknown[],
  load: (signal: AbortSignal) => Promise<T>,
) {
  const client = useQueryClient();
  return useQuery({
    queryKey: [
      scope.role === 'admin' ? 'admin' : 'user',
      'penalties',
      scope.role,
      scope.account,
      scope.userID,
      ...key,
    ],
    queryFn: async ({ signal }) => {
      const session = captureStationSession(client, scope.role);
      try {
        const result = await load(signal);
        if (!stationSessionMatches(client, scope.role, session))
          throw new StationSessionChangedError();
        return result;
      } catch (error) {
        if (!stationSessionMatches(client, scope.role, session))
          throw new StationSessionChangedError();
        if (isUnauthorized(error) || isForbidden(error)) clearStationSession(client, scope.role);
        throw error;
      }
    },
    retry: false,
  });
}
function Pagination({
  pager,
  metadata,
  busy,
}: {
  pager: PagePager;
  metadata: PageMetadata;
  busy: boolean;
}) {
  return (
    <PagePagination
      metadata={metadata}
      requestedPage={pager.page}
      onPageChange={pager.setPage}
      onPageSizeChange={pager.setPageSize}
      busy={busy}
    />
  );
}
function PenaltyFacts({ value }: { value: Penalty }) {
  const text = useLoanText(),
    name = useName();
  return (
    <dl className="loan-facts">
      <div>
        <dt>{text('类型与结果', 'Type and result')}</dt>
        <dd>
          {name(value.kind)} · {name(value.state)} · {name(value.result)}
        </dd>
      </div>
      <div>
        <dt>{text('原因', 'Reason')}</dt>
        <dd>{name(value.reason_code)}</dd>
      </div>
      <div>
        <dt>{text('开始时间', 'Started')}</dt>
        <dd>{formatDateTime(value.started_at)}</dd>
      </div>
      <div>
        <dt>{text('预计结束', 'Scheduled end')}</dt>
        <dd>
          {value.ends_at === null
            ? text('未设定结束时间', 'No scheduled end')
            : formatDateTime(value.ends_at)}
        </dd>
      </div>
      <div>
        <dt>{text('实际结束', 'Actual end')}</dt>
        <dd>{value.ended_at === null ? '—' : formatDateTime(value.ended_at)}</dd>
      </div>
    </dl>
  );
}
function RequestReference({
  scope,
  id,
  available,
}: {
  scope: Scope;
  id: string | null;
  available: boolean;
}) {
  const text = useLoanText();
  if (!id) return <span>—</span>;
  return (
    <span className="penalty-reference">
      <code>{id}</code>
      {available ? (
        <a
          href={`${scope.role === 'admin' ? '/logs' : '/steward'}?request_id=${encodeURIComponent(id)}&user_id=${scope.userID}`}
          target="_blank"
          rel="noreferrer"
        >
          {text('查看关联请求', 'View related request')}
        </a>
      ) : (
        <span>{text('关联请求日志已过期', 'Related request log has expired')}</span>
      )}
    </span>
  );
}
function Evidence({
  scope,
  caseID,
  action,
  onBack,
}: {
  scope: Scope;
  caseID: string;
  action: PenaltyAction;
  onBack: () => void;
}) {
  const text = useLoanText(),
    name = useName();
  const pager = usePagePager({
    station: scope.role === 'admin' ? 'admin' : 'user',
    listType: 'penalty-evidence',
    scopeKey: `${scope.account}:${scope.userID}:${caseID}:${action.id}`,
  });
  const query = useProtectedQuery(
    scope,
    ['evidence', caseID, action.id, pager.page, pager.pageSize],
    (signal) =>
      getPenaltyEvidence(
        scope.role,
        scope.userID,
        caseID,
        action.id,
        pager.page,
        pager.pageSize,
        signal,
      ),
  );
  const data = query.data;
  return (
    <>
      <button className="btn btn-secondary" onClick={onBack}>
        {text('返回处理记录', 'Back to actions')}
      </button>
      <h3>{text('当时依据', 'Evidence at the time')}</h3>
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : data ? (
        <>
          {data.statistics.rule ? (
            <dl className="loan-facts">
              <div>
                <dt>{text('适用规则', 'Applied rule')}</dt>
                <dd>{name(data.statistics.rule)}</dd>
              </div>
              <div>
                <dt>
                  {text('统计窗口（起点不含，终点含）', 'Window (start exclusive, end inclusive)')}
                </dt>
                <dd>
                  {formatDateTime(data.statistics.window_start!)} →{' '}
                  {formatDateTime(data.statistics.window_end!)}
                </dd>
              </div>
              <div>
                <dt>{text('实际次数 / 阈值', 'Actual count / threshold')}</dt>
                <dd>
                  {data.statistics.count} / {data.statistics.threshold}
                </dd>
              </div>
              {data.statistics.actual_chars !== undefined ? (
                <div>
                  <dt>{text('实际字符 / 最少字符', 'Actual / minimum characters')}</dt>
                  <dd>
                    {data.statistics.actual_chars} / {data.statistics.minimum_chars}
                  </dd>
                </div>
              ) : null}
              {data.statistics.direct_seconds !== undefined ? (
                <div>
                  <dt>{text('直接封禁（秒）', 'Direct ban (seconds)')}</dt>
                  <dd>{data.statistics.direct_seconds}</dd>
                </div>
              ) : null}
            </dl>
          ) : (
            <p>{text('此处理没有新增违规计数。', 'This action added no violation count.')}</p>
          )}
          {data.rules.version !== undefined ? (
            <details>
              <summary>{text('查看当时完整规则', 'View all rules at the time')}</summary>
              <dl className="loan-facts">
                {ruleFields.map((key) => (
                  <div key={key}>
                    <dt>{name(key)}</dt>
                    <dd>{data.rules[key]}</dd>
                  </div>
                ))}
              </dl>
            </details>
          ) : null}
          <h3>{text('参与计数的请求', 'Requests included in the count')}</h3>
          {data.members.data.map((member) => (
            <article className="loan-record" key={member.request_id}>
              <p>
                {formatDateTime(member.occurred_at)} ·{' '}
                {member.violation_kind === 'rpm'
                  ? text('请求频率', 'Request rate')
                  : text('内容长度', 'Content length')}
                {member.content_chars === null ? '' : ` · ${member.content_chars}`}
              </p>
              <RequestReference
                scope={scope}
                id={member.request_id}
                available={member.request_log_available}
              />
            </article>
          ))}
          {data.members.data.length === 0 ? (
            <p>{text('无计数成员。', 'No counted requests.')}</p>
          ) : null}
          <Pagination pager={pager} metadata={data.members.pagination} busy={query.isFetching} />
        </>
      ) : null}
    </>
  );
}
function CaseHistory({
  scope,
  caseID,
  onBack,
}: {
  scope: Scope;
  caseID: string;
  onBack: () => void;
}) {
  const text = useLoanText(),
    name = useName();
  const [action, setAction] = useState<PenaltyAction | null>(null);
  const pager = usePagePager({
    station: scope.role === 'admin' ? 'admin' : 'user',
    listType: 'penalty-actions',
    scopeKey: `${scope.account}:${scope.userID}:${caseID}`,
  });
  const query = useProtectedQuery(scope, ['case', caseID, pager.page, pager.pageSize], (signal) =>
    getPenalty(scope.role, scope.userID, caseID, pager.page, pager.pageSize, signal),
  );
  if (action)
    return (
      <Evidence
        key={action.id}
        scope={scope}
        caseID={caseID}
        action={action}
        onBack={() => setAction(null)}
      />
    );
  return (
    <>
      <button className="btn btn-secondary" onClick={onBack}>
        {text('返回处罚列表', 'Back to penalties')}
      </button>
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <>
          <PenaltyFacts value={query.data.case} />
          <h3>{text('处理记录', 'Actions')}</h3>
          {query.data.actions.data.map((a) => (
            <article className="loan-record" key={a.id}>
              <h4>
                {name(a.action)} · {formatDateTime(a.occurred_at)}
              </h4>
              <p>{name(a.reason_code)}</p>
              <p>
                {text('预计结束：原 → 新', 'Scheduled end: previous → new')} ·{' '}
                {a.previous_ends_at === null ? '—' : formatDateTime(a.previous_ends_at)} →{' '}
                {a.ends_at === null ? text('未设定', 'Unscheduled') : formatDateTime(a.ends_at)}
              </p>
              <p>
                {text('操作者', 'Actor')} · {a.actor_user_id ?? text('系统', 'System')}
              </p>
              {a.operation_id ? (
                <p>
                  {text('账务流水', 'Ledger operation')} · <code>{a.operation_id}</code>
                </p>
              ) : null}
              <RequestReference
                scope={scope}
                id={a.request_id}
                available={a.request_log_available}
              />
              <button className="btn btn-secondary" onClick={() => setAction(a)}>
                {text('查看当时依据', 'View evidence')} ({a.evidence_count})
              </button>
            </article>
          ))}
          <Pagination
            pager={pager}
            metadata={query.data.actions.pagination}
            busy={query.isFetching}
          />
        </>
      )}
    </>
  );
}
function PenaltyList({ scope }: { scope: Scope }) {
  const text = useLoanText(),
    name = useName();
  const [filter, setFilter] = useState<PenaltyFilter>({}),
    [caseID, setCaseID] = useState<string | null>(null);
  const pager = usePagePager({
    station: scope.role === 'admin' ? 'admin' : 'user',
    listType: 'penalties',
    scopeKey: `${scope.account}:${scope.userID}`,
    resetKey: JSON.stringify(filter),
  });
  const query = useProtectedQuery(scope, ['list', filter, pager.page, pager.pageSize], (signal) =>
    getPenalties(scope.role, scope.userID, pager.page, pager.pageSize, filter, signal),
  );
  if (caseID)
    return (
      <CaseHistory key={caseID} scope={scope} caseID={caseID} onBack={() => setCaseID(null)} />
    );
  return (
    <>
      <div className="filter-bar">
        <label>
          {text('处罚类型', 'Penalty type')}
          <select
            value={filter.type ?? ''}
            onChange={(e) =>
              setFilter((f) => ({
                ...f,
                type: (e.target.value as PenaltyFilter['type']) || undefined,
              }))
            }
          >
            <option value="">{text('全部', 'All')}</option>
            {penaltyKinds.map((k) => (
              <option key={k} value={k}>
                {name(k)}
              </option>
            ))}
          </select>
        </label>
        <label>
          {text('处罚状态', 'Penalty state')}
          <select
            value={filter.state ?? ''}
            onChange={(e) =>
              setFilter((f) => ({
                ...f,
                state: (e.target.value as PenaltyFilter['state']) || undefined,
              }))
            }
          >
            <option value="">{text('全部', 'All')}</option>
            {penaltyStates.map((k) => (
              <option key={k} value={k}>
                {name(k)}
              </option>
            ))}
          </select>
        </label>
      </div>
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <>
          {query.data.legacy_details_unavailable ? (
            <p role="status">
              {text(
                '当前旧自动封禁没有保存统计依据，无法展示详情。',
                'The current older automatic ban has no saved statistical evidence.',
              )}
            </p>
          ) : null}
          {query.data.data.length === 0 ? (
            <p>
              {text(
                '暂无仍在保留期内的自动处罚。',
                'No automatic penalties within the retention period.',
              )}
            </p>
          ) : (
            query.data.data.map((c) => (
              <article className="loan-record" key={c.id}>
                <PenaltyFacts value={c} />
                <button className="btn btn-secondary" onClick={() => setCaseID(c.id)}>
                  {text('查看处理记录', 'View actions')}
                </button>
              </article>
            ))
          )}
          <Pagination pager={pager} metadata={query.data.pagination} busy={query.isFetching} />
        </>
      )}
    </>
  );
}
export function PenaltyHistory(scope: Scope) {
  const [open, setOpen] = useState(false),
    text = useLoanText();
  return (
    <>
      <button className="btn btn-secondary" onClick={() => setOpen(true)}>
        {text('自动处罚记录', 'Automatic penalties')}
      </button>
      {open ? (
        <HistoryDialog
          title={text('自动处罚记录', 'Automatic penalties')}
          onClose={() => setOpen(false)}
        >
          <PenaltyList key={`${scope.role}:${scope.account}:${scope.userID}`} scope={scope} />
        </HistoryDialog>
      ) : null}
    </>
  );
}

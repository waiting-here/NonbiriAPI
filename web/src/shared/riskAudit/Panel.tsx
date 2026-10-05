import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link, useSearchParams } from 'react-router';
import { queryPath } from '@shared/operations/api';
import { ClientScans } from './ClientScans';
import { TaskScans } from './TaskScans';
import { TimeInput } from '@shared/components/TimeInput';
import { TimeContextNotice } from '@shared/components/TimeContext';
import { createTimeDraft, timeDraftValue } from '@shared/time';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { sourceFlagLabel, sourceQualityLabel } from '@shared/observability/sourceLabels';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { Tabs, Fold, Note } from '@shared/components/ui';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { PagePagination } from '@shared/operations/PagePagination';
import { isPageNumber, isPageSize } from '@shared/operations/pageNumbers';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import {
  accessPaths,
  riskAPI,
  sourceFields,
  type Config,
  type Condition,
  type Detail,
  type Filters,
  type Request,
  type RiskRole,
  type Rule,
  type RuleInput,
  type SharedIP,
  type Source,
  type Stats,
  type Summary,
  type UserIPs,
} from './api';
import { fieldLabel, pathLabel, riskCopy, type RiskCopy } from './copy';
import '@shared/operations/operations.css';
import './audit.css';

interface Scope {
  role: RiskRole;
  scopeKey: string;
  c: RiskCopy;
  filters: Filters;
  customRange: boolean;
}
const queryOptions = {
  retry: false,
  gcTime: 0,
  staleTime: 0,
  refetchOnWindowFocus: false,
} as const;
function useStamp() {
  const format = useDateTimeFormatter();
  return (n: number | null) => (n !== null && n > 0 ? format(n) : '—');
}
const filterFields = ['from', 'to', 'lookback_hours', 'kind', 'model'] as const;
function initialWindow(params?: URLSearchParams): Filters {
  const result: Filters = { kind: 'total', limit: 100 };
  if (!params) return result;
  for (const key of ['from', 'to', 'lookback_hours'] as const) {
    const raw = params.get('audit_' + key);
    if (raw && /^[1-9][0-9]{0,11}$/.test(raw) && Number.isSafeInteger(Number(raw)))
      result[key] = Number(raw);
  }
  const kind = params.get('audit_kind');
  if (kind && ['total', 'self', 'charity', 'unclassified'].includes(kind)) result.kind = kind;
  const model = params.get('audit_model');
  if (model && model.length <= 512) result.model = model;
  return result;
}
function Coverage({ value, c }: { value: string; c: RiskCopy }) {
  return (
    <p>
      <strong>{c.coverage}: </strong>
      {(
        {
          observed_minutes: c.observations,
          latest_100_observations: c.latestObservations,
          source_page: c.sourcePage,
          candidate_page: c.candidatePage,
          authenticated_logical_calls: c.authenticatedCalls,
          bounded_scan: c.partial,
        } as Record<string, string>
      )[value] ?? c.partial}
    </p>
  );
}
function SourceView({ source, c }: { source: Source; c: RiskCopy }) {
  const { t } = useTranslation();
  return (
    <details>
      <summary>{c.source}</summary>
      <dl className="ops-kv">
        <dt>{c.quality}</dt>
        <dd>{source.ip_quality ? sourceQualityLabel(source.ip_quality, t) : c.unknown}</dd>
        {sourceFields.map((field) => (
          <div key={field}>
            <dt>{fieldLabel(field, c)}</dt>
            <dd className="ops-wrap">
              {source[field] || c.unknown}
              {source.quality[field] && Object.values(source.quality[field]!).some(Boolean)
                ? ` (${Object.entries(source.quality[field]!)
                    .filter(([, v]) => v)
                    .map(([k]) => sourceFlagLabel(k, t))
                    .join(', ')})`
                : ''}
            </dd>
          </div>
        ))}
      </dl>
    </details>
  );
}
function RequestView({
  item,
  c,
  role,
  inspect,
}: {
  item: Request;
  c: RiskCopy;
  role: RiskRole;
  inspect?: (id: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <Card>
      <div className="ops-actions">
        <strong>{item.request_id}</strong>
        <Link
          to={queryPath(role === 'admin' ? '/logs' : '/steward', {
            tab: role === 'steward' ? 'logs' : undefined,
            request_id: item.request_id,
            from: Math.max(0, item.occurred_at - 60),
            to: item.occurred_at + 60,
          })}
        >
          {t('common.audit.openRequest')}
        </Link>
        {inspect ? (
          <button className="btn btn-secondary" onClick={() => inspect(item.user_id)}>
            {c.inspect} {item.user_id}
          </button>
        ) : null}
      </div>
      <dl className="ops-kv">
        <dt>{c.user}</dt>
        <dd>{item.user_id}</dd>
        <dt>{c.model}</dt>
        <dd>{item.model}</dd>
        <dt>{c.kind}</dt>
        <dd>{item.call_kind}</dd>
        <dt>{c.result}</dt>
        <dd>
          {item.outcome} ·{' '}
          {item.dispatched ? c.dispatched : item.outcome === 'failed' ? c.rejected : c.unknown}
        </dd>
        <dt>{c.reason}</dt>
        <dd>{item.rejection_reason || item.error_code || '—'}</dd>
        <dt>{c.duration}</dt>
        <dd>{item.duration_millis ?? c.unknown}</dd>
        <dt>{c.response}</dt>
        <dd>{item.response_started === null ? c.unknown : item.response_started ? c.yes : c.no}</dd>
      </dl>
      <SourceView source={item.source} c={c} />
      {item.matches_truncated ? (
        <p>
          {c.matches}: {item.match_count} · {c.matchLimit}
        </p>
      ) : null}
      {item.matches.length ? (
        <div>
          <h4>{c.matches}</h4>
          {item.matches.map((m) => (
            <p key={m.rule_id}>
              {m.name} · {c.revision} {m.revision} ·{' '}
              {m.status === 'confirmed' ? c.confirmed : c.suspected}
              <br />
              {m.evidence_note}
              <br />
              {m.evidence_url}
              <br />
              {m.fields.join(' + ')} · {m.quality}
            </p>
          ))}
        </div>
      ) : null}
    </Card>
  );
}
function SummaryView({ value, c }: { value: Summary; c: RiskCopy }) {
  return (
    <dl className="ops-kv">
      <dt>{c.rpmUsed}</dt>
      <dd>{value.rpm_committed}</dd>
      <dt>{c.rpmDenied}</dt>
      <dd>{value.rpm_denied}</dd>
      <dt>{c.concurrentDenied}</dt>
      <dd>{value.concurrency_denied}</dd>
      <dt>{c.peak}</dt>
      <dd>{value.peak}</dd>
      <dt>
        {c.completed} / {c.incomplete}
      </dt>
      <dd>
        {value.complete_minutes} / {value.incomplete_minutes}
      </dd>
      <dt>{c.rpm}</dt>
      <dd>
        {value.high_rpm_minutes} · {value.rpm_risk ? c.yes : c.no}
      </dd>
      <dt>{c.concurrency}</dt>
      <dd>
        {value.high_concurrency_minutes} · {value.concurrency_risk ? c.yes : c.no}
      </dd>
    </dl>
  );
}
function Samples({ value, c }: { value: Stats; c: RiskCopy }) {
  return (
    <>
      <p>
        {c.sampled}: {value.samples}; {c.dispatched}: {value.dispatched}; {c.rejected}:{' '}
        {value.rejected}
      </p>
      <p>
        {c.cancellations}: {value.cancellations.map((b) => `${b.label}: ${b.count}`).join(' · ')};{' '}
        {c.unknown}: {value.unknown_duration}
      </p>
    </>
  );
}
function DetailBody({ detail, c }: { detail: Detail; c: RiskCopy }) {
  const { t } = useTranslation();
  const stamp = useStamp();
  return (
    <>
      <Card>
        <SummaryView value={detail.summary} c={c} />
        <Coverage value={detail.coverage} c={c} />
        <p>{c.configHelp}</p>
        <details>
          <summary>{c.minutes}</summary>
          <div className="ops-table-scroll">
            <table className="ops-table">
              <thead>
                <tr>
                  {[c.minute, c.kind, c.rpmUsed, c.average, c.peak, c.cap, c.coverage].map((x) => (
                    <th key={x}>{x}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {detail.minutes.map((m) => (
                  <tr key={`${m.epoch}/${m.minute}/${m.call_kind}`}>
                    <td>{stamp(m.minute)}</td>
                    <td>{m.call_kind}</td>
                    <td>
                      {m.rpm_committed} ({m.rpm_pending} pending)
                    </td>
                    <td>{(m.occupancy_millis / 60000).toFixed(3)}</td>
                    <td>{m.peak}</td>
                    <td>
                      {m.rpm_limit || c.unknown} / {m.concurrency_limit || c.unknown}
                    </td>
                    <td>{m.coverage === 0 ? c.completed : `${c.partial} (${m.coverage})`}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </details>
      </Card>
      <Card>
        <h2>
          {c.comparison}: {detail.comparison.model || c.unknown}
        </h2>
        <p>{c.comparisonHelp}</p>
        <h3>{c.thisUser}</h3>
        <Samples value={detail.comparison.user} c={c} />
        <h3>{c.others}</h3>
        <Samples value={detail.comparison.others} c={c} />
        <Coverage value={detail.comparison.coverage} c={c} />
        {detail.comparison.has_more ? <p>{c.partial}</p> : null}
      </Card>
      <Card>
        <h2>{c.distribution}</h2>
        {detail.source_distribution.map((s, i) => (
          <p key={i} className="ops-wrap">
            {s.user_agent || c.unknown} · {sourceQualityLabel(s.ip_quality, t)} · {s.count}
          </p>
        ))}
      </Card>
    </>
  );
}
function UserModelFilter({
  selectedModel,
  c,
  onApply,
}: {
  selectedModel: string;
  c: RiskCopy;
  onApply: (model: string) => void;
}) {
  const [model, setModel] = useState(selectedModel);
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        onApply(model);
      }}
    >
      <label>
        {c.model}
        <input maxLength={512} value={model} onChange={(event) => setModel(event.target.value)} />
      </label>
      <button className="btn btn-secondary">{c.apply}</button>
    </form>
  );
}
function UserDetail({ userID, back, ...scope }: Scope & { userID: string; back: () => void }) {
  const { role, scopeKey, c } = scope;
  const [params, setParams] = useSearchParams();
  const rawPage = params.get('audit_user_page');
  const page = isPageNumber(rawPage) ? rawPage : '1';
  const rawSize = Number(params.get('audit_user_size'));
  const size = isPageSize(rawSize) && rawSize !== 10 ? rawSize : 20;
  const selectedModel = params.get('audit_user_model') ?? '';
  const from = Number(params.get('audit_user_from'));
  const to = Number(params.get('audit_user_to'));
  const watermark = params.get('audit_user_watermark') ?? undefined;
  const expectedTotal = params.get('audit_user_total') ?? undefined;
  const query = useQuery({
    queryKey: [
      'risk',
      role,
      scopeKey,
      'user',
      userID,
      from,
      to,
      selectedModel,
      page,
      size,
      watermark,
      expectedTotal,
    ],
    enabled: from > 0 && to > from,
    queryFn: ({ signal }) =>
      riskAPI(role).user(
        userID,
        {
          from,
          to,
          kind: scope.filters.kind,
          model: selectedModel,
          page,
          page_size: size,
          watermark,
          expected_total: expectedTotal,
        },
        signal,
      ),
    ...queryOptions,
  });
  useEffect(() => {
    const requests = query.data?.requests;
    if (!requests?.watermark || watermark !== undefined) return;
    setParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        if (!next.has('audit_user_watermark')) {
          next.set('audit_user_watermark', requests.watermark!);
          next.set('audit_user_total', requests.total_items ?? '0');
        }
        return next;
      },
      { replace: true },
    );
  }, [query.data, watermark, setParams]);
  return (
    <div className="ops-stack">
      <button className="btn btn-secondary" onClick={back}>
        {c.back}
      </button>
      <h2>
        {c.user}: {userID}
      </h2>
      <UserModelFilter
        key={`${userID}:${selectedModel}`}
        selectedModel={selectedModel}
        c={c}
        onApply={(model) => {
          setParams((previous) => {
            const next = new URLSearchParams(previous);
            if (model) next.set('audit_user_model', model);
            else next.delete('audit_user_model');
            next.set('audit_user_page', '1');
            next.delete('audit_user_watermark');
            next.delete('audit_user_total');
            return next;
          });
        }}
      />
      {query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : query.data ? (
        <>
          <DetailBody detail={query.data} c={c} />
          {query.data.requests.items.map((item) => (
            <RequestView key={item.log_id} item={item} c={c} role={role} />
          ))}
          <Coverage value={query.data.requests.coverage} c={c} />
          {query.data.requests.page &&
            query.data.requests.page_size &&
            query.data.requests.total_items &&
            query.data.requests.total_pages && (
              <PagePagination
                metadata={{
                  page: query.data.requests.page,
                  page_size: query.data.requests.page_size,
                  total_items: query.data.requests.total_items,
                  total_pages: query.data.requests.total_pages,
                }}
                requestedPage={page}
                pageSizes={[20, 50, 100]}
                busy={query.isFetching}
                onPageChange={(next) =>
                  setParams((previous) => {
                    const p = new URLSearchParams(previous);
                    p.set('audit_user_page', next);
                    return p;
                  })
                }
                onPageSizeChange={(next) =>
                  setParams((previous) => {
                    const p = new URLSearchParams(previous);
                    p.set('audit_user_size', String(next));
                    p.set('audit_user_page', '1');
                    return p;
                  })
                }
              />
            )}
          {query.data.requests.changed && (
            <p role="status">
              {c.partial}: {c.sourcePage}
            </p>
          )}
        </>
      ) : (
        <LoadingState />
      )}
    </div>
  );
}
function Users({ inspect, ...scope }: Scope & { inspect: (id: string) => void }) {
  const { c, filters } = scope;
  const [params, setParams] = useSearchParams();
  const rawSignal = params.get('audit_signal') ?? '';
  const signalFilter = rawSignal === 'rpm' || rawSignal === 'concurrency' ? rawSignal : '';
  return (
    <div className="ops-stack">
      <Card>
        <p>{c.configHelp}</p>
        <label>
          {c.signal}
          <select
            value={signalFilter}
            onChange={(e) =>
              setParams((previous) => {
                const next = new URLSearchParams(previous);
                if (e.target.value) next.set('audit_signal', e.target.value);
                else next.delete('audit_signal');
                next.delete('audit_users_scan');
                next.set('audit_users_page', '1');
                return next;
              })
            }
          >
            <option value="">{c.all}</option>
            <option value="rpm">{c.rpm}</option>
            <option value="concurrency">{c.concurrency}</option>
          </select>
        </label>
      </Card>
      <TaskScans<Summary>
        role={scope.role}
        scopeKey={scope.scopeKey}
        filters={filters}
        kind="users"
        signal={signalFilter}
        renderItem={(value) => (
          <Card key={value.user_id}>
            <div className="ops-actions">
              <strong>
                {c.user}: {value.user_id}
              </strong>
              <button className="btn btn-secondary" onClick={() => inspect(value.user_id)}>
                {c.inspect}
              </button>
            </div>
            <SummaryView value={value} c={c} />
          </Card>
        )}
      />
    </div>
  );
}
function IPs({ inspect, ...scope }: Scope & { inspect: (id: string) => void }) {
  const { t } = useTranslation();
  const stamp = useStamp();
  const { c, filters } = scope;
  return (
    <div className="ops-stack">
      <p>{c.ipHelp}</p>
      <TaskScans<SharedIP>
        role={scope.role}
        scopeKey={scope.scopeKey}
        filters={filters}
        kind="shared_ips"
        renderItem={(ip) => (
          <Card key={ip.ip}>
            <h2>{ip.ip}</h2>
            <p>
              {t('common.audit.distinctDiscord')}: {ip.users} · {c.count}: {ip.requests} ·{' '}
              {stamp(ip.first_seen)} — {stamp(ip.last_seen)}
            </p>
            <h3>{c.related}</h3>
            {ip.associations.map((a) => (
              <p key={a.user_id + a.call_kind}>
                <button className="btn btn-secondary" onClick={() => inspect(a.user_id)}>
                  {a.user_id}
                </button>{' '}
                · {a.call_kind} · {c.count}: {a.requests} · {c.dispatched}: {a.dispatched} ·{' '}
                {c.rejected}: {a.rejected}
                <br />
                {stamp(a.first_seen)} — {stamp(a.last_seen)}
              </p>
            ))}
            {ip.associations_truncated ? <p>{c.partialAssociations}</p> : null}
          </Card>
        )}
      />
    </div>
  );
}
function UserIPWindows(scope: Scope) {
  const { t } = useTranslation();
  const stamp = useStamp();
  return (
    <div className="ops-stack">
      <p>{t('common.audit.userIPsHelp')}</p>
      <TaskScans<UserIPs>
        role={scope.role}
        scopeKey={scope.scopeKey}
        filters={scope.filters}
        kind="user_ips"
        renderItem={(item) => (
          <Card key={item.discord_id}>
            <h2>
              {t('common.history.discord')}: {item.discord_id}
            </h2>
            <p>
              {t('common.audit.ipPeak', { count: item.peak })} · {stamp(item.window_from)} —{' '}
              {stamp(item.window_to)}
            </p>
            <div className="ops-table-scroll">
              <table className="ops-table">
                <thead>
                  <tr>
                    <th>{t('common.history.originalAccount')}</th>
                    <th>{scope.c.kind}</th>
                    <th>{scope.c.count}</th>
                    <th>{scope.c.dispatched}</th>
                    <th>{scope.c.rejected}</th>
                    <th>
                      {scope.c.from} / {scope.c.to}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {item.accounts.map((account) => (
                    <tr key={`${account.user_id}:${account.call_kind}`}>
                      <td>{account.user_id}</td>
                      <td>
                        {(
                          {
                            total: scope.c.total,
                            self: scope.c.self,
                            charity: scope.c.charity,
                            unclassified: scope.c.unclassified,
                          } as Record<string, string>
                        )[account.call_kind] ?? account.call_kind}
                      </td>
                      <td>{account.requests}</td>
                      <td>{account.dispatched}</td>
                      <td>{account.rejected}</td>
                      <td>
                        {stamp(account.first_seen)} — {stamp(account.last_seen)}
                        <br />
                        <Link
                          to={queryPath(scope.role === 'admin' ? '/logs' : '/steward', {
                            tab: scope.role === 'steward' ? 'logs' : undefined,
                            user_id: account.user_id,
                            from: item.window_from,
                            to: item.window_to + 1,
                          })}
                        >
                          {t('common.audit.openLogs')}
                        </Link>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <details>
              <summary>{t('common.audit.windowIPs')}</summary>
              <ul>
                {item.ips.map((ip) => (
                  <li key={ip} className="mono">
                    {ip}
                  </li>
                ))}
              </ul>
            </details>
            {item.ips_truncated || item.accounts_truncated ? (
              <p>{t('common.audit.windowTruncated')}</p>
            ) : null}
          </Card>
        )}
      />
    </div>
  );
}
function Clients({ inspect, ...scope }: Scope & { inspect: (id: string) => void }) {
  return (
    <ClientScans
      role={scope.role}
      scopeKey={scope.scopeKey}
      filters={scope.filters}
      renderItem={(item) => (
        <RequestView
          key={item.log_id}
          item={item}
          c={scope.c}
          role={scope.role}
          inspect={inspect}
        />
      )}
    />
  );
}
function emptyRule(): RuleInput {
  return {
    name: '',
    status: 'suspected',
    enabled: true,
    revision: 0,
    conditions: [{ field: 'user_agent', operator: 'prefix', value: '', case_sensitive: false }],
    evidence_note: '',
    evidence_url: '',
  };
}
function RuleEditor({
  rule,
  role,
  save,
  cancel,
  c,
  busy,
}: {
  rule?: Rule;
  role: RiskRole;
  save: (v: RuleInput) => void;
  cancel: () => void;
  c: RiskCopy;
  busy: boolean;
}) {
  const { t } = useTranslation();
  const [confirmation, setConfirmation] = useState<RuleInput | null>(null);
  const [value, setValue] = useState<RuleInput>(() => {
    const initial = rule ?? emptyRule();
    return {
      ...initial,
      conditions: initial.conditions.map((item) =>
        item.operator === 'ip_in' ? { ...item, value: (item.values ?? []).join('\n') } : item,
      ),
    };
  });
  const originalSeconds = rule?.auto_ban?.duration_seconds;
  const initialUnit =
    originalSeconds == null
      ? 'permanent'
      : originalSeconds % 86400 === 0
        ? 'days'
        : originalSeconds % 3600 === 0
          ? 'hours'
          : 'seconds';
  const [banMode, setBanMode] = useState<'none' | 'permanent' | 'hours' | 'days' | 'seconds'>(
    rule?.auto_ban ? initialUnit : 'none',
  );
  const [banEnabled, setBanEnabled] = useState(rule?.auto_ban?.enabled ?? false);
  const [durationInput, setDurationInput] = useState(
    originalSeconds == null
      ? '1'
      : String(
          originalSeconds / (initialUnit === 'days' ? 86400 : initialUnit === 'hours' ? 3600 : 1),
        ),
  );
  const multiplier = banMode === 'days' ? 86400 : banMode === 'hours' ? 3600 : 1;
  const duration = Number(durationInput) * multiplier;
  const validDuration =
    banMode === 'none' ||
    banMode === 'permanent' ||
    (/^[1-9][0-9]*$/.test(durationInput) &&
      Number.isSafeInteger(duration) &&
      duration <= 315360000);
  const condition = (index: number, update: Partial<Condition>) =>
    setValue((v) => ({
      ...v,
      conditions: v.conditions.map((item, i) => (i === index ? { ...item, ...update } : item)),
    }));
  return (
    <form
      className="ops-stack"
      onSubmit={(e) => {
        e.preventDefault();
        if (!validDuration) return;
        const auto_ban =
          role !== 'admin' || (banMode === 'none' && !rule?.auto_ban)
            ? undefined
            : banMode === 'none'
              ? null
              : {
                  enabled: banEnabled,
                  duration_seconds: banMode === 'permanent' ? null : duration,
                };
        const input = {
          ...value,
          conditions: value.conditions.map((item) =>
            item.operator === 'ip_in'
              ? { ...item, value: '', values: item.value.split(/[\s,，]+/u).filter(Boolean) }
              : item,
          ),
          auto_ban,
        };
        if (input.enabled && auto_ban?.enabled) setConfirmation(input);
        else save(input);
      }}
    >
      {!rule && (
        <div className="audit-patterns">
          <strong>{c.ruleTemplate}</strong>
          <div className="ops-actions">
            {(
              [
                ['Tavo', 'user_agent', 'prefix', 'Tavo/'],
                ['New API', 'openrouter_title', 'equals', 'New API'],
                ['One API', 'legacy_title', 'equals', 'One API'],
              ] as const
            ).map(([name, field, operator, match]) => (
              <button
                key={name}
                className="btn btn-secondary"
                type="button"
                disabled={busy}
                onClick={() =>
                  setValue({
                    ...emptyRule(),
                    name,
                    conditions: [{ field, operator, value: match, case_sensitive: false }],
                  })
                }
              >
                {name}
              </button>
            ))}
            <button
              className="btn btn-secondary"
              type="button"
              onClick={() => setValue((v) => ({ ...v, conditions: emptyRule().conditions }))}
            >
              {c.uaTemplate}
            </button>
            <button
              className="btn btn-secondary"
              type="button"
              onClick={() =>
                setValue((v) => ({
                  ...v,
                  conditions: [
                    { field: 'http_referer', operator: 'equals', value: '', case_sensitive: false },
                    {
                      field: 'openrouter_title',
                      operator: 'equals',
                      value: '',
                      case_sensitive: false,
                    },
                  ],
                }))
              }
            >
              {c.siteTemplate}
            </button>
          </div>
          <small>{c.templateHelp}</small>
        </div>
      )}
      <label>
        {c.name}
        <input
          required
          maxLength={120}
          value={value.name}
          onChange={(e) => setValue((v) => ({ ...v, name: e.target.value }))}
        />
      </label>
      <label>
        <input
          type="checkbox"
          checked={value.enabled}
          onChange={(e) => setValue((v) => ({ ...v, enabled: e.target.checked }))}
        />
        {c.enabled}
      </label>
      {role === 'admin' && (
        <fieldset className="audit-condition">
          <legend>{c.autoBan}</legend>
          <label>
            {c.autoBanDuration}
            <select value={banMode} onChange={(e) => setBanMode(e.target.value as typeof banMode)}>
              <option value="none">{c.noAutoBan}</option>
              <option value="hours">{c.autoBanHours}</option>
              <option value="days">{c.autoBanDays}</option>
              <option value="seconds">{c.autoBanSeconds}</option>
              <option value="permanent">{c.autoBanPermanent}</option>
            </select>
          </label>
          {banMode !== 'none' && (
            <label>
              <input
                type="checkbox"
                checked={banEnabled}
                onChange={(e) => setBanEnabled(e.target.checked)}
              />
              {c.autoBanEnabled}
            </label>
          )}
          {banMode !== 'none' && banMode !== 'permanent' && (
            <label>
              {c.autoBanDuration} (
              {
                c[
                  banMode === 'days'
                    ? 'autoBanDays'
                    : banMode === 'hours'
                      ? 'autoBanHours'
                      : 'autoBanSeconds'
                ]
              }
              )
              <input
                type="number"
                min="1"
                max={Math.floor(315360000 / multiplier)}
                step="1"
                required
                value={durationInput}
                onChange={(e) => setDurationInput(e.target.value)}
              />
            </label>
          )}
          {!validDuration && <p role="alert">{c.autoBanInvalid}</p>}
          <small>{c.autoBanHelp}</small>
        </fieldset>
      )}
      {value.conditions.map((item, i) => (
        <fieldset className="ops-field-grid audit-condition" key={i}>
          <legend>
            {c.and} {i + 1}
          </legend>
          <label>
            {c.field}
            <select
              value={item.field}
              onChange={(e) => {
                const field = e.target.value as Condition['field'];
                condition(i, {
                  field,
                  ...(field === 'effective_ip' || item.operator === 'ip_in'
                    ? {
                        operator: field === 'effective_ip' ? 'ip_in' : 'equals',
                        value: '',
                        values: undefined,
                        case_sensitive: false,
                      }
                    : {}),
                });
              }}
            >
              {sourceFields.map((f) => (
                <option key={f} value={f}>
                  {fieldLabel(f, c)}
                </option>
              ))}
            </select>
          </label>
          <label>
            {c.operator}
            <select
              value={item.operator}
              onChange={(e) =>
                condition(i, {
                  operator: e.target.value as Condition['operator'],
                  values: undefined,
                  ...(e.target.value === 'ip_in' ? { case_sensitive: false } : {}),
                })
              }
            >
              {(['equals', 'contains', 'prefix'] as const).map((o) => (
                <option key={o} value={o}>
                  {c[o]}
                </option>
              ))}
              {item.field === 'effective_ip' && <option value="ip_in">{c.ip_in}</option>}
            </select>
          </label>
          {item.operator === 'ip_in' ? (
            <label className="audit-ip-values">
              {c.ipValues}
              <textarea
                required
                rows={4}
                aria-label={c.ipValues}
                aria-describedby={`rule-ip-help-${i}`}
                value={item.value}
                onChange={(e) => condition(i, { value: e.target.value })}
              />
              <small id={`rule-ip-help-${i}`}>{c.ipValuesHelp}</small>
            </label>
          ) : (
            <>
              <label>
                {c.value}
                <input
                  required
                  maxLength={256}
                  value={item.value}
                  onChange={(e) => condition(i, { value: e.target.value })}
                />
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={item.case_sensitive}
                  onChange={(e) => condition(i, { case_sensitive: e.target.checked })}
                />
                {c.sensitive}
              </label>
            </>
          )}
          <button
            className="btn btn-secondary"
            type="button"
            disabled={value.conditions.length < 2}
            onClick={() =>
              setValue((v) => ({ ...v, conditions: v.conditions.filter((_, n) => n !== i) }))
            }
          >
            {c.remove}
          </button>
        </fieldset>
      ))}
      <button
        className="btn btn-secondary"
        type="button"
        disabled={value.conditions.length >= 8}
        onClick={() =>
          setValue((v) => ({ ...v, conditions: [...v.conditions, emptyRule().conditions[0]] }))
        }
      >
        {c.add}
      </button>
      <details className="audit-details">
        <summary>{c.advanced}</summary>
        <label>
          {c.status}
          <select
            value={value.status}
            onChange={(e) =>
              setValue((v) => ({ ...v, status: e.target.value as RuleInput['status'] }))
            }
          >
            <option value="suspected">{c.suspected}</option>
            <option value="confirmed">{c.confirmed}</option>
          </select>
        </label>
        <label>
          {c.evidence}
          <textarea
            maxLength={4096}
            value={value.evidence_note}
            onChange={(e) => setValue((v) => ({ ...v, evidence_note: e.target.value }))}
          />
        </label>
        <label>
          {c.link}
          <input
            type="url"
            maxLength={2048}
            value={value.evidence_url}
            onChange={(e) => setValue((v) => ({ ...v, evidence_url: e.target.value }))}
          />
        </label>
      </details>
      <div className="ops-actions">
        <button className="btn btn-primary" disabled={busy || !validDuration}>
          {c.save}
        </button>
        <button className="btn btn-secondary" type="button" disabled={busy} onClick={cancel}>
          {c.cancel}
        </button>
      </div>
      <ConfirmDialog
        open={confirmation !== null}
        title={t('common.riskAudit.confirmAutoBan')}
        description={
          <>
            <p>{confirmation?.name}</p>
            <p>{c.autoBanHelp}</p>
            <p>
              {confirmation?.auto_ban?.duration_seconds === null
                ? c.autoBanPermanent
                : `${durationInput} ${banMode === 'days' ? c.autoBanDays : banMode === 'hours' ? c.autoBanHours : c.autoBanSeconds}`}
            </p>
            {confirmation?.evidence_note ? <p>{confirmation.evidence_note}</p> : null}
          </>
        }
        confirmLabel={c.save}
        danger
        busy={busy}
        onCancel={() => setConfirmation(null)}
        onConfirm={() => {
          if (confirmation) {
            save(confirmation);
            setConfirmation(null);
          }
        }}
      />
    </form>
  );
}
function Rules({ role, scopeKey, c }: Scope) {
  const stamp = useStamp();
  const [params, setParams] = useSearchParams();
  const client = useQueryClient();
  const rawPage = params.get('audit_rules_page');
  const page = isPageNumber(rawPage) ? rawPage : '1';
  const rawSize = Number(params.get('audit_rules_size'));
  const size = isPageSize(rawSize) && rawSize !== 10 ? rawSize : 20;
  const revision = params.get('audit_rules_revision') ?? undefined;
  const [editing, setEditing] = useState<Rule | 'new' | null>(null);
  const query = useQuery({
    queryKey: ['risk', role, scopeKey, 'rules', page, size, revision],
    queryFn: ({ signal }) => riskAPI(role).numberedRules(page, size, revision, signal),
    ...queryOptions,
  });
  useEffect(() => {
    const data = query.data;
    if (!data || (revision === data.revision && !data.changed)) return;
    setParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        next.set('audit_rules_revision', data.revision);
        if (data.changed) next.set('audit_rules_page', '1');
        return next;
      },
      { replace: true },
    );
  }, [query.data, revision, setParams]);
  const mutation = useRetainedOperation<{ input?: RuleInput; rule?: Rule }, unknown>(
    async (v, _key, context) => {
      const result = v.input
        ? await riskAPI(role).saveRule(v.input, v.rule?.id, context.signal)
        : await riskAPI(role).deleteRule(v.rule!.id, v.rule!.revision, context.signal);
      context.commit(() => {
        setEditing(null);
        setParams((previous) => {
          const next = new URLSearchParams(previous);
          next.delete('audit_rules_revision');
          next.set('audit_rules_page', '1');
          return next;
        });
      });
      return result;
    },
    () => client.invalidateQueries({ queryKey: ['risk', role, scopeKey] }, { throwOnError: true }),
    role === 'admin' ? ['admin', 'risk-audit'] : ['user', 'steward', 'risk-audit'],
  );
  return (
    <Card>
      <p>{c.ruleSteps}</p>
      <p className="audit-muted">{c.ruleHelp}</p>
      {mutation.error ? (
        <ErrorState
          error={mutation.error}
          onRetry={() => {
            mutation.reset();
            void query.refetch();
          }}
        />
      ) : null}
      {mutation.refreshError ? (
        <ErrorState error={mutation.refreshError} onRetry={() => void mutation.refresh()} />
      ) : null}
      {editing ? (
        <RuleEditor
          key={editing === 'new' ? 'new' : editing.id}
          rule={editing === 'new' ? undefined : editing}
          role={role}
          c={c}
          busy={mutation.isPending}
          cancel={() => {
            mutation.reset();
            setEditing(null);
          }}
          save={(input) =>
            mutation.mutate({ input, rule: editing === 'new' ? undefined : editing })
          }
        />
      ) : (
        <>
          <button
            className="btn btn-secondary"
            onClick={() => {
              mutation.reset();
              setEditing('new');
            }}
          >
            {c.create}
          </button>
          {query.error ? (
            <ErrorState error={query.error} onRetry={() => void query.refetch()} />
          ) : query.data ? (
            <>
              <p>
                {c.count}: {query.data.total_items}
              </p>
              {query.data.items.map((rule) => (
                <Card key={rule.id}>
                  <h3>{rule.name}</h3>
                  <p>
                    {rule.status === 'confirmed' ? c.confirmed : c.suspected} · {c.revision}{' '}
                    {rule.revision} · {rule.enabled ? c.enabled : c.no}
                  </p>
                  {rule.auto_ban && (
                    <p>
                      {rule.auto_ban.enabled ? c.autoBanEnabled : c.autoBanDisabled} ·{' '}
                      {rule.auto_ban.duration_seconds === null
                        ? c.autoBanPermanent
                        : `${rule.auto_ban.duration_seconds} ${c.autoBanSeconds}`}
                    </p>
                  )}
                  {role !== 'admin' && rule.auto_ban && (
                    <p className="audit-muted">{c.autoBanProtected}</p>
                  )}
                  <p>
                    {rule.conditions
                      .map(
                        (v) =>
                          `${fieldLabel(v.field, c)} ${c[v.operator]} ${v.operator === 'ip_in' ? (v.values ?? []).join(', ') : v.value}`,
                      )
                      .join(' · ')}
                  </p>
                  <p>{rule.evidence_note}</p>
                  <p>{rule.evidence_url}</p>
                  <p>
                    {stamp(rule.updated_at)} · {rule.updated_by_role}{' '}
                    {rule.updated_by_user_id ?? ''}
                  </p>
                  <button
                    className="btn btn-secondary"
                    disabled={mutation.isPending || (role !== 'admin' && rule.auto_ban != null)}
                    onClick={() => {
                      mutation.reset();
                      setEditing(rule);
                    }}
                  >
                    {c.edit}
                  </button>
                  <button
                    className="btn btn-secondary"
                    disabled={mutation.isPending || (role !== 'admin' && rule.auto_ban != null)}
                    onClick={() => mutation.mutate({ rule })}
                  >
                    {c.remove}
                  </button>
                </Card>
              ))}
              {query.data.changed && (
                <p role="status">
                  {c.partial}: {c.ruleHelp}
                </p>
              )}
              <PagePagination
                metadata={query.data}
                requestedPage={page}
                pageSizes={[20, 50, 100]}
                busy={query.isFetching}
                onPageChange={(next) =>
                  setParams((previous) => {
                    const p = new URLSearchParams(previous);
                    p.set('audit_rules_page', next);
                    return p;
                  })
                }
                onPageSizeChange={(next) =>
                  setParams((previous) => {
                    const p = new URLSearchParams(previous);
                    p.set('audit_rules_size', String(next));
                    p.set('audit_rules_page', '1');
                    return p;
                  })
                }
              />
            </>
          ) : (
            <LoadingState />
          )}
        </>
      )}
    </Card>
  );
}
function ConfigForm({
  value,
  c,
  readonly,
  save,
  busy,
}: {
  value: Config;
  c: RiskCopy;
  readonly: boolean;
  save: (value: Config) => void;
  busy: boolean;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState({
    ...value,
    user_ip_window_hours: value.user_ip_window_hours ?? 24,
    user_ip_min_ips: value.user_ip_min_ips ?? 3,
  });
  const fields = [
    ['threshold_percent', c.threshold, 1, 100],
    ['consecutive_minutes', c.consecutive, 1, 60],
    ['shared_ip_hours', c.hours, 1, 720],
    ['shared_ip_users', c.accounts, 2, 1000],
    ['user_ip_window_hours', t('common.audit.userIPWindow'), 1, 720],
    ['user_ip_min_ips', t('common.audit.userIPMinimum'), 2, 1000],
  ] as const;
  return (
    <form
      className="ops-stack"
      onSubmit={(e) => {
        e.preventDefault();
        save(draft);
      }}
    >
      <div className="ops-field-grid">
        {fields.map(([field, label, min, max]) => (
          <label key={field}>
            {label}
            <input
              type="number"
              required
              min={min}
              max={max}
              step={1}
              disabled={readonly || busy}
              value={draft[field]}
              onChange={(e) => setDraft((v) => ({ ...v, [field]: Number(e.target.value) }))}
            />
          </label>
        ))}
      </div>
      <details>
        <summary>{t('common.operations.management.details')}</summary>
        <p>
          {c.policyRevision}: {value.revision}
        </p>
      </details>
      {readonly ? (
        <p>{c.adminOnly}</p>
      ) : (
        <button className="btn btn-primary" disabled={busy}>
          {c.save}
        </button>
      )}
    </form>
  );
}
function Configuration({ role, scopeKey, c }: Scope) {
  const client = useQueryClient(),
    query = useQuery({
      queryKey: ['risk', role, scopeKey, 'config'],
      queryFn: ({ signal }) => riskAPI(role).config(signal),
      ...queryOptions,
    });
  const mutation = useRetainedOperation<Config, Config>(
    (value, _key, context) => riskAPI(role).saveConfig(value, context.signal),
    () => client.invalidateQueries({ queryKey: ['risk', role, scopeKey] }, { throwOnError: true }),
    role === 'admin' ? ['admin', 'risk-audit'] : ['user', 'steward', 'risk-audit'],
  );
  return (
    <Card>
      <p>{c.configHelp}</p>
      {mutation.error ? (
        <ErrorState
          error={mutation.error}
          onRetry={() => {
            mutation.reset();
            void query.refetch();
          }}
        />
      ) : null}
      {query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : query.data ? (
        <ConfigForm
          key={query.data.revision}
          value={query.data}
          c={c}
          readonly={role !== 'admin'}
          save={(v) => mutation.mutate(v)}
          busy={mutation.isPending}
        />
      ) : (
        <LoadingState />
      )}
      {mutation.refreshError ? (
        <ErrorState error={mutation.refreshError} onRetry={() => void mutation.refresh()} />
      ) : null}
    </Card>
  );
}
function Access({ role, scopeKey, c, filters }: Scope) {
  const stamp = useStamp();
  const [params, setParams] = useSearchParams();
  const rawPage = params.get('audit_access_page');
  const page = isPageNumber(rawPage) ? rawPage : '1';
  const rawSize = Number(params.get('audit_access_size'));
  const size = isPageSize(rawSize) && rawSize !== 10 ? rawSize : 20;
  const selected = useMemo(
    () => ({
      user_id: params.get('audit_access_user') ?? '',
      key_generation: params.get('audit_access_key') ?? '',
      path_kind: params.get('audit_access_path') ?? '',
      status_class: params.get('audit_access_status') ?? '',
    }),
    [params],
  );
  const selectedKey = JSON.stringify(selected);
  const [draftState, setDraftState] = useState({ key: selectedKey, value: selected });
  const draft = draftState.key === selectedKey ? draftState.value : selected;
  const setDraft = (update: (value: typeof selected) => typeof selected) =>
    setDraftState((previous) => ({
      key: selectedKey,
      value: update(previous.key === selectedKey ? previous.value : selected),
    }));
  const fixedFrom = params.get('audit_access_from');
  const fixedTo = params.get('audit_access_to');
  const f =
    fixedFrom && fixedTo
      ? { from: fixedFrom, to: fixedTo, ...selected }
      : {
          from: filters.from,
          to: filters.to,
          lookback_hours: filters.lookback_hours,
          ...selected,
        };
  const watermark = params.get('audit_access_watermark');
  const expectedTotal = params.get('audit_access_total');
  const events = useQuery({
    queryKey: ['risk', role, scopeKey, 'access', f, page, size, watermark, expectedTotal],
    queryFn: ({ signal }) =>
      riskAPI(role).numberedAccess(
        { ...f, watermark: watermark ?? undefined, expected_total: expectedTotal ?? undefined },
        page,
        size,
        signal,
      ),
    ...queryOptions,
  });
  useEffect(() => {
    if (!events.data || (fixedFrom && fixedTo && watermark !== null)) return;
    setParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        if (!next.has('audit_access_from'))
          next.set('audit_access_from', String(events.data!.from));
        if (!next.has('audit_access_to')) next.set('audit_access_to', String(events.data!.to));
        if (!next.has('audit_access_watermark'))
          next.set('audit_access_watermark', events.data!.watermark);
        if (!next.has('audit_access_total'))
          next.set('audit_access_total', events.data!.total_items);
        return next;
      },
      { replace: true },
    );
  }, [events.data, fixedFrom, fixedTo, watermark, setParams]);
  const summary = useQuery({
    queryKey: ['risk', role, scopeKey, 'access-summary', f],
    queryFn: ({ signal }) => riskAPI(role).accessSummary(f, signal),
    ...queryOptions,
  });
  return (
    <div className="ops-stack">
      <p>{c.accessHelp}</p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setParams((previous) => {
            const next = new URLSearchParams(previous);
            for (const [key, value] of Object.entries(draft)) {
              const queryKey = {
                user_id: 'audit_access_user',
                key_generation: 'audit_access_key',
                path_kind: 'audit_access_path',
                status_class: 'audit_access_status',
              }[key as keyof typeof draft];
              if (value) next.set(queryKey, value);
              else next.delete(queryKey);
            }
            for (const key of ['from', 'to', 'watermark', 'total'])
              next.delete('audit_access_' + key);
            next.set('audit_access_page', '1');
            return next;
          });
        }}
      >
        <div className="ops-field-grid">
          <label>
            {c.user}
            <input
              pattern="[1-9][0-9]*"
              value={draft.user_id}
              onChange={(e) =>
                setDraft((v) => ({ ...v, user_id: e.target.value, key_generation: '' }))
              }
            />
          </label>
          <label>
            {c.key}
            <input
              maxLength={32}
              pattern="[0-9]+"
              disabled={!draft.user_id}
              value={draft.key_generation}
              onChange={(e) => setDraft((v) => ({ ...v, key_generation: e.target.value }))}
            />
          </label>
          <label>
            {c.path}
            <select
              value={draft.path_kind}
              onChange={(e) => setDraft((v) => ({ ...v, path_kind: e.target.value }))}
            >
              <option value="">{c.all}</option>
              {accessPaths.map((v) => (
                <option key={v} value={v}>
                  {pathLabel(v)}
                </option>
              ))}
            </select>
          </label>
          <label>
            {c.http}
            <select
              value={draft.status_class}
              onChange={(e) => setDraft((v) => ({ ...v, status_class: e.target.value }))}
            >
              <option value="">{c.all}</option>
              {[1, 2, 3, 4, 5].map((v) => (
                <option key={v} value={v}>
                  {v}xx
                </option>
              ))}
            </select>
          </label>
        </div>
        <button className="btn btn-secondary">{c.apply}</button>
      </form>
      {summary.error ? (
        <ErrorState error={summary.error} onRetry={() => void summary.refetch()} />
      ) : summary.data ? (
        <Card>
          <dl className="ops-kv">
            {(
              [
                [c.authenticated, summary.data.authenticated_events],
                [c.anonymous, summary.data.anonymous_events],
                [c.models, summary.data.model_list_events],
                [c.generations, summary.data.generation_requests],
                [c.ratio, summary.data.model_generation_ratio ?? c.unknown],
                [c.capture, stamp(summary.data.coverage.capture_started_at)],
                [c.dropped, summary.data.coverage.dropped],
                [
                  c.gap,
                  summary.data.coverage.last_gap_at === null
                    ? c.noGap
                    : stamp(summary.data.coverage.last_gap_at),
                ],
              ] as const
            ).map(([label, value]) => (
              <div key={label}>
                <dt>{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
          {summary.data.paths.map((p) => (
            <p key={p.path_kind + p.response_kind}>
              {p.path_kind} · {p.response_kind} · {c.authenticated}: {p.authenticated} ·{' '}
              {c.anonymous}: {p.anonymous}
            </p>
          ))}
        </Card>
      ) : (
        <LoadingState />
      )}
      {events.error ? (
        <ErrorState error={events.error} onRetry={() => void events.refetch()} />
      ) : events.data ? (
        <>
          {events.data.items.map((e) => (
            <Card key={e.id}>
              <p>
                {stamp(e.occurred_at)} · {c.user}: {e.user_id} · {c.key}:{' '}
                {e.caller_key_generation ?? c.unknown}
              </p>
              <p>
                {e.method} · {e.path_kind} · {e.http_status} · {e.response_kind}
              </p>
              <SourceView source={e.source} c={c} />
            </Card>
          ))}
          {events.data.changed && (
            <p role="status">
              {c.partial}: {c.sourcePage}
            </p>
          )}
          <PagePagination
            metadata={events.data}
            requestedPage={page}
            pageSizes={[20, 50, 100]}
            busy={events.isFetching}
            onPageChange={(next) =>
              setParams((previous) => {
                const p = new URLSearchParams(previous);
                p.set('audit_access_page', next);
                return p;
              })
            }
            onPageSizeChange={(next) =>
              setParams((previous) => {
                const p = new URLSearchParams(previous);
                p.set('audit_access_size', String(next));
                p.set('audit_access_page', '1');
                return p;
              })
            }
          />
        </>
      ) : (
        <LoadingState />
      )}
    </div>
  );
}
type Tab = 'users' | 'ips' | 'user_ips' | 'clients' | 'rules' | 'config' | 'access';
function useAuditAuthority(role: RiskRole, scopeKey: string) {
  const client = useQueryClient(),
    [error, setError] = useState<unknown>(null);
  useEffect(() => {
    let revoked = false;
    const matches = (key: readonly unknown[] | undefined) =>
      key?.[0] === 'risk' && key[1] === role && key[2] === scopeKey;
    const inspect = (value: unknown, key: readonly unknown[] | undefined) => {
      if (revoked || !matches(key) || (!isForbidden(value) && !isUnauthorized(value))) return;
      revoked = true;
      setError(value);
      client.removeQueries({ queryKey: ['risk', role, scopeKey] });
      for (const mutation of client.getMutationCache().getAll())
        if (matches(mutation.options.mutationKey)) client.getMutationCache().remove(mutation);
    };
    const stopQueries = client.getQueryCache().subscribe((event) => {
      if (event.type === 'updated') inspect(event.query.state.error, event.query.queryKey);
    });
    const stopMutations = client.getMutationCache().subscribe((event) => {
      if (event.type === 'updated')
        inspect(event.mutation.state.error, event.mutation.options.mutationKey);
    });
    return () => {
      stopQueries();
      stopMutations();
    };
  }, [client, role, scopeKey]);
  return error;
}
function RiskBody({ role, scopeKey }: { role: RiskRole; scopeKey: string }) {
  const authorityError = useAuditAuthority(role, scopeKey);
  const { t } = useTranslation(),
    c = riskCopy(t);
  const [params, setParams] = useSearchParams();
  const rawTab = params.get('audit_tab') ?? '';
  const tab: Tab = (
    ['users', 'ips', 'user_ips', 'clients', 'rules', 'config', 'access'] as string[]
  ).includes(rawTab)
    ? (rawTab as Tab)
    : 'users';
  const selectedUser = /^[1-9][0-9]{0,18}$/.test(params.get('audit_user') ?? '')
    ? params.get('audit_user')!
    : '';
  const setSelectedUser = useCallback(
    (id: string) =>
      setParams((previous) => {
        const next = new URLSearchParams(previous);
        if (id) {
          next.set('audit_user', id);
          const filter = initialWindow(previous);
          const to = Number(filter.to ?? Math.floor(Date.now() / 1000));
          const from = Number(filter.from ?? to - Number(filter.lookback_hours ?? 24) * 3600);
          next.set('audit_user_from', String(from));
          next.set('audit_user_to', String(to));
        } else next.delete('audit_user');
        next.delete('audit_user_watermark');
        next.delete('audit_user_total');
        next.set('audit_user_page', '1');
        return next;
      }),
    [setParams],
  );
  const client = useQueryClient();
  const [range, setRange] = useState(() =>
    params.has('audit_from') ? 'custom' : (params.get('audit_lookback_hours') ?? 'default'),
  );
  const [rangeError, setRangeError] = useState(false);
  const filters = useMemo(() => initialWindow(params), [params]);
  useEffect(() => {
    if (!selectedUser || params.has('audit_user_to')) return;
    setSelectedUser(selectedUser);
  }, [params, selectedUser, setSelectedUser]);
  const setFilters = (value: Filters) =>
    setParams((previous) => {
      const p = new URLSearchParams(previous);
      for (const key of filterFields) {
        p.delete('audit_' + key);
        if (value[key] !== undefined && value[key] !== '')
          p.set('audit_' + key, String(value[key]));
      }
      p.delete('audit_scan');
      p.delete('audit_page');
      for (const name of ['users', 'ips', 'user_ips']) {
        p.delete(`audit_${name}_scan`);
        p.delete(`audit_${name}_page`);
      }
      p.delete('audit_user_watermark');
      p.delete('audit_user_total');
      p.delete('audit_user_from');
      p.delete('audit_user_to');
      p.delete('audit_access_watermark');
      p.delete('audit_access_total');
      p.delete('audit_access_from');
      p.delete('audit_access_to');
      p.delete('audit_user');
      return p;
    });
  const [draft, setDraft] = useState(() => ({
    from: createTimeDraft(Number(filters.from ?? Math.floor(Date.now() / 1000) - 86400)),
    to: createTimeDraft(Number(filters.to ?? Math.floor(Date.now() / 1000))),
    kind: String(filters.kind ?? 'total'),
  }));
  const filterIdentity = [filters.from, filters.to, filters.lookback_hours, filters.kind].join('/');
  const [draftIdentity, setDraftIdentity] = useState(filterIdentity);
  if (draftIdentity !== filterIdentity) {
    setDraftIdentity(filterIdentity);
    setRange(filters.from !== undefined ? 'custom' : String(filters.lookback_hours ?? 'default'));
    setDraft({
      from: filters.from === undefined ? draft.from : createTimeDraft(Number(filters.from)),
      to: filters.to === undefined ? draft.to : createTimeDraft(Number(filters.to)),
      kind: String(filters.kind ?? 'total'),
    });
    setRangeError(false);
  }
  const customRange = filters.from !== undefined || filters.lookback_hours !== undefined;
  const scope = useMemo(
    () => ({ role, scopeKey, c, filters, customRange }),
    [role, scopeKey, c, filters, customRange],
  );
  function apply(e: FormEvent) {
    e.preventDefault();
    setRangeError(false);
    if (range !== 'custom') {
      setFilters({
        lookback_hours: range === 'default' ? undefined : Number(range),
        kind: draft.kind,
        limit: 100,
      });
      void client.invalidateQueries({ queryKey: ['risk', role, scopeKey] });
      return;
    }
    const from = timeDraftValue(draft.from),
      to = timeDraftValue(draft.to);
    if (
      typeof from === 'number' &&
      typeof to === 'number' &&
      to > from &&
      to - from <= 30 * 86400 &&
      to <= Date.now() / 1000
    ) {
      setFilters({ from, to, kind: draft.kind, limit: 100 });
      void client.invalidateQueries({ queryKey: ['risk', role, scopeKey] });
    } else setRangeError(true);
  }
  if (authorityError) return <ErrorState error={authorityError} />;
  return (
    <div className="page ops-page audit-page">
      <PageHeader title={c.title} description={c.description} />
      <Card>
        <Note>{t('common.audit.presentation.note')}</Note>
        <Fold plain title={t('common.audit.presentation.basis')}>
          <p>{c.caveat}</p>
          <p>{c.autoBanHelp}</p>
        </Fold>
        <Tabs<Tab>
          label={c.title}
          value={tab}
          tabs={(['users', 'ips', 'user_ips', 'clients', 'rules', 'config', 'access'] as Tab[]).map(
            (value) => ({
              value,
              label: value === 'user_ips' ? t('common.audit.userIPsTab') : c[value],
            }),
          )}
          onChange={(v) => {
            setParams((previous) => {
              const next = new URLSearchParams(previous);
              next.set('audit_tab', v);
              next.delete('audit_user');
              next.delete('audit_user_from');
              next.delete('audit_user_to');
              next.delete('audit_user_watermark');
              next.delete('audit_user_total');
              next.set('audit_user_page', '1');
              return next;
            });
          }}
        />
        {tab !== 'rules' && tab !== 'config' ? (
          <Fold
            plain
            title={t('common.operations.logs.presentation.more')}
            summary={
              range === 'default'
                ? c.defaultRange
                : range === 'custom'
                  ? c.custom
                  : range === '1'
                    ? c.lastHour
                    : range === '24'
                      ? c.lastDay
                      : c.lastWeek
            }
          >
            <form className="ops-stack" onSubmit={apply}>
              {range === 'custom' && <TimeContextNotice station={role} />}
              <div className="ops-field-grid">
                <label>
                  {c.range}
                  <select
                    value={range}
                    onChange={(e) => {
                      setRange(e.target.value);
                      setRangeError(false);
                    }}
                  >
                    <option value="default">{c.defaultRange}</option>
                    <option value="1">{c.lastHour}</option>
                    <option value="24">{c.lastDay}</option>
                    <option value="168">{c.lastWeek}</option>
                    <option value="custom">{c.custom}</option>
                  </select>
                </label>
                {range === 'custom' && (
                  <>
                    <label>
                      {c.from}
                      <TimeInput
                        station={role}
                        showZoneHint={false}
                        required
                        draft={draft.from}
                        onChange={(update) => setDraft((v) => ({ ...v, from: update(v.from) }))}
                      />
                    </label>
                    <label>
                      {c.to}
                      <TimeInput
                        station={role}
                        showZoneHint={false}
                        required
                        draft={draft.to}
                        onChange={(update) => setDraft((v) => ({ ...v, to: update(v.to) }))}
                      />
                    </label>
                  </>
                )}
                {tab !== 'access' && (
                  <label>
                    {c.kind}
                    <select
                      value={draft.kind}
                      onChange={(e) => setDraft((v) => ({ ...v, kind: e.target.value }))}
                    >
                      {(['total', 'self', 'charity', 'unclassified'] as const).map((v) => (
                        <option key={v} value={v}>
                          {c[v]}
                        </option>
                      ))}
                    </select>
                  </label>
                )}
              </div>
              <div className="ops-actions">
                <button
                  className="btn btn-primary"
                  disabled={
                    range === 'custom' &&
                    (timeDraftValue(draft.from) == null || timeDraftValue(draft.to) == null)
                  }
                >
                  {c.apply}
                </button>
                <button
                  className="btn btn-secondary"
                  type="button"
                  onClick={() =>
                    void client.invalidateQueries({ queryKey: ['risk', role, scopeKey] })
                  }
                >
                  {c.refresh}
                </button>
              </div>
              {rangeError && <p role="alert">{c.invalidRange}</p>}
              <small className="audit-muted">{c.rangeHelp}</small>
            </form>
          </Fold>
        ) : null}
      </Card>
      <div>
        {selectedUser ? (
          <UserDetail
            key={selectedUser}
            {...scope}
            userID={selectedUser}
            back={() => setSelectedUser('')}
          />
        ) : tab === 'users' ? (
          <Users {...scope} inspect={setSelectedUser} />
        ) : tab === 'ips' ? (
          <IPs {...scope} inspect={setSelectedUser} />
        ) : tab === 'user_ips' ? (
          <UserIPWindows {...scope} />
        ) : tab === 'clients' ? (
          <Clients {...scope} inspect={setSelectedUser} />
        ) : tab === 'rules' ? (
          <Rules {...scope} />
        ) : tab === 'config' ? (
          <Configuration {...scope} />
        ) : (
          <Access {...scope} />
        )}
      </div>
    </div>
  );
}
export function RiskAuditPanel({
  role,
  scopeKey,
  enabled = true,
}: {
  role: RiskRole;
  scopeKey: string;
  enabled?: boolean;
}) {
  if (!enabled || !scopeKey) return <LoadingState />;
  return <RiskBody key={role + '/' + scopeKey} role={role} scopeKey={scopeKey} />;
}

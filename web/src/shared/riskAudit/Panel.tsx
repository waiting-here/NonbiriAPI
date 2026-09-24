import { useEffect, useMemo, useState, type FormEvent } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
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
  type Source,
  type Stats,
  type Summary,
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
function stamp(n: number | null) {
  return n !== null && n > 0 ? new Date(n * 1000).toLocaleString() : '—';
}
function localTime(n: number) {
  const d = new Date(n * 1000);
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
}
function initialWindow(): Filters {
  return { kind: 'total', limit: 100 };
}
function usePager() {
  const [cursors, setCursors] = useState(['']);
  return {
    after: cursors.at(-1) ?? '',
    back: () => setCursors((v) => v.slice(0, -1)),
    next: (value: string) => setCursors((v) => [...v, value]),
    reset: () => setCursors(['']),
    hasBack: cursors.length > 1,
  };
}
function Pager({
  c,
  pager,
  next,
  more,
  pending = false,
}: {
  c: RiskCopy;
  pager: ReturnType<typeof usePager>;
  next: string;
  more: boolean;
  pending?: boolean;
}) {
  return (
    <div className="ops-actions">
      <button
        className="btn btn-secondary"
        type="button"
        disabled={!pager.hasBack || pending}
        onClick={pager.back}
      >
        {c.back}
      </button>
      <button
        className="btn btn-secondary"
        type="button"
        disabled={!more || !next || pending}
        onClick={() => pager.next(next)}
      >
        {c.next}
      </button>
    </div>
  );
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
  return (
    <details>
      <summary>{c.source}</summary>
      <dl className="ops-kv">
        <dt>{c.quality}</dt>
        <dd>{source.ip_quality || c.unknown}</dd>
        {sourceFields.map((field) => (
          <div key={field}>
            <dt>{fieldLabel(field, c)}</dt>
            <dd className="ops-wrap">
              {source[field] || c.unknown}
              {source.quality[field] && Object.values(source.quality[field]!).some(Boolean)
                ? ` (${Object.entries(source.quality[field]!)
                    .filter(([, v]) => v)
                    .map(([k]) => k)
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
  inspect,
}: {
  item: Request;
  c: RiskCopy;
  inspect?: (id: string) => void;
}) {
  return (
    <Card>
      <div className="ops-actions">
        <strong>{item.request_id}</strong>
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
            {s.user_agent || c.unknown} · {s.ip_quality} · {s.count}
          </p>
        ))}
      </Card>
    </>
  );
}
function UserDetail({ userID, back, ...scope }: Scope & { userID: string; back: () => void }) {
  const { role, scopeKey, c, filters } = scope,
    pager = usePager();
  const [model, setModel] = useState(''),
    [selectedModel, setSelectedModel] = useState('');
  const query = useQuery({
    queryKey: ['risk', role, scopeKey, 'user', userID, filters, selectedModel, pager.after],
    queryFn: ({ signal }) =>
      riskAPI(role).user(userID, { ...filters, model: selectedModel, after: pager.after }, signal),
    ...queryOptions,
  });
  return (
    <div className="ops-stack">
      <button className="btn btn-secondary" onClick={back}>
        {c.back}
      </button>
      <h2>
        {c.user}: {userID}
      </h2>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setSelectedModel(model);
          pager.reset();
        }}
      >
        <label>
          {c.model}
          <input maxLength={512} value={model} onChange={(e) => setModel(e.target.value)} />
        </label>
        <button className="btn btn-secondary">{c.apply}</button>
      </form>
      {query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : query.data ? (
        <>
          <DetailBody detail={query.data} c={c} />
          {query.data.requests.items.map((item) => (
            <RequestView key={item.log_id} item={item} c={c} />
          ))}
          <Coverage value={query.data.requests.coverage} c={c} />
          <Pager
            c={c}
            pager={pager}
            next={query.data.requests.next}
            more={query.data.requests.has_more}
            pending={query.isFetching}
          />
        </>
      ) : (
        <LoadingState />
      )}
    </div>
  );
}
function Users({ inspect, ...scope }: Scope & { inspect: (id: string) => void }) {
  const { role, scopeKey, c, filters } = scope,
    pager = usePager();
  const [signalFilter, setSignalFilter] = useState('');
  const query = useQuery({
    queryKey: ['risk', role, scopeKey, 'users', filters, signalFilter, pager.after],
    queryFn: ({ signal }) =>
      riskAPI(role).users({ ...filters, signal: signalFilter, after: pager.after }, signal),
    ...queryOptions,
  });
  return (
    <Card>
      <p>{c.configHelp}</p>
      <label>
        {c.signal}
        <select
          value={signalFilter}
          onChange={(e) => {
            setSignalFilter(e.target.value);
            pager.reset();
          }}
        >
          <option value="">{c.all}</option>
          <option value="rpm">{c.rpm}</option>
          <option value="concurrency">{c.concurrency}</option>
        </select>
      </label>
      {query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : query.data ? (
        <>
          <Coverage value={query.data.coverage} c={c} />
          <p>
            {c.sampled}: {query.data.scanned}
          </p>
          {query.data.items.length === 0 ? (
            <EmptyState title={c.empty} body={c.scanHelp} />
          ) : (
            <div className="ops-table-scroll">
              <table className="ops-table">
                <thead>
                  <tr>
                    {[
                      c.user,
                      c.rpmUsed,
                      c.rpm,
                      c.concurrency,
                      c.peak,
                      c.completed,
                      c.incomplete,
                      c.inspect,
                    ].map((v) => (
                      <th key={v}>{v}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {query.data.items.map((v) => (
                    <tr key={v.user_id}>
                      <td>{v.user_id}</td>
                      <td>{v.rpm_committed}</td>
                      <td>
                        {v.high_rpm_minutes} · {v.rpm_risk ? c.yes : c.no}
                      </td>
                      <td>
                        {v.high_concurrency_minutes} · {v.concurrency_risk ? c.yes : c.no}
                      </td>
                      <td>{v.peak}</td>
                      <td>{v.complete_minutes}</td>
                      <td>{v.incomplete_minutes}</td>
                      <td>
                        <button className="btn btn-secondary" onClick={() => inspect(v.user_id)}>
                          {c.inspect}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <Pager
            c={c}
            pager={pager}
            next={query.data.next}
            more={query.data.has_more}
            pending={query.isFetching}
          />
        </>
      ) : (
        <LoadingState />
      )}
    </Card>
  );
}
function IPs({ inspect, ...scope }: Scope & { inspect: (id: string) => void }) {
  const { role, scopeKey, c, filters } = scope,
    pager = usePager();
  const query = useQuery({
    queryKey: ['risk', role, scopeKey, 'ips', filters, pager.after],
    queryFn: ({ signal }) =>
      riskAPI(role).ips(
        { ...filters, from: scope.customRange ? filters.from : undefined, after: pager.after },
        signal,
      ),
    ...queryOptions,
  });
  return (
    <div className="ops-stack">
      <p>{c.ipHelp}</p>
      {query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : query.data ? (
        <>
          <Coverage value={query.data.coverage} c={c} />
          <p>
            {stamp(query.data.from)} — {stamp(query.data.to)}
          </p>
          {!query.data.items.length ? (
            <EmptyState title={c.empty} body={c.ipHelp} />
          ) : (
            query.data.items.map((ip) => (
              <Card key={ip.ip}>
                <h2>{ip.ip}</h2>
                <p>
                  {c.users}: {ip.users} · {c.count}: {ip.requests} · {stamp(ip.first_seen)} —{' '}
                  {stamp(ip.last_seen)}
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
            ))
          )}
          <Pager
            c={c}
            pager={pager}
            next={query.data.next}
            more={query.data.has_more}
            pending={query.isFetching}
          />
        </>
      ) : (
        <LoadingState />
      )}
    </div>
  );
}
function Clients({ inspect, ...scope }: Scope & { inspect: (id: string) => void }) {
  const { role, scopeKey, c, filters } = scope,
    pager = usePager(),
    client = useQueryClient();
  const [paused, setPaused] = useState(false);
  const key = ['risk', role, scopeKey, 'clients', filters, pager.after];
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => riskAPI(role).clients({ ...filters, after: pager.after }, signal),
    enabled: !paused,
    ...queryOptions,
  });
  return (
    <div className="ops-stack">
      <p>{c.scanHelp}</p>
      <button
        className="btn btn-secondary"
        onClick={() => {
          if (!paused) void client.cancelQueries({ queryKey: key });
          setPaused((v) => !v);
        }}
      >
        {paused ? c.resume : c.stop}
      </button>
      {query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : query.data ? (
        <>
          <p>
            {c.sampled}: {query.data.scanned} · {c.matches}: {query.data.items.length}
          </p>
          <Coverage value={query.data.coverage} c={c} />
          {query.data.items.map((item) => (
            <RequestView key={item.log_id} item={item} c={c} inspect={inspect} />
          ))}
          <Pager
            c={c}
            pager={pager}
            next={query.data.next}
            more={query.data.has_more}
            pending={query.isFetching || paused}
          />
        </>
      ) : paused ? null : (
        <LoadingState />
      )}
    </div>
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
  save,
  cancel,
  c,
  busy,
}: {
  rule?: Rule;
  save: (v: RuleInput) => void;
  cancel: () => void;
  c: RiskCopy;
  busy: boolean;
}) {
  const [value, setValue] = useState<RuleInput>(() => rule ?? emptyRule());
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
        save(value);
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
      {value.conditions.map((item, i) => (
        <fieldset className="ops-field-grid audit-condition" key={i}>
          <legend>
            {c.and} {i + 1}
          </legend>
          <label>
            {c.field}
            <select
              value={item.field}
              onChange={(e) => condition(i, { field: e.target.value as Condition['field'] })}
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
              onChange={(e) => condition(i, { operator: e.target.value as Condition['operator'] })}
            >
              {(['equals', 'contains', 'prefix'] as const).map((o) => (
                <option key={o} value={o}>
                  {c[o]}
                </option>
              ))}
            </select>
          </label>
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
        <button className="btn btn-primary" disabled={busy}>
          {c.save}
        </button>
        <button className="btn btn-secondary" type="button" disabled={busy} onClick={cancel}>
          {c.cancel}
        </button>
      </div>
    </form>
  );
}
function Rules({ role, scopeKey, c }: Scope) {
  const pager = usePager(),
    client = useQueryClient();
  const [editing, setEditing] = useState<Rule | 'new' | null>(null);
  const query = useQuery({
    queryKey: ['risk', role, scopeKey, 'rules', pager.after],
    queryFn: ({ signal }) => riskAPI(role).rules(pager.after, signal),
    ...queryOptions,
  });
  const mutation = useMutation({
    mutationKey: ['risk', role, scopeKey, 'rules'],
    gcTime: 0,
    mutationFn: (v: { input?: RuleInput; rule?: Rule }) =>
      v.input
        ? riskAPI(role).saveRule(v.input, v.rule?.id)
        : riskAPI(role).deleteRule(v.rule!.id, v.rule!.revision),
    onSuccess: async () => {
      setEditing(null);
      await client.invalidateQueries({ queryKey: ['risk', role, scopeKey] });
    },
  });
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
      {editing ? (
        <RuleEditor
          key={editing === 'new' ? 'new' : editing.id}
          rule={editing === 'new' ? undefined : editing}
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
                {c.count}: {query.data.total}
              </p>
              {query.data.items.map((rule) => (
                <Card key={rule.id}>
                  <h3>{rule.name}</h3>
                  <p>
                    {rule.status === 'confirmed' ? c.confirmed : c.suspected} · {c.revision}{' '}
                    {rule.revision} · {rule.enabled ? c.enabled : c.no}
                  </p>
                  <p>
                    {rule.conditions
                      .map((v) => `${fieldLabel(v.field, c)} ${c[v.operator]} ${v.value}`)
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
                    disabled={mutation.isPending}
                    onClick={() => {
                      mutation.reset();
                      setEditing(rule);
                    }}
                  >
                    {c.edit}
                  </button>
                  <button
                    className="btn btn-secondary"
                    disabled={mutation.isPending}
                    onClick={() => mutation.mutate({ rule })}
                  >
                    {c.remove}
                  </button>
                </Card>
              ))}
              <Pager c={c} pager={pager} next={query.data.next} more={query.data.has_more} />
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
  const [draft, setDraft] = useState(value);
  const fields = [
    ['threshold_percent', c.threshold, 1, 100],
    ['consecutive_minutes', c.consecutive, 1, 60],
    ['shared_ip_hours', c.hours, 1, 720],
    ['shared_ip_users', c.accounts, 2, 1000],
  ] as const;
  return (
    <form
      className="ops-stack"
      onSubmit={(e) => {
        e.preventDefault();
        save(draft);
      }}
    >
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
      <p>
        {c.policyRevision}: {value.revision}
      </p>
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
  const mutation = useMutation({
    mutationKey: ['risk', role, scopeKey, 'config'],
    gcTime: 0,
    mutationFn: (value: Config) => riskAPI(role).saveConfig(value),
    onSuccess: () => client.invalidateQueries({ queryKey: ['risk', role, scopeKey] }),
  });
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
    </Card>
  );
}
function Access({ role, scopeKey, c, filters }: Scope) {
  const pager = usePager();
  const [draft, setDraft] = useState({
      user_id: '',
      key_generation: '',
      path_kind: '',
      status_class: '',
    }),
    [selected, setSelected] = useState(draft);
  const f = {
    from: filters.from,
    to: filters.to,
    lookback_hours: filters.lookback_hours,
    ...selected,
  };
  const events = useQuery({
    queryKey: ['risk', role, scopeKey, 'access', f, pager.after],
    queryFn: ({ signal }) =>
      riskAPI(role).access({ ...f, cursor: pager.after, page_size: 100 }, signal),
    ...queryOptions,
  });
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
          setSelected(draft);
          pager.reset();
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
          <Pager
            c={c}
            pager={pager}
            next={events.data.next}
            more={Boolean(events.data.next)}
            pending={events.isFetching}
          />
        </>
      ) : (
        <LoadingState />
      )}
    </div>
  );
}
type Tab = 'users' | 'ips' | 'clients' | 'rules' | 'config' | 'access';
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
  const { i18n } = useTranslation(),
    c = riskCopy(i18n.language);
  const [tab, setTab] = useState<Tab>('users'),
    [selectedUser, setSelectedUser] = useState('');
  const client = useQueryClient();
  const [range, setRange] = useState('default');
  const [rangeError, setRangeError] = useState(false);
  const [filters, setFilters] = useState<Filters>(initialWindow),
    [draft, setDraft] = useState(() => ({
      from: localTime(Math.floor(Date.now() / 1000) - 86400),
      to: localTime(Math.floor(Date.now() / 1000)),
      kind: 'total',
    }));
  const [customRange, setCustomRange] = useState(false);
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
      setCustomRange(range !== 'default');
      void client.invalidateQueries({ queryKey: ['risk', role, scopeKey] });
      return;
    }
    const from = new Date(draft.from).getTime() / 1000,
      to = new Date(draft.to).getTime() / 1000;
    if (Number.isFinite(from) && to > from && to - from <= 30 * 86400 && to <= Date.now() / 1000) {
      setFilters({ from, to, kind: draft.kind, limit: 100 });
      setCustomRange(true);
      void client.invalidateQueries({ queryKey: ['risk', role, scopeKey] });
    } else setRangeError(true);
  }
  if (authorityError) return <ErrorState error={authorityError} />;
  return (
    <div className="page ops-page audit-page">
      <PageHeader title={c.title} description={c.description} />
      <Card>
        <p>{c.caveat}</p>
        <div className="ops-tabs audit-tabs" role="group" aria-label={c.title}>
          {(['users', 'ips', 'clients', 'rules', 'config', 'access'] as Tab[]).map((v) => (
            <button
              className={tab === v && !selectedUser ? 'btn btn-primary' : 'btn btn-secondary'}
              key={v}
              aria-pressed={tab === v && !selectedUser}
              onClick={() => {
                setTab(v);
                setSelectedUser('');
              }}
            >
              {c[v]}
            </button>
          ))}
        </div>
        {tab !== 'rules' && tab !== 'config' ? (
          <form className="ops-stack" onSubmit={apply}>
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
                    <input
                      type="datetime-local"
                      required
                      value={draft.from}
                      onChange={(e) => setDraft((v) => ({ ...v, from: e.target.value }))}
                    />
                  </label>
                  <label>
                    {c.to}
                    <input
                      type="datetime-local"
                      required
                      value={draft.to}
                      onChange={(e) => setDraft((v) => ({ ...v, to: e.target.value }))}
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
              <button className="btn btn-primary">{c.apply}</button>
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
        ) : null}
      </Card>
      <div key={JSON.stringify(filters)}>
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

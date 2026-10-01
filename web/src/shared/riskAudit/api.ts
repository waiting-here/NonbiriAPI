import { ApiError, apiFetch } from '@shared/query/http';
import { queryPath } from '@shared/operations/api';
import {
  normalizePageMetadata,
  validatePageResponse,
  type PageMetadata,
  type PageSize,
} from '@shared/operations/pageNumbers';

export type RiskRole = 'admin' | 'steward';
export interface ClientScan {
  id: string;
  state: 'queued' | 'running' | 'completed' | 'cancelled' | 'limited' | 'failed';
  reason: string;
  from: number;
  to: number;
  kind: string;
  model: string;
  candidates: string;
  scanned: string;
  matched: number;
  created_at: number;
  updated_at: number;
  expires_at: number;
  rule_count: number;
}
export interface ScanResults extends PageMetadata {
  scan: ClientScan;
  items: Request[];
}
export interface ScanInput {
  request_token: string;
  from?: number;
  to?: number;
  lookback_hours?: number;
  kind?: string;
  model?: string;
}
export type TaskKind = 'client_hits' | 'users' | 'shared_ips' | 'user_ips';
export interface TaskScan extends Omit<ClientScan, 'kind'> {
  kind: TaskKind;
  call_kind: string;
  signal: string;
  filter_revision: number;
  changed: boolean;
  coverage: string;
  truncated_reason: string;
  status: ClientScan['state'];
  scanned_candidates: string;
  source_watermark?: string;
  last_source_at?: number;
  last_source_id?: string;
}
export interface TaskInput extends Omit<ScanInput, 'kind'> {
  kind: TaskKind;
  call_kind?: string;
  signal?: '' | 'rpm' | 'concurrency';
}
export interface TaskResults<T> extends PageMetadata {
  scan: TaskScan;
  items: T[];
  coverage: string;
}

function clientScan(value: unknown): ClientScan {
  const o = obj(value);
  const state = text(o.state, 20);
  if (!['queued', 'running', 'completed', 'cancelled', 'limited', 'failed'].includes(state))
    return invalid();
  const count = (value: unknown) => {
    if (
      typeof value !== 'string' ||
      !/^(0|[1-9][0-9]{0,18})$/.test(value) ||
      BigInt(value) > 9223372036854775807n
    )
      return invalid();
    return value;
  };
  const result: ClientScan = {
    id: text(o.id, 26),
    state: state as ClientScan['state'],
    reason: text(o.reason, 64),
    from: num(o.from),
    to: num(o.to),
    kind: text(o.kind, 32),
    model: text(o.model, 512),
    candidates: count(o.candidates),
    scanned: count(o.scanned),
    matched: num(o.matched),
    created_at: num(o.created_at),
    updated_at: num(o.updated_at),
    expires_at: num(o.expires_at),
    rule_count: num(o.rule_count),
  };
  if (
    !/^scn_[A-Za-z0-9_-]{21}[AQgw]$/.test(result.id) ||
    result.matched > 100000 ||
    result.rule_count > 1000 ||
    result.to <= result.from
  )
    return invalid();
  return result;
}

function scanResults(value: unknown, id: string, page: string, size: PageSize): ScanResults {
  const o = obj(value);
  const metadata = normalizePageMetadata({
    page: o.page,
    page_size: o.page_size,
    total_items: o.total_items,
    total_pages: o.total_pages,
  });
  const items = list(o.items, request, 100);
  const scan = clientScan(o.scan);
  validatePageResponse(metadata, page, size, items.length);
  if (scan.id !== id || BigInt(metadata.total_items) > 100000n) return invalid();
  return { ...metadata, scan, items };
}
function taskScan(value: unknown): TaskScan {
  const o = obj(value);
  const base = clientScan(value);
  if (
    !['client_hits', 'users', 'shared_ips', 'user_ips'].includes(base.kind) ||
    o.status !== base.state
  )
    return invalid();
  const scanned = text(o.scanned_candidates, 20);
  if (scanned !== base.scanned) return invalid();
  return {
    ...base,
    kind: base.kind as TaskKind,
    call_kind: text(o.call_kind, 32),
    signal: text(o.signal ?? '', 20),
    filter_revision: num(o.filter_revision),
    changed: bool(o.changed),
    coverage: text(o.coverage, 64),
    truncated_reason: text(o.truncated_reason ?? '', 64),
    status: base.state,
    scanned_candidates: scanned,
    source_watermark: o.source_watermark === undefined ? undefined : text(o.source_watermark, 64),
    last_source_at: o.last_source_at == null ? undefined : num(o.last_source_at),
    last_source_id: o.last_source_id === undefined ? undefined : text(o.last_source_id, 20),
  };
}
function taskResults<T>(
  value: unknown,
  scanID: string,
  requestedPage: string,
  size: PageSize,
  decode: (v: unknown) => T,
): TaskResults<T> {
  const o = obj(value);
  const metadata = pageMetadata(o);
  const items = list(o.items, decode, 100);
  const scan = taskScan(o.scan);
  validatePageResponse(metadata, requestedPage, size, items.length);
  if (scan.id !== scanID || BigInt(metadata.total_items) > 100000n) return invalid();
  return { ...metadata, scan, items, coverage: text(o.coverage, 64) };
}
export const sourceFields = [
  'effective_ip',
  'user_agent',
  'origin',
  'referer',
  'http_referer',
  'openrouter_title',
  'legacy_title',
  'sdk_lang',
  'sdk_version',
  'sdk_runtime',
  'sdk_runtime_version',
] as const;
export type SourceField = (typeof sourceFields)[number];
export const accessPaths = [
  'models',
  'billing_subscription',
  'billing_usage',
  'v1_billing_subscription',
  'v1_billing_usage',
  'sub2api_billing',
  'chat_head',
] as const;
export interface Quality {
  truncated: boolean;
  multiple: boolean;
  invalid: boolean;
}
export type Source = Record<SourceField, string> & {
  ip_quality: string;
  quality: Partial<Record<SourceField, Quality>>;
};
export interface Condition {
  field: SourceField;
  operator: 'equals' | 'contains' | 'prefix';
  value: string;
  case_sensitive: boolean;
}
export interface AutoBan {
  enabled: boolean;
  duration_seconds: number | null;
}
export interface RuleInput {
  name: string;
  status: 'suspected' | 'confirmed';
  enabled: boolean;
  revision: number;
  conditions: Condition[];
  evidence_note: string;
  evidence_url: string;
  auto_ban?: AutoBan | null;
}
export interface Rule extends RuleInput {
  auto_ban: AutoBan | null;
  id: string;
  created_at: number;
  updated_at: number;
  created_by_role: string;
  updated_by_role: string;
  created_by_user_id: string | null;
  updated_by_user_id: string | null;
}
export interface Match {
  rule_id: string;
  revision: number;
  name: string;
  status: string;
  evidence_note: string;
  evidence_url: string;
  quality: string;
  fields: string[];
}
export interface Config {
  user_ip_window_hours?: number;
  user_ip_min_ips?: number;
  threshold_percent: number;
  consecutive_minutes: number;
  shared_ip_hours: number;
  shared_ip_users: number;
  revision: number;
  updated_at: number;
}
export interface Summary {
  user_id: string;
  rpm_committed: number;
  rpm_denied: number;
  concurrency_denied: number;
  peak: number;
  occupancy_millis: number;
  complete_minutes: number;
  incomplete_minutes: number;
  high_rpm_minutes: number;
  high_concurrency_minutes: number;
  rpm_risk: boolean;
  concurrency_risk: boolean;
  risk_scope: string;
}
export interface Minute {
  epoch: string;
  user_id: string;
  minute: number;
  call_kind: string;
  rpm_committed: number;
  rpm_released: number;
  rpm_pending: number;
  rpm_denied: number;
  concurrency_denied: number;
  occupancy_millis: number;
  peak: number;
  rpm_limit: number;
  concurrency_limit: number;
  coverage: number;
  config_revision: number;
  updated_at: number;
}
export interface Request {
  log_id: string;
  user_id: string;
  request_id: string;
  call_kind: string;
  occurred_at: number;
  source: Source;
  model: string;
  outcome: string;
  dispatched: boolean;
  error_code: string;
  rejection_reason: string;
  duration_millis: number | null;
  response_started: boolean | null;
  matches: Match[];
  match_count: number;
  matches_truncated: boolean;
}
export interface Page<T> {
  items: T[];
  next: string;
  has_more: boolean;
  from: number;
  to: number;
  coverage: string;
  scanned: number;
  page?: string;
  page_size?: PageSize;
  total_items?: string;
  total_pages?: string;
  watermark?: string;
  changed?: boolean;
}
export interface Stats {
  samples: number;
  dispatched: number;
  rejected: number;
  failed: number;
  unknown_duration: number;
  cancellations: { label: string; count: number }[];
}
export interface Detail {
  user_id: string;
  summary: Summary;
  minutes: Minute[];
  requests: Page<Request>;
  coverage: string;
  comparison: {
    model: string;
    from: number;
    to: number;
    user: Stats;
    others: Stats;
    has_more: boolean;
    coverage: string;
  };
  source_distribution: { user_agent: string; ip_quality: string; count: number }[];
}
export interface SharedIP {
  ip: string;
  users: number;
  requests: number;
  first_seen: number;
  last_seen: number;
  associations_truncated: boolean;
  associations: {
    user_id: string;
    call_kind: string;
    first_seen: number;
    last_seen: number;
    requests: number;
    dispatched: number;
    rejected: number;
  }[];
}
export interface UserIPs {
  discord_id: string;
  peak: number;
  window_from: number;
  window_to: number;
  ips: string[];
  ips_truncated: boolean;
  accounts: SharedIP['associations'];
  accounts_truncated: boolean;
}
export interface AccessEvent {
  id: string;
  user_id: string;
  caller_key_generation: string | null;
  path_kind: string;
  method: string;
  http_status: number;
  response_kind: string;
  source: Source;
  occurred_at: number;
}
export interface AccessSummary {
  authenticated_events: number;
  anonymous_events: number;
  model_list_events: number;
  generation_requests: number;
  model_generation_ratio: number | null;
  coverage: { capture_started_at: number; dropped: number; last_gap_at: number | null };
  paths: { path_kind: string; response_kind: string; authenticated: number; anonymous: number }[];
}
export type Filters = Record<string, string | number | undefined>;

function invalid(): never {
  throw new ApiError('invalid_response', 'The audit response could not be read.', 0);
}
function obj(v: unknown): Record<string, unknown> {
  if (!v || typeof v !== 'object' || Array.isArray(v)) return invalid();
  return v as Record<string, unknown>;
}
function text(v: unknown, max = 8192): string {
  if (typeof v !== 'string' || v.length > max) return invalid();
  return v;
}
function num(v: unknown): number {
  const n = typeof v === 'string' && /^\d+$/.test(v) ? Number(v) : v;
  if (typeof n !== 'number' || !Number.isSafeInteger(n) || n < 0) return invalid();
  return n;
}
function id(v: unknown): string {
  const s = text(v, 20);
  if (!/^[1-9]\d*$/.test(s)) return invalid();
  return s;
}
function bool(v: unknown): boolean {
  if (typeof v !== 'boolean') return invalid();
  return v;
}
function list<T>(v: unknown, decode: (v: unknown) => T, max = 100): T[] {
  if (v == null) return [];
  if (!Array.isArray(v) || v.length > max) return invalid();
  return v.map(decode);
}
function pageMetadata(o: Record<string, unknown>): PageMetadata {
  return normalizePageMetadata({
    page: o.page,
    page_size: o.page_size,
    total_items: o.total_items,
    total_pages: o.total_pages,
  });
}
function source(v: unknown): Source {
  const o = obj(v),
    quality = o.quality == null ? {} : obj(o.quality);
  const out = { ip_quality: text(o.ip_quality ?? '', 32), quality: {} } as Source;
  for (const key of sourceFields) {
    out[key] = text(o[key] ?? '');
    if (quality[key]) {
      const q = obj(quality[key]);
      out.quality[key] = {
        truncated: q.truncated === true,
        multiple: q.multiple === true,
        invalid: q.invalid === true,
      };
    }
  }
  return out;
}
function match(v: unknown): Match {
  const o = obj(v);
  return {
    rule_id: text(o.rule_id, 64),
    revision: num(o.revision),
    name: text(o.name, 120),
    status: text(o.status, 20),
    evidence_note: text(o.evidence_note),
    evidence_url: text(o.evidence_url, 2048),
    quality: text(o.quality, 40),
    fields: list(o.fields, (v) => text(v, 40), 8),
  };
}
function request(v: unknown): Request {
  const o = obj(v);
  return {
    log_id: id(o.log_id),
    user_id: id(o.user_id),
    request_id: text(o.request_id, 64),
    call_kind: text(o.call_kind, 32),
    occurred_at: num(o.occurred_at),
    source: source(o.source),
    model: text(o.model, 512),
    outcome: text(o.outcome, 40),
    dispatched: bool(o.dispatched),
    error_code: text(o.error_code, 64),
    rejection_reason: text(o.rejection_reason, 128),
    duration_millis: o.duration_millis == null ? null : num(o.duration_millis),
    response_started: o.response_started == null ? null : bool(o.response_started),
    matches: list(o.matches, match, 1000),
    match_count: num(o.match_count ?? 0),
    matches_truncated: o.matches_truncated === true,
  };
}
function summary(v: unknown): Summary {
  const o = obj(v);
  return {
    user_id: id(o.user_id),
    rpm_committed: num(o.rpm_committed),
    rpm_denied: num(o.rpm_denied),
    concurrency_denied: num(o.concurrency_denied),
    peak: num(o.peak),
    occupancy_millis: num(o.occupancy_millis),
    complete_minutes: num(o.complete_minutes),
    incomplete_minutes: num(o.incomplete_minutes),
    high_rpm_minutes: num(o.high_rpm_minutes),
    high_concurrency_minutes: num(o.high_concurrency_minutes),
    rpm_risk: bool(o.rpm_risk),
    concurrency_risk: bool(o.concurrency_risk),
    risk_scope: text(o.risk_scope, 32),
  };
}
function minute(v: unknown): Minute {
  const o = obj(v);
  return {
    epoch: text(o.epoch, 64),
    user_id: id(o.user_id),
    minute: num(o.minute),
    call_kind: text(o.call_kind, 32),
    rpm_committed: num(o.rpm_committed),
    rpm_released: num(o.rpm_released),
    rpm_pending: num(o.rpm_pending),
    rpm_denied: num(o.rpm_denied),
    concurrency_denied: num(o.concurrency_denied),
    occupancy_millis: num(o.occupancy_millis),
    peak: num(o.peak),
    rpm_limit: num(o.rpm_limit),
    concurrency_limit: num(o.concurrency_limit),
    coverage: num(o.coverage),
    config_revision: num(o.config_revision),
    updated_at: num(o.updated_at),
  };
}
function page<T>(v: unknown, decode: (v: unknown) => T): Page<T> {
  const o = obj(v);
  const metadata = o.page == null ? {} : pageMetadata(o);
  return {
    items: list(o.items, decode),
    next: text(o.next ?? '', 64),
    has_more: bool(o.has_more),
    from: num(o.from),
    to: num(o.to),
    coverage: text(o.coverage, 80),
    scanned: num(o.scanned ?? 0),
    ...metadata,
    ...(o.watermark == null ? {} : { watermark: text(o.watermark, 20) }),
    ...(o.changed == null ? {} : { changed: bool(o.changed) }),
  };
}
function stats(v: unknown): Stats {
  const o = obj(v);
  return {
    samples: num(o.samples),
    dispatched: num(o.dispatched),
    rejected: num(o.rejected),
    failed: num(o.failed),
    unknown_duration: num(o.unknown_duration),
    cancellations: list(
      o.cancellations,
      (v) => {
        const b = obj(v);
        return { label: text(b.label, 32), count: num(b.count) };
      },
      5,
    ),
  };
}
function detail(v: unknown): Detail {
  const o = obj(v),
    c = obj(o.comparison);
  return {
    user_id: id(o.user_id),
    summary: summary(o.summary),
    minutes: list(o.minutes, minute),
    requests: page(o.requests, request),
    coverage: text(o.coverage, 80),
    comparison: {
      model: text(c.model, 512),
      from: num(c.from),
      to: num(c.to),
      user: stats(c.user),
      others: stats(c.others),
      has_more: bool(c.has_more),
      coverage: text(c.coverage, 80),
    },
    source_distribution: list(o.source_distribution, (v) => {
      const s = obj(v);
      return {
        user_agent: text(s.user_agent, 2048),
        ip_quality: text(s.ip_quality, 32),
        count: num(s.count),
      };
    }),
  };
}
function config(v: unknown): Config {
  const o = obj(v);
  return {
    threshold_percent: num(o.threshold_percent),
    consecutive_minutes: num(o.consecutive_minutes),
    shared_ip_hours: num(o.shared_ip_hours),
    shared_ip_users: num(o.shared_ip_users),
    user_ip_window_hours: num(o.user_ip_window_hours ?? 24),
    user_ip_min_ips: num(o.user_ip_min_ips ?? 3),
    revision: num(o.revision),
    updated_at: num(o.updated_at),
  };
}
function rule(v: unknown): Rule {
  const o = obj(v);
  let autoBan: AutoBan | null = null;
  if (o.auto_ban != null) {
    const action = obj(o.auto_ban);
    const duration = action.duration_seconds === null ? null : num(action.duration_seconds);
    if (duration !== null && (!Number.isInteger(duration) || duration < 1 || duration > 315360000))
      return invalid();
    autoBan = { enabled: bool(action.enabled), duration_seconds: duration };
  }
  return {
    id: text(o.id, 64),
    name: text(o.name, 120),
    status:
      o.status === 'confirmed' ? 'confirmed' : o.status === 'suspected' ? 'suspected' : invalid(),
    enabled: bool(o.enabled),
    revision: num(o.revision),
    conditions: list(
      o.conditions,
      (v) => {
        const c = obj(v);
        const field = text(c.field, 40),
          operator = text(c.operator, 16);
        if (
          !sourceFields.includes(field as SourceField) ||
          !['equals', 'contains', 'prefix'].includes(operator)
        )
          return invalid();
        return {
          field: field as SourceField,
          operator: operator as Condition['operator'],
          value: text(c.value, 1024),
          case_sensitive: bool(c.case_sensitive),
        };
      },
      8,
    ),
    evidence_note: text(o.evidence_note),
    evidence_url: text(o.evidence_url, 2048),
    auto_ban: autoBan,
    created_at: num(o.created_at),
    updated_at: num(o.updated_at),
    created_by_role: text(o.created_by_role, 20),
    updated_by_role: text(o.updated_by_role, 20),
    created_by_user_id: o.created_by_user_id == null ? null : id(o.created_by_user_id),
    updated_by_user_id: o.updated_by_user_id == null ? null : id(o.updated_by_user_id),
  };
}
function sharedIP(v: unknown): SharedIP {
  const o = obj(v);
  return {
    ip: text(o.ip, 64),
    users: num(o.users),
    requests: num(o.requests),
    first_seen: num(o.first_seen),
    last_seen: num(o.last_seen),
    associations_truncated: bool(o.associations_truncated),
    associations: list(o.associations, ipAssociation, 24),
  };
}
function ipAssociation(v: unknown): SharedIP['associations'][number] {
  const a = obj(v);
  return {
    user_id: id(a.user_id),
    call_kind: text(a.call_kind, 32),
    first_seen: num(a.first_seen),
    last_seen: num(a.last_seen),
    requests: num(a.requests),
    dispatched: num(a.dispatched),
    rejected: num(a.rejected),
  };
}
function userIPs(v: unknown): UserIPs {
  const o = obj(v);
  return {
    discord_id: text(o.discord_id, 128),
    peak: num(o.peak),
    window_from: num(o.window_from),
    window_to: num(o.window_to),
    ips: list(o.ips, (value) => text(value, 64), 200),
    ips_truncated: bool(o.ips_truncated),
    accounts: list(o.accounts, ipAssociation, 24),
    accounts_truncated: bool(o.accounts_truncated),
  };
}
function access(v: unknown): AccessEvent {
  const o = obj(v);
  return {
    id: text(o.id, 64),
    user_id: id(o.user_id),
    caller_key_generation:
      o.caller_key_generation == null ? null : text(o.caller_key_generation, 32),
    path_kind: text(o.path_kind, 40),
    method: text(o.method, 8),
    http_status: num(o.http_status),
    response_kind: text(o.response_kind, 32),
    source: source(o.source),
    occurred_at: num(o.occurred_at),
  };
}
function accessSummary(v: unknown): AccessSummary {
  const o = obj(v),
    c = obj(o.coverage);
  const ratio = o.model_generation_ratio;
  if (ratio !== null && (typeof ratio !== 'number' || !Number.isFinite(ratio) || ratio < 0))
    return invalid();
  return {
    authenticated_events: num(o.authenticated_events),
    anonymous_events: num(o.anonymous_events),
    model_list_events: num(o.model_list_events),
    generation_requests: num(o.generation_requests),
    model_generation_ratio: ratio,
    coverage: {
      capture_started_at: num(c.capture_started_at),
      dropped: num(c.dropped),
      last_gap_at: c.last_gap_at === null ? null : num(c.last_gap_at),
    },
    paths: list(o.paths, (v) => {
      const p = obj(v);
      return {
        path_kind: text(p.path_kind, 40),
        response_kind: text(p.response_kind, 32),
        authenticated: num(p.authenticated),
        anonymous: num(p.anonymous),
      };
    }),
  };
}
export function riskAPI(role: RiskRole) {
  const base = role === 'admin' ? '/admin/api/abuse-audit' : '/api/steward/abuse-audit';
  const get = async <T>(
    path: string,
    decode: (v: unknown) => T,
    filters: Filters = {},
    signal?: AbortSignal,
  ) => decode(await apiFetch<unknown>(queryPath(base + path, filters), { signal }));
  return {
    createTask: async (input: TaskInput, signal?: AbortSignal) =>
      taskScan(await apiFetch<unknown>(base + '/scans', { method: 'POST', json: input, signal })),
    recentTasks: async (signal?: AbortSignal) =>
      list(obj(await apiFetch<unknown>(base + '/scans', { signal })).items, taskScan, 200),
    taskResults: async <T>(
      scanID: string,
      kind: TaskKind,
      page: string,
      size: PageSize,
      signal?: AbortSignal,
    ): Promise<TaskResults<T>> => {
      const decode = (
        kind === 'users'
          ? summary
          : kind === 'shared_ips'
            ? sharedIP
            : kind === 'user_ips'
              ? userIPs
              : request
      ) as (v: unknown) => T;
      const output = taskResults(
        await apiFetch<unknown>(
          queryPath(base + '/scans/' + encodeURIComponent(scanID) + '/results', {
            page,
            page_size: size,
          }),
          { signal },
        ),
        scanID,
        page,
        size,
        decode,
      );
      if (output.scan.kind !== kind) return invalid();
      return output;
    },
    cancelTask: async (scanID: string, signal?: AbortSignal) =>
      taskScan(
        await apiFetch<unknown>(base + '/scans/' + encodeURIComponent(scanID) + '/cancel', {
          method: 'POST',
          json: {},
          signal,
        }),
      ),
    createScan: async (input: ScanInput, signal?: AbortSignal) =>
      clientScan(
        await apiFetch<unknown>(base + '/client-scans', { method: 'POST', json: input, signal }),
      ),
    recentScans: async (signal?: AbortSignal) =>
      list(obj(await apiFetch<unknown>(base + '/client-scans', { signal })).items, clientScan, 10),
    scanResults: async (scanID: string, page: string, size: PageSize, signal?: AbortSignal) =>
      scanResults(
        await apiFetch<unknown>(
          queryPath(base + '/client-scans/' + encodeURIComponent(scanID) + '/results', {
            page,
            page_size: size,
          }),
          { signal },
        ),
        scanID,
        page,
        size,
      ),
    cancelScan: async (scanID: string, signal?: AbortSignal) =>
      clientScan(
        await apiFetch<unknown>(base + '/client-scans/' + encodeURIComponent(scanID) + '/cancel', {
          method: 'POST',
          json: {},
          signal,
        }),
      ),
    users: (f: Filters, s?: AbortSignal) => get('/users', (v) => page(v, summary), f, s),
    clients: (f: Filters, s?: AbortSignal) =>
      get('/users', (v) => page(v, request), { ...f, signal: 'client' }, s),
    user: (userID: string, f: Filters, s?: AbortSignal) =>
      get('/users/' + encodeURIComponent(userID), detail, f, s),
    ips: (f: Filters, s?: AbortSignal) => get('/shared-ips', (v) => page(v, sharedIP), f, s),
    config: (s?: AbortSignal) => get('/config', config, {}, s),
    saveConfig: async (value: Config, signal?: AbortSignal) =>
      config(await apiFetch<unknown>(base + '/config', { method: 'PUT', json: value, signal })),
    rules: (after: string, s?: AbortSignal) =>
      get(
        '/client-rules',
        (v) => {
          const o = obj(v);
          return {
            items: list(o.items, rule),
            next: text(o.next ?? '', 64),
            has_more: bool(o.has_more),
            total: num(o.total),
          };
        },
        { after, limit: 100 },
        s,
      ),
    numberedRules: (page: string, size: PageSize, revision?: string, s?: AbortSignal) =>
      get(
        '/client-rules',
        (v) => {
          const o = obj(v);
          const metadata = pageMetadata(o);
          const items = list(o.items, rule);
          const changed = bool(o.changed);
          validatePageResponse(metadata, changed ? '1' : page, size, items.length);
          return { ...metadata, items, revision: text(o.revision, 64), changed };
        },
        { page, page_size: size, revision },
        s,
      ),
    saveRule: async (value: RuleInput, ruleID?: string, signal?: AbortSignal) =>
      rule(
        await apiFetch<unknown>(
          base + '/client-rules' + (ruleID ? '/' + encodeURIComponent(ruleID) : ''),
          {
            method: ruleID ? 'PATCH' : 'POST',
            signal,
            json: {
              name: value.name,
              status: value.status,
              enabled: value.enabled,
              revision: value.revision,
              conditions: value.conditions,
              evidence_note: value.evidence_note,
              evidence_url: value.evidence_url,
              ...(value.auto_ban !== undefined ? { auto_ban: value.auto_ban } : {}),
            },
          },
        ),
      ),
    deleteRule: (ruleID: string, revision: number, signal?: AbortSignal) =>
      apiFetch(queryPath(base + '/client-rules/' + encodeURIComponent(ruleID), { revision }), {
        method: 'DELETE',
        signal,
      }),
    access: (f: Filters, s?: AbortSignal) =>
      get(
        '/access-events',
        (v) => {
          const o = obj(v);
          return {
            items: list(o.data, access),
            next: o.next_cursor == null ? '' : text(o.next_cursor, 2048),
          };
        },
        f,
        s,
      ),
    numberedAccess: (f: Filters, requestedPage: string, size: PageSize, s?: AbortSignal) =>
      get(
        '/access-events',
        (v) => {
          const o = obj(v);
          const metadata = pageMetadata(o);
          const items = list(o.data, access);
          validatePageResponse(metadata, requestedPage, size, items.length);
          return {
            ...metadata,
            items,
            watermark: text(o.watermark, 20),
            changed: bool(o.changed),
            from: num(o.from),
            to: num(o.to),
          };
        },
        { ...f, page: requestedPage, page_size: size },
        s,
      ),
    accessSummary: (f: Filters, s?: AbortSignal) => get('/access-summary', accessSummary, f, s),
  };
}

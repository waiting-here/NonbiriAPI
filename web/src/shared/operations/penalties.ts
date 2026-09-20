import { decoded, queryPath } from './api';
import { validateWindow } from './numberedPage';
import { normalizePageMetadata, validatePageResponse, type PageSize } from './pageNumbers';
import {
  array,
  boolean,
  decimalID,
  integer,
  invalidResponse,
  nullableDecimalID,
  nullableInteger,
  nullableOpaqueID,
  nullableUnixSecond,
  oneOf,
  opaqueID,
  record,
  unixSecond,
} from './wire';

export const penaltyKinds = ['deduction', 'ban', 'charity_suspend'] as const;
export const penaltyStates = ['active', 'ended'] as const;
const reasons = ['charity_rpm', 'charity_short_content'] as const;
export const ruleFields = [
  'rpm_ban_threshold',
  'rpm_ban_window_seconds',
  'rpm_ban_duration_seconds',
  'charity_min_chars',
  'charity_violation_deduct_milli',
  'charity_violation_ban_seconds',
  'charity_violation_window_seconds',
  'charity_violation_ban_threshold',
  'charity_violation_window_ban_seconds',
  'charity_suspend_window_seconds',
  'charity_suspend_threshold',
  'charity_suspend_duration_seconds',
] as const;
export type PenaltyRole = 'admin' | 'steward';
export type PenaltyFilter = {
  type?: (typeof penaltyKinds)[number];
  state?: (typeof penaltyStates)[number];
};

export function normalizePenalty(value: unknown) {
  const r = record(
    value,
    ['id', 'kind', 'reason_code', 'started_at', 'ends_at', 'ended_at', 'state', 'result'],
    'penalty',
  );
  const result = {
    id: opaqueID(r.id, 'abc_', 'penalty id'),
    kind: oneOf(r.kind, penaltyKinds, 'penalty kind'),
    reason_code: oneOf(r.reason_code, reasons, 'penalty reason'),
    started_at: unixSecond(r.started_at, 'penalty start'),
    ends_at: nullableUnixSecond(r.ends_at, 'scheduled end'),
    ended_at: nullableUnixSecond(r.ended_at, 'actual end'),
    state: oneOf(r.state, penaltyStates, 'penalty state'),
    result: oneOf(
      r.result,
      ['applied', 'extended', 'expired', 'released', 'adjusted'] as const,
      'penalty result',
    ),
  };
  if (
    (result.state === 'active') !== (result.ended_at === null) ||
    (result.ended_at !== null && result.ended_at < result.started_at) ||
    (result.ends_at !== null && result.ends_at < result.started_at)
  )
    invalidResponse('penalty interval');
  return result;
}
export type Penalty = ReturnType<typeof normalizePenalty>;

export function normalizePenaltyAction(value: unknown) {
  const r = record(
    value,
    [
      'id',
      'action',
      'occurred_at',
      'request_id',
      'request_log_available',
      'operation_id',
      'actor_user_id',
      'previous_ends_at',
      'ends_at',
      'reason_code',
      'evidence_count',
    ],
    'penalty action',
  );
  const result = {
    id: decimalID(r.id, 'action id'),
    action: oneOf(
      r.action,
      ['trigger', 'extend', 'adjust', 'release', 'expire'] as const,
      'penalty action',
    ),
    occurred_at: unixSecond(r.occurred_at, 'action time'),
    request_id: nullableOpaqueID(r.request_id, 'req_', 'request id'),
    request_log_available: boolean(r.request_log_available, 'request availability'),
    operation_id: nullableOpaqueID(r.operation_id, 'op_', 'ledger operation'),
    actor_user_id: nullableDecimalID(r.actor_user_id, 'actor id'),
    previous_ends_at: nullableUnixSecond(r.previous_ends_at, 'previous end'),
    ends_at: nullableUnixSecond(r.ends_at, 'new end'),
    reason_code: oneOf(
      r.reason_code,
      [...reasons, 'manual_release', 'manual_adjustment', 'expired'] as const,
      'action reason',
    ),
    evidence_count: integer(r.evidence_count, 'evidence count', 0, 4096),
  };
  if (result.request_log_available && result.request_id === null)
    invalidResponse('request availability');
  return result;
}
export type PenaltyAction = ReturnType<typeof normalizePenaltyAction>;

function pageOf<T>(
  value: unknown,
  page: string,
  size: PageSize,
  decode: (v: unknown) => T,
  identity?: (v: T) => string,
) {
  const r = record(value, ['data', 'pagination'], 'penalty page');
  const pagination = normalizePageMetadata(r.pagination);
  const data = array(r.data, 'penalty rows', size).map(decode);
  validatePageResponse(pagination, page, size, data.length);
  if (identity && new Set(data.map(identity)).size !== data.length)
    invalidResponse('duplicate penalty row');
  return { data, pagination };
}

export function normalizePenaltyList(value: unknown, page: string, size: PageSize) {
  const r = record(value, ['data', 'pagination', 'legacy_details_unavailable'], 'penalty list');
  return {
    ...pageOf(
      { data: r.data, pagination: r.pagination },
      page,
      size,
      normalizePenalty,
      (c) => c.id,
    ),
    legacy_details_unavailable: boolean(r.legacy_details_unavailable, 'legacy penalty notice'),
  };
}
export function normalizePenaltyDetail(value: unknown, page: string, size: PageSize) {
  const r = record(value, ['case', 'actions'], 'penalty detail');
  return {
    case: normalizePenalty(r.case),
    actions: pageOf(r.actions, page, size, normalizePenaltyAction, (a) => a.id),
  };
}
export function normalizePenaltyEvidence(value: unknown, page: string, size: PageSize) {
  const r = record(value, ['rules', 'statistics', 'members'], 'penalty evidence');
  const rules = record(r.rules, ['version', ...ruleFields], 'rule snapshot', []);
  if (Object.keys(rules).length !== 0 && Object.keys(rules).length !== ruleFields.length + 1)
    invalidResponse('incomplete rule snapshot');
  const parsedRules: Partial<Record<(typeof ruleFields)[number] | 'version', number>> = {};
  if (Object.keys(rules).length) {
    parsedRules.version = integer(rules.version, 'rule version', 1, 1);
    for (const key of ruleFields) parsedRules[key] = integer(rules[key], 'rule value');
  }
  const stats = record(
    r.statistics,
    [
      'rule',
      'window_start',
      'window_end',
      'threshold',
      'count',
      'actual_chars',
      'minimum_chars',
      'direct_seconds',
    ],
    'penalty statistics',
    [],
  );
  const statistics: {
    rule?: string;
    window_start?: number;
    window_end?: number;
    threshold?: number;
    count?: number;
    actual_chars?: number;
    minimum_chars?: number;
    direct_seconds?: number;
  } = {};
  if (Object.keys(stats).length) {
    statistics.rule = oneOf(
      stats.rule,
      [
        'rpm_window',
        'short_content_direct',
        'short_content_window',
        'short_content_suspend_window',
        'short_content_deduction',
      ] as const,
      'statistic rule',
    );
    statistics.window_start = integer(stats.window_start, 'window start', -315360000, 253402300799);
    statistics.window_end = unixSecond(stats.window_end, 'window end');
    statistics.threshold = integer(stats.threshold, 'threshold');
    statistics.count = integer(stats.count, 'count', 0, 4096);
    for (const key of ['actual_chars', 'minimum_chars', 'direct_seconds'] as const)
      if (key in stats) statistics[key] = integer(stats[key], key);
    if (statistics.window_end < statistics.window_start) invalidResponse('statistic window');
  }
  const members = pageOf(
    r.members,
    page,
    size,
    (value) => {
      const e = record(
        value,
        ['occurred_at', 'request_id', 'violation_kind', 'content_chars', 'request_log_available'],
        'evidence member',
      );
      return {
        occurred_at: unixSecond(e.occurred_at, 'evidence time'),
        request_id: opaqueID(e.request_id, 'req_', 'evidence request'),
        violation_kind: oneOf(
          e.violation_kind,
          ['rpm', 'short_content'] as const,
          'violation kind',
        ),
        content_chars: nullableInteger(e.content_chars, 'content length'),
        request_log_available: boolean(e.request_log_available, 'request availability'),
      };
    },
    (e) => e.request_id,
  );
  return { rules: parsedRules, statistics, members };
}

function base(role: PenaltyRole, userID: string) {
  return `${role === 'admin' ? '/admin/api' : '/api/steward'}/users/${decimalID(userID, 'penalty owner')}/penalties`;
}
export function getPenalties(
  role: PenaltyRole,
  userID: string,
  page: string,
  size: PageSize,
  filter: PenaltyFilter,
  signal?: AbortSignal,
) {
  validateWindow(page, size);
  return decoded(
    queryPath(base(role, userID), { page, page_size: size, ...filter }),
    (v) => normalizePenaltyList(v, page, size),
    { signal },
  );
}
export function getPenalty(
  role: PenaltyRole,
  userID: string,
  caseID: string,
  page: string,
  size: PageSize,
  signal?: AbortSignal,
) {
  validateWindow(page, size);
  return decoded(
    queryPath(`${base(role, userID)}/${opaqueID(caseID, 'abc_', 'penalty id')}`, {
      page,
      page_size: size,
    }),
    (v) => normalizePenaltyDetail(v, page, size),
    { signal },
  );
}
export function getPenaltyEvidence(
  role: PenaltyRole,
  userID: string,
  caseID: string,
  actionID: string,
  page: string,
  size: PageSize,
  signal?: AbortSignal,
) {
  validateWindow(page, size);
  return decoded(
    queryPath(
      `${base(role, userID)}/${opaqueID(caseID, 'abc_', 'penalty id')}/actions/${decimalID(actionID, 'action id')}/evidence`,
      { page, page_size: size },
    ),
    (v) => normalizePenaltyEvidence(v, page, size),
    { signal },
  );
}

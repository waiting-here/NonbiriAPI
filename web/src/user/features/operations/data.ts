import { useQuery } from '@tanstack/react-query';
import { ApiError } from '@shared/query/http';
import { decoded, queryPath } from '@shared/operations/api';
import {
  amount,
  boolean,
  decimal,
  decimalID,
  integer,
  invalidResponse,
  nullableDecimal,
  nullableString,
  nullableUnixSecond,
  oneOf,
  opaqueID,
  page,
  record,
  string,
  unixSecond,
  type CursorPage,
} from '@shared/operations/wire';

export interface UserAuthority {
  id: string;
  username: string;
  lang: '' | 'zh' | 'en';
  effective_level: number;
  is_banned: boolean;
}

interface IssueCommon {
  id: string;
  state: 'current' | 'closed';
  safe_detail: string;
  deep_link: { route_id: 'endpoint-detail' | 'models'; resource_id: string } | null;
  first_seen_at: number;
  last_seen_at: number;
  count: string;
  closed_at: number | null;
}

type IssueTuple =
  | { source: 'model_discovery'; resource_kind: 'endpoint_key'; summary_code: 'discovery_failed' }
  | { source: 'routing_projection'; resource_kind: 'model'; summary_code: 'no_routable_binding' }
  | { source: 'resource_validator'; resource_kind: 'endpoint' | 'endpoint_key'; summary_code: 'credential_invalid' | 'configuration_invalid' };

export type Issue = IssueCommon & IssueTuple;

export interface IssuePage extends CursorPage<Issue> {
  projection_incomplete: boolean;
}

export interface AnnouncementSummary {
  epoch: string;
  id: string;
  revision: string;
  severity: 'info' | 'warning' | 'important';
  pinned: boolean;
  dismissible: boolean;
  published_at: number;
  expires_at: number | null;
  effective_language: 'zh' | 'en';
  fallback_from: 'zh' | 'en' | null;
  title: string;
  excerpt: string;
}

export interface AnnouncementDetail extends Omit<AnnouncementSummary, 'excerpt'> {
  rendered_body: string;
}

const USAGE_FIELDS = [
  'total_requests', 'total_uncached_input_tokens', 'total_cache_write_input_tokens',
  'total_cache_read_input_tokens', 'total_output_tokens', 'total_prompt_tokens',
  'total_completion_tokens', 'total_unknown_usage_requests',
] as const;

export function normalizeUserAuthority(value: unknown): UserAuthority {
  const envelope = record(value, ['user'], 'user session');
  const fields = [
    'id', 'username', 'avatar', 'avatar_url', 'guild_nick', 'guild_avatar_url', 'lang', 'is_banned',
    'banned_until', 'charity_suspended_until', 'endpoint_limit', 'effective_endpoint_limit', 'rpm_limit',
    'effective_rpm_limit', 'concurrency_limit', 'effective_concurrency_limit', 'balance', 'donation_credit',
    'effective_level', 'level_display_name', 'game_profile_public', 'created_at', 'updated_at', 'usage',
  ] as const;
  const user = record(envelope.user, fields, 'user session subject');
  nullableString(user.avatar, 'avatar', { max: 512, bytes: 2_048 });
  nullableString(user.avatar_url, 'avatar URL', { max: 4_096, bytes: 4_096 });
  nullableString(user.guild_nick, 'guild nickname', { max: 128, bytes: 512 });
  nullableString(user.guild_avatar_url, 'guild avatar URL', { max: 4_096, bytes: 4_096 });
  nullableUnixSecond(user.banned_until, 'ban deadline');
  nullableUnixSecond(user.charity_suspended_until, 'charity suspension deadline');
  nullableDecimal(user.endpoint_limit, 'endpoint limit');
  decimal(user.effective_endpoint_limit, 'effective endpoint limit');
  nullableDecimal(user.rpm_limit, 'RPM limit');
  decimal(user.effective_rpm_limit, 'effective RPM limit');
  nullableDecimal(user.concurrency_limit, 'concurrency limit');
  decimal(user.effective_concurrency_limit, 'effective concurrency limit');
  amount(user.balance, 'balance', true);
  amount(user.donation_credit, 'donation credit', false);
  string(user.level_display_name, 'level display name', { min: 1, max: 128, bytes: 512 });
  boolean(user.game_profile_public, 'game profile visibility');
  unixSecond(user.created_at, 'account creation time');
  unixSecond(user.updated_at, 'account update time');
  const usage = record(user.usage, USAGE_FIELDS, 'usage summary');
  for (const field of USAGE_FIELDS) decimal(usage[field], `usage ${field}`);
  return {
    id: decimalID(user.id, 'user id'),
    username: string(user.username, 'username', { min: 1, max: 128, bytes: 512 }),
    lang: oneOf(user.lang, ['', 'zh', 'en'] as const, 'user language'),
    effective_level: integer(user.effective_level, 'effective level', 1, 5),
    is_banned: boolean(user.is_banned, 'ban state'),
  };
}

export function normalizeIssue(value: unknown): Issue {
  const root = record(value, [
    'id', 'state', 'source', 'resource_kind', 'summary_code', 'safe_detail', 'deep_link',
    'first_seen_at', 'last_seen_at', 'count', 'closed_at',
  ], 'issue');
  const source = oneOf(root.source, ['model_discovery', 'routing_projection', 'resource_validator'] as const, 'issue source');
  const resourceKind = oneOf(root.resource_kind, ['endpoint', 'endpoint_key', 'model'] as const, 'issue resource kind');
  const summaryCode = oneOf(root.summary_code, ['discovery_failed', 'no_routable_binding', 'credential_invalid', 'configuration_invalid'] as const, 'issue summary');
  let tuple: IssueTuple;
  if (source === 'model_discovery' && resourceKind === 'endpoint_key' && summaryCode === 'discovery_failed') {
    tuple = { source, resource_kind: resourceKind, summary_code: summaryCode };
  } else if (source === 'routing_projection' && resourceKind === 'model' && summaryCode === 'no_routable_binding') {
    tuple = { source, resource_kind: resourceKind, summary_code: summaryCode };
  } else if (source === 'resource_validator' && (resourceKind === 'endpoint' || resourceKind === 'endpoint_key')
      && (summaryCode === 'credential_invalid' || summaryCode === 'configuration_invalid')) {
    tuple = { source, resource_kind: resourceKind, summary_code: summaryCode };
  } else {
    return invalidResponse('issue source tuple');
  }

  let deepLink: Issue['deep_link'] = null;
  if (root.deep_link !== null) {
    const link = record(root.deep_link, ['route_id', 'resource_id'], 'issue deep link');
    const routeID = oneOf(link.route_id, ['endpoint-detail', 'models'] as const, 'issue route id');
    const resourceID = decimalID(link.resource_id, 'issue resource id');
    if ((resourceKind === 'model') !== (routeID === 'models')) invalidResponse('issue deep link route');
    deepLink = { route_id: routeID, resource_id: resourceID };
  }
  const state = oneOf(root.state, ['current', 'closed'] as const, 'issue state');
  const closedAt = nullableUnixSecond(root.closed_at, 'issue close time');
  if ((state === 'current') !== (closedAt === null)) throw new ApiError('invalid_response', 'The server returned an invalid issue state.', 200);
  return {
    id: opaqueID(root.id, 'iss_', 'issue id'),
    state,
    ...tuple,
    safe_detail: string(root.safe_detail, 'issue detail', { max: 4_096, bytes: 4_096, multiline: true }),
    deep_link: deepLink,
    first_seen_at: unixSecond(root.first_seen_at, 'issue first seen time'),
    last_seen_at: unixSecond(root.last_seen_at, 'issue last seen time'),
    count: decimal(root.count, 'issue count', { positive: true }),
    closed_at: closedAt,
  };
}

export function normalizeIssuePage(value: unknown): IssuePage {
  const root = record(value, ['data', 'next_cursor', 'projection_incomplete'], 'issues');
  const base = page({ data: root.data, next_cursor: root.next_cursor }, 'issues', normalizeIssue);
  return { ...base, projection_incomplete: boolean(root.projection_incomplete, 'issue projection marker') };
}

function normalizeAnnouncementCommon(value: unknown, detail: boolean): AnnouncementSummary | AnnouncementDetail {
  const fields = [
    'epoch', 'id', 'revision', 'severity', 'pinned', 'dismissible', 'published_at', 'expires_at',
    'effective_language', 'fallback_from', 'title', detail ? 'rendered_body' : 'excerpt',
  ];
  const root = record(value, fields, detail ? 'announcement detail' : 'announcement summary');
  const common = {
    epoch: opaqueID(root.epoch, 'b1e_', 'announcement epoch'),
    id: opaqueID(root.id, 'ann_', 'announcement id'),
    revision: decimal(root.revision, 'announcement revision', { positive: true }),
    severity: oneOf(root.severity, ['info', 'warning', 'important'] as const, 'announcement severity'),
    pinned: boolean(root.pinned, 'announcement pinned state'),
    dismissible: boolean(root.dismissible, 'announcement dismissible state'),
    published_at: unixSecond(root.published_at, 'announcement publish time'),
    expires_at: nullableUnixSecond(root.expires_at, 'announcement expiry'),
    effective_language: oneOf(root.effective_language, ['zh', 'en'] as const, 'announcement language'),
    fallback_from: root.fallback_from === null ? null : oneOf(root.fallback_from, ['zh', 'en'] as const, 'announcement fallback language'),
    title: string(root.title, 'announcement title', { min: 1, max: 160, bytes: 640 }),
  };
  if (detail) {
    return { ...common, rendered_body: string(root.rendered_body, 'rendered announcement body', { max: 65_536, bytes: 65_536, multiline: true }) };
  }
  return { ...common, excerpt: string(root.excerpt, 'announcement excerpt', { max: 240, bytes: 960 }) };
}

export function normalizeAnnouncementSummary(value: unknown): AnnouncementSummary {
  return normalizeAnnouncementCommon(value, false) as AnnouncementSummary;
}

export function normalizeAnnouncementDetail(value: unknown): AnnouncementDetail {
  return normalizeAnnouncementCommon(value, true) as AnnouncementDetail;
}

export const operationsKeys = {
  root: ['user', 'operations'] as const,
  session: ['user', 'operations', 'session'] as const,
  issues: (state: 'current' | 'closed', cursor: string | null) => ['user', 'operations', 'issues', state, cursor] as const,
  announcements: (cursor: string | null) => ['user', 'operations', 'announcements', cursor] as const,
  announcement: (id: string) => ['user', 'operations', 'announcement', id] as const,
};

export function useUserAuthority(enabled = true) {
  return useQuery({
    queryKey: operationsKeys.session,
    queryFn: () => decoded('/api/session', normalizeUserAuthority),
    enabled,
    staleTime: 0,
    retry: false,
  });
}

export function useIssues(state: 'current' | 'closed', cursor: string | null, enabled = true) {
  return useQuery({
    queryKey: operationsKeys.issues(state, cursor),
    queryFn: () => decoded(queryPath('/api/issues', { state, cursor, limit: 20 }), normalizeIssuePage),
    enabled,
  });
}

export function useAnnouncements(cursor: string | null, enabled = true) {
  return useQuery({
    queryKey: operationsKeys.announcements(cursor),
    queryFn: async () => {
      const result = await decoded(
        queryPath('/api/announcements', { cursor, limit: 20 }),
        (value) => page(value, 'announcements', normalizeAnnouncementSummary),
      );
      return { ...result, data: [...result.data].sort((left, right) => Number(right.pinned) - Number(left.pinned) || right.published_at - left.published_at) };
    },
    enabled,
  });
}

export function useAnnouncement(id: string | undefined, enabled = true) {
  return useQuery({
    queryKey: operationsKeys.announcement(id ?? 'none'),
    queryFn: () => {
      if (!id) throw new ApiError('invalid_request', 'An announcement id is required.', 400);
      return decoded(`/api/announcements/${encodeURIComponent(opaqueID(id, 'ann_', 'announcement id'))}`, normalizeAnnouncementDetail);
    },
    enabled: enabled && Boolean(id),
  });
}

export interface CredentialReportInput {
  connector_type: 'openai-compatible' | 'anthropic-compatible';
  base_url: string;
  secret: string;
  note: string;
}

export const REPORT_ACCEPTED_MESSAGE = 'If matching credentials exist, temporary protection will be applied and an administrator will review the report.';

export function shouldRetainCredentialReportIntent(error: unknown): boolean {
  return error instanceof ApiError
    && (error.status === 0 || error.code === 'invalid_response' || error.status >= 500);
}

export async function submitCredentialReport(input: CredentialReportInput, idempotencyKey: string): Promise<void> {
  let response: Response;
  try {
    response = await fetch('/api/reports/credential-theft', {
      method: 'POST',
      credentials: 'same-origin',
      cache: 'no-store',
      headers: {
        Accept: 'application/json',
        'Cache-Control': 'no-store',
        'Content-Type': 'application/json',
        'Idempotency-Key': idempotencyKey,
      },
      body: JSON.stringify(input),
    });
  } catch {
    throw new ApiError('network_error', 'The network request failed.', 0);
  }
  if (response.status !== 202) {
    const status = response.status;
    throw new ApiError(status === 409 ? 'conflict' : status === 503 ? 'service_unavailable' : 'http_error',
      status === 409 ? 'This retry does not match the original submission.' : `Request failed (HTTP ${status}).`, status);
  }
  let payload: unknown;
  try {
    payload = await response.json();
  } catch {
    throw new ApiError('invalid_response', 'The server returned an invalid report receipt.', 202);
  }
  const root = record(payload, ['accepted', 'message'], 'report receipt');
  if (root.accepted !== true || root.message !== REPORT_ACCEPTED_MESSAGE
    || response.headers.get('X-Nonbiri-Report-Accepted') !== '1'
    || response.headers.get('Cache-Control')?.toLowerCase() !== 'no-store') {
    throw new ApiError('invalid_response', 'The server returned an invalid report receipt.', 202);
  }
}

export function announcementDismissalKey(accountID: string, value: Pick<AnnouncementSummary, 'epoch' | 'id' | 'revision'>): string {
  return `nonbiri:announcement-dismissed:${value.epoch}:${accountID}:${value.id}:${value.revision}`;
}

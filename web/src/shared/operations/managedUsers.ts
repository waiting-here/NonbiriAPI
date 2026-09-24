import { apiFetch } from '@shared/query/http';
import { decoded, idempotentOptions, queryPath } from './api';
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
  record,
  string,
  unixSecond,
} from './wire';
import {
  normalizeNumberedPage,
  validateWindow,
  validateText,
  invalidRequest,
  type NumberedPage,
} from './numberedPage';
import { isPageNumber, type PageSize } from './pageNumbers';

export type ManagementRole = 'admin' | 'steward';
export const managementRoot = (role: ManagementRole) =>
  [role === 'admin' ? 'admin' : 'user', 'operations'] as const;
const base = (role: ManagementRole) =>
  role === 'admin' ? '/admin/api/users' : '/api/steward/users';
const userPath = (role: ManagementRole, id: string) =>
  `${base(role)}/${encodeURIComponent(decimalID(id, 'user id'))}`;

export interface UsageSummary {
  total_requests: string;
  total_uncached_input_tokens: string;
  total_cache_write_input_tokens: string;
  total_cache_read_input_tokens: string;
  total_output_tokens: string;
  total_prompt_tokens: string;
  total_completion_tokens: string;
  total_unknown_usage_requests: string;
}

export function normalizeUsageSummary(value: unknown): UsageSummary {
  const fields = [
    'total_requests',
    'total_uncached_input_tokens',
    'total_cache_write_input_tokens',
    'total_cache_read_input_tokens',
    'total_output_tokens',
    'total_prompt_tokens',
    'total_completion_tokens',
    'total_unknown_usage_requests',
  ] as const;
  const root = record(value, fields, 'usage summary');
  const result = Object.fromEntries(
    fields.map((key) => [key, decimal(root[key], `usage ${key}`)]),
  ) as unknown as UsageSummary;
  if (
    BigInt(result.total_prompt_tokens) !==
      BigInt(result.total_uncached_input_tokens) +
        BigInt(result.total_cache_write_input_tokens) +
        BigInt(result.total_cache_read_input_tokens) ||
    BigInt(result.total_completion_tokens) !== BigInt(result.total_output_tokens)
  ) {
    invalidResponse('usage derived totals');
  }
  return result;
}

export interface AdminUser {
  id: string;
  discord_id: string | null;
  username: string;
  avatar_url: string | null;
  guild_nick: string | null;
  guild_avatar_url: string | null;
  is_admin: boolean;
  is_banned: boolean;
  banned_reason: string;
  banned_until: number | null;
  charity_suspended_until: number | null;
  endpoint_limit: string | null;
  effective_endpoint_limit: string;
  rpm_limit: string | null;
  effective_rpm_limit: string;
  concurrency_limit: string | null;
  effective_concurrency_limit: string;
  lang: '' | 'zh' | 'en';
  balance: string;
  game_balance: string;
  donation_credit: string;
  level: { manual: number | null; automatic: number; effective: number; display_name: string };
  game_profile_public: boolean;
  revision: string;
  usage: UsageSummary;
  created_at: number;
  updated_at: number;
}

export function normalizeAdminUser(value: unknown): AdminUser {
  const root = record(
    value,
    [
      'id',
      'discord_id',
      'username',
      'avatar_url',
      'guild_nick',
      'guild_avatar_url',
      'is_admin',
      'is_banned',
      'banned_reason',
      'banned_until',
      'charity_suspended_until',
      'endpoint_limit',
      'effective_endpoint_limit',
      'rpm_limit',
      'effective_rpm_limit',
      'concurrency_limit',
      'effective_concurrency_limit',
      'lang',
      'balance',
      'game_balance',
      'donation_credit',
      'level',
      'game_profile_public',
      'revision',
      'usage',
      'created_at',
      'updated_at',
    ],
    'administrator user',
  );
  const level = record(
    root.level,
    ['manual', 'automatic', 'effective', 'display_name'],
    'administrator user level',
  );
  const automatic = integer(level.automatic, 'automatic level', 1, 4);
  const effective = integer(level.effective, 'effective level', 1, 6);
  const manual = level.manual === null ? null : integer(level.manual, 'manual level', 1, 6);
  const isBanned = boolean(root.is_banned, 'user banned state');
  const bannedUntil = nullableUnixSecond(root.banned_until, 'ban expiry');
  const bannedReason = string(root.banned_reason, 'ban reason', {
    max: 1_024,
    bytes: 4_096,
    multiline: true,
  });
  if (!isBanned && (bannedUntil !== null || bannedReason !== '')) invalidResponse('user ban state');
  return {
    id: decimalID(root.id, 'administrator user id'),
    discord_id: nullableString(root.discord_id, 'Discord id', {
      max: 128,
      bytes: 128,
      ascii: true,
    }),
    username: string(root.username, 'username', { min: 1, max: 128, bytes: 512 }),
    avatar_url: nullableString(root.avatar_url, 'avatar URL', { max: 4_096, bytes: 4_096 }),
    guild_nick: nullableString(root.guild_nick, 'guild nickname', { max: 128, bytes: 512 }),
    guild_avatar_url: nullableString(root.guild_avatar_url, 'guild avatar URL', {
      max: 4_096,
      bytes: 4_096,
    }),
    is_admin: boolean(root.is_admin, 'administrator marker'),
    is_banned: isBanned,
    banned_reason: bannedReason,
    banned_until: bannedUntil,
    charity_suspended_until: nullableUnixSecond(
      root.charity_suspended_until,
      'charity suspension expiry',
    ),
    endpoint_limit: nullableDecimal(root.endpoint_limit, 'endpoint limit'),
    effective_endpoint_limit: decimal(root.effective_endpoint_limit, 'effective endpoint limit'),
    rpm_limit: nullableDecimal(root.rpm_limit, 'RPM limit'),
    effective_rpm_limit: decimal(root.effective_rpm_limit, 'effective RPM limit'),
    concurrency_limit: nullableDecimal(root.concurrency_limit, 'concurrency limit'),
    effective_concurrency_limit: decimal(
      root.effective_concurrency_limit,
      'effective concurrency limit',
    ),
    lang: oneOf(root.lang, ['', 'zh', 'en'] as const, 'user language'),
    balance: amount(root.balance, 'user balance'),
    game_balance: amount(root.game_balance, 'user game balance'),
    donation_credit: amount(root.donation_credit, 'donation credit', false),
    level: {
      manual,
      automatic,
      effective,
      display_name: string(level.display_name, 'level display name', { max: 64, bytes: 256 }),
    },
    game_profile_public: boolean(root.game_profile_public, 'game profile setting'),
    revision: decimal(root.revision, 'user revision', { positive: true }),
    usage: normalizeUsageSummary(root.usage),
    created_at: unixSecond(root.created_at, 'user creation time'),
    updated_at: unixSecond(root.updated_at, 'user update time'),
  };
}

export const managedUserKeys = {
  list: (
    role: ManagementRole,
    account: string,
    banned: string,
    query: string,
    level: string,
    page: string,
    size: PageSize,
  ) => [...managementRoot(role), 'users', account, banned, query, level, page, size] as const,
  detail: (role: ManagementRole, account: string, id: string) =>
    [...managementRoot(role), 'user', account, id] as const,
};

export async function getManagedUsersPage(
  role: ManagementRole,
  banned: '' | 'true' | 'false',
  query: string,
  level: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<NumberedPage<AdminUser>> {
  validateWindow(page, pageSize);
  validateText(query, 512, true);
  if (!['', 'true', 'false'].includes(banned) || !['', '1', '2', '3', '4', '5'].includes(level))
    invalidRequest();
  return decoded(
    queryPath(base(role), {
      is_banned: banned || undefined,
      q: query || undefined,
      level: level || undefined,
      page,
      page_size: pageSize,
    }),
    (value) =>
      normalizeNumberedPage(
        value,
        'managed user page',
        normalizeAdminUser,
        page,
        pageSize,
        (user) => user.id,
      ),
    { signal },
  );
}

export async function getManagedUserDetail(
  role: ManagementRole,
  id: string,
  signal?: AbortSignal,
): Promise<AdminUser> {
  if (!isPageNumber(id, 9_223_372_036_854_775_807n)) invalidRequest();
  return decoded(
    userPath(role, id),
    (value) => {
      const user = normalizeAdminUser(value);
      if (user.id !== id) invalidResponse('managed user identity');
      return user;
    },
    { signal },
  );
}

export function mutateManagedUser(
  role: ManagementRole,
  id: string,
  body: unknown,
  key: string,
): Promise<AdminUser> {
  return decoded(
    userPath(role, id),
    normalizeAdminUser,
    idempotentOptions(key, { method: 'PATCH', json: body }),
  );
}
export async function banManagedUser(
  role: ManagementRole,
  id: string,
  body: unknown,
  key: string,
): Promise<void> {
  await apiFetch<void>(
    `${userPath(role, id)}/ban`,
    idempotentOptions(key, { method: 'POST', json: body }),
  );
}
export async function unbanManagedUser(
  role: ManagementRole,
  id: string,
  revision: string,
  key: string,
): Promise<void> {
  await apiFetch<void>(
    `${userPath(role, id)}/unban`,
    idempotentOptions(key, { method: 'POST', json: { expected_revision: revision } }),
  );
}

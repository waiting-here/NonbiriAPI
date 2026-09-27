import { apiFetch } from '@shared/query/http';
import { decoded, idempotentOptions, queryPath } from './api';
import {
  amount,
  array,
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

export type AccountState = 'all' | 'active' | 'deleted';
export interface DeletedAccount {
  record_id: string;
  former_user_id: string | null;
  discord_id: string | null;
  snapshot_version: 1 | 2;
  registered_at: number | null;
  deleted_at: number | null;
  effective_level: number | null;
  ban: { state: 'known' | 'unknown'; active_at_deletion: boolean | null; reason: string | null; until: number | null };
  charity_pause: { state: 'known' | 'unknown'; active_at_deletion: boolean | null; reason: string | null; until: number | null };
  source: 'unknown' | 'self' | 'admin' | 'system';
  actor_user_id: string | null;
  blacklist_action: 'unknown' | 'none' | 'added' | 'appended';
  blacklist_reason_codes: ('deletion_penalty_evasion' | 'deletion_debt_evasion')[];
  general_balance: string | null;
  game_balance: string | null;
  donation_credit: string | null;
  sketch_paper: string | null;
  sketch_brush: string | null;
  alert_id?: string;
}
export type ManagedAccount =
  | { account_state: 'active'; user: AdminUser }
  | { account_state: 'deleted'; deleted: DeletedAccount };

function normalizeDeletionPenalty(value: unknown, label: string): DeletedAccount['ban'] {
  const root = record(value, ['state', 'active_at_deletion', 'reason', 'until'], label);
  return {
    state: oneOf(root.state, ['known', 'unknown'] as const, `${label} state`),
    active_at_deletion: root.active_at_deletion === null ? null : boolean(root.active_at_deletion, `${label} active`),
    reason: nullableString(root.reason, `${label} reason`, { max: 1024, bytes: 4096, multiline: true }),
    until: nullableUnixSecond(root.until, `${label} until`),
  };
}

export function normalizeDeletedAccount(value: unknown): DeletedAccount {
  const fields = ['record_id', 'former_user_id', 'discord_id', 'snapshot_version', 'registered_at', 'deleted_at', 'effective_level', 'ban', 'charity_pause', 'source', 'actor_user_id', 'blacklist_action', 'blacklist_reason_codes', 'general_balance', 'game_balance', 'donation_credit', 'sketch_paper', 'sketch_brush'];
  const root = record(value, [...fields, 'alert_id'], 'deleted account', fields);
  const nullableID = (input: unknown, label: string) => input === null ? null : decimalID(input, label);
  const nullableAmount = (input: unknown, label: string) => input === null ? null : amount(input, label);
  return {
    record_id: decimalID(root.record_id, 'record id'),
    former_user_id: nullableID(root.former_user_id, 'former user id'),
    discord_id: nullableString(root.discord_id, 'former Discord id', { max: 128, bytes: 128 }),
    snapshot_version: integer(root.snapshot_version, 'snapshot version', 1, 2) as 1 | 2,
    registered_at: nullableUnixSecond(root.registered_at, 'registration time'),
    deleted_at: nullableUnixSecond(root.deleted_at, 'deletion time'),
    effective_level: root.effective_level === null ? null : integer(root.effective_level, 'former level', 1, 6),
    ban: normalizeDeletionPenalty(root.ban, 'former ban'),
    charity_pause: normalizeDeletionPenalty(root.charity_pause, 'former charity pause'),
    source: oneOf(root.source, ['unknown', 'self', 'admin', 'system'] as const, 'deletion source'),
    actor_user_id: nullableID(root.actor_user_id, 'deletion actor'),
    blacklist_action: oneOf(root.blacklist_action, ['unknown', 'none', 'added', 'appended'] as const, 'blacklist action'),
    blacklist_reason_codes: array(root.blacklist_reason_codes, 'blacklist reasons', 2).map((code) => oneOf(code, ['deletion_penalty_evasion', 'deletion_debt_evasion'] as const, 'blacklist reason')),
    general_balance: nullableAmount(root.general_balance, 'former general balance'),
    game_balance: nullableAmount(root.game_balance, 'former game balance'),
    donation_credit: nullableAmount(root.donation_credit, 'former donation credit'),
    sketch_paper: nullableAmount(root.sketch_paper, 'former sketch paper'),
    sketch_brush: nullableAmount(root.sketch_brush, 'former sketch brush'),
    ...(root.alert_id === undefined ? {} : { alert_id: decimalID(root.alert_id, 'deletion alert') }),
  };
}

function normalizeManagedAccount(value: unknown): ManagedAccount {
  if (value !== null && typeof value === 'object' && !Array.isArray(value) && 'account_state' in value) {
    const fields = value as Record<string, unknown>;
    if (fields.account_state === 'deleted') {
      const row = record(value, ['account_state', 'deleted'], 'deleted account row');
      return { account_state: 'deleted', deleted: normalizeDeletedAccount(row.deleted) };
    }
    if (fields.account_state !== 'active') invalidResponse('account state');
    const active = { ...fields };
    delete active.account_state;
    return { account_state: 'active', user: normalizeAdminUser(active) };
  }
  return { account_state: 'active', user: normalizeAdminUser(value) };
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
    userID: string,
    page: string,
    size: PageSize,
    state: AccountState = 'active',
    discordID = '',
  ) => [...managementRoot(role), 'users', account, banned, query, level, userID, page, size, state, discordID] as const,
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
  userID = '',
): Promise<NumberedPage<AdminUser>> {
  validateWindow(page, pageSize);
  validateText(query, 512, true);
  if (
    !['', 'true', 'false'].includes(banned) ||
    !['', '1', '2', '3', '4', '5', '6'].includes(level) ||
    (userID !== '' && !isPageNumber(userID, 9_223_372_036_854_775_807n))
  )
    invalidRequest();
  return decoded(
    queryPath(base(role), {
      is_banned: banned || undefined,
      account_state: 'active',
      q: query || undefined,
      level: level || undefined,
      user_id: userID || undefined,
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

export async function getManagedAccountsPage(
  role: ManagementRole,
  state: AccountState,
  banned: '' | 'true' | 'false',
  query: string,
  level: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
  userID = '',
  discordID = '',
): Promise<NumberedPage<ManagedAccount>> {
  validateWindow(page, pageSize);
  validateText(query, 512, true);
  if (!['all', 'active', 'deleted'].includes(state) || !['', 'true', 'false'].includes(banned) || !['', '1', '2', '3', '4', '5', '6'].includes(level) ||
      (userID !== '' && !isPageNumber(userID, 9_223_372_036_854_775_807n)) ||
      (discordID !== '' && !isPageNumber(discordID, 18_446_744_073_709_551_615n))) invalidRequest();
  return decoded(
    queryPath(base(role), { account_state: state, is_banned: banned || undefined, q: query || undefined,
      level: level || undefined, user_id: userID || undefined, discord_id: discordID || undefined, page, page_size: pageSize }),
    (value) => normalizeNumberedPage(value, 'managed account page', normalizeManagedAccount, page, pageSize,
      (row) => row.account_state === 'active' ? `active:${row.user.id}` : `deleted:${row.deleted.record_id}`),
    { signal },
  );
}

export async function getDeletedAccountDetail(role: ManagementRole, recordID: string, signal?: AbortSignal): Promise<DeletedAccount> {
  if (!isPageNumber(recordID, 9_223_372_036_854_775_807n)) invalidRequest();
  return decoded(`${base(role)}/deleted/${encodeURIComponent(recordID)}`,
    (value) => { const result = normalizeDeletedAccount(value); if (result.record_id !== recordID) invalidResponse('deleted account identity'); return result; }, { signal });
}

export interface DeletionDuelAbort {
  id: string;
  discord_id: string;
  game_key: 'bidding' | 'likes';
  match_id: string;
  former_user_id: string;
  reason: 'self_deletion_cancelled_match';
  occurred_at: number;
}

export function getDeletionDuelAborts(discordID: string, page: string, signal?: AbortSignal): Promise<NumberedPage<DeletionDuelAbort>> {
  validateWindow(page, 20);
  if (!/^[1-9][0-9]{0,19}$/.test(discordID) || BigInt(discordID) > (1n << 64n) - 1n) invalidRequest();
  return decoded(queryPath('/admin/api/users/deletion-duel-aborts', { discord_id: discordID, page, page_size: 20 }),
    (value) => normalizeNumberedPage(value, 'deletion duel aborts', (candidate) => {
      const row = record(candidate, ['id', 'discord_id', 'game_key', 'match_id', 'former_user_id', 'reason', 'occurred_at'], 'deletion duel abort');
      const result: DeletionDuelAbort = {
        id: decimalID(row.id, 'duel abort id'),
        discord_id: string(row.discord_id, 'duel abort Discord id', { max: 20, ascii: true }),
        game_key: oneOf(row.game_key, ['bidding', 'likes'] as const, 'duel abort game'),
        match_id: string(row.match_id, 'duel abort match id', { min: 1, max: 128, bytes: 128 }),
        former_user_id: decimalID(row.former_user_id, 'duel abort former user id'),
        reason: oneOf(row.reason, ['self_deletion_cancelled_match'] as const, 'duel abort reason'),
        occurred_at: unixSecond(row.occurred_at, 'duel abort time'),
      };
      if (result.discord_id !== discordID) invalidResponse('duel abort identity');
      return result;
    }, page, 20, (row) => row.id), { signal });
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

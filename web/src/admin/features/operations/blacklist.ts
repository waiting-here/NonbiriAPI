import { apiFetch } from '@shared/query/http';
import type { ManagementRole } from '@shared/operations/managedUsers';
import { decoded, idempotentOptions, queryPath } from '@shared/operations/api';
import {
  array,
  decimalID,
  invalidResponse,
  oneOf,
  record,
  string,
  unixSecond,
} from '@shared/operations/wire';
import {
  normalizePageMetadata,
  validatePageResponse,
  type PageSize,
} from '@shared/operations/pageNumbers';

export function validDiscordID(value: string): boolean {
  return /^[1-9][0-9]{0,19}$/.test(value) && BigInt(value) <= (1n << 64n) - 1n;
}
export interface BlacklistEntry {
  discord_id: string;
  reason: string;
  created_at: number;
  user_id: string | null;
  first_actor_kind: 'admin' | 'steward6' | 'automatic' | 'unknown';
  first_actor_user_id: string | null;
}
function entry(value: unknown): BlacklistEntry {
  const root = record(
    value,
    ['discord_id', 'reason', 'created_at', 'user_id', 'first_actor_kind', 'first_actor_user_id'],
    'blacklist entry',
    ['discord_id', 'reason', 'created_at', 'user_id'],
  );
  const id = string(root.discord_id, 'Discord ID');
  if (!validDiscordID(id)) invalidResponse('Discord ID');
  return {
    discord_id: id,
    reason: string(root.reason, 'blacklist reason', { min: 1, multiline: true }),
    created_at: unixSecond(root.created_at, 'blacklist creation time'),
    user_id: root.user_id === null ? null : decimalID(root.user_id, 'blacklisted user'),
    first_actor_kind:
      root.first_actor_kind === undefined
        ? 'unknown'
        : oneOf(
            root.first_actor_kind,
            ['admin', 'steward6', 'automatic', 'unknown'],
            'first actor',
          ),
    first_actor_user_id:
      root.first_actor_user_id === undefined || root.first_actor_user_id === null
        ? null
        : decimalID(root.first_actor_user_id, 'first actor user'),
  };
}
export const blacklistKeys = ['admin', 'blacklist'] as const;
const base = (role: ManagementRole) =>
  role === 'admin' ? '/admin/api/blacklist' : '/api/steward/blacklist';
export function getBlacklist(
  role: ManagementRole,
  page: string,
  pageSize: PageSize,
  q: string,
  actorKind: string,
  actorUserID: string,
  discordID: string,
  signal?: AbortSignal,
) {
  return decoded(
    queryPath(base(role), {
      page,
      page_size: pageSize,
      q: q || undefined,
      actor_kind: actorKind || undefined,
      actor_user_id: actorUserID || undefined,
      discord_id: discordID || undefined,
    }),
    (value) => {
      const root = record(value, ['data', 'next_cursor', 'pagination'], 'blacklist');
      if (root.next_cursor !== null) invalidResponse('blacklist cursor');
      const data = array(root.data, 'blacklist entries', 100).map(entry);
      const pagination = normalizePageMetadata(root.pagination);
      validatePageResponse(pagination, page, pageSize);
      if (
        new Set(data.map((item) => item.discord_id)).size !== data.length ||
        (discordID && data.some((item) => item.discord_id !== discordID))
      )
        invalidResponse('blacklist filter');
      return { data, pagination };
    },
    { signal },
  );
}
export function addBlacklist(
  role: ManagementRole,
  discord_id: string,
  reason: string,
  key: string,
  signal?: AbortSignal,
) {
  return apiFetch<void>(
    base(role),
    idempotentOptions(key, { method: 'POST', json: { discord_id, reason }, signal }),
  );
}
export function removeBlacklist(id: string, key: string, signal?: AbortSignal) {
  return apiFetch<void>(
    `/admin/api/blacklist/${encodeURIComponent(id)}/remove`,
    idempotentOptions(key, { method: 'POST', signal }),
  );
}

export interface BlacklistEvent {
  id: string;
  actor_kind: BlacklistEntry['first_actor_kind'];
  actor_user_id: string | null;
  reason_codes: string[];
  safe_note: string;
  created_at: number;
}
export function getBlacklistEvents(
  role: ManagementRole,
  discordID: string,
  page: string,
  signal?: AbortSignal,
) {
  return decoded(
    queryPath(`${base(role)}/${encodeURIComponent(discordID)}/events`, { page, page_size: 20 }),
    (value) => {
      const root = record(value, ['data', 'next_cursor', 'pagination'], 'blacklist events');
      if (root.next_cursor !== null) invalidResponse('blacklist event cursor');
      const data = array(root.data, 'blacklist events', 20).map((candidate): BlacklistEvent => {
        const row = record(
          candidate,
          ['id', 'actor_kind', 'actor_user_id', 'reason_codes', 'safe_note', 'created_at'],
          'blacklist event',
        );
        return {
          id: decimalID(row.id, 'blacklist event id'),
          actor_kind: oneOf(
            row.actor_kind,
            ['admin', 'steward6', 'automatic', 'unknown'],
            'blacklist event actor',
          ),
          actor_user_id:
            row.actor_user_id === null
              ? null
              : decimalID(row.actor_user_id, 'blacklist event actor id'),
          reason_codes: array(row.reason_codes, 'blacklist event reasons', 2).map((code) =>
            string(code, 'blacklist reason code', { ascii: true }),
          ),
          safe_note: string(row.safe_note, 'blacklist event note', { min: 1, multiline: true }),
          created_at: unixSecond(row.created_at, 'blacklist event time'),
        };
      });
      const pagination = normalizePageMetadata(root.pagination);
      validatePageResponse(pagination, page, 20);
      return { data, pagination };
    },
    { signal },
  );
}

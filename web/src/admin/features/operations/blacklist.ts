import { apiFetch } from '@shared/query/http';
import { decoded, idempotentOptions, queryPath } from '@shared/operations/api';
import {
  array,
  decimalID,
  invalidResponse,
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
}
function entry(value: unknown): BlacklistEntry {
  const root = record(value, ['discord_id', 'reason', 'created_at', 'user_id'], 'blacklist entry');
  const id = string(root.discord_id, 'Discord ID', { max: 20 });
  if (!validDiscordID(id)) invalidResponse('Discord ID');
  return {
    discord_id: id,
    reason: string(root.reason, 'blacklist reason', { min: 1, max: 1024, bytes: 4096 }),
    created_at: unixSecond(root.created_at, 'blacklist creation time'),
    user_id: root.user_id === null ? null : decimalID(root.user_id, 'blacklisted user'),
  };
}
export const blacklistKeys = ['admin', 'blacklist'] as const;
export function getBlacklist(page: string, pageSize: PageSize, q: string, signal?: AbortSignal) {
  return decoded(
    queryPath('/admin/api/blacklist', { page, page_size: pageSize, q: q || undefined }),
    (value) => {
      const root = record(value, ['data', 'next_cursor', 'pagination'], 'blacklist');
      if (root.next_cursor !== null) invalidResponse('blacklist cursor');
      const data = array(root.data, 'blacklist entries', 100).map(entry);
      const pagination = normalizePageMetadata(root.pagination);
      validatePageResponse(pagination, page, pageSize, data.length);
      if (
        new Set(data.map((item) => item.discord_id)).size !== data.length ||
        (q && data.some((item) => item.discord_id !== q))
      )
        invalidResponse('blacklist filter');
      return { data, pagination };
    },
    { signal },
  );
}
export function addBlacklist(discord_id: string, reason: string, key: string) {
  return apiFetch<void>(
    '/admin/api/blacklist',
    idempotentOptions(key, { method: 'POST', json: { discord_id, reason } }),
  );
}
export function removeBlacklist(id: string, key: string) {
  return apiFetch<void>(
    `/admin/api/blacklist/${encodeURIComponent(id)}/remove`,
    idempotentOptions(key, { method: 'POST' }),
  );
}

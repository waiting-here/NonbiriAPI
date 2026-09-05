import { decoded, queryPath } from '@shared/operations/api';
import {
  amount,
  boolean,
  cursor,
  decimal,
  decimalID,
  invalidResponse,
  integer,
  nullableDecimalID,
  nullableString,
  nullableUnixSecond,
  opaqueID,
  oneOf,
  page,
  record,
  string,
  unixSecond,
  type CursorPage,
} from '@shared/operations/wire';

/**
 * Administrator-only donation projection used by the grouped charity page.
 *
 * The server's administrator response contains reviewer fields and the
 * physical endpoint-key reference because other administrator operations may
 * need them.  This feature deliberately decodes the response into a smaller
 * read model.  In particular, source endpoint IDs, safe notes, fingerprints,
 * ciphertext and donor text never enter this query's data cache.
 */
export const ADMIN_CHARITY_STATUSES = ['pending', 'approved', 'rejected', 'deleted', 'expired'] as const;
export type AdminCharityStatus = (typeof ADMIN_CHARITY_STATUSES)[number];

export const ADMIN_CHARITY_KEY_STATES = [
  'pending',
  'available',
  'disabled',
  'suspended',
  'exhausted',
  'expired',
  'ended',
] as const;
export type AdminCharityKeyState = (typeof ADMIN_CHARITY_KEY_STATES)[number];

export const ADMIN_CHARITY_CATEGORIES = ['subscription', 'api_platform'] as const;
export type AdminCharityCategory = (typeof ADMIN_CHARITY_CATEGORIES)[number];

export const ADMIN_CHARITY_CONNECTOR_TYPES = [
  'openai-compatible',
  'anthropic-compatible',
] as const;
export type AdminCharityConnectorType = (typeof ADMIN_CHARITY_CONNECTOR_TYPES)[number];

export const ADMIN_CHARITY_ENDED_REASONS = [
  'withdrawn',
  'terminated',
  'expired',
  'member_removed',
  'account_deleted',
] as const;
export type AdminCharityEndedReason = (typeof ADMIN_CHARITY_ENDED_REASONS)[number];

function hasForbiddenControl(value: string): boolean {
  return Array.from(value).some((character) => {
    const point = character.codePointAt(0) ?? 0;
    return point < 0x20 || (point >= 0x7f && point <= 0x9f);
  });
}

function channelName(value: unknown): string {
  const name = string(value, 'administrator charity channel name', { min: 1, max: 128 });
  if (name.trim() !== name || hasForbiddenControl(name)) {
    invalidResponse('administrator charity channel name');
  }
  return name;
}

function channelURL(value: unknown): string {
  const url = string(value, 'administrator charity source URL', {
    min: 1,
    max: 4_096,
    bytes: 4_096,
  });
  if (hasForbiddenControl(url)) invalidResponse('administrator charity source URL');
  return url;
}

function validateAdminOwner(value: unknown): void {
  if (value === null) return;
  const root = record(
    value,
    ['user_id', 'discord_id', 'display_name'],
    'administrator charity owner',
  );
  decimalID(root.user_id, 'administrator charity owner ID');
  nullableString(root.discord_id, 'administrator charity owner Discord ID', {
    min: 1,
    max: 64,
    bytes: 64,
    ascii: true,
  });
  string(root.display_name, 'administrator charity owner display name', {
    max: 128,
    bytes: 512,
  });
}

function validateAdminReviewer(value: unknown): boolean {
  if (value === null) return false;
  const root = record(value, ['user_id', 'role'], 'administrator charity reviewer');
  nullableDecimalID(root.user_id, 'administrator charity reviewer ID');
  oneOf(root.role, ['admin', 'steward', 'level5'] as const, 'administrator charity reviewer role');
  return true;
}

export interface AdminCharityCustomSource {
  kind: 'custom';
  connector_type: AdminCharityConnectorType;
  base_url: string;
}

export interface AdminCharityMainstreamSource {
  kind: 'mainstream';
  channel_id: string;
  channel_revision: string;
  connector_type: AdminCharityConnectorType;
  base_url: string;
  name: string;
  category: AdminCharityCategory;
}

export type AdminCharitySource = AdminCharityCustomSource | AdminCharityMainstreamSource;

export interface AdminCharityKey {
  id: string;
  display_head: string;
  display_tail: string;
  safe_source: AdminCharitySource;
  physical_enabled: boolean;
  charity_state: AdminCharityKeyState;
  limits: { price: string | null; calls: string | null; tokens: string | null };
  usage: {
    price_used: string;
    price_inflight: string;
    calls_used: string;
    calls_inflight: string;
    tokens_used: string;
    tokens_inflight: string;
  };
  token_reserve: number;
  authorized_expires_at: number | null;
  expires_at: number | null;
  streak: { generation: string; count: string; failure_disabled: boolean };
  ended_reason: AdminCharityEndedReason | null;
}

export interface AdminCharityDonation {
  id: string;
  status: AdminCharityStatus;
  revision: string;
  keys: AdminCharityKey[];
  created_at: number;
  updated_at: number;
}

function normalizeCustomSource(value: unknown): AdminCharityCustomSource {
  const root = record(value, ['kind', 'connector_type', 'base_url'], 'administrator custom charity source');
  if (root.kind !== 'custom') invalidResponse('administrator custom charity source kind');
  return {
    kind: 'custom',
    connector_type: oneOf(
      root.connector_type,
      ADMIN_CHARITY_CONNECTOR_TYPES,
      'administrator charity connector',
    ),
    base_url: channelURL(root.base_url),
  };
}

function normalizeMainstreamSource(value: unknown): AdminCharityMainstreamSource {
  const root = record(
    value,
    ['kind', 'connector_type', 'base_url', 'channel_id', 'name', 'channel_revision', 'category'],
    'administrator mainstream charity source',
  );
  if (root.kind !== 'mainstream') invalidResponse('administrator mainstream charity source kind');
  return {
    kind: 'mainstream',
    channel_id: opaqueID(root.channel_id, 'mch_', 'administrator charity channel ID'),
    channel_revision: decimal(root.channel_revision, 'administrator charity channel revision', {
      positive: true,
    }),
    connector_type: oneOf(
      root.connector_type,
      ADMIN_CHARITY_CONNECTOR_TYPES,
      'administrator charity connector',
    ),
    base_url: channelURL(root.base_url),
    name: channelName(root.name),
    category: oneOf(root.category, ADMIN_CHARITY_CATEGORIES, 'administrator charity channel category'),
  };
}

export function normalizeAdminCharitySource(value: unknown): AdminCharitySource {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    invalidResponse('administrator charity source');
  }
  const kind = (value as Record<string, unknown>).kind;
  if (kind === 'custom') return normalizeCustomSource(value);
  if (kind === 'mainstream') return normalizeMainstreamSource(value);
  invalidResponse('administrator charity source kind');
}

function normalizeAdminCharityKey(value: unknown): AdminCharityKey {
  // The allow-list mirrors the server wire, but forbidden reviewer/physical
  // identity fields are intentionally not read into the returned object.
  const root = record(
    value,
    [
      'id',
      'endpoint_key_id',
      'display_head',
      'display_tail',
      'safe_source',
      'physical_enabled',
      'charity_state',
      'limits',
      'usage',
      'token_reserve',
      'expires_at',
      'streak',
      'ended_reason',
      'authorized_expires_at',
      'safe_note',
    ],
    'administrator charity key',
  );
  const id = decimalID(root.id, 'administrator charity key ID');
  nullableDecimalID(root.endpoint_key_id, 'administrator charity source endpoint key ID');
  const displayHead = string(root.display_head, 'administrator charity key head', {
    max: 16,
    bytes: 16,
    ascii: true,
  });
  const displayTail = string(root.display_tail, 'administrator charity key tail', {
    max: 16,
    bytes: 16,
    ascii: true,
  });
  string(root.safe_note, 'administrator charity safe note', {
    max: 256,
    bytes: 1_024,
    multiline: true,
  });
  const limits = record(root.limits, ['price', 'calls', 'tokens'], 'administrator charity key limits');
  const usage = record(
    root.usage,
    [
      'price_used',
      'price_inflight',
      'calls_used',
      'calls_inflight',
      'tokens_used',
      'tokens_inflight',
    ],
    'administrator charity key usage',
  );
  const streak = record(
    root.streak,
    ['generation', 'count', 'failure_disabled'],
    'administrator charity key streak',
  );
  const state = oneOf(root.charity_state, ADMIN_CHARITY_KEY_STATES, 'administrator charity key state');
  const endedReason = root.ended_reason === null
    ? null
    : oneOf(root.ended_reason, ADMIN_CHARITY_ENDED_REASONS, 'administrator charity key ended reason');
  const authorizedExpiresAt = nullableUnixSecond(
    root.authorized_expires_at,
    'administrator charity authorized expiry',
  );
  const expiresAt = nullableUnixSecond(root.expires_at, 'administrator charity effective expiry');
  if (authorizedExpiresAt !== null && (expiresAt === null || expiresAt > authorizedExpiresAt)) {
    invalidResponse('administrator charity expiry bounds');
  }
  if (
    (endedReason !== null && state !== 'ended' && state !== 'expired') ||
    (state === 'ended' && endedReason === null)
  ) {
    invalidResponse('administrator charity key ended reason');
  }
  return {
    id,
    display_head: displayHead,
    display_tail: displayTail,
    safe_source: normalizeAdminCharitySource(root.safe_source),
    physical_enabled: boolean(root.physical_enabled, 'administrator charity physical state'),
    charity_state: state,
    limits: {
      price: limits.price === null ? null : amount(limits.price, 'administrator charity price limit', false),
      calls: limits.calls === null ? null : decimal(limits.calls, 'administrator charity call limit'),
      tokens: limits.tokens === null ? null : decimal(limits.tokens, 'administrator charity token limit'),
    },
    usage: {
      price_used: amount(usage.price_used, 'administrator charity price used', false),
      price_inflight: amount(usage.price_inflight, 'administrator charity price inflight', false),
      calls_used: decimal(usage.calls_used, 'administrator charity calls used'),
      calls_inflight: decimal(usage.calls_inflight, 'administrator charity calls inflight'),
      tokens_used: decimal(usage.tokens_used, 'administrator charity tokens used'),
      tokens_inflight: decimal(usage.tokens_inflight, 'administrator charity tokens inflight'),
    },
    token_reserve: integer(root.token_reserve, 'administrator charity token reserve', 0, 2_147_483_647),
    authorized_expires_at: authorizedExpiresAt,
    expires_at: expiresAt,
    streak: {
      generation: decimal(streak.generation, 'administrator charity streak generation'),
      count: decimal(streak.count, 'administrator charity failure streak'),
      failure_disabled: boolean(streak.failure_disabled, 'administrator charity failure-disabled state'),
    },
    ended_reason: endedReason,
  };
}

function normalizeReview(
  value: unknown,
  reviewerPresent: boolean,
): { decision: 'approve' | 'reject'; reason: string; reviewed_at: number } | null {
  if (value === null) return null;
  const root = record(value, ['decision', 'reason', 'reviewed_at'], 'administrator charity review');
  const result = {
    decision: oneOf(root.decision, ['approve', 'reject'] as const, 'administrator charity review decision'),
    // Review text is deliberately validated but not returned to the page's
    // grouped projection. It is management material, not group metadata.
    reason: string(root.reason, 'administrator charity review reason', {
      max: 1_024,
      bytes: 4_096,
      multiline: true,
    }),
    reviewed_at: unixSecond(root.reviewed_at, 'administrator charity review time'),
  };
  if (!reviewerPresent && (result.decision !== 'approve' || result.reason !== '')) {
    invalidResponse('administrator charity automatic review');
  }
  return result;
}

export function normalizeAdminCharityDonation(value: unknown): AdminCharityDonation {
  const root = record(
    value,
    [
      'id',
      'status',
      'revision',
      'description',
      'review_result',
      'keys',
      'owner',
      'reviewer',
      'created_at',
      'updated_at',
    ],
    'administrator charity donation',
  );
  const status = oneOf(root.status, ADMIN_CHARITY_STATUSES, 'administrator charity donation status');
  // Validate the closed management projection without retaining donor text,
  // owner identity or reviewer identity in the feature-local query cache.
  string(root.description, 'administrator charity description', {
    max: 1_024,
    bytes: 4_096,
    multiline: true,
  });
  validateAdminOwner(root.owner);
  const reviewerPresent = validateAdminReviewer(root.reviewer);
  const review = normalizeReview(root.review_result, reviewerPresent);
  if (reviewerPresent && review === null) invalidResponse('administrator charity reviewer state');
  if (status === 'pending' && review !== null) invalidResponse('administrator charity pending review');
  if ((status === 'approved' || status === 'expired') && review?.decision !== 'approve') {
    invalidResponse('administrator charity approved review');
  }
  if (status === 'rejected' && review?.decision !== 'reject') {
    invalidResponse('administrator charity rejected review');
  }
  if (!Array.isArray(root.keys) || root.keys.length > 100) invalidResponse('administrator charity donation keys');
  return {
    id: decimalID(root.id, 'administrator charity donation ID'),
    status,
    revision: decimal(root.revision, 'administrator charity donation revision', { positive: true }),
    keys: root.keys.map(normalizeAdminCharityKey),
    created_at: unixSecond(root.created_at, 'administrator charity donation creation time'),
    updated_at: unixSecond(root.updated_at, 'administrator charity donation update time'),
  };
}

export const adminCharityKeys = {
  root: ['admin', 'operations', 'charity', 'grouped'] as const,
  donations: (status: string, cursorValue: string | null) =>
    ['admin', 'operations', 'charity', 'grouped', 'donations', status, cursorValue] as const,
};

export function getAdminCharityDonations(
  status = '',
  cursorValue: string | null = null,
  signal?: AbortSignal,
): Promise<CursorPage<AdminCharityDonation>> {
  if (status !== '' && !ADMIN_CHARITY_STATUSES.includes(status as AdminCharityStatus)) {
    invalidResponse('administrator charity status filter');
  }
  if (cursorValue !== null) {
    // The page decoder validates the authority cursor too; validating before
    // the request keeps an invalid caller value from crossing the boundary.
    cursor(cursorValue, 'administrator charity cursor');
  }
  return decoded(
    queryPath('/admin/api/donations', { status: status || undefined, cursor: cursorValue, limit: 50 }),
    (value) => {
      const result = page(value, 'administrator charity donation page', normalizeAdminCharityDonation);
      if (result.data.length > 100) invalidResponse('administrator charity donation page size');
      return result;
    },
    { signal },
  );
}

/** A single group row keeps the originating donation so every key remains independently visible. */
export interface AdminCharityGroupItem {
  donation_id: string;
  donation_status: AdminCharityStatus;
  key: AdminCharityKey;
}

export interface AdminCharityGroup {
  source: AdminCharitySource;
  items: AdminCharityGroupItem[];
}

function sourceGroupKey(source: AdminCharitySource, donationID: string, keyID: string): string {
  if (source.kind === 'custom') {
    // Custom resources never merge merely because connector and URL happen to
    // match. They remain independent provenance groups.
    return `custom:${donationID}:${keyID}`;
  }
  return JSON.stringify([
    source.channel_id,
    source.channel_revision,
    source.connector_type,
    source.base_url,
    source.name,
    source.category,
  ]);
}

export function groupAdminCharityDonations(
  donations: readonly AdminCharityDonation[],
): AdminCharityGroup[] {
  const groups = new Map<string, AdminCharityGroup>();
  for (const donation of donations) {
    for (const key of donation.keys) {
      const groupKey = sourceGroupKey(key.safe_source, donation.id, key.id);
      const existing = groups.get(groupKey);
      const item = { donation_id: donation.id, donation_status: donation.status, key };
      if (existing) existing.items.push(item);
      else groups.set(groupKey, { source: key.safe_source, items: [item] });
    }
  }
  return [...groups.values()];
}

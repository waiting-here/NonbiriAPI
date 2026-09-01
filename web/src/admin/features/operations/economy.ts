import { decoded, idempotentOptions, queryPath } from '@shared/operations/api';
import {
  amount, array, boolean, decimal, integer, invalidResponse, nullableString, nullableUnixSecond,
  oneOf, opaqueID, page, record, string, unixSecond, type CursorPage,
} from '@shared/operations/wire';

interface ActivityPumps { platform: number; welfare: number; next_pool: number }
interface RPSPumps { platform: number; welfare: number; thursday: number }
function normalizeActivityPumps(value: unknown): ActivityPumps {
  const root = record(value, ['platform', 'welfare', 'next_pool'], 'pool split');
  const result = {
    platform: integer(root.platform, 'platform split', 0, 9_999),
    welfare: integer(root.welfare, 'welfare split', 0, 9_999),
    next_pool: integer(root.next_pool, 'next-pool split', 0, 9_999),
  };
  if (result.platform + result.welfare + result.next_pool >= 10_000) invalidResponse('pool split total');
  return result;
}

function normalizeRPSPumps(value: unknown): RPSPumps {
  const root = record(value, ['platform', 'welfare', 'thursday'], 'RPS pool split');
  const result = {
    platform: integer(root.platform, 'RPS platform split', 0, 9_999),
    welfare: integer(root.welfare, 'RPS welfare split', 0, 9_999),
    thursday: integer(root.thursday, 'RPS Thursday split', 0, 9_999),
  };
  if (result.platform + result.welfare + result.thursday >= 10_000) invalidResponse('RPS pool split total');
  return result;
}

export interface Pool {
  id: string; pool_type: 'welfare' | 'thursday'; period_id: string | null;
  state: 'open' | 'closed'; revision: string; balance: string;
  created_at: number; closed_at: number | null;
}

export function normalizePool(value: unknown): Pool {
  const root = record(value, ['id', 'pool_type', 'period_id', 'state', 'revision', 'balance', 'created_at', 'closed_at'], 'shared pool');
  const type = oneOf(root.pool_type, ['welfare', 'thursday'] as const, 'pool type');
  const state = oneOf(root.state, ['open', 'closed'] as const, 'pool state');
  const period = root.period_id === null ? null : opaqueID(root.period_id, 'thu_', 'pool period id');
  const closed = nullableUnixSecond(root.closed_at, 'pool close time');
  if ((type === 'welfare') !== (period === null) || (state === 'open') !== (closed === null)) invalidResponse('pool state');
  return {
    id: opaqueID(root.id, 'pol_', 'pool id'), pool_type: type, period_id: period, state,
    revision: decimal(root.revision, 'pool revision', { positive: true }),
    balance: amount(root.balance, 'pool balance', false), created_at: unixSecond(root.created_at, 'pool creation time'), closed_at: closed,
  };
}

export interface Settlement {
  frozen_pool: string; contribution_count: string; eligible_contribution_count: string;
  processed_count: string; payout_total: string; rollover: string;
}
export interface Period {
  id: string; period_key: string; state: 'configured' | 'open' | 'settling' | 'settled' | 'configuration_error';
  revision: string; opens_at: number; closes_at: number; literature: string; entry: string;
  per_user_limit: number; pumps_bp: ActivityPumps; current_pool_id: string; next_pool_id: string;
  settlement: Settlement | null; created_at: number; terminal_at: number | null;
}

export function normalizePeriod(value: unknown): Period {
  const root = record(value, ['id', 'period_key', 'state', 'revision', 'opens_at', 'closes_at', 'literature', 'entry', 'per_user_limit', 'pumps_bp', 'current_pool_id', 'next_pool_id', 'settlement', 'created_at', 'terminal_at'], 'Thursday period');
  const state = oneOf(root.state, ['configured', 'open', 'settling', 'settled', 'configuration_error'] as const, 'Thursday period state');
  let settlement: Settlement | null = null;
  if (root.settlement !== null) {
    const item = record(root.settlement, ['frozen_pool', 'contribution_count', 'eligible_contribution_count', 'processed_count', 'payout_total', 'rollover'], 'Thursday settlement');
    settlement = {
      frozen_pool: amount(item.frozen_pool, 'frozen pool', false),
      contribution_count: decimal(item.contribution_count, 'contribution count'),
      eligible_contribution_count: decimal(item.eligible_contribution_count, 'eligible contribution count'),
      processed_count: decimal(item.processed_count, 'processed contribution count'),
      payout_total: amount(item.payout_total, 'payout total', false),
      rollover: amount(item.rollover, 'rollover', false),
    };
  }
  const terminal = nullableUnixSecond(root.terminal_at, 'Thursday terminal time');
  if ((state === 'settling' || state === 'settled') !== (settlement !== null)
      || (state === 'settled') !== (terminal !== null)) invalidResponse('Thursday period terminal state');
  const opens = unixSecond(root.opens_at, 'Thursday opening time');
  const closes = unixSecond(root.closes_at, 'Thursday closing time');
  if (closes !== opens + 86_400) invalidResponse('Thursday window');
  return {
    id: opaqueID(root.id, 'thu_', 'Thursday period id'),
    period_key: string(root.period_key, 'Thursday period key', { min: 1, max: 128, bytes: 128, ascii: true }),
    state, revision: decimal(root.revision, 'Thursday revision', { positive: true }), opens_at: opens, closes_at: closes,
    literature: string(root.literature, 'Thursday literature', { min: 1, max: 4_096, bytes: 16_384, multiline: true }),
    entry: amount(root.entry, 'Thursday entry', false), per_user_limit: integer(root.per_user_limit, 'Thursday user limit', 1, 1_000),
    pumps_bp: normalizeActivityPumps(root.pumps_bp), current_pool_id: opaqueID(root.current_pool_id, 'pol_', 'Thursday current pool id'),
    next_pool_id: opaqueID(root.next_pool_id, 'pol_', 'Thursday next pool id'), settlement,
    created_at: unixSecond(root.created_at, 'Thursday creation time'), terminal_at: terminal,
  };
}

export interface ActivitiesConfig {
  revision: string; master_enabled: boolean;
  welfare: { enabled: boolean; threshold: string; cap: string };
  thursday: { enabled: boolean };
}
export function normalizeActivitiesConfig(value: unknown): ActivitiesConfig {
  const root = record(value, ['revision', 'master_enabled', 'welfare', 'thursday'], 'activities configuration');
  const welfare = record(root.welfare, ['enabled', 'threshold', 'cap'], 'welfare configuration');
  const thursday = record(root.thursday, ['enabled'], 'Thursday configuration');
  return {
    revision: decimal(root.revision, 'activities configuration revision', { positive: true }),
    master_enabled: boolean(root.master_enabled, 'activities master switch'),
    welfare: { enabled: boolean(welfare.enabled, 'welfare switch'), threshold: amount(welfare.threshold, 'welfare threshold', false), cap: amount(welfare.cap, 'welfare cap', false) },
    thursday: { enabled: boolean(thursday.enabled, 'Thursday switch') },
  };
}

interface RPSModeConfig {
  enabled: boolean; base: string; pumps_bp: RPSPumps; queue_seconds: number;
  gesture_seconds: number; dealer_seconds: number; follower_seconds: number; queue_capacity: number;
}
interface GamesConfig {
  revision: string; master_enabled: boolean;
  fishing: { enabled: boolean; bait_prices: { worm: string; lure: string; premium: string }; rtp_percent: { standard: number; premium: number }; treasure_multipliers: { bottle: number; clover: number; shell: number } };
  linklink: { enabled: boolean; specs: Record<'6x8' | '8x8' | '10x10', { enabled: boolean; price: string }> };
  rps: { enabled: boolean; modes: Record<'quick' | 'standard' | 'deathmatch', RPSModeConfig> };
}

function normalizeRPSMode(value: unknown, label: string): RPSModeConfig {
  const root = record(value, ['enabled', 'base', 'pumps_bp', 'queue_seconds', 'gesture_seconds', 'dealer_seconds', 'follower_seconds', 'queue_capacity'], label);
  return {
    enabled: boolean(root.enabled, `${label} switch`), base: amount(root.base, `${label} base`, false), pumps_bp: normalizeRPSPumps(root.pumps_bp),
    queue_seconds: integer(root.queue_seconds, `${label} queue seconds`, 30, 120), gesture_seconds: integer(root.gesture_seconds, `${label} gesture seconds`, 5, 20),
    dealer_seconds: integer(root.dealer_seconds, `${label} dealer seconds`, 5, 15), follower_seconds: integer(root.follower_seconds, `${label} follower seconds`, 5, 15),
    queue_capacity: integer(root.queue_capacity, `${label} queue capacity`, 1, 4_096),
  };
}

export function normalizeGamesConfig(value: unknown): GamesConfig {
  const root = record(value, ['revision', 'master_enabled', 'fishing', 'linklink', 'rps'], 'games configuration');
  const fishing = record(root.fishing, ['enabled', 'bait_prices', 'rtp_percent', 'treasure_multipliers'], 'Fishing configuration');
  const bait = record(fishing.bait_prices, ['worm', 'lure', 'premium'], 'Fishing bait prices');
  const rtp = record(fishing.rtp_percent, ['standard', 'premium'], 'Fishing RTP');
  const treasure = record(fishing.treasure_multipliers, ['bottle', 'clover', 'shell'], 'Fishing treasure multipliers');
  const linklink = record(root.linklink, ['enabled', 'specs'], 'LinkLink configuration');
  const specs = record(linklink.specs, ['6x8', '8x8', '10x10'], 'LinkLink specifications');
  const normalizeSpec = (name: '6x8' | '8x8' | '10x10') => {
    const spec = record(specs[name], ['enabled', 'price'], `LinkLink ${name}`);
    return { enabled: boolean(spec.enabled, `LinkLink ${name} switch`), price: amount(spec.price, `LinkLink ${name} price`, false) };
  };
  const rps = record(root.rps, ['enabled', 'modes'], 'RPS configuration');
  const modes = record(rps.modes, ['quick', 'standard', 'deathmatch'], 'RPS modes');
  return {
    revision: decimal(root.revision, 'games configuration revision', { positive: true }), master_enabled: boolean(root.master_enabled, 'games master switch'),
    fishing: {
      enabled: boolean(fishing.enabled, 'Fishing switch'),
      bait_prices: { worm: amount(bait.worm, 'worm price', false), lure: amount(bait.lure, 'lure price', false), premium: amount(bait.premium, 'premium price', false) },
      rtp_percent: { standard: integer(rtp.standard, 'standard RTP', 0, 100), premium: integer(rtp.premium, 'premium RTP', 0, 100) },
      treasure_multipliers: { bottle: integer(treasure.bottle, 'bottle multiplier', 0, 1_000_000), clover: integer(treasure.clover, 'clover multiplier', 0, 1_000_000), shell: integer(treasure.shell, 'shell multiplier', 0, 1_000_000) },
    },
    linklink: { enabled: boolean(linklink.enabled, 'LinkLink switch'), specs: { '6x8': normalizeSpec('6x8'), '8x8': normalizeSpec('8x8'), '10x10': normalizeSpec('10x10') } },
    rps: { enabled: boolean(rps.enabled, 'RPS switch'), modes: { quick: normalizeRPSMode(modes.quick, 'quick RPS'), standard: normalizeRPSMode(modes.standard, 'standard RPS'), deathmatch: normalizeRPSMode(modes.deathmatch, 'deathmatch RPS') } },
  };
}

export function gamesConfigPatch(input: GamesConfig): Record<string, unknown> {
  const modePatch = (mode: RPSModeConfig) => ({
    enabled: mode.enabled,
    base: mode.base,
    pumps_bp: mode.pumps_bp,
    queue_seconds: mode.queue_seconds,
    gesture_seconds: mode.gesture_seconds,
    dealer_seconds: mode.dealer_seconds,
    follower_seconds: mode.follower_seconds,
  });
  return {
    expected_revision: input.revision,
    master_enabled: input.master_enabled,
    fishing: input.fishing,
    linklink: input.linklink,
    rps: {
      enabled: input.rps.enabled,
      modes: {
        quick: modePatch(input.rps.modes.quick),
        standard: modePatch(input.rps.modes.standard),
        deathmatch: modePatch(input.rps.modes.deathmatch),
      },
    },
  };
}

export function thursdayMutationRevision(config: ActivitiesConfig | undefined, period: Period | null | undefined): string | null {
  if (period?.state === 'configured') return period.revision;
  return config?.revision ?? null;
}

export interface ActiveCounts {
  games: { game: 'fishing' | 'linklink' | 'rps'; mode: string | null; spec: string | null; phase: string | null; count: string }[];
  queues: { mode: 'quick' | 'standard' | 'deathmatch'; count: string }[];
}
export function normalizeActiveCounts(value: unknown): ActiveCounts {
  const root = record(value, ['games', 'queues'], 'active game counts');
  return {
    games: array(root.games, 'active game rows', 256).map((entry) => {
      const row = record(entry, ['game', 'mode', 'spec', 'phase', 'count'], 'active game row');
      return {
        game: oneOf(row.game, ['fishing', 'linklink', 'rps'] as const, 'active game'),
        mode: nullableString(row.mode, 'active game mode', { min: 1, max: 64, bytes: 64, ascii: true }),
        spec: nullableString(row.spec, 'active game specification', { min: 1, max: 64, bytes: 64, ascii: true }),
        phase: nullableString(row.phase, 'active game phase', { min: 1, max: 64, bytes: 64, ascii: true }),
        count: decimal(row.count, 'active game count'),
      };
    }),
    queues: array(root.queues, 'game queue counts', 3).map((entry) => {
      const row = record(entry, ['mode', 'count'], 'game queue count');
      return { mode: oneOf(row.mode, ['quick', 'standard', 'deathmatch'] as const, 'queue mode'), count: decimal(row.count, 'queue count') };
    }),
  };
}

export const adminEconomyKeys = {
  activities: ['admin', 'operations', 'activities', 'config'] as const,
  thursday: ['admin', 'operations', 'activities', 'thursday'] as const,
  pools: (type: string, state: string, cursor: string | null) => ['admin', 'operations', 'pools', type, state, cursor] as const,
  games: ['admin', 'operations', 'games', 'config'] as const,
  counts: ['admin', 'operations', 'games', 'counts'] as const,
};

export const getActivitiesConfig = () => decoded('/admin/api/activities/config', normalizeActivitiesConfig);
export const getAdminThursday = () => decoded('/admin/api/activities/thursday', (value) => {
  const root = record(value, ['period'], 'administrator Thursday state');
  return { period: root.period === null ? null : normalizePeriod(root.period) };
});
export const getPools = (type: string, state: string, cursor: string | null): Promise<CursorPage<Pool>> => decoded(queryPath('/admin/api/pools', { pool_type: type || undefined, state: state || undefined, cursor, limit: 50 }), (value) => page(value, 'shared pool page', normalizePool));
export const patchActivitiesConfig = (body: unknown, key: string) => decoded('/admin/api/activities/config', normalizeActivitiesConfig, idempotentOptions(key, { method: 'PATCH', json: body }));
export const putThursdayNext = (body: unknown, key: string) => decoded('/admin/api/activities/thursday/next', normalizePeriod, idempotentOptions(key, { method: 'PUT', json: body }));
export const resumeThursday = (id: string, revision: string, key: string) => decoded(`/admin/api/activities/thursday/${encodeURIComponent(opaqueID(id, 'thu_', 'Thursday period id'))}/resume`, normalizePeriod, idempotentOptions(key, { method: 'POST', json: { expected_revision: revision } }));
export const adjustPool = (id: string, body: unknown, key: string) => decoded(`/admin/api/pools/${encodeURIComponent(opaqueID(id, 'pol_', 'pool id'))}/adjustments`, normalizePool, idempotentOptions(key, { method: 'POST', json: body }));
export const getGamesConfig = () => decoded('/admin/api/games/config', normalizeGamesConfig);
export const getActiveCounts = () => decoded('/admin/api/games/active-counts', normalizeActiveCounts);
export const patchGamesConfig = (body: unknown, key: string) => decoded('/admin/api/games/config', normalizeGamesConfig, idempotentOptions(key, { method: 'PATCH', json: body }));

export type { GamesConfig, RPSModeConfig };

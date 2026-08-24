import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, apiFetch } from '@shared/query/http';
import { asRecord, hasControlCharacters } from '@shared/query/normalize';
import { parseEconomyString } from '@shared/utils/formatNumber';
import { BAIT_IDS, type BaitId, type FishingBait, type FishingGameParams, type FishingGameSummary, type FishingResult, type FishingState, type GameSummary, type GamesConfig, type Leaderboard, type LeaderboardEntry, type PendingRound, type StartRoundResponse } from './types';

const MAX_AMOUNT_CHARS = 32;
const MAX_ID_BYTES = 64;
const MAX_TEXT_CHARS = 512;
const BAIT_SET = new Set<string>(BAIT_IDS);
const SAFE_DISCORD_TOKEN = /^[A-Za-z0-9_-]{1,256}$/;
// Round IDs are opaque, bounded server tokens.  The current server uses the
// fixed `grd_` prefix followed by an unpadded, lower-case RFC 4648 base32
// encoding of at least 128 bits.  Keep the prefix/entropy boundary, but do
// not infer the token's length or payload so future server IDs remain valid.
const ROUND_ID = /^grd_[a-z2-7]{26,60}$/;
const INT64_MIN = -(1n << 63n);
const INT64_MAX = (1n << 63n) - 1n;

const SPECIES_TIER = new Map<string, string>([
  ['whitebait', 'small'], ['gudgeon', 'small'], ['horse_mouth', 'small'], ['smelt', 'small'], ['loach', 'small'],
  ['crucian', 'regular'], ['tilapia', 'regular'], ['yellow_catfish', 'regular'], ['ayu', 'regular'], ['stream_carp', 'regular'],
  ['common_carp', 'big'], ['snakehead', 'big'], ['catfish', 'big'], ['mandarin_fish', 'big'], ['rainbow_trout', 'big'],
  ['grass_carp', 'giant'], ['silver_carp', 'giant'], ['bighead_carp', 'giant'], ['black_carp', 'giant'], ['japanese_eel', 'giant'],
  ['yellowcheek', 'legend'], ['taimen', 'legend'], ['koi', 'legend'],
  ['boot', 'junk'], ['seaweed', 'junk'], ['plastic_bag', 'junk'], ['branch', 'junk'],
  ['old_tire', 'junk'], ['glasses', 'junk'], ['phone_case', 'junk'], ['fry', 'junk'],
  ['bottle', 'treasure'], ['clover', 'treasure'], ['shell', 'treasure'],
]);
const FISH_SPECIES = new Set<string>([...SPECIES_TIER].filter(([, tier]) => tier !== 'junk' && tier !== 'treasure').map(([species]) => species));
const TIER_SIZE: Readonly<Record<string, readonly [number, number]>> = Object.freeze({
  junk: [0, 0],
  small: [5, 25],
  regular: [15, 35],
  big: [30, 80],
  giant: [60, 150],
  legend: [100, 200],
  treasure: [0, 0],
});

export const fishingKeys = {
  all: ['user', 'games'] as const,
  config: ['user', 'games', 'config'] as const,
  state: ['user', 'games', 'fishing', 'state'] as const,
  leaderboard: (board: 'single' | 'total') => ['user', 'games', 'fishing', 'leaderboard', board] as const,
};

const fishingMutationKeys = {
  start: ['user', 'games', 'fishing', 'start'] as const,
  settle: ['user', 'games', 'fishing', 'settle'] as const,
  acknowledge: ['user', 'games', 'fishing', 'acknowledge'] as const,
  profile: ['user', 'games', 'fishing', 'profile'] as const,
};

/** Captured outcomes and round identifiers must never remain in Query cache. */
function clearFishingMutationCache(queryClient: ReturnType<typeof useQueryClient>, mutationKey: readonly unknown[]) {
  queueMicrotask(() => {
    for (const mutation of queryClient.getMutationCache().findAll({ mutationKey, exact: true })) {
      queryClient.getMutationCache().remove(mutation);
    }
  });
}

function invalid(field: string): never {
  throw new ApiError('invalid_response', `The server returned an invalid fishing ${field}.`, 200);
}

function recordField(record: Record<string, unknown>, key: string): unknown {
  return record[key];
}

function hasWireField(record: Record<string, unknown>, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(record, key);
}

function strictString(value: unknown, field: string, maxBytes = MAX_TEXT_CHARS): string {
  if (typeof value !== 'string' || value.length === 0) invalid(field);
  if (value.trim().length === 0 || new TextEncoder().encode(value).byteLength > maxBytes || hasControlCharacters(value)) invalid(field);
  return value;
}

function requiredString(value: unknown, field: string, maxBytes = MAX_TEXT_CHARS): string {
  return strictString(value, field, maxBytes);
}

function optionalWireString(value: unknown, field: string, maxBytes = MAX_TEXT_CHARS): string | undefined {
  if (value === undefined || value === null) return undefined;
  return strictString(value, field, maxBytes);
}

function parseInt64(value: unknown, field: string): { wire: string; parsed: bigint } {
  if (typeof value !== 'string' || value.length === 0 || value.length > MAX_AMOUNT_CHARS) invalid(field);
  const parsed = parseEconomyString(value);
  if (parsed === null || parsed < INT64_MIN || parsed > INT64_MAX) invalid(field);
  return { wire: value, parsed };
}

function nonNegativeAmount(value: unknown, field: string): string {
  const parsed = parseInt64(value, field);
  if (parsed.parsed < 0n) invalid(field);
  return parsed.wire;
}

function signedAmount(value: unknown, field: string): string {
  return parseInt64(value, field).wire;
}

function positiveAmount(value: unknown, field: string): string {
  const parsed = parseInt64(value, field);
  if (parsed.parsed <= 0n) invalid(field);
  return parsed.wire;
}

function roundId(value: unknown, field: string): string {
  if (typeof value !== 'string' || !ROUND_ID.test(value)) {
    invalid(field);
  }
  return value;
}

function safeInteger(value: unknown, field: string, min: number, max: number): number {
  if (!Number.isSafeInteger(value) || (value as number) < min || (value as number) > max) invalid(field);
  return value as number;
}

function booleanField(value: unknown, field: string): boolean {
  if (typeof value !== 'boolean') invalid(field);
  return value;
}

function bait(value: unknown): BaitId {
  if (typeof value !== 'string' || !BAIT_SET.has(value)) invalid('bait');
  return value as BaitId;
}

function parseBait(value: unknown): FishingBait {
  const record = asRecord(value);
  if (!record) invalid('bait entry');
  return { id: bait(recordField(record, 'id')), price: positiveAmount(recordField(record, 'price'), 'bait price') };
}

function parseFishingParams(value: unknown): FishingGameParams {
  const record = asRecord(value);
  if (!record) invalid('game parameters');
  const rawBaitsValue = recordField(record, 'baits');
  if (!Array.isArray(rawBaitsValue)) invalid('bait roster');
  const rawBaits = rawBaitsValue;
  if (rawBaits.length !== BAIT_IDS.length) invalid('bait roster');
  const baits = rawBaits.map(parseBait);
  const ids = new Set(baits.map(({ id }) => id));
  if (ids.size !== BAIT_IDS.length || BAIT_IDS.some((id) => !ids.has(id))) invalid('bait roster');
  const rtp = asRecord(recordField(record, 'rtp_percent'));
  const treasure = asRecord(recordField(record, 'treasure_multipliers'));
  if (!rtp || !treasure) invalid('game parameters');
  return {
    baits,
    rtpPercent: {
      standard: safeInteger(recordField(rtp, 'standard'), 'standard RTP', 0, 100),
      premium: safeInteger(recordField(rtp, 'premium'), 'premium RTP', 0, 100),
    },
    treasureMultipliers: {
      bottle: safeInteger(recordField(treasure, 'bottle'), 'bottle multiplier', 1, 1000),
      clover: safeInteger(recordField(treasure, 'clover'), 'clover multiplier', 1, 1000),
      shell: safeInteger(recordField(treasure, 'shell'), 'shell multiplier', 1, 1000),
    },
  };
}

function parseGame(value: unknown): FishingGameSummary {
  const record = asRecord(value);
  if (!record || recordField(record, 'id') !== 'fishing' || recordField(record, 'version') !== 1) invalid('game registration');
  return {
    id: 'fishing',
    version: 1,
    enabled: booleanField(recordField(record, 'enabled'), 'game enabled flag'),
    params: parseFishingParams(recordField(record, 'params')),
  };
}

function parseGameSummary(value: unknown): GameSummary {
  const record = asRecord(value);
  if (!record) invalid('game registration');
  const id = requiredString(recordField(record, 'id'), 'game id', MAX_ID_BYTES);
  const version = safeInteger(recordField(record, 'version'), 'game version', 1, 1_000_000);
  const enabled = booleanField(recordField(record, 'enabled'), 'game enabled flag');
  if (id === 'fishing' && version === 1) return parseGame(record);
  // Unknown registrations remain structurally visible to their own feature parser.
  return { id, version, enabled };
}

export function normalizeGamesConfig(value: unknown): GamesConfig {
  const record = asRecord(value);
  if (!record) invalid('configuration');
  const gamesValue = recordField(record, 'games');
  if (!Array.isArray(gamesValue)) invalid('game registrations');
  const games = gamesValue;
  const parsedGames = games.map(parseGameSummary);
  const seenRegistrations = new Set<string>();
  for (const game of parsedGames) {
    const key = `${game.id}\u0000${game.version}`;
    if (seenRegistrations.has(key)) invalid('duplicate game registration');
    seenRegistrations.add(key);
  }
  if (parsedGames.filter((game) => game.id === 'fishing' && game.version === 1).length !== 1) {
    invalid('fishing game registration');
  }
  return {
    masterEnabled: booleanField(recordField(record, 'master_enabled'), 'master enabled flag'),
    credits: signedAmount(recordField(record, 'credits'), 'credits'),
    gameProfilePublic: booleanField(recordField(record, 'game_profile_public'), 'profile privacy flag'),
    games: parsedGames,
  };
}

export function isFishingGameSummary(value: GameSummary): value is FishingGameSummary {
  return value.id === 'fishing' && value.version === 1 && 'params' in value;
}

function parsePending(value: unknown): PendingRound {
  const record = asRecord(value);
  if (!record) invalid('pending round');
  const createdAt = safeInteger(recordField(record, 'created_at'), 'pending created time', 0, Number.MAX_SAFE_INTEGER);
  const autoSettleAt = safeInteger(recordField(record, 'auto_settle_at'), 'pending auto-settle time', 0, Number.MAX_SAFE_INTEGER);
  if (autoSettleAt < createdAt) invalid('pending time range');
  return {
    roundId: roundId(recordField(record, 'round_id'), 'pending round id'),
    bait: bait(recordField(record, 'bait')),
    price: positiveAmount(recordField(record, 'price'), 'pending price'),
    createdAt,
    autoSettleAt,
  };
}

export function normalizeFishingStart(value: unknown, expectedBait?: BaitId): StartRoundResponse {
  const record = asRecord(value);
  if (!record) invalid('start response');
  const state = recordField(record, 'state');
  if (state !== 'pending' && state !== 'committed' && state !== 'released') invalid('start state');
  const pending = parsePending(record);
  if (expectedBait !== undefined && pending.bait !== expectedBait) invalid('start bait');
  return {
    ...pending,
    gameId: recordField(record, 'game_id') === 'fishing' ? 'fishing' : invalid('game id'),
    gameVersion: recordField(record, 'game_version') === 1 ? 1 : invalid('game version'),
    credits: signedAmount(recordField(record, 'credits'), 'start credits'),
    state,
    idempotentReplay: booleanField(recordField(record, 'idempotent_replay'), 'replay flag'),
  };
}

export function normalizeFishingResult(value: unknown): FishingResult {
  const record = asRecord(value);
  if (!record) invalid('result');
  const isJunk = booleanField(recordField(record, 'is_junk'), 'junk flag');
  const isTreasure = booleanField(recordField(record, 'is_treasure'), 'treasure flag');
  if (isJunk && isTreasure) invalid('result kind');
  const replay = recordField(record, 'idempotent_replay');
  if (replay !== undefined && typeof replay !== 'boolean') invalid('replay flag');
  const speciesKey = requiredString(recordField(record, 'species_key'), 'species key', MAX_ID_BYTES);
  const tier = requiredString(recordField(record, 'tier'), 'result tier', MAX_ID_BYTES);
  const expectedTier = SPECIES_TIER.get(speciesKey);
  const sizeCm = safeInteger(recordField(record, 'size_cm'), 'result size', 0, 200);
  const sizeRange = TIER_SIZE[tier];
  if (!expectedTier || expectedTier !== tier || !sizeRange || sizeCm < sizeRange[0] || sizeCm > sizeRange[1]) {
    invalid('result roster');
  }
  if ((tier === 'junk') !== isJunk || (tier === 'treasure') !== isTreasure) invalid('result kind');
  const meter = booleanField(recordField(record, 'meter'), 'meter flag');
  const expectedMeter = !isJunk && !isTreasure && sizeCm >= 100;
  if (meter !== expectedMeter) invalid('meter flag');
  return {
    roundId: roundId(recordField(record, 'round_id'), 'result round id'),
    gameId: recordField(record, 'game_id') === 'fishing' ? 'fishing' : invalid('game id'),
    gameVersion: recordField(record, 'game_version') === 1 ? 1 : invalid('game version'),
    bait: bait(recordField(record, 'bait')),
    price: positiveAmount(recordField(record, 'price'), 'result price'),
    speciesKey,
    tier,
    sizeCm,
    isJunk,
    isTreasure,
    meter,
    creditsWon: nonNegativeAmount(recordField(record, 'credits_won'), 'credits won'),
    credits: signedAmount(recordField(record, 'credits'), 'result credits'),
    settledAt: safeInteger(recordField(record, 'settled_at'), 'settled time', 0, Number.MAX_SAFE_INTEGER),
    ...(replay === undefined ? {} : { idempotentReplay: replay }),
  };
}

/**
 * The state endpoint projects an outcome for recovery, not a settle replay.
 * Its frozen wire deliberately omits `idempotent_replay`; accepting that field
 * here would blur the two contracts and could make a replay flag look like a
 * newly revealed result.
 */
function normalizeFishingStateResult(value: unknown): FishingResult {
  const record = asRecord(value);
  if (!record || hasWireField(record, 'idempotent_replay')) invalid('state result replay flag');
  return normalizeFishingResult(record);
}

/** A settle response is only usable for the round whose path was requested. */
export function normalizeFishingSettlement(value: unknown, expectedRoundId: string): FishingResult {
  const record = asRecord(value);
  if (!record || !hasWireField(record, 'idempotent_replay') || typeof recordField(record, 'idempotent_replay') !== 'boolean') {
    invalid('settlement replay flag');
  }
  const result = normalizeFishingResult(value);
  if (result.roundId !== expectedRoundId) invalid('settlement round id');
  return result;
}

export function normalizeFishingState(value: unknown): FishingState {
  const record = asRecord(value);
  if (!record) invalid('state');
  const pending = recordField(record, 'pending_round');
  const unrevealed = recordField(record, 'unrevealed_result');
  if (pending === undefined || unrevealed === undefined) invalid('state');
  if (pending !== null && pending !== undefined && !asRecord(pending)) invalid('pending state');
  if (unrevealed !== null && unrevealed !== undefined && !asRecord(unrevealed)) invalid('unrevealed state');
  const pendingRound = pending === null || pending === undefined ? null : parsePending(pending);
  const unrevealedResult = unrevealed === null || unrevealed === undefined ? null : normalizeFishingStateResult(unrevealed);
  const hasMoreUnrevealed = booleanField(recordField(record, 'has_more_unrevealed'), 'unrevealed flag');
  // The server only sets the continuation bit when it has a current result
  // to project.  A pending round and its own unrevealed result are mutually
  // exclusive state projections; accepting either malformed shape would let
  // the presentation race two economic states.
  if (hasMoreUnrevealed && !unrevealedResult) invalid('unrevealed continuation');
  if (pendingRound && unrevealedResult && pendingRound.roundId === unrevealedResult.roundId) invalid('pending/unrevealed round');
  return {
    pendingRound,
    unrevealedResult,
    hasMoreUnrevealed,
  };
}

function parseLeaderboardEntry(value: unknown, board: 'single' | 'total'): LeaderboardEntry {
  const record = asRecord(value);
  if (!record) invalid('leaderboard entry');
  const hasDisplayName = hasWireField(record, 'display_name');
  const displayName = hasDisplayName
    ? strictString(recordField(record, 'display_name'), 'leaderboard display name')
    : undefined;
  const hasAvatar = hasWireField(record, 'avatar_url');
  const rawAvatarUrl = hasAvatar
    ? strictString(recordField(record, 'avatar_url'), 'leaderboard avatar URL')
    : undefined;
  const avatarUrl = safeAvatarUrl(rawAvatarUrl);
  const hasLevel4 = hasWireField(record, 'level4_badge');
  const level4 = hasLevel4 ? booleanField(recordField(record, 'level4_badge'), 'leaderboard badge') : undefined;
  if (board === 'total' && hasWireField(record, 'species_key')) invalid('total leaderboard score');
  const speciesKey = optionalWireString(recordField(record, 'species_key'), 'leaderboard species key', MAX_ID_BYTES);
  const size = recordField(record, 'size_cm');
  const totalCredits = recordField(record, 'total_credits');
  if (board === 'single' && (!speciesKey || size === undefined)) invalid('single leaderboard score');
  if (board === 'total' && totalCredits === undefined) invalid('total leaderboard score');
  if (board === 'single' && totalCredits !== undefined) invalid('single leaderboard score');
  if (board === 'total' && (speciesKey !== undefined || size !== undefined)) invalid('total leaderboard score');
  if (board === 'single' && speciesKey !== undefined && !FISH_SPECIES.has(speciesKey)) invalid('leaderboard species');
  const parsedSize = size === undefined ? undefined : safeInteger(size, 'leaderboard size', 0, 200);
  if (board === 'single') {
    const tier = speciesKey === undefined ? undefined : SPECIES_TIER.get(speciesKey);
    const range = tier === undefined ? undefined : TIER_SIZE[tier];
    if (!tier || !range || parsedSize === undefined || parsedSize < range[0] || parsedSize > range[1]) {
      invalid('single leaderboard score');
    }
  }
  // Anonymous rows must omit all identity fields.  A malformed null/empty
  // identity is not silently downgraded to anonymous, because doing so could
  // hide a server wire regression while displaying the wrong privacy state.
  const publicIdentity = displayName !== undefined;
  if (!publicIdentity && (hasAvatar || hasLevel4)) invalid('anonymous leaderboard identity');
  return {
    rank: safeInteger(recordField(record, 'rank'), 'leaderboard rank', 1, Number.MAX_SAFE_INTEGER),
    ...(speciesKey ? { speciesKey } : {}),
    ...(parsedSize === undefined ? {} : { sizeCm: parsedSize }),
    ...(totalCredits === undefined ? {} : { totalCredits: nonNegativeAmount(totalCredits, 'leaderboard total') }),
    ...(publicIdentity ? { displayName } : {}),
    ...(publicIdentity && avatarUrl ? { avatarUrl } : {}),
    ...(publicIdentity && level4 !== undefined ? { level4Badge: level4 } : {}),
    isMe: booleanField(recordField(record, 'is_me'), 'leaderboard owner flag'),
  };
}

function sameLeaderboardProjection(left: LeaderboardEntry, right: LeaderboardEntry): boolean {
  return left.rank === right.rank
    && left.speciesKey === right.speciesKey
    && left.sizeCm === right.sizeCm
    && left.totalCredits === right.totalCredits
    && left.displayName === right.displayName
    && left.avatarUrl === right.avatarUrl
    && left.level4Badge === right.level4Badge
    && left.isMe === right.isMe;
}

export function normalizeLeaderboard(value: unknown, board: 'single' | 'total'): Leaderboard {
  const record = asRecord(value);
  if (!record || recordField(record, 'board') !== board) invalid('leaderboard');
  if (!hasWireField(record, 'window_start')) invalid('leaderboard window');
  const rawEntriesValue = recordField(record, 'entries');
  if (!Array.isArray(rawEntriesValue)) invalid('leaderboard entries');
  const rawEntries = rawEntriesValue;
  if (rawEntries.length > 20) invalid('leaderboard size');
  if (!hasWireField(record, 'me')) invalid('leaderboard mine');
  const rawMine = recordField(record, 'me');
  if (rawMine === undefined || (rawMine !== null && !asRecord(rawMine))) invalid('leaderboard mine');
  const windowStart = recordField(record, 'window_start');
  if (windowStart === undefined) invalid('leaderboard window');
  if (windowStart !== null) safeInteger(windowStart, 'leaderboard window', 0, Number.MAX_SAFE_INTEGER);
  if (board === 'single' && windowStart !== null && windowStart !== undefined) invalid('single leaderboard window');
  if (board === 'total' && (windowStart === null || windowStart === undefined)) invalid('total leaderboard window');
  const entries = rawEntries.map((entry) => parseLeaderboardEntry(entry, board));
  entries.forEach((entry, index) => {
    if (entry.rank !== index + 1) invalid('leaderboard rank');
  });
  const markedEntries = entries.filter((entry) => entry.isMe);
  if (markedEntries.length > 1) invalid('leaderboard owner flag');
  const me = rawMine === null ? null : parseLeaderboardEntry(rawMine, board);
  if (me !== null) {
    if (!me.isMe) invalid('leaderboard owner flag');
    const matchingEntry = entries.find((entry) => entry.rank === me.rank);
    if (me.rank <= 20) {
      if (!matchingEntry || !matchingEntry.isMe || !sameLeaderboardProjection(matchingEntry, me)) {
        invalid('leaderboard mine');
      }
    } else {
      // A rank beyond the returned window proves that at least 21 eligible
      // rows exist.  Since this wire has no total-count field, accepting a
      // short `entries` array here would silently accept an incomplete Top 20.
      if (entries.length !== 20 || markedEntries.length > 0) {
        invalid('leaderboard top window');
      }
    }
  } else if (markedEntries.length > 0) {
    invalid('leaderboard owner flag');
  }
  return {
    board,
    windowStart: windowStart === null || windowStart === undefined ? null : (windowStart as number),
    entries,
    me,
  };
}

export async function fetchGamesConfig(): Promise<GamesConfig> {
  return normalizeGamesConfig(await apiFetch<unknown>('/api/games'));
}

export async function fetchFishingState(): Promise<FishingState> {
  return normalizeFishingState(await apiFetch<unknown>('/api/games/fishing/state'));
}

export async function fetchFishingLeaderboard(board: 'single' | 'total'): Promise<Leaderboard> {
  return normalizeLeaderboard(await apiFetch<unknown>(`/api/games/fishing/leaderboard?board=${board}`), board);
}

export const useFishingConfig = (enabled = true) => useQuery({
  queryKey: fishingKeys.config,
  queryFn: fetchGamesConfig,
  enabled,
  staleTime: 0,
  refetchOnWindowFocus: true,
});

export const useFishingState = (enabled = true) => useQuery({
  queryKey: fishingKeys.state,
  queryFn: fetchFishingState,
  enabled,
  staleTime: 0,
  refetchOnWindowFocus: true,
  refetchInterval: (query) => query.state.data?.pendingRound ? 10_000 : false,
});

export const useFishingLeaderboard = (board: 'single' | 'total', enabled = true) => useQuery({
  queryKey: fishingKeys.leaderboard(board),
  queryFn: () => fetchFishingLeaderboard(board),
  enabled,
  staleTime: 15_000,
  refetchOnWindowFocus: true,
});

export function useStartFishing() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: fishingMutationKeys.start,
    mutationFn: async ({ bait: selectedBait, idempotencyKey }: { bait: BaitId; idempotencyKey: string }) => {
      const value = await apiFetch<unknown>('/api/games/fishing/rounds', {
        method: 'POST',
        headers: { 'Idempotency-Key': idempotencyKey },
        json: { bait: selectedBait },
      });
      return normalizeFishingStart(value, selectedBait);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: fishingKeys.state });
      void queryClient.invalidateQueries({ queryKey: fishingKeys.leaderboard('single') });
      void queryClient.invalidateQueries({ queryKey: fishingKeys.leaderboard('total') });
      // The page performs the authoritative /api/games refetch after it has
      // associated this response with the selected bait and active flow.
    },
    onSettled: () => clearFishingMutationCache(queryClient, fishingMutationKeys.start),
  });
}

export function useSettleFishing() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: fishingMutationKeys.settle,
    mutationFn: async (roundId: string) => normalizeFishingSettlement(
      await apiFetch<unknown>(`/api/games/fishing/rounds/${encodeURIComponent(roundId)}/settle`, { method: 'POST' }),
      roundId,
    ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: fishingKeys.state });
      void queryClient.invalidateQueries({ queryKey: fishingKeys.leaderboard('single') });
      void queryClient.invalidateQueries({ queryKey: fishingKeys.leaderboard('total') });
      // The settlement result is not allowed to mutate the balance cache.
      // The component first validates round/bait/price/generation, then
      // refetches /api/games as the sole balance authority.
    },
    onSettled: () => clearFishingMutationCache(queryClient, fishingMutationKeys.settle),
  });
}

export function useAcknowledgeFishing() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: fishingMutationKeys.acknowledge,
    mutationFn: (roundId: string) => apiFetch<void>(
      `/api/games/fishing/rounds/${encodeURIComponent(roundId)}/ack`,
      { method: 'POST' },
    ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: fishingKeys.state });
    },
    onSettled: () => clearFishingMutationCache(queryClient, fishingMutationKeys.acknowledge),
  });
}

export function useUpdateGameProfilePublic() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: fishingMutationKeys.profile,
    mutationFn: async (value: boolean) => {
      const response = await apiFetch<unknown>('/api/me', {
        method: 'PATCH',
        json: { game_profile_public: value },
      });
      const root = asRecord(response);
      const user = root ? asRecord(recordField(root, 'user')) : null;
      const confirmed = user ? recordField(user, 'game_profile_public') : undefined;
      if (typeof confirmed !== 'boolean' || confirmed !== value) invalid('profile response');
      return confirmed;
    },
    // The component performs one coordinated three-way authoritative read
    // after PATCH so privacy projections cannot mix generations.
    onSettled: () => clearFishingMutationCache(queryClient, fishingMutationKeys.profile),
  });
}

export function isKnownBait(value: string): value is BaitId {
  return BAIT_SET.has(value);
}

export function isAffordable(credits: string, price: string): boolean {
  const balance = parseEconomyString(credits);
  const cost = parseEconomyString(price);
  return balance !== null
    && cost !== null
    && balance >= INT64_MIN
    && balance <= INT64_MAX
    && cost > 0n
    && cost <= INT64_MAX
    && balance >= cost;
}

/** Only the server-approved static Discord avatar surface may reach <img>. */
export function safeAvatarUrl(value: string | undefined): string | undefined {
  if (!value) return undefined;
  // URL normalisation erases an explicit default :443 port, so reject any
  // non-canonical origin spelling before constructing the URL object too.
  if (!value.startsWith('https://cdn.discordapp.com/')) return undefined;
  try {
    const parsed = new URL(value);
    // Mirror the server's two canonical Discord CDN shapes.  Keeping this
    // exact makes an upstream response unable to turn a leaderboard row into
    // an arbitrary remote image request.
    if (
      parsed.protocol !== 'https:'
      || parsed.hostname !== 'cdn.discordapp.com'
      || parsed.host !== 'cdn.discordapp.com'
      || parsed.port !== ''
      || parsed.origin !== 'https://cdn.discordapp.com'
      || parsed.username
      || parsed.password
      || parsed.hash
      || parsed.search !== '?size=64'
      || parsed.pathname.includes('%')
    ) return undefined;
    const segments = parsed.pathname.slice(1).split('/');
    const validGlobal = segments.length === 3
      && segments[0] === 'avatars'
      && SAFE_DISCORD_TOKEN.test(segments[1] ?? '')
      && SAFE_DISCORD_TOKEN.test((segments[2] ?? '').replace(/\.png$/, ''));
    const validGuild = segments.length === 6
      && segments[0] === 'guilds'
      && SAFE_DISCORD_TOKEN.test(segments[1] ?? '')
      && segments[2] === 'users'
      && SAFE_DISCORD_TOKEN.test(segments[3] ?? '')
      && segments[4] === 'avatars'
      && SAFE_DISCORD_TOKEN.test((segments[5] ?? '').replace(/\.png$/, ''));
    if ((!validGlobal && !validGuild) || !parsed.pathname.endsWith('.png')) return undefined;
    return parsed.toString();
  } catch {
    return undefined;
  }
}

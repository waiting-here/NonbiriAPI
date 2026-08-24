import { describe, expect, it } from 'vitest';
import { ApiError } from '@shared/query/http';
import {
  isAffordable,
  isFishingGameSummary,
  normalizeFishingResult,
  normalizeFishingSettlement,
  normalizeFishingState,
  normalizeFishingStart,
  normalizeGamesConfig,
  normalizeLeaderboard,
  safeAvatarUrl,
} from './client';

const config = {
  master_enabled: true,
  credits: '5000000',
  game_profile_public: false,
  games: [{
    id: 'fishing', version: 1, enabled: true,
    params: {
      baits: [
        { id: 'worm', price: '2500000' },
        { id: 'lure', price: '5000000' },
        { id: 'premium', price: '7500000' },
      ],
      rtp_percent: { standard: 90, premium: 88 },
      treasure_multipliers: { bottle: 2, clover: 3, shell: 5 },
    },
  }],
};

const roundId = (character: string) => `grd_${character.toLowerCase().repeat(26)}`;
const TEST_ROUND_ID = roundId('a');
const OTHER_ROUND_ID = roundId('b');

const wireResult = {
  round_id: TEST_ROUND_ID, game_id: 'fishing', game_version: 1, bait: 'worm', price: '2500000',
  species_key: 'koi', tier: 'legend', size_cm: 180, is_junk: false, is_treasure: false,
  meter: true, credits_won: '12000000', credits: '14500000', settled_at: 1_787_450_010,
};
const settleWireResult = { ...wireResult, idempotent_replay: false };

describe('fishing client wire', () => {
  it('normalizes the closed-world config without converting amounts to numbers', () => {
    const parsed = normalizeGamesConfig(config);
    expect(parsed.credits).toBe('5000000');
    const fishing = parsed.games.find(isFishingGameSummary);
    expect(fishing?.params.baits.map(({ id }) => id)).toEqual(['worm', 'lure', 'premium']);
    expect(typeof fishing?.params.baits[0]?.price).toBe('string');
  });

  it('keeps future registrations visible while selecting fishing by id/version', () => {
    const parsed = normalizeGamesConfig({
      ...config,
      games: [
        config.games[0],
        { id: 'future-game', version: 1, enabled: false, params: { ignored: true } },
      ],
    });
    expect(parsed.games.map(({ id, version }) => `${id}@${version}`)).toEqual(['fishing@1', 'future-game@1']);
    expect(() => normalizeGamesConfig({ ...config, games: [config.games[0], config.games[0]] })).toThrow(ApiError);
  });

  it('fails closed on malformed or incomplete server wire', () => {
    expect(() => normalizeGamesConfig({ ...config, credits: '2.5' })).toThrow(ApiError);
    expect(normalizeGamesConfig({ ...config, credits: '-1' }).credits).toBe('-1');
    expect(normalizeGamesConfig({ ...config, credits: '-9223372036854775808' }).credits).toBe('-9223372036854775808');
    expect(normalizeGamesConfig({ ...config, credits: '9223372036854775807' }).credits).toBe('9223372036854775807');
    expect(() => normalizeGamesConfig({ ...config, credits: '9223372036854775808' })).toThrow(ApiError);
    expect(() => normalizeGamesConfig({ ...config, credits: '0e0' })).toThrow(ApiError);
    expect(() => normalizeGamesConfig({
      ...config,
      games: [{ ...config.games[0], params: { ...config.games[0].params, baits: [{ id: 'worm', price: '0' }, ...config.games[0].params.baits.slice(1)] } }],
    })).toThrow(ApiError);
    expect(() => normalizeFishingState({ pending_round: null, unrevealed_result: null })).toThrow(ApiError);
    expect(() => normalizeFishingState({ pending_round: null, unrevealed_result: null, has_more_unrevealed: true })).toThrow(ApiError);
    expect(() => normalizeFishingState({
      pending_round: { round_id: TEST_ROUND_ID, bait: 'worm', price: '0', created_at: 1, auto_settle_at: 2 },
      unrevealed_result: null,
      has_more_unrevealed: false,
    })).toThrow(ApiError);
    expect(() => normalizeFishingState({
      pending_round: { round_id: TEST_ROUND_ID, bait: 'worm', price: '2500000', created_at: 3, auto_settle_at: 2 },
      unrevealed_result: null,
      has_more_unrevealed: false,
    })).toThrow(ApiError);
    expect(() => normalizeFishingResult({ ...wireResult, credits_won: 12 })).toThrow(ApiError);
    expect(() => normalizeFishingResult({ ...wireResult, price: '0' })).toThrow(ApiError);
    expect(normalizeFishingResult({ ...wireResult, credits: '-1' }).credits).toBe('-1');
    expect(normalizeFishingResult({ ...wireResult, credits: '-9223372036854775808' }).credits).toBe('-9223372036854775808');
    expect(normalizeFishingResult({ ...wireResult, credits: '9223372036854775807' }).credits).toBe('9223372036854775807');
    expect(() => normalizeFishingResult({ ...wireResult, credits: '9223372036854775808' })).toThrow(ApiError);
    expect(normalizeFishingStart({
      round_id: TEST_ROUND_ID, game_id: 'fishing', game_version: 1, bait: 'worm', price: '2500000',
      credits: '-1', state: 'pending', created_at: 1, auto_settle_at: 2, idempotent_replay: false,
    }).credits).toBe('-1');
    expect(() => normalizeFishingStart({
      round_id: TEST_ROUND_ID, game_id: 'fishing', game_version: 1, bait: 'lure', price: '5000000',
      credits: '5000000', state: 'pending', created_at: 1, auto_settle_at: 2, idempotent_replay: false,
    }, 'worm')).toThrow(ApiError);
    expect(() => normalizeFishingState({
      pending_round: null,
      unrevealed_result: { ...wireResult, credits: '9223372036854775808' },
      has_more_unrevealed: false,
    })).toThrow(ApiError);
    expect(() => normalizeFishingState({
      pending_round: { round_id: TEST_ROUND_ID, bait: 'worm', price: '2500000', created_at: 1, auto_settle_at: 2 },
      unrevealed_result: { ...wireResult, round_id: TEST_ROUND_ID },
      has_more_unrevealed: false,
    })).toThrow(ApiError);
    expect(() => normalizeLeaderboard({ board: 'total', window_start: null, entries: [], me: null }, 'total')).toThrow(ApiError);
    expect(() => normalizeLeaderboard({ board: 'single', window_start: null, entries: {}, me: null }, 'single')).toThrow(ApiError);
  });

  it('rejects overlong or control-bearing authoritative identifiers without truncating them', () => {
    const tooLong = 'a'.repeat(65);
    expect(() => normalizeFishingResult({ ...wireResult, round_id: tooLong })).toThrow(ApiError);
    expect(() => normalizeFishingResult({ ...wireResult, round_id: 'x'.repeat(64) })).toThrow(ApiError);
    expect(() => normalizeFishingResult({ ...wireResult, round_id: 'future.round/v2' })).toThrow(ApiError);
    expect(() => normalizeFishingResult({ ...wireResult, round_id: 'grd_abc' })).toThrow(ApiError);
    expect(() => normalizeFishingResult({ ...wireResult, round_id: 'grd/test' })).toThrow(ApiError);
    expect(() => normalizeFishingResult({ ...wireResult, round_id: `grd_${'a'.repeat(25)}` })).toThrow(ApiError);
    expect(normalizeFishingResult({ ...wireResult, round_id: `grd_${'a'.repeat(27)}` }).roundId).toHaveLength(31);
    expect(normalizeFishingResult({ ...wireResult, round_id: `grd_${'a'.repeat(56)}` }).roundId).toHaveLength(60);
    expect(normalizeFishingResult({ ...wireResult, round_id: `grd_${'a'.repeat(60)}` }).roundId).toHaveLength(64);
    expect(() => normalizeFishingResult({ ...wireResult, round_id: `grd_${'A'.repeat(26)}` })).toThrow(ApiError);
    expect(() => normalizeFishingResult({ ...wireResult, round_id: `grd_${'W'.repeat(26)}` })).toThrow(ApiError);
    expect(normalizeFishingResult({ ...wireResult, round_id: `grd_${'a'.repeat(26)}` }).roundId).toHaveLength(30);
    expect(normalizeFishingResult({ ...wireResult, round_id: `grd_${'wxyz234567'.repeat(3)}` }).roundId).toHaveLength(34);
    for (const invalidCharacter of ['0', '1', '8', '9']) {
      expect(() => normalizeFishingResult({ ...wireResult, round_id: `grd_${invalidCharacter.repeat(26)}` })).toThrow(ApiError);
    }
    expect(() => normalizeFishingResult({ ...wireResult, round_id: `grd_${'a'.repeat(25)}=` })).toThrow(ApiError);
    expect(() => normalizeFishingResult({ ...wireResult, round_id: 'grd test' })).toThrow(ApiError);
    expect(() => normalizeFishingResult({ ...wireResult, round_id: 'grd\t/test' })).toThrow(ApiError);
    expect(() => normalizeFishingResult({ ...wireResult, round_id: 'grd\u0000/test' })).toThrow(ApiError);
    expect(() => normalizeFishingResult({ ...wireResult, species_key: `${'a'.repeat(64)}\n` })).toThrow(ApiError);
    expect(() => normalizeFishingResult({ ...wireResult, tier: 'legend\u0000' })).toThrow(ApiError);
    expect(() => normalizeFishingResult({ ...wireResult, species_key: 'not-a-v1-species' })).toThrow(ApiError);
    expect(() => normalizeFishingResult({ ...wireResult, species_key: 'koi', tier: 'small', size_cm: 20 })).toThrow(ApiError);
    expect(() => normalizeLeaderboard({
      board: 'single', window_start: null,
      entries: [{ rank: 1, species_key: tooLong, size_cm: 100, is_me: false }], me: null,
    }, 'single')).toThrow(ApiError);
  });

  it('requires a settle response to identify the requested round exactly', () => {
    expect(normalizeFishingSettlement(settleWireResult, TEST_ROUND_ID).roundId).toBe(TEST_ROUND_ID);
    expect(() => normalizeFishingSettlement(settleWireResult, OTHER_ROUND_ID)).toThrow(ApiError);
    expect(() => normalizeFishingSettlement(wireResult, TEST_ROUND_ID)).toThrow(ApiError);
    expect(() => normalizeFishingSettlement({ ...settleWireResult, idempotent_replay: null }, TEST_ROUND_ID)).toThrow(ApiError);
    expect(normalizeFishingSettlement({ ...settleWireResult, credits: '-1' }, TEST_ROUND_ID).credits).toBe('-1');
  });

  it('keeps pending state outcome-free and parses recovered outcome separately', () => {
    const state = normalizeFishingState({
      pending_round: { round_id: TEST_ROUND_ID, bait: 'worm', price: '2500000', created_at: 1, auto_settle_at: 2 },
      unrevealed_result: null,
      has_more_unrevealed: false,
    });
    expect(state.pendingRound).toMatchObject({ roundId: TEST_ROUND_ID, bait: 'worm' });
    expect(state.unrevealedResult).toBeNull();
    expect(normalizeFishingResult(wireResult).speciesKey).toBe('koi');
    expect(() => normalizeFishingState({
      pending_round: null,
      unrevealed_result: { ...wireResult, idempotent_replay: false },
      has_more_unrevealed: false,
    })).toThrow(ApiError);
  });

  it('normalizes anonymous leaderboard rows by omission and keeps public rows bounded', () => {
    const parsed = normalizeLeaderboard({
      board: 'single', window_start: null,
      entries: [
         { rank: 1, species_key: 'koi', size_cm: 180, is_me: false },
         { rank: 2, species_key: 'taimen', size_cm: 170, display_name: 'Angler', avatar_url: 'https://cdn.discordapp.com/avatars/a/b.png?size=64', level4_badge: true, is_me: true },
      ],
      me: { rank: 2, species_key: 'taimen', size_cm: 170, display_name: 'Angler', avatar_url: 'https://cdn.discordapp.com/avatars/a/b.png?size=64', level4_badge: true, is_me: true },
    }, 'single');
    expect(parsed.entries[0]).not.toHaveProperty('displayName');
    expect(parsed.entries[0]).not.toHaveProperty('avatarUrl');
    expect(parsed.entries[0]).not.toHaveProperty('level4Badge');
    expect(parsed.entries[1]).toMatchObject({ displayName: 'Angler', level4Badge: true });
  });

  it('rejects malformed identity fields on anonymous rows and safely omits invalid public avatars', () => {
    const base = { board: 'single' as const, window_start: null, entries: [{ rank: 1, species_key: 'koi', size_cm: 180, is_me: false }], me: null };
    for (const field of [
      { display_name: '' },
      { display_name: null },
      { display_name: 42 },
      { avatar_url: null },
      { avatar_url: '' },
      { avatar_url: 42 },
      { level4_badge: null },
      { level4_badge: 'yes' },
    ]) {
      expect(() => normalizeLeaderboard({ ...base, entries: [{ ...base.entries[0], ...field }] }, 'single')).toThrow(ApiError);
    }
    const publicRow = normalizeLeaderboard({
      ...base,
      entries: [{ rank: 1, species_key: 'koi', size_cm: 180, display_name: 'Public', avatar_url: 'https://evil.example/a.png', is_me: false }],
      me: null,
    }, 'single');
    expect(publicRow.entries[0]).toMatchObject({ displayName: 'Public' });
    expect(publicRow.entries[0]).not.toHaveProperty('avatarUrl');
  });

  it('rejects total-board species fields when present and rejects blank public names', () => {
    const total = { board: 'total' as const, window_start: 1_787_000_000, entries: [], me: null };
    expect(() => normalizeLeaderboard({
      ...total,
      entries: [{ rank: 1, species_key: null, total_credits: '1', is_me: false }],
    }, 'total')).toThrow(ApiError);
    expect(() => normalizeLeaderboard({
      board: 'single', window_start: null,
      entries: [{ rank: 1, species_key: 'koi', size_cm: 180, display_name: ' \t\n ', is_me: false }],
      me: null,
    }, 'single')).toThrow(ApiError);
  });

  it('derives meter from the frozen large-fish outcome rule', () => {
    expect(() => normalizeFishingResult({ ...wireResult, meter: false })).toThrow(ApiError);
    expect(normalizeFishingResult({ ...wireResult, size_cm: 100, meter: true }).meter).toBe(true);
    expect(() => normalizeFishingResult({ ...wireResult, species_key: 'japanese_eel', tier: 'giant', size_cm: 99, meter: true })).toThrow(ApiError);
    expect(normalizeFishingResult({ ...wireResult, species_key: 'japanese_eel', tier: 'giant', size_cm: 99, meter: false }).meter).toBe(false);
    expect(() => normalizeFishingResult({ ...wireResult, species_key: 'boot', tier: 'junk', size_cm: 0, is_junk: true, meter: true })).toThrow(ApiError);
    expect(normalizeFishingResult({ ...wireResult, species_key: 'boot', tier: 'junk', size_cm: 0, is_junk: true, meter: false }).meter).toBe(false);
  });

  it('allowlists static Discord PNGs and never guesses affordability through floats', () => {
    expect(safeAvatarUrl('https://cdn.discordapp.com/avatars/a/b.png?size=64')).toBe('https://cdn.discordapp.com/avatars/a/b.png?size=64');
    expect(safeAvatarUrl('https://cdn.discordapp.com/guilds/g/users/u/avatars/hash.png?size=64')).toBe('https://cdn.discordapp.com/guilds/g/users/u/avatars/hash.png?size=64');
    expect(safeAvatarUrl('https://evil.example/a.png')).toBeUndefined();
    expect(safeAvatarUrl('https://cdn.discordapp.com/avatars/a/b.gif')).toBeUndefined();
    expect(safeAvatarUrl('https://cdn.discordapp.com:444/avatars/a/b.png?size=64')).toBeUndefined();
    expect(safeAvatarUrl('https://cdn.discordapp.com:443/avatars/a/b.png?size=64')).toBeUndefined();
    expect(safeAvatarUrl('https://cdn.discordapp.com/avatars/a/b.png?size=128')).toBeUndefined();
    expect(safeAvatarUrl('https://cdn.discordapp.com/avatars/a/b.png?size=64&x=1')).toBeUndefined();
    expect(safeAvatarUrl('https://cdn.discordapp.com/other/a/b.png?size=64')).toBeUndefined();
    expect(isAffordable('5000000', '2500000')).toBe(true);
    expect(isAffordable('2499999', '2500000')).toBe(false);
    expect(isAffordable('9007199254740991', '1')).toBe(true);
    expect(isAffordable('not-an-amount', '1')).toBe(false);
  });

  it('rejects a leaderboard response above the frozen Top 20 bound', () => {
    const entries = Array.from({ length: 21 }, (_, index) => ({
      rank: index + 1,
      species_key: 'koi',
       size_cm: 120,
      is_me: false,
    }));
    expect(() => normalizeLeaderboard({ board: 'single', window_start: null, entries, me: null }, 'single')).toThrow(ApiError);
  });

  it('enforces the closed species tier size range and canonical entry ranks', () => {
    expect(() => normalizeLeaderboard({
      board: 'single', window_start: null,
      entries: [{ rank: 1, species_key: 'koi', size_cm: 99, is_me: false }], me: null,
    }, 'single')).toThrow(ApiError);
    expect(() => normalizeLeaderboard({
      board: 'single', window_start: null,
      entries: [{ rank: 1, species_key: 'koi', size_cm: 180, is_me: false }, { rank: 1, species_key: 'taimen', size_cm: 170, is_me: false }], me: null,
    }, 'single')).toThrow(ApiError);
    expect(() => normalizeLeaderboard({
      board: 'single', window_start: null,
      entries: [{ rank: 2, species_key: 'koi', size_cm: 180, is_me: false }], me: null,
    }, 'single')).toThrow(ApiError);
  });

  it('requires the complete leaderboard envelope and consistent mine projection', () => {
    const entry = { rank: 2, species_key: 'taimen', size_cm: 170, is_me: true };
    const valid = { board: 'single', window_start: null, entries: [
      { rank: 1, species_key: 'koi', size_cm: 180, is_me: false },
      entry,
    ], me: entry };
    expect(normalizeLeaderboard(valid, 'single').me).toMatchObject({ rank: 2, isMe: true });
    expect(() => normalizeLeaderboard({ ...valid, window_start: undefined }, 'single')).toThrow(ApiError);
    expect(() => normalizeLeaderboard({ ...valid, me: undefined }, 'single')).toThrow(ApiError);
    expect(() => normalizeLeaderboard({ ...valid, me: { ...entry, is_me: false } }, 'single')).toThrow(ApiError);
    expect(() => normalizeLeaderboard({ ...valid, entries: [
      { rank: 1, species_key: 'koi', size_cm: 180, is_me: true },
      entry,
    ], me: entry }, 'single')).toThrow(ApiError);
    expect(() => normalizeLeaderboard({ ...valid, entries: [
      { rank: 1, species_key: 'koi', size_cm: 180, is_me: false },
      { ...entry, size_cm: 169 },
    ], me: entry }, 'single')).toThrow(ApiError);
    expect(() => normalizeLeaderboard({ ...valid, entries: [
      { rank: 1, species_key: 'koi', size_cm: 180, is_me: false },
      { rank: 2, species_key: 'taimen', size_cm: 170, is_me: false },
    ], me: entry }, 'single')).toThrow(ApiError);
    expect(() => normalizeLeaderboard({ ...valid, entries: [
      { rank: 1, species_key: 'koi', size_cm: 180, is_me: true },
      { rank: 2, species_key: 'taimen', size_cm: 170, is_me: true },
    ], me: entry }, 'single')).toThrow(ApiError);
    expect(() => normalizeLeaderboard({
      board: 'single', window_start: null,
      entries: [{ rank: 1, species_key: 'koi', size_cm: 180, is_me: true }], me: null,
    }, 'single')).toThrow(ApiError);
    const completeTopTwenty = Array.from({ length: 20 }, (_, index) => ({
      rank: index + 1,
      species_key: 'koi',
      size_cm: 120,
      is_me: false,
    }));
    expect(normalizeLeaderboard({
      board: 'single', window_start: null,
      entries: completeTopTwenty,
      me: { rank: 21, species_key: 'koi', size_cm: 180, is_me: true },
    }, 'single').me?.rank).toBe(21);
    expect(() => normalizeLeaderboard({
      board: 'single', window_start: null,
      entries: [{ rank: 1, species_key: 'koi', size_cm: 180, is_me: false }],
      me: { rank: 21, species_key: 'koi', size_cm: 180, is_me: true },
    }, 'single')).toThrow(ApiError);
    expect(normalizeLeaderboard({
      board: 'single', window_start: null,
      entries: completeTopTwenty,
      me: { rank: Number.MAX_SAFE_INTEGER, species_key: 'koi', size_cm: 180, is_me: true },
    }, 'single').me?.rank).toBe(Number.MAX_SAFE_INTEGER);
  });
});

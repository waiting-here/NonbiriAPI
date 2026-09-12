import { afterEach, describe, expect, it, vi } from 'vitest';
import { readFishingLeaderboard, startFishing } from './api';
import {
  normalizeFishingLeaderboard,
  normalizeFishingPending,
  normalizeFishingResult,
  normalizeFishingState,
} from './normalize';

const id = 'fb_AAAAAAAAAAAAAAAAAAAAAA';
function resultWire(count: 1 | 10 = 1) {
  return {
    batch_id: id,
    bait: 'worm',
    count,
    unit_price: '1.25',
    entry_total: count === 1 ? '1.25' : '12.5',
    rules_version: 1,
    payment: { general: count === 1 ? '1.25' : '12.5', game: '0' },
    outcomes: Array.from({ length: count }, (_, ordinal) => ({
      ordinal,
      species_key: 'whitebait',
      tier: 'small',
      size_cm: 5 + ordinal,
      blue_fat_fish_length_cm: undefined as string | null | undefined,
      reward: '0.5',
    })),
    payout_total: count === 1 ? '0.5' : '5',
    balance: '100.25',
    game_balance: '0',
    settled_at: 1_800_000_000,
    idempotent_replay: false,
  };
}
function pendingWire(state: 'settlement_pending' | 'recovery_required' = 'settlement_pending') {
  return {
    batch_id: id,
    bait: 'worm',
    count: 10,
    entry_total: '12.5',
    rules_version: 1,
    payment: { general: '12.5', game: '0' },
    state,
    next_attempt_at: state === 'settlement_pending' ? 1_800_000_010 : null,
    retry_exhausted: state === 'recovery_required',
  };
}

describe('Fishing beta.1 wire', () => {
  afterEach(() => vi.unstubAllGlobals());
  it('validates one and ten outcome atomic batches with exact arithmetic and ordinal order', () => {
    expect(normalizeFishingResult(resultWire()).outcomes).toHaveLength(1);
    expect(normalizeFishingResult(resultWire(10)).payoutTotal).toBe('5');
    const broken = resultWire(10);
    broken.outcomes[4].ordinal = 5;
    expect(() => normalizeFishingResult(broken)).toThrow(/ordinal/i);
    const payout = resultWire(10);
    payout.payout_total = '5.001';
    expect(() => normalizeFishingResult(payout)).toThrow(/arithmetic/i);
  });

  it('enforces the pending/recovery matrix and state priority', () => {
    expect(normalizeFishingPending(pendingWire()).state).toBe('settlement_pending');
    expect(normalizeFishingPending(pendingWire('recovery_required')).retryExhausted).toBe(true);
    const impossible = pendingWire('recovery_required');
    impossible.next_attempt_at = 1_800_000_010;
    expect(() => normalizeFishingPending(impossible)).toThrow(/matrix/i);
    expect(() =>
      normalizeFishingState({
        settlement_pending: pendingWire(),
        unrevealed: resultWire(),
        has_more_unrevealed: false,
      }),
    ).toThrow(/priority/i);
    expect(() =>
      normalizeFishingState({
        settlement_pending: null,
        unrevealed: null,
        has_more_unrevealed: true,
      }),
    ).toThrow(/more fishing results/i);
  });

  it('keeps anonymous/public and Top20+me leaderboard unions closed', () => {
    const single = normalizeFishingLeaderboard(
      {
        board: 'single',
        window_start: null,
        entries: [
          {
            rank: '1',
            species_key: 'koi',
            size_cm: 100,
            identity: { kind: 'anonymous' },
            is_me: false,
          },
        ],
        me: {
          rank: '21',
          species_key: 'whitebait',
          size_cm: 5,
          identity: {
            kind: 'public',
            display_name: 'A very long but valid angler',
            avatar_url: null,
          },
          is_me: true,
        },
      },
      'single',
    );
    expect(single.me?.rank).toBe('21');
    const duplicateMe = {
      board: 'single',
      window_start: null,
      entries: [
        {
          rank: '1',
          species_key: 'koi',
          size_cm: 100,
          identity: { kind: 'anonymous' },
          is_me: true,
        },
      ],
      me: {
        rank: '21',
        species_key: 'whitebait',
        size_cm: 5,
        identity: { kind: 'anonymous' },
        is_me: true,
      },
    };
    expect(() => normalizeFishingLeaderboard(duplicateMe, 'single')).toThrow(/me row/i);
    expect(() =>
      normalizeFishingLeaderboard(
        {
          board: 'total',
          window_start: 1,
          entries: [],
          me: {
            rank: '21',
            species_key: 'koi',
            size_cm: 100,
            identity: { kind: 'anonymous' },
            is_me: true,
          },
        },
        'total',
      ),
    ).toThrow(/row/i);
  });

  it('accepts a signed SM128 result balance and rejects overflow', () => {
    const boundary = resultWire();
    boundary.balance = '-170141183460469231731687303715884105.727';
    expect(normalizeFishingResult(boundary).balance).toBe(boundary.balance);
    boundary.balance = '170141183460469231731687303715884105.728';
    expect(() => normalizeFishingResult(boundary)).toThrow(/fishing balance/i);
  });

  it('normalizes the optional blue fat fish length without JavaScript number conversion', () => {
    const old = resultWire();
    Reflect.deleteProperty(old.outcomes[0], 'blue_fat_fish_length_cm');
    expect(normalizeFishingResult(old).outcomes[0].blueFatFishLengthCM).toBeNull();

    const blue = resultWire();
    blue.outcomes[0] = {
      ordinal: 0,
      species_key: 'koi',
      tier: 'legend',
      size_cm: 100,
      blue_fat_fish_length_cm: `201${'9'.repeat(125)}`,
      reward: '0.5',
    };
    const normalized = normalizeFishingResult(blue).outcomes[0];
    expect(normalized.blueFatFishLengthCM).toBe(`201${'9'.repeat(125)}`);
    expect(normalized.sizeCM).toBe(100);
    expect(normalized.speciesKey).toBe('koi');

    for (const invalidLength of ['200', '0201', '201.5', `2${'0'.repeat(128)}`]) {
      const invalid = resultWire();
      invalid.outcomes[0] = {
        ordinal: 0,
        species_key: 'koi',
        tier: 'legend',
        size_cm: 100,
        blue_fat_fish_length_cm: invalidLength,
        reward: '0.5',
      };
      expect(() => normalizeFishingResult(invalid)).toThrow(/blue fat fish length/i);
    }

    const nonLegend = resultWire();
    nonLegend.outcomes[0].blue_fat_fish_length_cm = '201';
    expect(() => normalizeFishingResult(nonLegend)).toThrow(/blue fat fish length/i);
  });

  it('accepts recent_single with a long display length and keeps old single rows nullable', () => {
    const length = `201${'8'.repeat(125)}`;
    const recent = normalizeFishingLeaderboard(
      {
        board: 'recent_single',
        window_start: 1_799_000_000,
        entries: [
          {
            rank: '1',
            species_key: 'taimen',
            size_cm: 120,
            blue_fat_fish_length_cm: length,
            identity: { kind: 'anonymous' },
            is_me: false,
          },
        ],
        me: null,
      },
      'recent_single',
    );
    expect(recent.board).toBe('recent_single');
    expect(recent.windowStart).toBe(1_799_000_000);
    if (recent.board !== 'recent_single') throw new Error('Expected recent single board.');
    expect(recent.entries[0].blueFatFishLengthCM).toBe(length);

    const historical = normalizeFishingLeaderboard(
      {
        board: 'single',
        window_start: null,
        entries: [
          {
            rank: '1',
            species_key: 'koi',
            size_cm: 100,
            identity: { kind: 'anonymous' },
            is_me: false,
          },
        ],
        me: null,
      },
      'single',
    );
    if (historical.board !== 'single') throw new Error('Expected historical single board.');
    expect(historical.entries[0].blueFatFishLengthCM).toBeNull();
  });

  it('requests each fishing board with its exact board discriminator', async () => {
    const boardWire = (board: 'single' | 'recent_single' | 'total') =>
      board === 'total'
        ? { board, window_start: 1_799_000_000, entries: [], me: null }
        : {
            board,
            window_start: board === 'single' ? null : 1_799_000_000,
            entries: [],
            me: null,
          };
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const board = new URL(String(input), 'https://example.test').searchParams.get('board') as
        'single' | 'recent_single' | 'total';
      return new Response(JSON.stringify(boardWire(board)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    });
    vi.stubGlobal('fetch', fetchMock);

    await Promise.all([
      readFishingLeaderboard('single'),
      readFishingLeaderboard('recent_single'),
      readFishingLeaderboard('total'),
    ]);

    expect(
      fetchMock.mock.calls.map(([input]) => new URL(String(input), 'https://example.test').search),
    ).toEqual(['?board=single', '?board=recent_single', '?board=total']);
  });

  it('preserves a JSON body on HTTP 202 instead of treating it as empty', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(JSON.stringify(pendingWire()), {
            status: 202,
            headers: { 'Content-Type': 'application/json' },
          }),
      ),
    );
    await expect(
      startFishing({ bait: 'worm', count: 10, idempotencyKey: 'stable-key' }),
    ).resolves.toMatchObject({ batchID: id, state: 'settlement_pending' });
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});

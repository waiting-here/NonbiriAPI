import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  readFishingLeaderboard,
  readFishingPondLeaderboard,
  returnFishingOutcome,
  returnFishingBatch,
  startFishing,
} from './api';
import {
  normalizeFishingLeaderboard,
  normalizeFishingPending,
  normalizeFishingResult,
  normalizeFishingState,
  normalizeFishingPondReceipt,
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
      net_reward: '0.5',
      rake: { platform: '0', welfare: '0', thursday: '0' },
    })),
    payout_total: count === 1 ? '0.5' : '5',
    net_payout_total: count === 1 ? '0.5' : '5',
    rake: { platform: '0', welfare: '0', thursday: '0' },
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

describe('Fishing wire', () => {
  it('uses authoritative proceeds and rake without recomputing settlement', () => {
    const value = resultWire(10);
    value.rules_version = 2;
    value.net_payout_total = '4.85';
    value.rake = { platform: '0.05', welfare: '0.05', thursday: '0.05' };
    value.outcomes.forEach((outcome) => {
      outcome.net_reward = '0.485';
      outcome.rake = { platform: '0.005', welfare: '0.005', thursday: '0.005' };
    });
    expect(normalizeFishingResult(value).netPayoutTotal).toBe('4.85');
    const incorrect = structuredClone(value);
    incorrect.outcomes[0].net_reward = '0.486';
    expect(normalizeFishingResult(incorrect).outcomes[0].netReward).toBe('0.486');
    const sum = structuredClone(value);
    sum.rake.platform = '0.051';
    expect(normalizeFishingResult(sum).rake.platform).toBe('0.051');
    value.rules_version = 1;
    expect(normalizeFishingResult(value).netPayoutTotal).toBe('4.85');
    expect(() => normalizeFishingResult({ ...value, net_payout_total: '4.8501' })).toThrow();
  });

  afterEach(() => vi.unstubAllGlobals());
  it('validates one and ten outcome batches and their display ordinals', () => {
    expect(normalizeFishingResult(resultWire()).outcomes).toHaveLength(1);
    expect(normalizeFishingResult(resultWire(10)).payoutTotal).toBe('5');
    const broken = resultWire(10);
    broken.outcomes[4].ordinal = 5;
    expect(() => normalizeFishingResult(broken)).toThrow(/ordinal/i);
    const payout = resultWire(10);
    payout.payout_total = '5.001';
    expect(normalizeFishingResult(payout).payoutTotal).toBe('5.001');
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
      net_reward: '0.5',
      rake: { platform: '0', welfare: '0', thursday: '0' },
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
        net_reward: '0.5',
        rake: { platform: '0', welfare: '0', thursday: '0' },
      };
      expect(() => normalizeFishingResult(invalid)).toThrow(/blue fat fish length/i);
    }

    const nonLegend = resultWire();
    nonLegend.outcomes[0].blue_fat_fish_length_cm = '201';
    expect(() => normalizeFishingResult(nonLegend)).toThrow(/blue fat fish length/i);
  });

  it('keeps presentation lengths exact without rejecting simultaneous display fields', () => {
    const value = resultWire();
    const length = '9'.repeat(128);
    Object.assign(value.outcomes[0], {
      species_key: 'koi',
      tier: 'legend',
      size_cm: 137,
      milk_crab_length_cm: length,
    });
    expect(normalizeFishingResult(value).outcomes[0].milkCrabLengthCM).toBe(length);
    expect(normalizeFishingResult(resultWire()).outcomes[0].milkCrabLengthCM).toBeNull();
    const row = {
      rank: '1',
      species_key: 'koi',
      size_cm: 137,
      milk_crab_length_cm: length,
      identity: { kind: 'anonymous' },
      is_me: false,
    };
    const board = { board: 'single', window_start: null, entries: [row], me: null };
    expect(normalizeFishingLeaderboard(board, 'single').entries[0]).toMatchObject({
      milkCrabLengthCM: length,
      blueFatFishLengthCM: null,
    });
    value.outcomes[0].blue_fat_fish_length_cm = '201';
    expect(normalizeFishingResult(value).outcomes[0]).toMatchObject({
      milkCrabLengthCM: length,
      blueFatFishLengthCM: '201',
    });
    Object.assign(row, { blue_fat_fish_length_cm: '201' });
    expect(normalizeFishingLeaderboard(board, 'single').entries[0]).toMatchObject({
      milkCrabLengthCM: length,
      blueFatFishLengthCM: '201',
    });
    value.outcomes[0].blue_fat_fish_length_cm = null;
    for (const invalid of ['200', '0201', '201.5', '9'.repeat(129), 201]) {
      Object.assign(value.outcomes[0], { milk_crab_length_cm: invalid });
      expect(() => normalizeFishingResult(value)).toThrow(/milk crab length/);
    }
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

describe('Pond wire projection', () => {
  it('keeps old catches ineligible and accepts gifted species and persisted donor identity', () => {
    expect(normalizeFishingResult(resultWire()).outcomes[0].pond).toBeNull();
    const value = resultWire();
    Object.assign(value.outcomes[0], {
      species_key: 'old_tire',
      tier: 'junk',
      size_cm: 0,
      reward: '0',
      net_reward: '0',
      pond: {
        return_state: 'gift',
        gift: {
          kind: 'public',
          display_name: 'Donor at return time',
          avatar_url: null,
        },
      },
    });
    const outcome = normalizeFishingResult(value).outcomes[0];
    expect(outcome.speciesKey).toBe('old_tire');
    expect(outcome.pond).toEqual({
      returnState: 'gift',
      gift: { kind: 'public', displayName: 'Donor at return time', avatarURL: null },
    });
    Object.assign(value.outcomes[0], {
      pond: { return_state: 'gift', gift: { kind: 'anonymous' } },
    });
    expect(normalizeFishingResult(value).outcomes[0].pond?.gift).toEqual({ kind: 'anonymous' });
  });

  it('checks receipt identity and uses the natural catch resource without a payload', async () => {
    const receipt = {
      batch_id: id,
      ordinal: 0,
      return_state: 'returned',
      returned_at: 1_800_000_000,
    };
    expect(normalizeFishingPondReceipt(receipt).returnState).toBe('returned');
    const fetchMock = vi.fn(
      async () =>
        new Response(JSON.stringify({ ...receipt, ordinal: 1 }), {
          headers: { 'content-type': 'application/json' },
        }),
    );
    vi.stubGlobal('fetch', fetchMock);
    await expect(returnFishingOutcome(id, 0)).rejects.toMatchObject({ code: 'invalid_response' });
    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(init.method).toBe('POST');
    expect(init.body).toBeUndefined();
    expect(new Headers(init.headers).get('Idempotency-Key')).toBeNull();
  });

  it('reads mixed batch receipts from one bodyless request and rejects an unrelated batch', async () => {
    const wire = {
      batch_id: id,
      receipts: [
        { batch_id: id, ordinal: 1, return_state: 'returned', returned_at: 1_800_000_000 },
        { batch_id: id, ordinal: 3, return_state: 'released', returned_at: 1_800_000_001 },
      ],
    };
    const fetchMock = vi.fn(
      async () =>
        new Response(JSON.stringify(wire), {
          headers: { 'content-type': 'application/json' },
        }),
    );
    vi.stubGlobal('fetch', fetchMock);
    const result = await returnFishingBatch(id);
    expect(result.receipts.map((receipt) => receipt.returnState)).toEqual(['returned', 'released']);
    const [input, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(input).toBe(`/api/games/fishing/batches/${id}/return`);
    expect(init.method).toBe('POST');
    expect(init.body).toBeUndefined();
    expect(new Headers(init.headers).get('Idempotency-Key')).toBeNull();
    wire.batch_id = 'fb_AQEBAQEBAQEBAQEBAQEBAQ';
    await expect(returnFishingBatch(id)).rejects.toMatchObject({ code: 'invalid_response' });
  });

  it('keeps large counts exact and rejects a mismatched board period', async () => {
    const wire = {
      board: 'gifts',
      period: 'all',
      window_start: null,
      entries: [
        { rank: '1', item_count: '9007199254740993', identity: { kind: 'anonymous' }, is_me: true },
      ],
      me: null,
    };
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(JSON.stringify(wire), {
            headers: { 'content-type': 'application/json' },
          }),
      ),
    );
    const parsed = await readFishingPondLeaderboard('gifts', 'all');
    expect(parsed.entries[0].itemCount).toBe('9007199254740993');
    await expect(readFishingPondLeaderboard('gifts', '7d')).rejects.toMatchObject({
      code: 'invalid_response',
    });
  });

  afterEach(() => vi.unstubAllGlobals());
});

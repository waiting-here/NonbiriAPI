import { afterEach, describe, expect, it, vi } from 'vitest';
import { startFishing } from './api';
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
    outcomes: Array.from({ length: count }, (_, ordinal) => ({
      ordinal,
      species_key: 'whitebait',
      tier: 'small',
      size_cm: 5 + ordinal,
      reward: '0.5',
    })),
    payout_total: count === 1 ? '0.5' : '5',
    balance: '100.25',
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

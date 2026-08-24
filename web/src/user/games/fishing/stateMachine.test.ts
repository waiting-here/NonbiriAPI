import { describe, expect, it } from 'vitest';
import {
  appendDeferredResult,
  FISHING_PHASE_DURATIONS_MS,
  fishingFlowReducer,
  initialFishingFlow,
  isFishingBusy,
  MAX_DEFERRED_RESULTS,
  nextFishingPhase,
} from './stateMachine';
import type { FishingResult } from './types';

const BASE32 = 'abcdefghijklmnopqrstuvwxyz234567';
const testRoundId = (index: number) => `grd_${'a'.repeat(25)}${BASE32[index]}`;
const TEST_ROUND_ID = `grd_${'b'.repeat(26)}`;

const result: FishingResult = {
  roundId: TEST_ROUND_ID,
  gameId: 'fishing',
  gameVersion: 1,
  bait: 'worm',
  price: '2500000',
  speciesKey: 'koi',
  tier: 'legend',
  sizeCm: 180,
  isJunk: false,
  isTreasure: false,
  meter: true,
  creditsWon: '12000000',
  credits: '14500000',
  settledAt: 1_787_450_010,
};

describe('fishing flow state machine', () => {
  it('keeps presentation phases separate from server result and balance', () => {
    let state = fishingFlowReducer(initialFishingFlow, { type: 'begin', roundId: result.roundId });
    expect(state.phase).toBe('casting');
    expect(state.result).toBeNull();
    expect(state.roundId).toBe(result.roundId);

    state = fishingFlowReducer(state, { type: 'phase', phase: 'waiting' });
    state = fishingFlowReducer(state, { type: 'phase', phase: 'reeling' });
    state = fishingFlowReducer(state, { type: 'phase', phase: 'settling' });
    expect(state.result).toBeNull();
    state = fishingFlowReducer(state, { type: 'settled', result });
    expect(state.phase).toBe('result');
    expect(state.result).toEqual(result);
    expect(state.resultSource).toBe('settled');
  });

  it('restores a server result without replaying animation or deriving payout', () => {
    const state = fishingFlowReducer(initialFishingFlow, { type: 'recovered', result });
    expect(state).toMatchObject({ phase: 'result', result, resultSource: 'recovered' });
    expect(fishingFlowReducer(state, { type: 'acknowledged' })).toEqual(initialFishingFlow);
  });

  it('preserves a failed pending round for an explicit retry', () => {
    const error = new Error('temporary failure');
    const started = fishingFlowReducer(initialFishingFlow, { type: 'begin', roundId: result.roundId });
    const failed = fishingFlowReducer(started, { type: 'failed', error });
    expect(failed.phase).toBe('error');
    expect(failed.roundId).toBe(result.roundId);
    expect(failed.result).toBeNull();
    expect(fishingFlowReducer(failed, { type: 'phase', phase: 'settling' }).phase).toBe('settling');
  });

  it('defines bounded animation timing and busy phases', () => {
    expect(FISHING_PHASE_DURATIONS_MS.casting).toBeGreaterThan(0);
    expect(FISHING_PHASE_DURATIONS_MS.waiting).toBeGreaterThan(0);
    expect(FISHING_PHASE_DURATIONS_MS.reeling).toBeGreaterThan(0);
    expect(nextFishingPhase('casting')).toBe('waiting');
    expect(nextFishingPhase('waiting')).toBe('reeling');
    expect(nextFishingPhase('reeling')).toBe('settling');
    expect(nextFishingPhase('settling')).toBeNull();
    expect(isFishingBusy('casting')).toBe(true);
    expect(isFishingBusy('settling')).toBe(true);
    expect(isFishingBusy('result')).toBe(false);
  });

  it('bounds deferred result copies without evicting authoritative entries', () => {
    const queue = Array.from({ length: MAX_DEFERRED_RESULTS }, (_, index) => ({
      ...result,
      roundId: testRoundId(index),
    }));
    const appended = appendDeferredResult(queue, { ...result, roundId: `grd_${'c'.repeat(26)}` });
    expect(appended.overflowed).toBe(true);
    expect(appended.results).toHaveLength(MAX_DEFERRED_RESULTS);
    expect(appended.results[0]?.roundId).toBe(testRoundId(0));
    expect(appended.results[MAX_DEFERRED_RESULTS - 1]?.roundId).toBe(testRoundId(MAX_DEFERRED_RESULTS - 1));
  });
});

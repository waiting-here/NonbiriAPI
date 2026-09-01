import { describe, expect, it } from 'vitest';
import { fishingPresentationPhase, nextRevealCount } from './stateMachine';
import type { FishingBatchResult } from './types';

const result = { outcomes: [{}, {}, {}] } as unknown as FishingBatchResult;
describe('Fishing presentation clock', () => {
  it('never derives an outcome and only advances within the authoritative array', () => {
    expect(
      fishingPresentationPhase({ submitting: false, pending: false, result: null, revealed: 0 }),
    ).toBe('idle');
    expect(
      fishingPresentationPhase({ submitting: true, pending: false, result: null, revealed: 0 }),
    ).toBe('casting');
    expect(
      fishingPresentationPhase({ submitting: false, pending: true, result: null, revealed: 0 }),
    ).toBe('pending');
    expect(nextRevealCount(2, result)).toBe(3);
    expect(nextRevealCount(3, result)).toBe(3);
  });
});

import { describe, it, expect } from 'vitest';
import { createTimeDraft } from '@shared/time';
import { validRewardWindow } from './form';
describe('reward limits', () => {
  const empty = () => createTimeDraft(null);
  it('rejects malformed limits instead of silently making them unlimited', () => {
    for (const q of ['NaN', '1e3', '0', '-1', '1.5', '9007199254740992']) {
      expect(validRewardWindow(q, empty(), empty())).toBe(false);
    }
  });
  it('distinguishes an empty time from an unresolved edit', () => {
    const blank = empty();
    expect(validRewardWindow('', blank, blank)).toBe(true);
    expect(validRewardWindow('10', { ...blank, text: '2027-01-15T12:00' }, blank)).toBe(false);
  });
});

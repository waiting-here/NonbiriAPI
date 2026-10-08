import { describe, expect, it } from 'vitest';
import { formatLevelRanges } from './levels';

describe('level ranges', () => {
  it('keeps gaps visible and joins consecutive levels', () => {
    expect(formatLevelRanges([1, 2, 3, 5, 6, 8])).toBe('L1–L3, L5–L6, L8');
    expect(formatLevelRanges([1, 2, 3, 4, 5, 6], '、')).toBe('L1–L6');
    expect(formatLevelRanges([2, 4, 6], '、')).toBe('L2、L4、L6');
  });
});

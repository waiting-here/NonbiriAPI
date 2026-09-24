import { describe, expect, it } from 'vitest';
import { fishingChanceFromPercent, fishingChanceValid } from './fishing';

describe('legendary blue-fish probability', () => {
  it('converts exact percentages including both endpoints without binary rounding', () => {
    for (const [raw, bps] of [
      ['0', 0],
      ['0.01', 1],
      ['0.29', 29],
      ['10', 1000],
      ['37.5', 3750],
      ['100.00', 10000],
    ] as const) {
      expect(fishingChanceFromPercent(raw)).toBe(bps);
      expect(fishingChanceValid(bps)).toBe(true);
    }
  });
  it('rejects sub-basis-point precision, coercions and out-of-range values', () => {
    for (const raw of ['', '-1', '100.01', '1.001', '1e1', 'Infinity', '01', '9007199254740993'])
      expect(Number.isNaN(fishingChanceFromPercent(raw))).toBe(true);
    for (const value of [-1, 10001, 0.5, Number.NaN, Infinity])
      expect(fishingChanceValid(value)).toBe(false);
  });
});

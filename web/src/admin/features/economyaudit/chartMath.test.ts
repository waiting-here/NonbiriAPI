import { describe, expect, it } from 'vitest';
import { chartAmountAtRatio, chartRatio } from './chartMath';

describe('economy chart coordinates', () => {
  it('keeps very large integer amounts out of floating-point conversion', () => {
    const maximum = 9_000_000_000_000_000_000_000_000n;
    expect(chartRatio(maximum.toString(), maximum)).toBe(1_000_000);
    expect(chartRatio((maximum / 2n).toString(), maximum)).toBe(500_000);
    expect(chartAmountAtRatio(500_000, maximum)).toBe((maximum / 2n).toString());
  });

  it('handles zero and non-finite axis values', () => {
    expect(chartRatio('0', 0n)).toBe(0);
    expect(chartAmountAtRatio(Number.NaN, 5000n)).toBe('0');
    expect(chartAmountAtRatio(250_000, 0n)).toBe('0');
  });
});

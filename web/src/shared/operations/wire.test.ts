import { describe, expect, it } from 'vitest';
import { amount, cursor, page } from './wire';

describe('bounded credit amounts', () => {
  const limit = 9_000_000_000_000_000n;

  it('preserves exact values at both the custom and default boundaries', () => {
    expect(amount('0.001', 'credits', false, limit)).toBe('0.001');
    expect(amount('9000000000000', 'credits', false, limit)).toBe('9000000000000');
    expect(amount('9000000000000.001', 'credits')).toBe('9000000000000.001');
    expect(amount('-1.001', 'credits')).toBe('-1.001');
    const unsignedLimit = (1n << 128n) - 1n;
    const maximumCredits = '340282366920938463463374607431768211.455';
    expect(amount(maximumCredits, 'credits', false, unsignedLimit)).toBe(maximumCredits);
    expect(() => amount(maximumCredits, 'credits')).toThrow();
    expect(() => amount('340282366920938463463374607431768211.456', 'credits', false, unsignedLimit)).toThrow();
  });

  it.each(['9000000000000.001', '-0.001', '-0', '-0.0', '1.000', '0.0001', '1e3', '0\n', '1\r', '1\u2028', '1\u2029'])('rejects %s for a bounded unsigned amount', (value) => {
    expect(() => amount(value, 'credits', false, limit)).toThrow();
  });
});

describe('operations pagination cursor wire', () => {
  it('accepts null and canonical unpadded raw-base64url within 512 bytes', () => {
    expect(cursor(null)).toBeNull();
    expect(cursor('YWJj')).toBe('YWJj');
    expect(page({ data: [], next_cursor: '_w' }, 'fixture page', () => null).next_cursor).toBe('_w');
  });

  it.each(['abc=', 'A', 'AB', '界', 'A'.repeat(513)])('rejects a malformed page cursor', (value) => {
    expect(() => page({ data: [], next_cursor: value }, 'fixture page', () => null)).toThrow(/cursor/i);
  });
});

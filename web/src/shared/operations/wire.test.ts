import { describe, expect, it } from 'vitest';
import { cursor, page } from './wire';

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

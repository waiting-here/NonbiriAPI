import { describe, expect, it } from 'vitest';
import {
  buildSessionHeaders,
  normalizeSessionHeaders,
  sameSessionHeaders,
  SESSION_HEADER_FORMATS,
} from './sessionHeaders';
describe('managed session header contract', () => {
  it('preserves zero and all five formats, defaulting omitted or blank expiry to 300', () => {
    for (const format of SESSION_HEADER_FORMATS)
      expect(normalizeSessionHeaders([{ name: 'X-Session-ID', idle_seconds: 0, format }])).toEqual([
        { name: 'X-Session-ID', idle_seconds: 0, format },
      ]);
    expect(normalizeSessionHeaders([{ name: 'X-Session-ID' }])[0]).toMatchObject({
      idle_seconds: 300,
      format: 'uuid_v4',
    });
    expect(
      buildSessionHeaders([{ name: ' X-Session-ID ', idle_seconds: '', format: 'hex_32' }]),
    ).toEqual([{ name: 'X-Session-ID', idle_seconds: 300, format: 'hex_32' }]);
    expect(normalizeSessionHeaders(undefined)).toEqual([]);
    expect(buildSessionHeaders([])).toEqual([]);
  });
  it.each([
    null,
    [{ name: 'X-Session-ID', idle_seconds: -1 }],
    [{ name: 'X-Session-ID', idle_seconds: 86401 }],
    [{ name: 'X-Session-ID', idle_seconds: 1.5 }],
    [{ name: 'X-Session-ID', format: 'other' }],
  ])('rejects malformed projections: %j', (input) => {
    expect(() => normalizeSessionHeaders(input)).toThrow();
  });
  it('compares canonical content rather than references, order or header-name case', () => {
    const first = [
      { name: 'X-Session-ID', idle_seconds: 0, format: 'opencode_session' as const },
      { name: 'X-Message-ID' },
    ];
    const second = [
      { name: 'x-message-id', idle_seconds: 300, format: 'uuid_v4' as const },
      { name: 'x-session-id', idle_seconds: 0, format: 'opencode_session' as const },
    ];
    expect(sameSessionHeaders(first, second)).toBe(true);
    expect(sameSessionHeaders(first, [{ ...first[0], idle_seconds: 300 }, first[1]])).toBe(false);
    expect(sameSessionHeaders(first, [{ ...first[0], format: 'opencode_part' }, first[1]])).toBe(
      false,
    );
    expect(sameSessionHeaders(first, [])).toBe(false);
  });
});

import { describe, expect, it } from 'vitest';
import { normalizeSmartBackoffFields, smartBackoffDeadline } from './smartBackoff';

const state = {
  smart_backoff: {
    state: 'waiting',
    reason: 'month_quota',
    source: 'custom_rule',
    retry_at: 1_900_000_000,
  },
};
describe('automatic backoff response boundary', () => {
  it('accepts runtime state and historical rule sources without configuration', () => {
    expect(normalizeSmartBackoffFields({ smart_backoff: null })).toEqual({ smart_backoff: null });
    for (const status of ['waiting', 'ready', 'probing'])
      for (const source of ['builtin', 'custom_rule']) {
        const value = { smart_backoff: { ...state.smart_backoff, state: status, source } };
        expect(normalizeSmartBackoffFields(value)).toEqual(value);
      }
    for (const invalid of [
      { smart_backoff: undefined },
      { smart_backoff: { ...state.smart_backoff, state: 'active' } },
      { smart_backoff: { ...state.smart_backoff, source: 'upstream' } },
      { smart_backoff: { ...state.smart_backoff, retry_at: '1900000000' } },
      { smart_backoff: { ...state.smart_backoff, message: 'raw diagnostics' } },
    ])
      expect(() => normalizeSmartBackoffFields(invalid)).toThrow();
  });
  it('refreshes waiting deadlines without treating ready or in-progress trials as timers', () => {
    expect(smartBackoffDeadline(null)).toBeNull();
    for (const status of ['waiting', 'ready', 'probing'] as const) {
      const value = normalizeSmartBackoffFields({
        smart_backoff: { ...state.smart_backoff, state: status },
      }).smart_backoff;
      expect(smartBackoffDeadline(value)).toBe(status === 'waiting' ? 1_900_000_000 : null);
    }
  });
});

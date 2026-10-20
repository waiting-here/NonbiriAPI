import { describe, expect, it } from 'vitest';
import { normalizeSmartBackoffFields, smartBackoffInput } from './smartBackoff';

const configuration = {
  smart_backoff_profile: 'day_month' as const,
  smart_backoff_rules: [
    { kind: 'message' as const, value: 'Monthly allowance exhausted', period: 'month' as const },
  ],
  smart_backoff: {
    state: 'waiting',
    reason: 'month_quota',
    source: 'custom_rule',
    retry_at: 1_900_000_000,
  },
};
describe('smart backoff response boundary', () => {
  it('requires explicit configuration and finite safe runtime fields', () => {
    expect(normalizeSmartBackoffFields(configuration)).toEqual(configuration);
    for (const state of ['waiting', 'ready', 'probing'])
      expect(
        normalizeSmartBackoffFields({
          ...configuration,
          smart_backoff: { ...configuration.smart_backoff, state },
        }).smart_backoff?.state,
      ).toBe(state);
    for (const invalid of [
      { ...configuration, smart_backoff_profile: undefined },
      { ...configuration, smart_backoff_rules: null },
      { ...configuration, smart_backoff: undefined },
      { ...configuration, smart_backoff: { ...configuration.smart_backoff, state: 'active' } },
      { ...configuration, smart_backoff: { ...configuration.smart_backoff, source: 'upstream' } },
      {
        ...configuration,
        smart_backoff: { ...configuration.smart_backoff, retry_at: '1900000000' },
      },
      {
        ...configuration,
        smart_backoff: { ...configuration.smart_backoff, message: 'raw diagnostics' },
      },
    ])
      expect(() => normalizeSmartBackoffFields(invalid)).toThrow();
  });
  it('keeps literal rule matching and enforces the selected allowance cycles', () => {
    expect(
      smartBackoffInput({
        ...configuration,
        smart_backoff_rules: [
          { kind: 'message', value: '  Period quota [exhausted].*  ', period: 'auto' },
        ],
      }).smart_backoff_rules[0].value,
    ).toBe('Period quota [exhausted].*');
    for (const rules of [
      [{ kind: 'code', value: 'bad code', period: 'day' }],
      [{ kind: 'message', value: 'x'.repeat(161), period: 'auto' }],
      [{ kind: 'message', value: 'line\nline', period: 'auto' }],
      [{ kind: 'code', value: '1308', period: 'week' }],
      Array.from({ length: 9 }, () => ({ kind: 'code', value: '1308', period: 'auto' })),
    ])
      expect(() =>
        normalizeSmartBackoffFields({ ...configuration, smart_backoff_rules: rules }),
      ).toThrow();
    expect(
      normalizeSmartBackoffFields({ ...configuration, smart_backoff_profile: 'none' })
        .smart_backoff_rules,
    ).toEqual(configuration.smart_backoff_rules);
  });
});

import { expect, it } from 'vitest';
import { normalizeDiscoveryEvidence } from '../normalizers';

it('keeps the last successful fetch and known deltas when a later fetch fails', () => {
  const value = {
    state: 'failed',
    revision: '3',
    result: null,
    safe_class: 'timeout',
    observed_at: 1700000010,
    count: null,
    last_success_at: 1700000000,
    added_count: '2',
    removed_count: '1',
  };
  expect(normalizeDiscoveryEvidence(value)).toEqual(value);
});

it('accepts old evidence with no summary and migrated evidence with unknown deltas', () => {
  const legacy = {
    state: 'succeeded',
    revision: '1',
    result: 'nonempty',
    safe_class: 'none',
    observed_at: 1700000000,
    count: '3',
  };
  expect(normalizeDiscoveryEvidence(legacy)).toEqual(legacy);
  expect(
    normalizeDiscoveryEvidence({
      ...legacy,
      last_success_at: 1700000000,
      added_count: null,
      removed_count: null,
    }),
  ).toMatchObject({ added_count: null, removed_count: null });
});

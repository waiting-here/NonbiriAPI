import { describe, expect, it } from 'vitest';
import { overloadResources, shortageValue } from './shortage';
import type { LikesEvent } from './types';

describe('overload resource cues', () => {
  const event: LikesEvent = {
    id: 1,
    round: 1,
    score: null,
    cast: null,
    transition: null,
    kind: 'overload',
    stage: 'payment',
    seat: 0,
    data: {
      reason: 'personal-resources',
      shortage: { payment: 'api', resources: [{ resource: 'api', required: 100, available: 10 }] },
    },
  };
  it('only reveals the affected seat from events already on the timeline', () => {
    expect(overloadResources([], 0)).toEqual([]);
    expect(overloadResources([event], 1)).toEqual([]);
    expect(overloadResources([event], null)).toEqual([]);
    expect(overloadResources([event], 0)).toEqual([
      { resource: 'api', required: 100, available: 10 },
    ]);
  });
  it('preserves old energy evidence but never guesses a personal shortage', () => {
    expect(overloadResources([{ ...event, data: { reason: 'personal-resources' } }], 0)).toEqual(
      [],
    );
    expect(
      overloadResources(
        [{ ...event, seat: null, data: { reason: 'shared-energy', required: 100, available: 5 } }],
        null,
      ),
    ).toEqual([{ resource: 'energy', required: 100, available: 5 }]);
  });
  it('rejects unbounded, unknown and duplicate resource metadata', () => {
    expect(() => shortageValue({ payment: 'gold', resources: [] })).toThrow();
    expect(() =>
      shortageValue({
        payment: 'api',
        resources: [{ resource: 'api', required: -1, available: 0 }],
      }),
    ).toThrow();
    expect(() =>
      shortageValue({
        payment: 'api',
        resources: Array.from({ length: 4 }, () => ({
          resource: 'api',
          required: 1,
          available: 0,
        })),
      }),
    ).toThrow();
  });
});

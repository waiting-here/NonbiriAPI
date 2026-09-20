import { expect, it } from 'vitest';
import { fishingRevealCue } from './feedback';
import type { FishingOutcome } from './types';

it('groups bulk catches, emphasizes a revealed rarity and keeps hidden catches silent', () => {
  const outcomes = Array.from(
    { length: 10 },
    (_, ordinal) => ({ ordinal, tier: ordinal === 5 ? 'legend' : 'small' }) as FishingOutcome,
  );
  const cues = outcomes.map((_, index) => fishingRevealCue(outcomes, index, index + 1));
  expect(cues).toEqual([
    'fishing_common',
    null,
    'fishing_common',
    null,
    null,
    'fishing_epic',
    null,
    null,
    'fishing_common',
    'fishing_epic',
  ]);
  expect(fishingRevealCue(outcomes, 0, 10)).toBe('fishing_epic');
  expect(fishingRevealCue(outcomes, 10, 10)).toBeNull();
  expect(fishingRevealCue(outcomes, 10, 11)).toBeNull();
});

import type { GameSoundCue } from '../common/sound';
import type { FishingOutcome } from './types';

const rank = (outcome: FishingOutcome) =>
  outcome.tier === 'legend' || outcome.tier === 'treasure'
    ? 2
    : outcome.tier === 'big' || outcome.tier === 'giant'
      ? 1
      : 0;

/** Batch accents follow revealed facts, with one stronger cue for a new rarity. */
export function fishingRevealCue(
  outcomes: readonly FishingOutcome[],
  previous: number,
  revealed: number,
): GameSoundCue | null {
  if (revealed <= previous || revealed > outcomes.length) return null;
  const before = Math.max(-1, ...outcomes.slice(0, previous).map(rank));
  const current = Math.max(0, ...outcomes.slice(previous, revealed).map(rank));
  const last = revealed === outcomes.length;
  if (previous > 0 && revealed % 3 !== 0 && !last && current <= before) return null;
  const level = last ? Math.max(before, current) : current;
  return level === 2 ? 'fishing_epic' : level === 1 ? 'fishing_rare' : 'fishing_common';
}

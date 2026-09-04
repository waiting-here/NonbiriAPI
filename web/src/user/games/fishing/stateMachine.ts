import type { FishingBatchResult } from './types';

export type FishingPresentationPhase = 'idle' | 'casting' | 'pending' | 'bite' | 'result';

export const FISHING_REVEAL_MS = 420;

/** Presentation only: it never creates, shuffles, settles, or changes outcomes. */
export function fishingPresentationPhase(options: {
  readonly submitting: boolean;
  readonly pending: boolean;
  readonly result: FishingBatchResult | null;
  readonly revealed: number;
}): FishingPresentationPhase {
  if (options.result) return options.revealed >= options.result.outcomes.length ? 'result' : 'bite';
  if (options.pending) return 'pending';
  if (options.submitting) return 'casting';
  return 'idle';
}

export function nextRevealCount(current: number, result: FishingBatchResult): number {
  return Math.min(result.outcomes.length, current + 1);
}

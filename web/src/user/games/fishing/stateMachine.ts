import type { FishingFlowState, FishingPhase, FishingResult } from './types';

export type FishingAction =
  | { readonly type: 'begin'; readonly roundId: string }
  | { readonly type: 'phase'; readonly phase: Exclude<FishingPhase, 'idle' | 'result' | 'error'> }
  | { readonly type: 'settled'; readonly result: FishingResult }
  | { readonly type: 'recovered'; readonly result: FishingResult }
  | { readonly type: 'acknowledged' }
  | { readonly type: 'failed'; readonly error: FishingFlowState['error'] }
  | { readonly type: 'reset' };

export const initialFishingFlow: FishingFlowState = Object.freeze({
  phase: 'idle',
  roundId: null,
  result: null,
  error: null,
  resultSource: null,
});

export function fishingFlowReducer(
  state: FishingFlowState,
  action: FishingAction,
): FishingFlowState {
  switch (action.type) {
    case 'begin':
      return { phase: 'casting', roundId: action.roundId, result: null, error: null, resultSource: null };
    case 'phase':
      if (!state.roundId || state.result) return state;
      return { ...state, phase: action.phase, error: null };
    case 'settled':
      return {
        phase: 'result',
        roundId: action.result.roundId,
        result: action.result,
        error: null,
        resultSource: 'settled',
      };
    case 'recovered':
      return {
        phase: 'result',
        roundId: action.result.roundId,
        result: action.result,
        error: null,
        resultSource: 'recovered',
      };
    case 'acknowledged':
      return initialFishingFlow;
    case 'failed':
      return { ...state, phase: 'error', error: action.error };
    case 'reset':
      return initialFishingFlow;
  }
}

export function phaseLabelKey(phase: FishingPhase): string {
  return `games.fishing.phase.${phase}`;
}

export function isFishingBusy(phase: FishingPhase): boolean {
  return phase === 'casting' || phase === 'waiting' || phase === 'reeling' || phase === 'settling';
}

/**
 * The browser keeps only a small presentation ring.  The server remains the
 * source of truth for any result that cannot fit in this local queue.
 */
export const MAX_DEFERRED_RESULTS = 32;

export function appendDeferredResult(
  queue: readonly FishingResult[],
  result: FishingResult,
): { readonly results: readonly FishingResult[]; readonly overflowed: boolean } {
  if (queue.some((candidate) => candidate.roundId === result.roundId)) {
    return { results: queue, overflowed: false };
  }
  if (queue.length >= MAX_DEFERRED_RESULTS) {
    return { results: queue, overflowed: true };
  }
  return { results: [...queue, result], overflowed: false };
}

/**
 * This is presentation timing only. It never creates or derives an outcome;
 * the caller must still settle through the server and render its response.
 */
export const FISHING_PHASE_DURATIONS_MS: Readonly<Record<'casting' | 'waiting' | 'reeling', number>> = Object.freeze({
  casting: 650,
  waiting: 950,
  reeling: 750,
});

export function nextFishingPhase(phase: FishingPhase): FishingPhase | null {
  if (phase === 'casting') return 'waiting';
  if (phase === 'waiting') return 'reeling';
  if (phase === 'reeling') return 'settling';
  return null;
}

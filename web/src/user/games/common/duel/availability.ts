import type { DuelText } from './copy';
import type { DuelLobbyContext } from './types';
export type EntryProblem = 'game-closed' | 'mode-closed' | 'unavailable';
export function entryProblem(
  context: Pick<DuelLobbyContext, 'accepting' | 'config'>,
  mode: string,
): EntryProblem | null {
  if (!context.accepting || !context.config.enabled) return 'game-closed';
  const selected = context.config.modes[mode];
  if (!selected?.enabled) return 'mode-closed';
  if (!context.config.available || !selected.available) return 'unavailable';
  return null;
}
export function entryMessage(problem: EntryProblem, text: DuelText): string {
  if (problem === 'game-closed') return text('common.thisGameIsNotOpen');
  if (problem === 'mode-closed') return text('common.thisModeIsNotOpen');
  return text('common.theGameIsTemporarilyUnavailableTryAgain');
}

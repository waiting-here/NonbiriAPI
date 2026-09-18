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

export function entryMessage(problem: EntryProblem, t: (zh: string, en: string) => string): string {
  if (problem === 'game-closed') return t('游戏尚未开放', 'This game is not open');
  if (problem === 'mode-closed') return t('此模式暂未开放', 'This mode is not open');
  return t('游戏暂时不可用，请稍后再试', 'The game is temporarily unavailable. Try again later');
}

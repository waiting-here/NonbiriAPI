import { useTranslation } from 'react-i18next';

export function useGameAdminText() {
  const { i18n } = useTranslation();
  return (zh: string, en: string) => (i18n.resolvedLanguage?.startsWith('zh') ? zh : en);
}
export type GameID = 'bidding' | 'likes';
export const modesFor = (game: GameID) =>
  game === 'bidding' ? ['tier1', 'tier2', 'tier3'] : ['quick', 'standard'];
export function gameLabel(game: GameID | 'blackjack', t: (zh: string, en: string) => string) {
  if (game === 'blackjack') return t('二十一点', 'Blackjack');
  return game === 'bidding'
    ? t('竞标对决', 'Bidding Duel')
    : t('回合制对战小游戏（测试）', 'Turn-based Battle Minigame (Test)');
}
export function modeLabel(mode: string, t: (zh: string, en: string) => string) {
  return (
    {
      table: t('单桌', 'Single table'),
      tier1: t('初级场', 'Tier 1'),
      tier2: t('中级场', 'Tier 2'),
      tier3: t('高级场', 'Tier 3'),
      quick: t('快速', 'Quick'),
      standard: t('标准', 'Standard'),
    }[mode] ?? mode
  );
}

import { useTranslation } from 'react-i18next';
import abilities from './abilities.json';
import type { CardDefinition, Faction } from './types';

export function useGwentText() {
  const { i18n } = useTranslation();
  return (zh: string, en: string) => (i18n.resolvedLanguage?.startsWith('zh') ? zh : en);
}
export const factionName: Record<Faction, string> = {
  openai: 'OpenAI',
  deepseek: 'DeepSeek',
  claude: 'Claude',
  gemini: 'Gemini',
};
export const rowName = (row: string, t: (zh: string, en: string) => string) =>
  ({
    close: t('推理前线', 'Front line'),
    ranged: t('感知矩阵', 'Sensor matrix'),
    siege: t('算力集群', 'Compute cluster'),
    agile: t('双排部署', 'Agile'),
    weather: t('天气', 'Weather'),
    leader: t('领袖', 'Leader'),
  })[row] ?? row;
export const help = abilities as Record<string, { name: string; description: string }>;
export const cardArt = (card: CardDefinition) =>
  `/assets/gwent/cards/${card.image.split('/').at(-1)}`;
export function choiceName(kind: string, t: (zh: string, en: string) => string) {
  return (
    {
      mulligan: t('换掉最多两张手牌', 'Replace up to two opening cards'),
      initiative: t('选择先手阵营', 'Choose who goes first'),
      medic: t('选择恢复的单位', 'Choose a unit to restore'),
      decoy: t('选择回收的单位', 'Choose a unit to recall'),
      copy: t('选择复制目标', 'Choose an ability to copy'),
      context_window: t('选择重抽的手牌', 'Choose a card to redraw'),
      analysis: t('清除天气，或继续并获得强化', 'Clear weather, or continue for a boost'),
      reveal: t('查看牌库顶牌', 'Inspect the top of your deck'),
      row: t('选择部署战线', 'Choose a row'),
      retrieve: t('选择加入手牌的单位', 'Choose a unit to take into your hand'),
    }[kind] ?? t('完成卡牌选择', 'Resolve the card choice')
  );
}

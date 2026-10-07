import { leaderDefinitions } from '../cards/leaders.js';

export const ROWS = ['close', 'ranged', 'siege'];
export const MAX_HEROES = 4;
export const rowNames = {
  close: '推理前线',
  ranged: '感知矩阵',
  siege: '算力集群',
  agile: '推理前线 / 感知矩阵',
};
export function validateCatalog(cards) {
  const ids = new Set();
  for (const card of cards) {
    if (!card.id || ids.has(card.id)) throw Error('卡牌 ID 缺失或重复：' + card.id);
    if (!['unit', 'hero', 'skill', 'weather', 'leader'].includes(card.type))
      throw Error('未知卡牌类型');
    if (!['openai', 'deepseek', 'claude', 'gemini', 'neutral'].includes(card.faction))
      throw Error('未知阵营');
    if (
      !Number.isInteger(card.power) ||
      card.power < 0 ||
      !Number.isInteger(card.maxCopies) ||
      card.maxCopies < 1
    )
      throw Error('无效卡牌数值');
    if (
      !Array.isArray(card.abilities) ||
      ![...ROWS, 'agile', 'special', 'weather', 'leader'].includes(card.row)
    )
      throw Error('无效技能或战线');
    if (
      card.starterCopies !== undefined &&
      (!Number.isInteger(card.starterCopies) ||
        card.starterCopies < 0 ||
        card.starterCopies > card.maxCopies)
    )
      throw Error('无效预设副本数量');
    ids.add(card.id);
    if (
      card.abilities.includes('muster') &&
      (card.type !== 'unit' ||
        typeof card.musterGroup !== 'string' ||
        !/^[a-z0-9_]{1,80}$/.test(card.musterGroup))
    )
      throw Error('无效召集组');
    if (card.row === 'agile' && !['unit', 'hero'].includes(card.type))
      throw Error('双排牌必须是单位');
    if (
      card.abilities.includes('decoy') &&
      (card.type !== 'skill' || card.row !== 'special' || card.power !== 0)
    )
      throw Error('无效诱饵牌');
  }
  for (const card of cards.filter((card) => card.musterGroup))
    if (
      !card.abilities.includes('muster') ||
      cards.some(
        (other) => other.musterGroup === card.musterGroup && other.faction !== card.faction,
      )
    )
      throw Error('召集组必须属于同一阵营');
  const names = new Set(cards.map((card) => card.name));
  for (const card of cards)
    for (const [key, ability] of [
      ['transformForm', 'berserker'],
      ['avengerForm', 'avenger'],
    ]) {
      const form = card[key];
      if (card.abilities.includes(ability) !== !!form) throw Error('变身或离场召唤需要形态映射');
      if (!form) continue;
      if (
        card.type !== 'unit' ||
        !/^[a-z0-9_]{1,80}$/.test(form.id) ||
        ids.has(form.id) ||
        typeof form.name !== 'string' ||
        !form.name ||
        names.has(form.name) ||
        !Number.isInteger(form.power) ||
        form.power < 0 ||
        !ROWS.includes(form.row) ||
        form.row !== card.row ||
        !Array.isArray(form.abilities) ||
        form.abilities.some((id) => id !== 'bond' && id !== 'morale' && id !== 'horn')
      )
        throw Error('无效衍生形态');
      ids.add(form.id);
      names.add(form.name);
    }
  for (const leader of leaderDefinitions(cards).filter((card) => card.choiceOf)) {
    if (ids.has(leader.id) || names.has(leader.name)) throw Error('领袖方案 ID 或名称重复');
    ids.add(leader.id);
    names.add(leader.name);
  }
  return cards;
}
// Retained decks may predate the hero limit. This checks every other rule so
// valid legacy compositions can be kept for editing without admitting bad data.
export function validateStoredDeck(deck, catalog) {
  if (!deck || !Array.isArray(deck.cards)) throw Error('无效卡组格式');
  const map = new Map(catalog.map((card) => [card.id, card]));
  const leader = leaderDefinitions(catalog).find((card) => card.id === deck.leader);
  if (!leader || leader.type !== 'leader' || leader.faction !== deck.faction)
    throw Error('无效领袖');
  let units = 0,
    specials = 0;
  const seen = new Set();
  for (const entry of deck.cards) {
    if (!entry || typeof entry !== 'object') throw Error('无效卡牌条目');
    const card = map.get(entry.id);
    if (
      !card ||
      seen.has(entry.id) ||
      !Number.isInteger(entry.count) ||
      entry.count < 1 ||
      entry.count > card.maxCopies
    )
      throw Error('无效卡牌或数量：' + entry.id);
    if (card.type === 'leader' || ![deck.faction, 'neutral'].includes(card.faction))
      throw Error('卡牌不属于本阵营');
    seen.add(entry.id);
    if (['hero', 'unit'].includes(card.type)) units += entry.count;
    else specials += entry.count;
  }
  if (units < 22 || specials > 10) throw Error('卡组需要至少 22 张单位，最多 10 张特殊牌');
  return { units, specials, total: units + specials };
}
export function heroCount(deck, catalog) {
  const map = new Map(catalog.map((card) => [card.id, card]));
  return deck.cards.reduce(
    (sum, entry) => sum + (map.get(entry.id)?.type === 'hero' ? entry.count : 0),
    0,
  );
}
export function validateDeck(deck, catalog) {
  const stats = validateStoredDeck(deck, catalog);
  if (heroCount(deck, catalog) > MAX_HEROES)
    throw Error(`每套卡组最多 ${MAX_HEROES} 张英雄，请调整后再保存或开战`);
  return stats;
}

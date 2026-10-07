/* eslint-disable */
import { validateDeck, validateStoredDeck } from '../game/rules.js';
import {
  themePresets,
  legacyThemePresets,
  randomDeckProfile,
  isBuiltInSelection,
  isRandomSelection,
  nextDeckSeed,
} from './presets.js';

export const MAX_PROFILES = 8;
export const DEFAULT_PRESET = 'standard-balanced';
function deckCopy(deck) {
  return {
    faction: deck.faction,
    leader: deck.leader,
    cards: deck.cards.map(({ id, count }) => ({ id, count })),
  };
}
function deckName(name) {
  if (typeof name !== 'string' || !name.trim() || name.trim().length > 40)
    throw Error('卡组名称需要 1–40 个字符');
  return name.trim();
}
export function exportDeck(deck, name, catalog) {
  validateStoredDeck(deck, catalog);
  return JSON.stringify(
    {
      format: 'ai-gwent-arena-deck',
      version: 1,
      name: deckName(name),
      deck: deckCopy(deck),
    },
    null,
    2,
  );
}
export function importDeck(text, catalog, faction) {
  if (typeof text !== 'string' || new TextEncoder().encode(text).length > 65536)
    throw Error('卡组文件不能超过 64 KB');
  let record;
  try {
    record = JSON.parse(text);
  } catch {
    throw Error('卡组文件不是有效 JSON');
  }
  if (record?.format !== 'ai-gwent-arena-deck' || record.version !== 1)
    throw Error('请导入 Arena 导出的卡组文件；经典版卡牌 ID 与 Arena 不兼容');
  const name = deckName(record.name);
  validateStoredDeck(record.deck, catalog);
  if (record.deck.faction !== faction) throw Error('导入卡组的阵营与当前阵营不同，请先切换阵营');
  return { name, deck: deckCopy(record.deck) };
}

export function starterDeck(faction, catalog) {
  return {
    faction,
    leader: `${faction}_leader`,
    cards: catalog
      .filter(
        (card) =>
          card.type !== 'leader' &&
          [faction, 'neutral'].includes(card.faction) &&
          (card.starterCopies ?? card.maxCopies) > 0,
      )
      .map((card) => ({
        id: card.id,
        count: card.starterCopies ?? card.maxCopies,
      })),
  };
}

// Persist stable IDs, never the indices used by the upstream engine.
export class DeckStore {
  constructor(catalog, storage, { seedSource = nextDeckSeed } = {}) {
    this.catalog = catalog;
    this.storage = storage;
    this.saved = new Map();
    this.profiles = new Map();
    this.warnings = [];
    this.seedSource = seedSource;
    this.randomProfiles = new Map();
    this.themes = new Map(
      ['openai', 'deepseek', 'claude', 'gemini'].map((faction) => [
        faction,
        themePresets(faction, catalog),
      ]),
    );
    for (const faction of ['openai', 'deepseek', 'claude', 'gemini']) {
      try {
        const modern = storage.getItem(this.profileKey(faction));
        if (modern) {
          const record = this.validateRecord(JSON.parse(modern), faction);
          this.install(faction, record);
          continue;
        }
        const raw = storage.getItem(this.key(faction));
        if (!raw) continue;
        const record = JSON.parse(raw);
        if (record.version !== 1 || record.deck?.faction !== faction) throw Error('卡组格式不兼容');
        validateStoredDeck(record.deck, catalog);
        this.install(faction, {
          version: 2,
          active: 'legacy',
          profiles: [{ id: 'legacy', name: '我的卡组', deck: this.copy(record.deck) }],
        });
      } catch {
        this.warnings.push(faction);
      }
    }
  }
  key(faction) {
    return `arena-deck-v1-${faction}`;
  }
  profileKey(faction) {
    return `arena-decks-v2-${faction}`;
  }
  validateRecord(record, faction) {
    if (
      record?.version !== 2 ||
      !Array.isArray(record.profiles) ||
      record.profiles.length > MAX_PROFILES
    )
      throw Error('无效卡组配置');
    const ids = new Set(),
      names = new Set();
    for (const profile of record.profiles) {
      if (
        !profile ||
        typeof profile.id !== 'string' ||
        !/^[a-z0-9_-]{1,64}$/i.test(profile.id) ||
        isBuiltInSelection(profile.id) ||
        ids.has(profile.id)
      )
        throw Error('卡组标识无效或重复');
      const name = deckName(profile.name);
      if (names.has(name)) throw Error('同阵营卡组名称不能重复');
      validateStoredDeck(profile.deck, this.catalog);
      if (profile.deck.faction !== faction) throw Error('卡组阵营不匹配');
      ids.add(profile.id);
      names.add(name);
    }
    const retired =
      record.active === 'preset' ||
      legacyThemePresets(faction, this.catalog).some((profile) => profile.id === record.active);
    const active = retired ? DEFAULT_PRESET : record.active;
    if (!this.builtInIds(faction).has(active) && !ids.has(active)) throw Error('所选卡组不存在');
    return {
      version: 2,
      active,
      profiles: record.profiles.map(({ id, name, deck }) => ({
        id,
        name: deckName(name),
        deck: this.copy(deck),
      })),
    };
  }
  record(faction) {
    return structuredClone(
      this.profiles.get(faction) || {
        version: 2,
        active: DEFAULT_PRESET,
        profiles: [],
      },
    );
  }
  install(faction, record) {
    this.profiles.set(faction, record);
    const active = record.profiles.find((profile) => profile.id === record.active);
    if (active) this.saved.set(faction, this.copy(active.deck));
    else this.saved.delete(faction);
  }
  commit(faction, record) {
    const checked = this.validateRecord(record, faction);
    this.storage.setItem(this.profileKey(faction), JSON.stringify(checked));
    this.install(faction, checked);
    this.warnings = this.warnings.filter((item) => item !== faction);
  }
  activeId(faction) {
    return this.profiles.get(faction)?.active || DEFAULT_PRESET;
  }
  builtInIds(faction) {
    return new Set([
      ...this.themes.get(faction).map((profile) => profile.id),
      'random-preset',
      'random-theme',
    ]);
  }
  randomProfile(faction, id, scope = 'player') {
    const key = `${scope}/${faction}/${id}`;
    if (!this.randomProfiles.has(key)) this.reroll(faction, id, scope);
    return structuredClone(this.randomProfiles.get(key));
  }
  reroll(faction, id, scope = 'player') {
    if (!isRandomSelection(id)) throw Error('请先选择随机预设或随机组牌');
    const profile = randomDeckProfile(faction, this.catalog, {
      mode: id,
      seed: this.seedSource(),
    });
    this.randomProfiles.set(`${scope}/${faction}/${id}`, profile);
    return structuredClone(profile);
  }
  list(faction, scope = 'player') {
    return [
      ...structuredClone(this.themes.get(faction)),
      ...['random-preset', 'random-theme'].map((id) => this.randomProfile(faction, id, scope)),
      ...this.record(faction).profiles.map((profile) => ({
        ...profile,
        kind: 'custom',
      })),
    ];
  }
  profile(faction, id = this.activeId(faction), scope = 'player') {
    const profile = this.list(faction, scope).find((profile) => profile.id === id);
    if (!profile) throw Error('所选卡组不存在');
    return profile;
  }
  select(faction, id) {
    this.profile(faction, id);
    const record = this.record(faction);
    record.active = id;
    this.commit(faction, record);
  }
  remove(faction, id) {
    if (isBuiltInSelection(id)) throw Error('预设卡组不能删除');
    this.profile(faction, id);
    const record = this.record(faction);
    record.profiles = record.profiles.filter((profile) => profile.id !== id);
    if (record.active === id) record.active = DEFAULT_PRESET;
    this.commit(faction, record);
  }
  copy(deck) {
    return deckCopy(deck);
  }
  get(faction, id, scope = 'player') {
    const deck = this.profile(faction, id, scope).deck;
    validateDeck(deck, this.catalog);
    return this.copy(deck);
  }
  save(deck, { id = this.activeId(deck.faction), name } = {}) {
    validateDeck(deck, this.catalog);
    const copy = this.copy(deck);
    const record = this.record(deck.faction);
    const existing = record.profiles.find((profile) => profile.id === id);
    if (!existing && record.profiles.length >= MAX_PROFILES)
      throw Error(`每个阵营最多保存 ${MAX_PROFILES} 套卡组`);
    if (id && !this.builtInIds(deck.faction).has(id) && !existing) throw Error('所选卡组不存在');
    const profileId =
      existing?.id ||
      `deck-${globalThis.crypto?.randomUUID?.() || Date.now().toString(36) + '-' + Math.random().toString(36).slice(2)}`;
    const profile = {
      id: profileId,
      name: deckName(name ?? existing?.name ?? '我的卡组'),
      deck: copy,
    };
    if (existing) record.profiles[record.profiles.indexOf(existing)] = profile;
    else record.profiles.push(profile);
    record.active = profileId;
    // If persistence fails, retain the previously usable deck and selection.
    this.commit(deck.faction, record);
    return copy;
  }
}

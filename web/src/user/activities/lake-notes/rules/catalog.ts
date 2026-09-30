import data from './catalog.json';
import type { Effects, FishType, Loadout, Profile } from './types';

export const catalog = data;
export const TICK_SECONDS = 1 / 60;
export const MAX_U128 = (1n << 128n) - 1n;
export const MAX_SAFE_INTEGER = Number.MAX_SAFE_INTEGER;
export { RULES_ID } from './identity';
export const clamp = (n: number, a: number, b: number) => Math.max(a, Math.min(b, n));
export const round = (n: number) => Math.floor(n + 0.5);
export const fish = (kind: string) =>
  catalog.FISH_TYPES.find((f) => f.kind === kind) as FishType | undefined;
export const gear = (id: string) => catalog.GEAR[id as keyof typeof catalog.GEAR];
export const bait = (id?: string) =>
  id ? catalog.BAITS[id as keyof typeof catalog.BAITS] : undefined;
export const periodAt = (minutes: number) =>
  minutes < 300 || minutes >= 1260
    ? 'night'
    : minutes < 540
      ? 'dawn'
      : minutes < 1020
        ? 'day'
        : 'dusk';
export const weatherForDay = (day: number) =>
  catalog.WEATHER_CYCLE[(day - 1) % catalog.WEATHER_CYCLE.length];
export const fishAvailable = (f: FishType, period: string, weather: string) =>
  (!f.periods || f.periods.includes(period)) && (!f.weathers || f.weathers.includes(weather));
const xpThresholds = [0, 100, 380, 770, 1300, 2150, 3300, 4800, 6900, 10000, 15000];
export const xpForLevel = (level: number) =>
  level % 2 === 0
    ? xpThresholds[level / 2]
    : Math.floor((xpThresholds[Math.floor(level / 2)] + xpThresholds[Math.ceil(level / 2)]) / 2);
export function levelFromXp(xp: string) {
  let level = 0;
  while (level < 20 && BigInt(xp) >= BigInt(xpForLevel(level + 1))) level++;
  return level;
}
export function parseAmount(s: string) {
  if (!/^(0|[1-9][0-9]{0,38})$/.test(s) || BigInt(s) > MAX_U128) throw Error('invalid amount');
  return BigInt(s);
}
export function addAmount(s: string, n: number | bigint) {
  const v = parseAmount(s) + BigInt(n);
  if (v < 0n || v > MAX_U128) throw Error('amount overflow or insufficient coins');
  return String(v);
}
export function initialProfile(): Profile {
  return {
    coins: '0',
    xp: '0',
    location: 'lake',
    day: 1,
    clockMinutes: 360,
    ownedGear: ['bambooPole'],
    equipped: { rod: 'bambooPole' },
    baitStock: Object.fromEntries(Object.keys(catalog.BAITS).map((k) => [k, 0])),
    basket: [],
    records: {},
    nextCatchId: 1,
    savedLoadouts: [null, null, null],
    debrisStock: Object.fromEntries(Object.keys(catalog.DEBRIS).map((k) => [k, 0])),
    trashRecovered: '0',
    treasureOpened: '0',
    contracts: [],
    contractsDay: 0,
    completedContracts: '0',
    streak: '0',
  };
}
export function equipmentEffects(loadout: Loadout): Effects {
  const effects: Effects = {
    barHeight: 0,
    acceleration: 1,
    progressGain: 1,
    progressLoss: 1,
    rareWeight: 1,
    legendWeight: 1,
    bottomBounce: 2 / 3,
    barbedCount: 0,
    qualityBonus: 0,
    spinnerSeconds: 0,
    sonar: false,
    biteDelay: 0,
    epicWeight: 0,
    fishSpeed: 0,
  };
  const rod = gear(loadout.rod);
  if (!rod) throw Error('unknown rod');
  if (loadout.rod === 'trainingRod') effects.progressLoss = 2 / 3;
  let trapCount = 0;
  for (const id of [loadout.tackle1, loadout.tackle2].slice(
    0,
    'tackleSlots' in rod ? rod.tackleSlots : 0,
  )) {
    if (id === 'corkBobber') effects.barHeight += 24 / 568;
    else if (id === 'leadBobber') effects.bottomBounce *= 0.1;
    else if (id === 'trapBobber') trapCount++;
    else if (id === 'barbedHook') effects.barbedCount++;
    else if (id === 'qualityBobber') effects.qualityBonus++;
    else if (id === 'curiosityLure') effects.legendWeight = 2;
    else if (id === 'spinner') effects.spinnerSeconds += 5;
    else if (id === 'dressedSpinner') effects.spinnerSeconds += 10;
    else if (id === 'sonarBobber') effects.sonar = true;
  }
  if (trapCount === 1) effects.progressLoss *= 2 / 3;
  if (trapCount >= 2) effects.progressLoss *= 0.5;
  return effects;
}
export function barHeight(p: Profile, effects: Effects, rod: string, baitId?: string) {
  const skillBonus = (p.first === 'steady' ? 8 / 568 : 0) + (p.fourth === 'master' ? 8 / 568 : 0);
  const base = 96 / 568 + levelFromXp(p.xp) * (4 / 568) + skillBonus;
  const baitEffects: Partial<Effects> | undefined = bait(baitId)?.effects;
  return clamp(
    Math.max(rod === 'trainingRod' ? 136 / 568 : 0, base) +
      effects.barHeight +
      (baitEffects?.barHeight || 0),
    0.08,
    0.5,
  );
}

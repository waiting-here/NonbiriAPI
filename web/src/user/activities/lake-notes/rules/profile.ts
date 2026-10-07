import { addAmount, catalog, fish, gear, levelFromXp, parseAmount } from './catalog';
import type { Catch, Contract, FishType, Loadout, Profile } from './types';

export const caught = (p: Profile) =>
  Object.values(p.records).reduce((n, r) => n + BigInt(r.caught), 0n);
export const copies = (p: Profile, id: string) => p.ownedGear.filter((k) => k === id).length;
export function catchValue(item: Catch) {
  const type = fish(item.kind);
  return type ? Math.floor(type.basePrice * [1, 1.25, 1.5, 2][item.quality]) : 0;
}
export function advanceClock(p: Profile, minutes: number) {
  const total = p.clockMinutes + minutes;
  const oldDay = p.day;
  p.day += Math.floor(total / 1440);
  p.clockMinutes = total % 1440;
  if (!Number.isSafeInteger(p.day)) throw Error('day overflow');
  if (p.day !== oldDay)
    p.contracts = p.contracts.filter((q) => q.status === 'active' || q.day === p.day);
}
export function awardBait(p: Profile, id: string, count: number) {
  const added = Math.min(count, 999 - p.baitStock[id]);
  p.baitStock[id] += added;
  const converted = (count - added) * catalog.BAITS[id as keyof typeof catalog.BAITS].cost;
  p.coins = addAmount(p.coins, converted);
  return { added, converted };
}
export function advanceContracts(p: Profile, event: string, f?: FishType, perfect = false) {
  for (const quest of p.contracts.filter((q) => q.status === 'active')) {
    if (
      (quest.type === 'catch' && event === 'fish' && f?.location === quest.location) ||
      (quest.type === 'perfect' && event === 'fish' && perfect) ||
      (quest.type === 'cleanup' && event === 'cleanup')
    )
      quest.progress = Math.min(quest.target, quest.progress + 1);
  }
}
export function storeCatch(
  p: Profile,
  type: FishType,
  length: number,
  quality: number,
  perfect: boolean,
) {
  if (p.nextCatchId >= Number.MAX_SAFE_INTEGER) throw Error('catch identity overflow');
  const previous = p.records[type.kind] || {
    caught: '0',
    maxLength: 0,
    bestQuality: 0,
    perfectCount: '0',
  };
  p.records[type.kind] = {
    caught: addAmount(previous.caught, 1),
    maxLength: Math.max(previous.maxLength, length),
    bestQuality: Math.max(previous.bestQuality, quality),
    perfectCount: addAmount(previous.perfectCount, perfect ? 1 : 0),
  };
  const item = { id: p.nextCatchId++, kind: type.kind, length, quality, perfect, locked: false };
  if (p.basket.length >= 80) p.coins = addAmount(p.coins, catchValue(item));
  else p.basket.push(item);
  advanceContracts(p, 'fish', type, perfect);
}
export function contractProgress(p: Profile, q: Contract) {
  return q.type === 'delivery'
    ? Math.min(q.target, p.basket.filter((c) => c.kind === q.kind && !c.locked).length)
    : q.progress;
}
export function skillOptions(p: Profile, tier: number) {
  if (tier === 5) return ['steady', 'tracker'];
  if (tier === 10)
    return p.first === 'steady'
      ? ['calm', 'control']
      : p.first === 'tracker'
        ? ['legendHunter', 'brawler']
        : [];
  if (tier === 15) return ['patience', 'reader'];
  if (tier === 20) return ['master', 'deepSeeker'];
  return [];
}
export function pendingSkillTier(p: Profile) {
  const level = levelFromXp(p.xp);
  if (level >= 5 && !p.first) return 5;
  if (level >= 10 && p.first && !p.second) return 10;
  if (level >= 15 && p.second && !p.third) return 15;
  if (level >= 20 && p.third && !p.fourth) return 20;
  return 0;
}
export function fitLoadout(l: Loadout) {
  const rod = gear(l.rod);
  if (!rod || !('tackleSlots' in rod)) throw Error('unknown rod');
  const result = { ...l };
  if (rod.tackleSlots < 1) delete result.tackle1;
  if (rod.tackleSlots < 2) delete result.tackle2;
  if (rod.tackleSlots < 3) delete result.tackle3;
  return result;
}
export function validLoadout(p: Profile, l: Loadout) {
  const rod = gear(l.rod);
  return Boolean(
    rod &&
    rod.slot === 'rod' &&
    copies(p, l.rod) &&
    [l.tackle1, l.tackle2, l.tackle3].every(
      (id, i) =>
        !id ||
        (gear(id)?.slot === 'tackle' &&
          [l.tackle1, l.tackle2, l.tackle3].filter((v) => v === id).length <=
            Math.min(2, copies(p, id)) &&
          'tackleSlots' in rod &&
          i < rod.tackleSlots),
    ),
  );
}
export function unlockReady(p: Profile, id: string, copy: number) {
  if (id === 'qualityBobber')
    return (
      Object.values(p.records).reduce((n, r) => n + BigInt(r.perfectCount), 0n) >=
      BigInt(copy === 1 ? 3 : 10)
    );
  if (id === 'curiosityLure' || id === 'legendRod')
    return (
      catalog.FISH_TYPES.filter((f) => f.rarity === 'legendary').reduce(
        (n, f) => n + BigInt(p.records[f.kind]?.caught || '0'),
        0n,
      ) >= BigInt(id === 'legendRod' ? catalog.GEAR.legendRod.legendaryRequired : copy)
    );
  return true;
}
export function ensureContractBoard(p: Profile) {
  if (p.contractsDay === p.day) return;
  const day = p.day;
  p.contracts = p.contracts.filter((q) => q.status === 'active' || q.day === day);
  const level = levelFromXp(p.xp);
  const maxDifficulty =
    p.equipped.rod === 'trainingRod' ? 49 : level < 5 ? 49 : level < 10 ? 65 : 80;
  const pool = catalog.FISH_TYPES.filter(
    (f: FishType) =>
      f.location === p.location && f.difficulty <= maxDifficulty && !f.periods && !f.weathers,
  );
  const destinations = Object.keys(catalog.LOCATIONS);
  const destination =
    destinations[(destinations.indexOf(p.location) + 1 + (day % 2)) % destinations.length];
  for (const [slot, template] of Object.entries(catalog.CONTRACT_SLOTS)) {
    const id = `${day}-${slot}`;
    if (p.contracts.some((q) => q.id === id)) continue;
    const f =
      template.type === 'delivery'
        ? pool[(day - 1 + (slot === 'delivery2' ? 1 : 0)) % pool.length]
        : undefined;
    p.contracts.push({
      id,
      day,
      slot,
      type: template.type,
      target: template.target,
      kind: f?.kind,
      location: f?.location || (template.type === 'catch' ? destination : undefined),
      progress: 0,
      status: 'available',
    });
  }
  p.contractsDay = day;
}
export function contractReward(q: Contract) {
  if (q.type === 'delivery')
    return { coins: Math.ceil(fish(q.kind!)!.basePrice * q.target * 1.8), bait: 'basic', count: 3 };
  if (q.type === 'perfect') return { coins: 220, bait: 'glimmer', count: 1 };
  if (q.type === 'cleanup') return { coins: 140, bait: 'basic', count: 5 };
  return { coins: 150, bait: 'basic', count: 3 };
}

export function validateProfile(p: Profile) {
  for (const amount of [
    p.coins,
    p.xp,
    p.trashRecovered,
    p.treasureOpened,
    p.completedContracts,
    p.streak,
  ])
    parseAmount(amount);
  const invalid = () => {
    throw Error('invalid profile state');
  };
  if (
    !Number.isSafeInteger(p.day) ||
    p.day < 1 ||
    !Number.isSafeInteger(p.contractsDay) ||
    p.contractsDay < 0 ||
    p.contractsDay > p.day ||
    !Number.isFinite(p.clockMinutes) ||
    p.clockMinutes < 0 ||
    p.clockMinutes >= 1440 ||
    !Number.isSafeInteger(p.nextCatchId) ||
    p.nextCatchId < 1 ||
    !catalog.LOCATIONS[p.location as keyof typeof catalog.LOCATIONS]
  )
    invalid();
  if (
    p.ownedGear.length < 1 ||
    p.ownedGear.length > 24 ||
    copies(p, 'bambooPole') !== 1 ||
    !validLoadout(p, p.equipped)
  )
    invalid();
  for (const id of p.ownedGear)
    if (!gear(id) || copies(p, id) > (gear(id).slot === 'rod' ? 1 : 2)) invalid();
  for (const [stock, keys, max] of [
    [p.baitStock, Object.keys(catalog.BAITS), 999],
    [p.debrisStock, Object.keys(catalog.DEBRIS), 9999],
  ] as const) {
    if (Object.keys(stock).length !== keys.length) invalid();
    for (const id of keys)
      if (!Number.isInteger(stock[id]) || stock[id] < 0 || stock[id] > max) invalid();
  }
  if (
    p.selectedBait &&
    (!catalog.BAITS[p.selectedBait as keyof typeof catalog.BAITS] ||
      p.baitStock[p.selectedBait] < 1)
  )
    invalid();
  if (
    p.basket.length > 80 ||
    p.savedLoadouts.length !== 3 ||
    p.contracts.length > 8 ||
    Object.keys(p.records).length > catalog.FISH_TYPES.length
  )
    invalid();
  const ids = new Set<number>();
  for (const c of p.basket) {
    const f = fish(c.kind);
    if (
      !f ||
      !Number.isSafeInteger(c.id) ||
      c.id < 1 ||
      c.id >= p.nextCatchId ||
      ids.has(c.id) ||
      !Number.isInteger(c.length) ||
      c.length < f.length[0] ||
      c.length > f.length[1] ||
      !Number.isInteger(c.quality) ||
      c.quality < 0 ||
      c.quality > 3
    )
      invalid();
    ids.add(c.id);
  }
  for (const [kind, r] of Object.entries(p.records)) {
    const f = fish(kind);
    if (
      !f ||
      parseAmount(r.caught) < 1n ||
      parseAmount(r.perfectCount) > parseAmount(r.caught) ||
      !Number.isInteger(r.maxLength) ||
      r.maxLength < f.length[0] ||
      r.maxLength > f.length[1] ||
      !Number.isInteger(r.bestQuality) ||
      r.bestQuality < 0 ||
      r.bestQuality > 3
    )
      invalid();
  }
  if (caught(p) > (1n << 128n) - 1n || parseAmount(p.streak) > caught(p)) invalid();
  const level = levelFromXp(p.xp),
    skills = [p.first, p.second, p.third, p.fourth];
  for (let i = 0; i < 4; i++) {
    if (
      (skills[i] && (level < (i + 1) * 5 || !skillOptions(p, (i + 1) * 5).includes(skills[i]!))) ||
      (!skills[i] && skills.slice(i + 1).some(Boolean))
    )
      invalid();
  }
  return p;
}

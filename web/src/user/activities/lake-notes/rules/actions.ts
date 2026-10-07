import { addAmount, catalog, gear, levelFromXp } from './catalog';
import {
  advanceClock,
  awardBait,
  contractProgress,
  contractReward,
  copies,
  catchValue,
  ensureContractBoard,
  fitLoadout,
  pendingSkillTier,
  skillOptions,
  unlockReady,
  validLoadout,
  validateProfile,
} from './profile';
import type { Action, Profile } from './types';

/** Preview only. The server authorizes and commits actions using its own profile. */
export function applyAction(p: Profile, a: Action) {
  validateProfile(p);
  const masks: Record<string, string> = {
    buy_gear: 'i',
    equip_gear: 'is',
    save_gear_loadout: 'n',
    load_gear_loadout: 'n',
    buy_bait: 'iq',
    select_bait: 'i',
    sell_fish: 'f',
    sell_all_fish: '',
    set_fish_lock: 'fl',
    sell_debris: 'i',
    sell_all_debris: '',
    switch_location: 'i',
    rest: '',
    choose_skill: 'i',
    respec: '',
    accept_contract: 'i',
    cancel_contract: 'i',
    claim_contract: 'i',
  };
  const mask = masks[a.action];
  const fail = () => {
    throw Error('invalid profile action');
  };
  if (
    mask === undefined ||
    (a.id && !mask.includes('i')) ||
    (a.slot && !mask.includes('s')) ||
    (a.index !== undefined && !mask.includes('n')) ||
    (a.fish_ids?.length && !mask.includes('f')) ||
    (a.locked !== undefined && !mask.includes('l')) ||
    (a.quantity !== undefined &&
      (!mask.includes('q') || !Number.isInteger(a.quantity) || a.quantity < 1 || a.quantity > 999))
  )
    fail();
  if (
    mask.includes('n') &&
    (a.index === undefined || !Number.isInteger(a.index) || a.index < 0 || a.index > 2)
  )
    fail();
  if (
    mask.includes('f') &&
    (!a.fish_ids?.length ||
      a.fish_ids.length > 80 ||
      new Set(a.fish_ids).size !== a.fish_ids.length ||
      a.fish_ids.some((id) => !p.basket.some((c) => c.id === id)))
  )
    fail();
  const profile = structuredClone(p),
    id = a.id || '';
  const spend = (n: number) => {
    profile.coins = addAmount(profile.coins, -n);
  };
  switch (a.action) {
    case 'buy_gear': {
      const g = gear(id);
      if (
        !g ||
        copies(profile, id) >= (g.slot === 'rod' ? 1 : 2) ||
        !unlockReady(profile, id, copies(profile, id) + 1)
      )
        fail();
      spend(g.cost);
      profile.ownedGear.push(id);
      if (g.slot === 'rod') profile.equipped = fitLoadout({ ...profile.equipped, rod: id });
      else {
        const rod = gear(profile.equipped.rod);
        const slots = rod && 'tackleSlots' in rod ? rod.tackleSlots : 0;
        if (slots >= 1 && !profile.equipped.tackle1) profile.equipped.tackle1 = id;
        else if (slots >= 2 && !profile.equipped.tackle2) profile.equipped.tackle2 = id;
        else if (slots >= 3 && !profile.equipped.tackle3) profile.equipped.tackle3 = id;
      }
      break;
    }
    case 'equip_gear': {
      if (a.slot === 'rod') {
        if (gear(id)?.slot !== 'rod' || !copies(profile, id)) fail();
        profile.equipped = fitLoadout({ ...profile.equipped, rod: id });
      } else {
        if (!['tackle1', 'tackle2', 'tackle3'].includes(a.slot || '')) fail();
        const slot = a.slot as 'tackle1' | 'tackle2' | 'tackle3',
          used = (['tackle1', 'tackle2', 'tackle3'] as const).filter(
            (s) => s !== slot && profile.equipped[s] === id,
          ).length,
          rod = gear(profile.equipped.rod);
        if (
          !rod ||
          !('tackleSlots' in rod) ||
          rod.tackleSlots < Number(slot.slice(-1)) ||
          (id &&
            (gear(id)?.slot !== 'tackle' ||
              !copies(profile, id) ||
              used >= Math.min(2, copies(profile, id))))
        )
          fail();
        if (id) profile.equipped[slot] = id;
        else delete profile.equipped[slot];
      }
      break;
    }
    case 'save_gear_loadout':
      profile.savedLoadouts[a.index!] = { ...profile.equipped, bait: profile.selectedBait };
      break;
    case 'load_gear_loadout': {
      const saved = profile.savedLoadouts[a.index!];
      if (!saved) fail();
      const candidate = fitLoadout(saved!);
      if (!validLoadout(profile, candidate)) fail();
      profile.equipped = candidate;
      delete profile.equipped.bait;
      if (saved!.bait && profile.baitStock[saved!.bait] > 0) profile.selectedBait = saved!.bait;
      else delete profile.selectedBait;
      break;
    }
    case 'buy_bait': {
      const b = catalog.BAITS[id as keyof typeof catalog.BAITS];
      if (!b || profile.baitStock[id] >= 999) fail();
      let count = Math.min(a.quantity ?? 1, 999 - profile.baitStock[id]);
      const affordable = BigInt(profile.coins) / BigInt(b.cost);
      if (affordable < BigInt(count)) count = Number(affordable);
      if (!count) fail();
      spend(b.cost * count);
      profile.baitStock[id] += count;
      break;
    }
    case 'select_bait': {
      const rod = gear(profile.equipped.rod);
      if (
        id &&
        (!catalog.BAITS[id as keyof typeof catalog.BAITS] ||
          profile.baitStock[id] < 1 ||
          !rod ||
          !('baitAllowed' in rod) ||
          !rod.baitAllowed)
      )
        fail();
      if (id) profile.selectedBait = id;
      else delete profile.selectedBait;
      break;
    }
    case 'sell_fish':
    case 'sell_all_fish':
    case 'set_fish_lock': {
      if (a.action === 'set_fish_lock' && a.locked === undefined) fail();
      profile.basket = profile.basket.filter((c) => {
        const selected = a.action === 'sell_all_fish' || a.fish_ids!.includes(c.id);
        if (!selected) return true;
        if (a.action === 'set_fish_lock') {
          c.locked = a.locked!;
          return true;
        }
        if (c.locked) {
          if (a.action === 'sell_fish') fail();
          return true;
        }
        profile.coins = addAmount(profile.coins, catchValue(c));
        return false;
      });
      break;
    }
    case 'sell_debris':
    case 'sell_all_debris': {
      if (a.action === 'sell_debris' && !catalog.DEBRIS[id as keyof typeof catalog.DEBRIS]) fail();
      for (const [key, d] of Object.entries(catalog.DEBRIS))
        if (a.action === 'sell_all_debris' || id === key) {
          profile.coins = addAmount(profile.coins, profile.debrisStock[key] * d.value);
          profile.debrisStock[key] = 0;
        }
      break;
    }
    case 'switch_location':
      if (!catalog.LOCATIONS[id as keyof typeof catalog.LOCATIONS]) fail();
      profile.location = id;
      break;
    case 'rest': {
      const boundary = [300, 540, 1020, 1260, 1740].find((n) => n > profile.clockMinutes)!;
      advanceClock(profile, boundary - profile.clockMinutes);
      break;
    }
    case 'choose_skill': {
      const tier = pendingSkillTier(profile);
      if (!skillOptions(profile, tier).includes(id)) fail();
      profile[
        ({ 5: 'first', 10: 'second', 15: 'third', 20: 'fourth' } as const)[tier as 5 | 10 | 15 | 20]
      ] = id;
      break;
    }
    case 'respec':
      if (![profile.first, profile.second, profile.third, profile.fourth].some(Boolean)) fail();
      spend(1000 + levelFromXp(profile.xp) * 50);
      delete profile.first;
      delete profile.second;
      delete profile.third;
      delete profile.fourth;
      break;
    case 'accept_contract':
    case 'cancel_contract':
    case 'claim_contract': {
      if (a.action === 'accept_contract') ensureContractBoard(profile);
      const index = profile.contracts.findIndex((q) => q.id === id);
      if (index < 0) fail();
      const q = profile.contracts[index];
      if (a.action === 'accept_contract') {
        if (
          q.status !== 'available' ||
          q.day !== profile.day ||
          profile.contracts.filter((q) => q.status === 'active').length >= 3
        )
          fail();
        q.status = 'active';
        q.progress = 0;
      } else if (a.action === 'cancel_contract') {
        if (q.status !== 'active') fail();
        if (q.day === profile.day) {
          q.status = 'available';
          q.progress = 0;
        } else profile.contracts.splice(index, 1);
      } else {
        if (q.status !== 'active' || contractProgress(profile, q) < q.target) fail();
        if (q.type === 'delivery') {
          const selected = profile.basket
            .filter((c) => c.kind === q.kind && !c.locked)
            .sort((a, b) => catchValue(a) - catchValue(b) || a.id - b.id)
            .slice(0, q.target);
          const ids = new Set(selected.map((c) => c.id));
          profile.basket = profile.basket.filter((c) => !ids.has(c.id));
        }
        q.status = 'completed';
        profile.completedContracts = addAmount(profile.completedContracts, 1);
        const reward = contractReward(q);
        profile.coins = addAmount(profile.coins, reward.coins);
        awardBait(profile, reward.bait, reward.count);
        if (q.day !== profile.day)
          profile.contracts = profile.contracts.filter((entry) => entry.id !== q.id);
      }
      break;
    }
  }
  return { profile, coin_delta: String(BigInt(profile.coins) - BigInt(p.coins)) };
}

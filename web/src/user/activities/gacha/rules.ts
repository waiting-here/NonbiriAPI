import { cardById, cards, rarities, tierOf } from './catalog';
import type { DrawItem, Pity, Pool, Rules } from './types';

export const defaultRules: Rules = {
  weights: [5500, 3500, 900, 100],
  sr_pity: 10,
  ssr_pity: 60,
  sr_featured: 5000,
  ssr_featured: 5000,
  sr_guarantee: true,
  ssr_guarantee: true,
};
export function freshPity(rules: Rules): Pity {
  return {
    sr: 0,
    ssr: 0,
    sr_limit: rules.sr_pity,
    ssr_limit: rules.ssr_pity,
    sr_featured: false,
    ssr_featured: false,
    ssr_misses: 0,
  };
}
export function poolOpen(pool: Pool, now: number) {
  return (
    !pool.paused &&
    (pool.kind === 'permanent' ||
      (pool.published &&
        pool.starts_at !== null &&
        pool.ends_at !== null &&
        now >= pool.starts_at &&
        now < pool.ends_at))
  );
}
export function protectPity(pity: Pity, rules: Rules): Pity {
  return {
    ...pity,
    sr_limit: pity.sr === 0 ? rules.sr_pity : Math.min(pity.sr_limit, rules.sr_pity),
    ssr_limit: pity.ssr === 0 ? rules.ssr_pity : Math.min(pity.ssr_limit, rules.ssr_pity),
  };
}
export function drawOne(
  pool: Pool,
  previous: Pity,
  random: (n: number) => number,
  newcomer = false,
): { item: DrawItem; pity: Pity } {
  const pity = protectPity(previous, pool.rules);
  let rarity: (typeof rarities)[number];
  let guarantee: DrawItem['guarantee'] = 'none';
  if (newcomer || pity.ssr + 1 >= pity.ssr_limit) {
    rarity = 'SSR';
    guarantee = newcomer ? 'newcomer' : 'ssr';
  } else if (pity.sr + 1 >= pity.sr_limit) {
    rarity = random(10000) < pool.rules.weights[3] ? 'SSR' : 'SR';
    guarantee = 'sr';
  } else {
    const roll = random(10000);
    let cumulative = 0;
    rarity = 'SSR';
    for (let i = 0; i < rarities.length; i++) {
      cumulative += pool.rules.weights[i];
      if (roll < cumulative) {
        rarity = rarities[i];
        break;
      }
    }
  }
  let candidates = cards.filter(
    (card) => card.source === 'permanent' && tierOf(card.rarity) === rarity,
  );
  if (pool.kind === 'limited' && (rarity === 'SR' || rarity === 'SSR')) {
    const featured = pool.featured
      .map((id) => cardById[id])
      .filter((card) => card && tierOf(card.rarity) === rarity);
    if (featured.length) {
      const flag = rarity === 'SR' ? 'sr_featured' : 'ssr_featured';
      const enabled = rarity === 'SR' ? pool.rules.sr_guarantee : pool.rules.ssr_guarantee;
      const probability = rarity === 'SR' ? pool.rules.sr_featured : pool.rules.ssr_featured;
      const limit = Math.max(1, pool.rules.ssr_guarantee_misses ?? 1);
      const guaranteed =
        rarity === 'SSR' ? pity.ssr_featured || (pity.ssr_misses ?? 0) >= limit : pity.sr_featured;
      const hit = (enabled && guaranteed) || random(10000) < probability;
      if (hit) candidates = featured;
      if (rarity === 'SSR') {
        pity.ssr_misses = hit || !enabled ? 0 : Math.min(limit, (pity.ssr_misses ?? 0) + 1);
        pity.ssr_featured = enabled && pity.ssr_misses >= limit;
      } else pity[flag] = enabled && !hit;
    }
  }
  const card = candidates[random(candidates.length)];
  pity.sr = rarity === 'SR' || rarity === 'SSR' ? 0 : pity.sr + 1;
  pity.ssr = rarity === 'SSR' ? 0 : pity.ssr + 1;
  if (pity.sr === 0) pity.sr_limit = pool.rules.sr_pity;
  if (pity.ssr === 0) pity.ssr_limit = pool.rules.ssr_pity;
  return { item: { card_id: card.id, duplicate: false, guarantee }, pity };
}
export function randomInt(n: number) {
  const threshold = Math.floor(0x100000000 / n) * n;
  const values = new Uint32Array(1);
  do {
    crypto.getRandomValues(values);
  } while (values[0] >= threshold);
  return values[0] % n;
}

export function consolidatedRates(rules: Rules): [number, number, number, number] {
  const rewards: [number, number, number, number] = [0, 0, 0, 0];
  if (
    !Number.isInteger(rules.sr_pity) ||
    rules.sr_pity < 1 ||
    rules.sr_pity > 100 ||
    !Number.isInteger(rules.ssr_pity) ||
    rules.ssr_pity < rules.sr_pity ||
    rules.ssr_pity > 1000 ||
    rules.weights.reduce((a, b) => a + b, 0) !== 10000
  )
    return rewards;
  let alive = new Array<number>(rules.sr_pity).fill(0),
    duration = 0;
  alive[0] = 1;
  for (let draw = 1; draw <= rules.ssr_pity; draw++) {
    const next = new Array<number>(rules.sr_pity).fill(0);
    alive.forEach((mass, sr) => {
      duration += mass;
      let rates = rules.weights.map((value) => value / 10000);
      if (draw === rules.ssr_pity) rates = [0, 0, 0, 1];
      else if (sr + 1 === rules.sr_pity) rates = [0, 0, 1 - rates[3], rates[3]];
      rates.forEach((rate, i) => {
        rewards[i] += mass * rate;
      });
      next[0] += mass * rates[2];
      if (sr + 1 < next.length) next[sr + 1] += mass * (rates[0] + rates[1]);
    });
    alive = next;
  }
  return rewards.map((value) => value / duration) as typeof rewards;
}

import { expect, it } from 'vitest';
import { defaultRules, drawOne, freshPity, consolidatedRates } from './rules';
import type { Pool } from './types';

it('previews the same 15% two-miss SSR-tier guarantee and resets on a hit', () => {
  const rules = { ...defaultRules, ssr_featured: 1500, ssr_guarantee_misses: 2 };
  const pool: Pool = {
    id: 'l',
    name: 'Stars',
    cover: 'l',
    kind: 'limited',
    group: 'special',
    rules,
    featured: ['l-chatgpt'],
    starts_at: 0,
    ends_at: 2000000000,
    paused: false,
    published: true,
    revision: 1,
    rates: consolidatedRates(rules),
  };
  let pity = freshPity(rules);
  for (let hit = 1; hit <= 3; hit++) {
    const result = drawOne(pool, { ...pity, ssr: 59 }, (n) => n - 1);
    pity = result.pity;
    expect(result.item.card_id === 'l-chatgpt').toBe(hit === 3);
    expect(pity.ssr_misses).toBe(hit === 3 ? 0 : hit);
    expect(pity.ssr_featured).toBe(hit === 2);
  }
  expect(consolidatedRates(rules)).toEqual(consolidatedRates(defaultRules));
  for (const roll of [1499, 1500]) {
    const result = drawOne(pool, { ...freshPity(rules), ssr: 59 }, (n) => (n === 10000 ? roll : 0));
    expect(result.item.card_id === 'l-chatgpt').toBe(roll === 1499);
  }
});

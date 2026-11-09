import { expect, it } from 'vitest';
import { lakeDraft, lakeWire } from './LakeFields';
import { directions, type LakeSettings } from '@shared/lakenotes/api';

it('keeps daily caps in their source units, including fractional credits, zero, and unlimited', () => {
  const raw: LakeSettings = {
    enabled: true,
    exchanges: Object.fromEntries(
      directions.map((direction) => [
        direction,
        {
          enabled: true,
          source_amount: '7',
          target_amount: '250',
          daily_source_limit: '10005',
        },
      ]),
    ) as LakeSettings['exchanges'],
  };
  const draft = lakeDraft(raw);
  expect(draft.exchanges.general_to_coins.daily_source_limit).toBe('10.005');
  expect(draft.exchanges.game_to_coins.daily_source_limit).toBe('10.005');
  expect(draft.exchanges.coins_to_general.daily_source_limit).toBe('10005');
  expect(lakeWire(draft)).toEqual(raw);
  draft.exchanges.general_to_coins.daily_source_limit = '0';
  draft.exchanges.game_to_coins.daily_source_limit = '';
  const next = lakeWire(draft);
  expect(next.exchanges.general_to_coins.daily_source_limit).toBe('0');
  expect(next.exchanges.game_to_coins.daily_source_limit).toBe('');
});

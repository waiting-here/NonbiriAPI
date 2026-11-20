import { describe, expect, it } from 'vitest';
import {
  displayValue,
  economyProblem,
  exchangePreview,
  readField,
  type WhaleField,
} from './whaleConfig';

const field: WhaleField = {
  key: 'wallet_cap_milli',
  group: 'exchange',
  unit: 'general_credits',
  min: 1,
  max: 9_000_000_000_000_000,
  en: 'Wallet cap',
  zh: '余额上限',
};
export const sampleValues = {
  platform_cut_bp: 300,
  welfare_cut_bp: 300,
  thursday_cut_bp: 300,
  lot_gross_milli: 5_000_000,
  lot_pearls: 5,
  conversion_min_bp: 8000,
  conversion_base_bp: 9200,
  conversion_max_bp: 12000,
  affection_level_10_bp: 1200,
  mood_high_bonus_bp: 400,
  sleep_bonus_bp: 500,
  decor_jelly_lamp_bonus_bp: 300,
  decor_coral_tea_bonus_bp: 400,
  feed_silver_fish_base_pearls: 10,
  feed_silver_fish_price_milli: 10_000_000,
  feed_sweet_shrimp_base_pearls: 25,
  feed_sweet_shrimp_price_milli: 25_000_000,
};
describe('Whale Haven economics', () => {
  it('round-trips exact display amounts up to the wallet ceiling', () => {
    expect(readField('8999999999999.999', field)).toBe(8_999_999_999_999_999);
    expect(displayValue(8_999_999_999_999_999, field.unit)).toBe('8999999999999.999');
    expect(readField('3.0001', field)).toBeNull();
    expect(readField('1e5', field)).toBeNull();
    expect(readField('0.01', { ...field, unit: 'percent' })).toBe(1);
  });
  it('shows the attainable bonus, feed economics, and independently rounded cuts per lot', () => {
    expect(exchangePreview(sampleValues)).toMatchObject({
      grossBP: 12000,
      bestReturnPct: 109.2,
      net: 4550000,
      cuts: [150000, 150000, 150000],
    });
    const custom = {
      ...sampleValues,
      conversion_max_bp: 50000,
      affection_level_10_bp: 0,
      feed_silver_fish_price_milli: 5000000,
    };
    expect(exchangePreview(custom).grossBP).toBe(10800);
    expect(exchangePreview(custom).bestReturnPct).toBeCloseTo(196.56);
    expect(
      exchangePreview({
        ...sampleValues,
        lot_gross_milli: 1003,
        platform_cut_bp: 3333,
        welfare_cut_bp: 100,
        thursday_cut_bp: 0,
      }),
    ).toMatchObject({ net: 659, cuts: [334, 10, 0] });
  });
  it('accepts zero fees and rejects a 100% combined cut', () => {
    expect(
      economyProblem({
        ...sampleValues,
        platform_cut_bp: 0,
        thursday_cut_bp: 0,
        welfare_cut_bp: 0,
      }),
    ).toBeNull();
    expect(economyProblem({ ...sampleValues, platform_cut_bp: 9400 })).not.toBeNull();
  });
});

import { describe, expect, it } from 'vitest';
import {
  quotePricing,
  resolveSize,
  validPricingPolicy,
  validSizeCapability,
  type PricingPolicy,
  type SizeCapability,
} from './capabilities';

describe('image size and price policy', () => {
  const pricing: PricingPolicy = {
    default: { paper: '2', brush: '1' },
    fallback: 'default',
    tiers: [
      { tier: 'medium', paper: '3', brush: '1' },
      { tier: 'auto', paper: '5', brush: '0' },
    ],
    sizes: [
      { width: 1024, height: 768, paper: '7', brush: '0' },
      { width: 768, height: 1024, paper: '8', brush: '0' },
    ],
  };

  it('quotes exact dimensions before tier, preserving orientation and auto', () => {
    expect(quotePricing(pricing, { width: 1024, height: 768, tier: 'medium' }, 3)).toEqual({
      unit: { paper: '7', brush: '0' },
      total: { paper: '21', brush: '0' },
      basis: 'size',
      price_key: '1024x768',
    });
    expect(quotePricing(pricing, { width: 768, height: 1024, tier: 'medium' }, 1)?.unit.paper).toBe(
      '8',
    );
    expect(quotePricing(pricing, { tier: 'medium' }, 1)?.basis).toBe('tier');
    expect(quotePricing(pricing, { width: 512, height: 512 }, 1)?.basis).toBe('default');
    expect(quotePricing(pricing, { auto: true }, 1)?.basis).toBe('auto');
  });

  it('does not price unavailable sizes or accept ambiguous schedules', () => {
    expect(
      quotePricing({ ...pricing, fallback: 'unavailable' }, { width: 512, height: 512 }, 1),
    ).toBeNull();
    expect(validPricingPolicy({ ...pricing, tiers: [...pricing.tiers, pricing.tiers[0]] })).toBe(
      false,
    );
    expect(validPricingPolicy({ ...pricing, sizes: [...pricing.sizes, pricing.sizes[0]] })).toBe(
      false,
    );
    expect(quotePricing(pricing, { auto: true, width: 256 }, 1)).toBeNull();
    expect(
      quotePricing(
        { ...pricing, default: { paper: '170141183460469231731687303715884105', brush: '0' } },
        {},
        16,
      ),
    ).toBeNull();
  });

  it('keeps resolution, ratio, size, and price tier linked', () => {
    const grid: SizeCapability = {
      mode: 'resolution_ratio_grid',
      combinations: [
        { ratio: '4:3', resolution: 'medium', width: 1024, height: 768, tier: 'medium' },
      ],
    };
    const resolved = resolveSize(grid, { aspect_ratio: '4:3', resolution: 'medium' });
    expect(resolved).toEqual({
      values: { aspect_ratio: '4:3', resolution: 'medium', size: '1024x768' },
      selection: { width: 1024, height: 768, tier: 'medium' },
    });
    expect(quotePricing(pricing, resolved!.selection, 2)?.total.paper).toBe('14');
    expect(
      resolveSize(grid, { aspect_ratio: '4:3', resolution: 'medium', size: '768x1024' }),
    ).toBeNull();

    const ratio: SizeCapability = {
      mode: 'ratio_size_map',
      combinations: [{ ratio: 'portrait', width: 768, height: 1024 }],
    };
    expect(resolveSize(ratio, { aspect_ratio: 'portrait' })?.values.size).toBe('768x1024');

    const unknown: SizeCapability = {
      mode: 'ratio_resolution',
      combinations: [{ ratio: '4:3', resolution: 'single', tier: 'medium' }],
    };
    expect(resolveSize(unknown, { aspect_ratio: '4:3', resolution: 'single' })?.selection).toEqual({
      tier: 'medium',
    });

    const dimensions: SizeCapability = {
      mode: 'width_height',
      width: { minimum: 256, maximum: 1024, step: 256 },
      height: { minimum: 256, maximum: 1024, step: 256 },
      max_pixels: 786432,
      combinations: [{ width: 768, height: 1024, tier: 'large' }],
      auto: true,
    };
    expect(resolveSize(dimensions, { size: '768x1024' })?.selection.tier).toBe('large');
    expect(resolveSize(dimensions, { size: '1024x1024' })).toBeNull();
    expect(resolveSize(dimensions, { size: '512x512' })).toBeNull();
    expect(resolveSize(dimensions, { auto: true })?.selection).toEqual({ auto: true });
    expect(
      validSizeCapability({ ...dimensions, combinations: [{ width: 257, height: 256 }] }),
    ).toBe(false);
    expect(
      validSizeCapability({
        mode: 'ratio_resolution',
        combinations: [
          { ratio: '1:1', resolution: 'low', width: 256, height: 256, tier: 'small' },
          { ratio: '4:4', resolution: 'low', width: 256, height: 256, tier: 'small' },
        ],
      }),
    ).toBe(false);
  });
});

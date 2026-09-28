import { decodePricingPolicy } from '@shared/picturebook/publicApi';
import type { ImageModel } from '@shared/picturebook/publicTypes';
import type { PricingPolicy } from '@shared/picturebook/capabilities';

export interface PriceDraft {
  paper: string;
  brush: string;
  fallback: PricingPolicy['fallback'];
  tiers: PricingPolicy['tiers'];
  sizes: { width: string; height: string; paper: string; brush: string }[];
}

export function initialPrices(model: Pick<ImageModel, 'price' | 'pricing'>): PriceDraft {
  return {
    ...(model.pricing?.default ?? model.price),
    fallback: model.pricing?.fallback ?? 'default',
    tiers: model.pricing?.tiers ?? [],
    sizes: (model.pricing?.sizes ?? []).map((row) => ({
      ...row,
      width: String(row.width),
      height: String(row.height),
    })),
  };
}

export function draftPricing(value: PriceDraft): PricingPolicy {
  return decodePricingPolicy({
    default: { paper: value.paper, brush: value.brush },
    fallback: value.fallback,
    tiers: value.tiers,
    sizes: value.sizes.map((row) => ({
      ...row,
      width: Number(row.width),
      height: Number(row.height),
    })),
  });
}

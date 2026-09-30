import type { ParameterKey, Price, Scalar } from './publicTypes';

export const maxCurrencyUnits = ((1n << 127n) - 1n) / 1000n;
export function currencyUnits(value: string): bigint | null {
  if (!/^(0|[1-9][0-9]{0,38})$/.test(value)) return null;
  const n = BigInt(value);
  return n <= maxCurrencyUnits ? n : null;
}
export function multipliedPrice(paper: string, brush: string, count: number): Price | null {
  const p = currencyUnits(paper),
    b = currencyUnits(brush);
  if (
    p === null ||
    b === null ||
    (p === 0n && b === 0n) ||
    !Number.isSafeInteger(count) ||
    count < 1 ||
    count > 16
  )
    return null;
  const totalP = p * BigInt(count),
    totalB = b * BigInt(count);
  return totalP <= maxCurrencyUnits && totalB <= maxCurrencyUnits
    ? { paper: String(totalP), brush: String(totalB) }
    : null;
}
export function parseDimensions(value: string): [number, number] | null {
  if (!/^[1-9][0-9]{0,4}x[1-9][0-9]{0,4}$/.test(value)) return null;
  const [width, height] = value.split('x').map(Number);
  return [width, height];
}

export type SizeMode =
  'resolution_ratio_grid' | 'ratio_size_map' | 'ratio_resolution' | 'width_height';
export interface SizeCombination {
  ratio?: string;
  resolution?: string;
  width?: number;
  height?: number;
  tier?: string;
}
export interface DimensionRange {
  minimum: number;
  maximum: number;
  step: number;
}
export interface SizeCapability {
  mode: SizeMode;
  combinations?: SizeCombination[];
  width?: DimensionRange;
  height?: DimensionRange;
  max_pixels?: number;
  auto?: boolean;
}
export interface SizeInput {
  aspect_ratio?: string;
  resolution?: string;
  size?: string;
  auto?: boolean;
}
export interface PriceSelection {
  width?: number;
  height?: number;
  tier?: string;
  auto?: boolean;
}
export interface ResolvedSize {
  values: Partial<Record<ParameterKey, Scalar>>;
  selection: PriceSelection;
}
export interface TierPrice extends Price {
  tier: string;
}
export interface SizePrice extends Price {
  width: number;
  height: number;
}
export interface PricingPolicy {
  default: Price;
  fallback: 'default' | 'unavailable';
  tiers: TierPrice[];
  sizes: SizePrice[];
}
export interface PriceQuote {
  unit: Price;
  total: Price;
  basis: 'default' | 'tier' | 'size' | 'auto';
  price_key: string;
}

const label = (text: unknown): text is string =>
  typeof text === 'string' &&
  text.length > 0 &&
  new TextEncoder().encode(text).byteLength <= 128 &&
  !/[\p{Cc}\p{Cf}]/u.test(text);
const validDimension = (axis: DimensionRange) =>
  Number.isSafeInteger(axis.minimum) &&
  Number.isSafeInteger(axis.maximum) &&
  Number.isSafeInteger(axis.step) &&
  axis.minimum >= 1 &&
  axis.maximum <= 65536 &&
  axis.minimum <= axis.maximum &&
  axis.step >= 1 &&
  axis.step <= 65536;

export function validSizeCapability(capability: SizeCapability): boolean {
  const rows = capability.combinations ?? [];
  if (
    rows.length > 2048 ||
    !['resolution_ratio_grid', 'ratio_size_map', 'ratio_resolution', 'width_height'].includes(
      capability.mode,
    )
  )
    return false;
  if (capability.mode === 'width_height') {
    if (
      !capability.width ||
      !capability.height ||
      !validDimension(capability.width) ||
      !validDimension(capability.height)
    )
      return false;
  } else if (capability.width || capability.height || capability.max_pixels) return false;
  if (capability.mode !== 'width_height' && rows.length === 0 && !capability.auto) return false;
  if (
    capability.max_pixels !== undefined &&
    (!Number.isSafeInteger(capability.max_pixels) ||
      capability.max_pixels < 0 ||
      capability.max_pixels > 65536 ** 2)
  )
    return false;
  const identities = new Set<string>();
  const dimensions = new Map<string, string>();
  for (const row of rows) {
    const width = row.width ?? 0,
      height = row.height ?? 0;
    if (
      !Number.isSafeInteger(width) ||
      !Number.isSafeInteger(height) ||
      width < 0 ||
      height < 0 ||
      width > 65536 ||
      height > 65536 ||
      (width === 0) !== (height === 0) ||
      (row.tier !== undefined && !label(row.tier))
    )
      return false;
    let identity: string;
    switch (capability.mode) {
      case 'resolution_ratio_grid':
        if (!label(row.ratio) || !label(row.resolution) || width === 0) return false;
        identity = `${row.ratio}\0${row.resolution}`;
        break;
      case 'ratio_size_map':
        if (!label(row.ratio) || row.resolution || width === 0) return false;
        identity = row.ratio;
        break;
      case 'ratio_resolution':
        if (!label(row.ratio) || (row.resolution !== undefined && !label(row.resolution)))
          return false;
        identity = `${row.ratio}\0${row.resolution ?? ''}`;
        break;
      case 'width_height':
        if (
          row.ratio ||
          row.resolution ||
          width === 0 ||
          !containsDimensions(capability, width, height)
        )
          return false;
        identity = `${width}x${height}`;
        break;
    }
    if (identities.has(identity)) return false;
    identities.add(identity);
    if (width > 0) {
      const key = `${width}x${height}`;
      if (dimensions.has(key)) return false;
      dimensions.set(key, row.tier ?? '');
    }
  }
  return true;
}

function containsDimensions(c: SizeCapability, width: number, height: number): boolean {
  if (!c.width || !c.height) return false;
  const validAxis = (axis: DimensionRange, value: number) =>
    value >= axis.minimum && value <= axis.maximum && (value - axis.minimum) % axis.step === 0;
  return (
    validAxis(c.width, width) &&
    validAxis(c.height, height) &&
    (!c.max_pixels || width * height <= c.max_pixels)
  );
}

export function resolveSize(c: SizeCapability, input: SizeInput): ResolvedSize | null {
  if (!validSizeCapability(c)) return null;
  if (input.auto) {
    return c.auto && !input.aspect_ratio && !input.resolution && !input.size
      ? { values: { size: 'auto' }, selection: { auto: true } }
      : null;
  }
  if (input.size === 'auto') return null;
  if (c.mode === 'width_height') {
    if (input.aspect_ratio || input.resolution) return null;
    const dimensions = parseDimensions(input.size ?? '');
    if (!dimensions || !containsDimensions(c, ...dimensions)) return null;
    const [width, height] = dimensions;
    const matched = c.combinations?.find((row) => row.width === width && row.height === height);
    if (c.combinations?.length && !matched) return null;
    const tier = matched?.tier;
    return {
      values: { size: input.size },
      selection: { width, height, ...(tier ? { tier } : {}) },
    };
  }
  const row = c.combinations?.find(
    (item) =>
      item.ratio === input.aspect_ratio &&
      (c.mode === 'ratio_size_map' || (item.resolution ?? '') === (input.resolution ?? '')),
  );
  if (!row) return null;
  const size = row.width ? `${row.width}x${row.height}` : undefined;
  if (input.size && input.size !== size) return null;
  return {
    values: {
      aspect_ratio: row.ratio,
      ...(c.mode !== 'ratio_size_map' && row.resolution ? { resolution: row.resolution } : {}),
      ...(size ? { size } : {}),
    },
    selection: {
      ...(size ? { width: row.width, height: row.height } : {}),
      ...(row.tier ? { tier: row.tier } : {}),
    },
  };
}

const validUnit = (price: Price) =>
  currencyUnits(price.paper) !== null &&
  currencyUnits(price.brush) !== null &&
  (price.paper !== '0' || price.brush !== '0');

export function validPricingPolicy(policy: PricingPolicy): boolean {
  if (
    !validUnit(policy.default) ||
    !['default', 'unavailable'].includes(policy.fallback) ||
    policy.tiers.length > 64 ||
    policy.sizes.length > 2048
  )
    return false;
  const tiers = new Set<string>(),
    sizes = new Set<string>();
  for (const row of policy.tiers) {
    if (!label(row.tier) || tiers.has(row.tier) || !validUnit(row)) return false;
    tiers.add(row.tier);
  }
  for (const row of policy.sizes) {
    const key = `${row.width}x${row.height}`;
    if (
      !Number.isSafeInteger(row.width) ||
      !Number.isSafeInteger(row.height) ||
      row.width < 1 ||
      row.width > 65536 ||
      row.height < 1 ||
      row.height > 65536 ||
      sizes.has(key) ||
      !validUnit(row)
    )
      return false;
    sizes.add(key);
  }
  return true;
}

export function quotePricing(
  policy: PricingPolicy,
  selection: PriceSelection,
  count: number,
  capability?: SizeCapability,
): PriceQuote | null {
  if (!validPricingPolicy(policy)) return null;
  if (selection.auto) {
    if (selection.width || selection.height || (selection.tier && selection.tier !== 'auto'))
      return null;
  } else if (
    ((selection.width ?? 0) !== 0 && (selection.height ?? 0) === 0) ||
    ((selection.height ?? 0) !== 0 && (selection.width ?? 0) === 0) ||
    (selection.width !== undefined && !Number.isSafeInteger(selection.width)) ||
    (selection.height !== undefined && !Number.isSafeInteger(selection.height)) ||
    (selection.width ?? 0) < 0 ||
    (selection.height ?? 0) < 0 ||
    (selection.width ?? 0) > 65536 ||
    (selection.height ?? 0) > 65536 ||
    (selection.tier !== undefined && !label(selection.tier))
  )
    return null;
  const tier = selection.auto
    ? 'auto'
    : capability?.mode === 'width_height' && policy.fallback === 'unavailable'
      ? undefined
      : selection.tier;
  const size =
    !selection.auto && selection.width && selection.height
      ? policy.sizes.find((row) => row.width === selection.width && row.height === selection.height)
      : undefined;
  const tierPrice = !size && tier ? policy.tiers.find((row) => row.tier === tier) : undefined;
  if (!size && !tierPrice && policy.fallback === 'unavailable') return null;
  const unit = size ?? tierPrice ?? policy.default;
  const total = multipliedPrice(unit.paper, unit.brush, count);
  if (!total) return null;
  return {
    unit: { paper: unit.paper, brush: unit.brush },
    total,
    basis: selection.auto ? 'auto' : size ? 'size' : tierPrice ? 'tier' : 'default',
    price_key: selection.auto
      ? 'auto'
      : size
        ? `${size.width}x${size.height}`
        : (tierPrice?.tier ?? ''),
  };
}

/** Whole, priced pairs only; dimensions never imply a tier or a Cartesian grid. */
export function exactSizeCandidates(
  capability: SizeCapability,
  policy: PricingPolicy,
): SizePrice[] {
  if (capability.mode !== 'width_height' || !validSizeCapability(capability)) return [];
  const allowed = capability.combinations?.length
    ? new Set(capability.combinations.map((row) => `${row.width}x${row.height}`))
    : null;
  const pairs = new Map<string, SizePrice>();
  for (const row of policy.sizes) {
    const key = `${row.width}x${row.height}`;
    if (
      Number.isSafeInteger(row.width) &&
      Number.isSafeInteger(row.height) &&
      containsDimensions(capability, row.width, row.height) &&
      (!allowed || allowed.has(key)) &&
      validUnit(row) &&
      !pairs.has(key)
    )
      pairs.set(key, row);
  }
  return [...pairs.values()].sort(
    (left, right) => left.width - right.width || left.height - right.height,
  );
}

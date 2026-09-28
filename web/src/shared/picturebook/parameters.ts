import type {
  ImageModel,
  LengthUnit,
  ParameterKey,
  ParameterRule,
  Price,
  Scalar,
  SubmitInput,
} from './publicTypes';
import {
  multipliedPrice,
  parseDimensions,
  quotePricing,
  resolveSize,
  type PriceSelection,
  type ResolvedSize,
} from './capabilities';

export { currencyUnits, maxCurrencyUnits, multipliedPrice, parseDimensions } from './capabilities';

export const normalizeLines = (value: string) => value.replace(/\r\n?/g, '\n');
export function textLength(value: string, unit: LengthUnit): number {
  const normalized = normalizeLines(value);
  return unit === 'utf8_bytes'
    ? new TextEncoder().encode(normalized).byteLength
    : unit === 'unicode_scalars'
      ? Array.from(normalized).length
      : normalized.length;
}
export const promptBytes = (prompt: string, negative = '') =>
  textLength(prompt, 'utf8_bytes') + textLength(negative, 'utf8_bytes');
export type ParameterValues = Partial<Record<ParameterKey, string>>;
export function initialValues(model: ImageModel): ParameterValues {
  const values: ParameterValues = { prompt: '' };
  for (const rule of model.parameters) {
    if (!rule.supported) continue;
    const initial = rule.default ?? (rule.key === 'n' ? 1 : undefined);
    if (initial !== undefined) values[rule.key] = String(initial);
  }
  if (model.size_capability) {
    const capability = model.size_capability;
    const resolved = resolveSize(capability, {
      aspect_ratio: values.aspect_ratio, resolution: values.resolution,
      size: values.size === 'auto' ? undefined : values.size, auto: values.size === 'auto',
    });
    if (resolved) Object.assign(values, resolved.values);
    else {
      const rows = capability.combinations;
      const row = rows?.find((item) =>
        (!values.aspect_ratio || item.ratio === values.aspect_ratio)
        && (!values.resolution || item.resolution === values.resolution)
        && (!values.size || `${item.width}x${item.height}` === values.size),
      ) ?? rows?.[0];
      if (row) {
        delete values.aspect_ratio; delete values.resolution; delete values.size;
        if (row.ratio) values.aspect_ratio = row.ratio;
        if (row.resolution) values.resolution = row.resolution;
        if (row.width && row.height) values.size = `${row.width}x${row.height}`;
      } else if (capability.auto) values.size = 'auto';
    }
  }
  return values;
}
function scalarValue(rule: ParameterRule, raw: string): Scalar | null {
  if (rule.type === 'string') return normalizeLines(raw);
  if (!raw.trim()) return null;
  const number = Number(raw);
  return Number.isFinite(number) && (rule.type !== 'integer' || Number.isSafeInteger(number))
    ? number
    : null;
}
export function validScalar(rule: ParameterRule, value: Scalar): boolean {
  if (rule.type === 'string') {
    if (typeof value !== 'string') return false;
    const length = textLength(value, rule.length_unit ?? 'utf8_bytes');
    if (
      rule.key !== 'prompt' &&
      rule.key !== 'negative_prompt' &&
      textLength(value, 'utf8_bytes') > 512
    )
      return false;
    if (rule.min_length !== undefined && length < rule.min_length) return false;
    if (rule.max_length !== undefined && length > rule.max_length) return false;
    if (rule.dimensions) {
      const size = parseDimensions(value);
      if (!size) return false;
      for (const [axis, number] of [
        [rule.dimensions.width, size[0]],
        [rule.dimensions.height, size[1]],
      ] as const) {
        if (
          number < axis.minimum ||
          number > axis.maximum ||
          (number - axis.minimum) % axis.step !== 0
        )
          return false;
      }
    }
  } else {
    if (
      typeof value !== 'number' ||
      !Number.isFinite(value) ||
      (rule.type === 'integer' && !Number.isSafeInteger(value))
    )
      return false;
    if (rule.minimum !== undefined && value < rule.minimum) return false;
    if (rule.maximum !== undefined && value > rule.maximum) return false;
    if (rule.step !== undefined) {
      const decimal = (number: number) => {
        const [mantissa, exponent = '0'] = String(number).toLowerCase().split('e');
        const [whole, fraction = ''] = mantissa.split('.');
        return {
          coefficient: BigInt(whole + fraction),
          exponent: Number(exponent) - fraction.length,
        };
      };
      const parts = [value, rule.minimum ?? 0, rule.step].map(decimal);
      const scale = Math.min(...parts.map((part) => part.exponent));
      const [quantity, base, step] = parts.map(
        (part) => part.coefficient * 10n ** BigInt(part.exponent - scale),
      );
      if (step <= 0n || (quantity - base) % step !== 0n) return false;
    }
  }
  return !rule.enum || rule.enum.some((option) => option === value);
}
export type InputProblem = ParameterKey | 'combination' | 'prompt_size' | 'price';
function selectedSize(model: ImageModel, values: ParameterValues): ResolvedSize | null {
  return model.size_capability
    ? resolveSize(model.size_capability, {
        aspect_ratio: values.aspect_ratio,
        resolution: values.resolution,
        size: values.size === 'auto' ? undefined : values.size,
        auto: values.size === 'auto',
      })
    : { values: {}, selection: {} };
}

export function previewPrice(model: ImageModel, values: ParameterValues) {
  const resolved = selectedSize(model, values);
  if (!resolved) return null;
  const countRule = model.parameters.find((rule) => rule.key === 'n' && rule.supported);
  const count = Number(values.n || countRule?.default || 1);
  if (model.pricing) return quotePricing(model.pricing, resolved.selection, count);
  const total = multipliedPrice(model.price.paper, model.price.brush, count);
  return total ? { unit: model.price, total, basis: 'default' as const, price_key: '' } : null;
}

export function prepareSubmission(
  model: ImageModel,
  values: ParameterValues,
): { input: SubmitInput; price: Price; unit: Price; basis: string } | { problem: InputProblem } {
  const effectiveValues = { ...values };
  const resolved = selectedSize(model, values);
  if (!resolved) return { problem: 'size' };
  const selection: PriceSelection = resolved.selection;
  if (model.size_capability) {
    for (const [key, value] of Object.entries(resolved.values))
      effectiveValues[key as ParameterKey] = String(value);
  }
  const output: Partial<Record<ParameterKey, Scalar>> = {};
  for (const rule of model.parameters) {
    if (!rule.supported) continue;
    const raw = effectiveValues[rule.key];
    const fallback = rule.default ?? (rule.key === 'n' ? 1 : undefined);
    const value = raw === undefined || raw === '' ? fallback : scalarValue(rule, raw);
    if (value === undefined) {
      if (rule.required) return { problem: rule.key };
      continue;
    }
    if (value === null || !validScalar(rule, value)) return { problem: rule.key };
    output[rule.key] = value;
  }
  if (model.size_capability) {
    if (
      Object.entries(resolved.values).some(([key, value]) => output[key as ParameterKey] !== value)
    )
      return { problem: 'size' };
  }
  if (typeof output.prompt !== 'string' || !output.prompt.trim()) return { problem: 'prompt' };
  if (
    promptBytes(
      output.prompt,
      typeof output.negative_prompt === 'string' ? output.negative_prompt : '',
    ) > 65536
  )
    return { problem: 'prompt_size' };
  for (const combination of model.combinations) {
    if (
      !combination.allowed.some((tuple) =>
        combination.keys.every((key, index) => (output[key] ?? null) === tuple[index]),
      )
    )
      return { problem: 'combination' };
  }
  const n = typeof output.n === 'number' ? output.n : 1;
  const quote = model.pricing ? quotePricing(model.pricing, selection, n) : null;
  const price = model.pricing
    ? quote?.total
    : multipliedPrice(model.price.paper, model.price.brush, n);
  if (!price) return { problem: 'price' };
  const input = {
    model_id: model.id,
    expected_model_revision: model.revision,
    ...(model.pricing_revision ? { expected_pricing_revision: model.pricing_revision } : {}),
    ...output,
  } as SubmitInput;
  if (new TextEncoder().encode(JSON.stringify(input)).byteLength > 262144)
    return { problem: 'prompt_size' };
  return { input, price, unit: quote?.unit ?? model.price, basis: quote?.basis ?? 'legacy' };
}

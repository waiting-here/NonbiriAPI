import type {
  ImageModel,
  LengthUnit,
  ParameterKey,
  ParameterRule,
  Price,
  Scalar,
  SubmitInput,
} from './publicTypes';

export const maxCurrencyUnits = ((1n << 127n) - 1n) / 1000n;
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
export type ParameterValues = Partial<Record<ParameterKey, string>>;
export function initialValues(model: ImageModel): ParameterValues {
  const values: ParameterValues = { prompt: '' };
  for (const rule of model.parameters) {
    if (!rule.supported) continue;
    const initial = rule.default ?? (rule.key === 'n' ? 1 : undefined);
    if (initial !== undefined) values[rule.key] = String(initial);
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
export function parseDimensions(value: string): [number, number] | null {
  if (!/^[1-9][0-9]{0,4}x[1-9][0-9]{0,4}$/.test(value)) return null;
  const [width, height] = value.split('x').map(Number);
  return [width, height];
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
export function prepareSubmission(
  model: ImageModel,
  values: ParameterValues,
): { input: SubmitInput; price: Price } | { problem: InputProblem } {
  const output: Partial<Record<ParameterKey, Scalar>> = {};
  for (const rule of model.parameters) {
    if (!rule.supported) continue;
    const raw = values[rule.key];
    const fallback = rule.default ?? (rule.key === 'n' ? 1 : undefined);
    const value = raw === undefined || raw === '' ? fallback : scalarValue(rule, raw);
    if (value === undefined) {
      if (rule.required) return { problem: rule.key };
      continue;
    }
    if (value === null || !validScalar(rule, value)) return { problem: rule.key };
    output[rule.key] = value;
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
  const price = multipliedPrice(model.price.paper, model.price.brush, n);
  if (!price) return { problem: 'price' };
  const input = {
    model_id: model.id,
    expected_model_revision: model.revision,
    ...output,
  } as SubmitInput;
  if (new TextEncoder().encode(JSON.stringify(input)).byteLength > 262144)
    return { problem: 'prompt_size' };
  return { input, price };
}

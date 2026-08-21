// BigInt-safe formatting for counters and economy amounts.
//
// Economy amounts arrive from the API as canonical decimal strings (frozen
// wire contract): an optional '-' followed by digits only, with no exponent,
// no '+', no leading zeros, no whitespace and no '-0'. They are parsed
// straight into BigInt and never pass through JavaScript `number`, so
// int64-scale values keep full precision. Plain counters may still arrive as
// JSON numbers; they are accepted only as safe integers and converted
// losslessly.
//
// Display rules (frozen product requirements):
// - Values below 10,000 render as a grouped integer; 10,000 and above use
//   K/M/B/T letter abbreviations (10^3/6/9/12, T is the ceiling) with 1-2
//   significant digits. Scientific notation is never used.
// - Display credits are milli-credits divided by 1000 and rounded half away
//   from zero; the exact milli-credit value stays available for tooltips.
// - Both supported UI languages (zh-CN and en-US) use comma digit grouping,
//   so formatting is locale-independent by design.

export interface FormattedNumber {
  /** Short display form; may be abbreviated. */
  display: string;
  /** Exact value with digit grouping, for tooltips, logs and screen readers. */
  exact: string;
  /** True when `display` is abbreviated and `exact` adds information. */
  abbreviated: boolean;
}

const NOT_AVAILABLE = '—';

// Canonical decimal wire shape: optional '-', then 0 or a nonzero leading
// digit followed by digits. Anything else (exponent, '+', leading zeros,
// whitespace, empty, '-0') is rejected rather than coerced.
const CANONICAL_DECIMAL = /^-?(0|[1-9][0-9]*)$/;

const COMPACT_THRESHOLD = 10_000n;

// Ordered ascending; the largest unit not larger than the value wins. T is
// the ceiling: magnitudes beyond 10^12 keep the T suffix.
const COMPACT_UNITS = [
  { suffix: 'K', size: 1_000n },
  { suffix: 'M', size: 1_000_000n },
  { suffix: 'B', size: 1_000_000_000n },
  { suffix: 'T', size: 1_000_000_000_000n },
] as const;

function unavailable(): FormattedNumber {
  return { display: NOT_AVAILABLE, exact: NOT_AVAILABLE, abbreviated: false };
}

/**
 * Parses a canonical decimal wire string into a BigInt. Returns null for any
 * input that is not exactly canonical, so malformed server data can degrade
 * gracefully instead of being silently coerced through `number`.
 */
export function parseEconomyString(value: string): bigint | null {
  if (!CANONICAL_DECIMAL.test(value)) return null;
  const parsed = BigInt(value);
  if (parsed === 0n && value.startsWith('-')) return null; // reject "-0"
  return parsed;
}

function toBigInt(value: number | string | bigint): bigint | null {
  if (typeof value === 'bigint') return value;
  if (typeof value === 'string') return parseEconomyString(value);
  // Plain JSON-number counters must be losslessly representable.
  return Number.isSafeInteger(value) ? BigInt(value) : null;
}

function group(value: bigint): string {
  const negative = value < 0n;
  let digits = (negative ? -value : value).toString();
  let grouped = '';
  while (digits.length > 3) {
    grouped = `,${digits.slice(-3)}${grouped}`;
    digits = digits.slice(0, -3);
  }
  return negative ? `-${digits}${grouped}` : `${digits}${grouped}`;
}

// Divides numer/denom rounding half away from zero (e.g. 500/1000 -> 1,
// -500/1000 -> -1). denom must be positive; callers pass fixed constants.
function divRoundHalfAway(numer: bigint, denom: bigint): bigint {
  if (numer >= 0n) return (numer * 2n + denom) / (denom * 2n);
  return -((-numer * 2n + denom) / (denom * 2n));
}

/**
 * Formats a plain counter as a full grouped integer. Use this for counts that
 * stay readable in full (requests, records); token totals usually read better
 * with formatCompact.
 */
export function formatCount(value: number | string | bigint): FormattedNumber {
  const parsed = toBigInt(value);
  if (parsed === null) return unavailable();
  const display = group(parsed);
  return { display, exact: display, abbreviated: false };
}

function formatCompactValue(parsed: bigint, exact: string): FormattedNumber {
  const negative = parsed < 0n;
  const abs = negative ? -parsed : parsed;
  if (abs < COMPACT_THRESHOLD) {
    const display = group(parsed);
    return { display, exact, abbreviated: false };
  }
  let unit: { readonly suffix: string; readonly size: bigint } = COMPACT_UNITS[0];
  for (const candidate of COMPACT_UNITS) {
    if (abs >= candidate.size) unit = candidate;
  }
  const whole = abs / unit.size;
  const digitCount = whole.toString().length;
  let mantissa: string;
  if (digitCount === 1) {
    // One decimal keeps two significant figures (1,500,000 -> "1.5M").
    const tenths = divRoundHalfAway(abs * 10n, unit.size);
    const ones = tenths / 10n;
    const tenth = tenths % 10n;
    mantissa = tenth === 0n ? `${ones}` : `${ones}.${tenth}`;
  } else if (digitCount === 2) {
    // Already at most two significant figures (10,000 -> "10K").
    mantissa = `${whole}`;
  } else {
    // Round the integer part down to two significant figures.
    const factor = 10n ** BigInt(digitCount - 2);
    const rounded = divRoundHalfAway(whole, factor);
    if (rounded >= 100n) {
      // Rounding carried into the next magnitude (999,999 -> "1M"); restart
      // from that exact boundary so the suffix stays correct.
      return formatCompactValue((negative ? -1n : 1n) * 10n ** BigInt(digitCount) * unit.size, exact);
    }
    mantissa = group(rounded * factor);
  }
  return {
    display: `${negative ? '-' : ''}${mantissa}${unit.suffix}`,
    exact,
    abbreviated: true,
  };
}

/**
 * Formats a count or amount compactly: below 10,000 a full grouped integer,
 * otherwise a K/M/B/T abbreviation with 1-2 significant digits. The exact
 * value is always returned alongside for tooltip display.
 */
export function formatCompact(value: number | string | bigint): FormattedNumber {
  const parsed = toBigInt(value);
  if (parsed === null) return unavailable();
  return formatCompactValue(parsed, group(parsed));
}

/**
 * Formats an amount given in milli-credits. The display form is whole
 * display credits (milli-credits / 1000, rounded half away from zero); the
 * exact form keeps the full milli-credit figure for tooltips and logs.
 * Negative balances are supported and never render as "-0".
 */
export function formatCreditsFromMilli(milliCredits: number | string | bigint): FormattedNumber {
  const parsed = toBigInt(milliCredits);
  if (parsed === null) return unavailable();
  return {
    display: group(divRoundHalfAway(parsed, 1000n)),
    exact: group(parsed),
    abbreviated: false,
  };
}

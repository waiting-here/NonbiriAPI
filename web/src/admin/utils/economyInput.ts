// Display-credits <-> canonical milli-credit string conversion for the admin
// economy inputs. This module is deliberately dependency-free so it runs
// under the Node test runner alongside its unit tests. All arithmetic is
// BigInt: economy values never pass through JavaScript `number`.
//
// Wire truth: every economy amount is a canonical decimal milli-credit string
// (optional '-', digits only, no leading zeros, no '-0', within the int64
// range) — the same frozen canonical shape the shared A5 formatter enforces
// on the render side. Display credits are milli-credits / 1000, so a display
// input may carry at most three fraction digits.

const INT64_MIN = -(2n ** 63n);
const INT64_MAX = 2n ** 63n - 1n;

// One display credit is 1000 milli-credits.
const MILLI_PER_CREDIT = 1000n;

// Accepted display-credits input: optional '-', integer part without leading
// zeros, optional fraction of 1-3 digits (exactly thousandths).
const DISPLAY_INPUT = /^(-?)(0|[1-9][0-9]*)(?:\.([0-9]{1,3}))?$/;

// Canonical decimal wire shape, mirroring the frozen contract.
const CANONICAL_MILLI = /^-?(0|[1-9][0-9]*)$/;

/**
 * Converts a display-credits input into the canonical milli-credit wire
 * string, or null when the input is not a bounded canonical decimal (bad
 * shape, '-0', or outside the int64 range). The caller decides whether an
 * empty input means "field omitted".
 */
export function displayCreditsToMilliString(input: string): string | null {
  const match = DISPLAY_INPUT.exec(input.trim());
  if (!match) return null;
  const negative = match[1] === '-';
  const whole = BigInt(match[2]);
  const fraction = match[3] ?? '';
  const milli = whole * MILLI_PER_CREDIT + BigInt(fraction.padEnd(3, '0') || '0');
  const signed = negative ? -milli : milli;
  if (signed === 0n && negative) return null; // "-0" and "-0.000" are not canonical
  if (signed < INT64_MIN || signed > INT64_MAX) return null;
  return signed.toString();
}

/**
 * Converts a canonical milli-credit wire string into the exact display-credits
 * input form (up to three fraction digits, trailing zeros trimmed). A value
 * that is not canonical returns '' so the editor starts empty instead of
 * fabricating a number.
 */
export function milliStringToDisplayInput(milli: string): string {
  if (!CANONICAL_MILLI.test(milli)) return '';
  const parsed = BigInt(milli);
  if (parsed === 0n && milli.startsWith('-')) return '';
  const negative = parsed < 0n;
  const abs = negative ? -parsed : parsed;
  const whole = abs / MILLI_PER_CREDIT;
  const thousandths = (abs % MILLI_PER_CREDIT).toString().padStart(3, '0');
  const fraction = thousandths.replace(/0+$/, '');
  const body = fraction ? `${whole}.${fraction}` : `${whole}`;
  return negative && parsed !== 0n ? `-${body}` : body;
}

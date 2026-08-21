// Unit tests for the BigInt-safe number formatters, run with the Node.js
// built-in test runner (no extra dependencies):
//
//   node --test src/shared/utils/formatNumber.test.js
//
// The .ts module is loaded through Node's native TypeScript type stripping.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  formatCompact,
  formatCount,
  formatCreditsFromMilli,
  parseEconomyString,
} from './formatNumber.ts';

const INT64_MAX = '9223372036854775807';
const INT64_MIN = '-9223372036854775808';

test('parseEconomyString accepts only canonical decimal strings', () => {
  assert.equal(parseEconomyString('0'), 0n);
  assert.equal(parseEconomyString('7'), 7n);
  assert.equal(parseEconomyString('-12'), -12n);
  assert.equal(parseEconomyString(INT64_MAX), BigInt(INT64_MAX));
  assert.equal(parseEconomyString(INT64_MIN), BigInt(INT64_MIN));
});

test('parseEconomyString rejects non-canonical shapes', () => {
  for (const bad of ['', '-', '1.5', '1e5', '01', '+1', ' 1', '1 ', '-0', '١٢', '1,000']) {
    assert.equal(parseEconomyString(bad), null, `expected rejection: ${JSON.stringify(bad)}`);
  }
});

test('formatCount renders full grouped integers', () => {
  assert.deepEqual(formatCount(0), { display: '0', exact: '0', abbreviated: false });
  assert.equal(formatCount(9999).display, '9,999');
  assert.equal(formatCount(1234567).display, '1,234,567');
  assert.equal(formatCount(-1234567).display, '-1,234,567');
  assert.equal(formatCount(Number.MAX_SAFE_INTEGER).display, '9,007,199,254,740,991');
  assert.equal(formatCount(INT64_MAX).display, '9,223,372,036,854,775,807');
});

test('formatCount degrades gracefully on unsafe or malformed input', () => {
  const unavailable = { display: '—', exact: '—', abbreviated: false };
  assert.deepEqual(formatCount(Number.MAX_SAFE_INTEGER + 1), unavailable);
  assert.deepEqual(formatCount(1.5), unavailable);
  assert.deepEqual(formatCount(NaN), unavailable);
  assert.deepEqual(formatCount(Infinity), unavailable);
  assert.deepEqual(formatCount('12abc'), unavailable);
});

test('formatCompact keeps values below 10,000 unabbreviated', () => {
  assert.deepEqual(formatCompact(0), { display: '0', exact: '0', abbreviated: false });
  assert.deepEqual(formatCompact(9999), {
    display: '9,999',
    exact: '9,999',
    abbreviated: false,
  });
});

test('formatCompact crosses the 9,999/10,000 boundary without precision loss', () => {
  assert.deepEqual(formatCompact(10000), { display: '10K', exact: '10,000', abbreviated: true });
  assert.equal(formatCompact(10001).display, '10K');
  assert.equal(formatCompact(10500).display, '10K');
  assert.equal(formatCompact(99999).display, '99K');
});

test('formatCompact rounds to 1-2 significant digits across K/M/B/T', () => {
  assert.equal(formatCompact(15000).display, '15K');
  assert.equal(formatCompact(150000).display, '150K');
  // Rounding carries into the next magnitude instead of showing "1000K".
  assert.equal(formatCompact(999999).display, '1M');
  assert.equal(formatCompact(1000000).display, '1M');
  assert.equal(formatCompact(1045000).display, '1M');
  assert.equal(formatCompact(1234567).display, '1.2M');
  assert.equal(formatCompact(1500000).display, '1.5M');
  assert.equal(formatCompact(9499999).display, '9.5M');
  assert.equal(formatCompact(9994999).display, '10M');
  assert.equal(formatCompact(10000000).display, '10M');
  assert.equal(formatCompact(1000000000).display, '1B');
  assert.equal(formatCompact(12345678901).display, '12B');
  assert.equal(formatCompact(1000000000000).display, '1T');
});

test('formatCompact caps at T and never uses scientific notation', () => {
  const maxInt64 = formatCompact(INT64_MAX);
  assert.equal(maxInt64.display, '9,200,000T');
  assert.equal(maxInt64.exact, '9,223,372,036,854,775,807');

  const beyond = formatCompact(10n ** 21n);
  assert.match(beyond.display, /T$/);
  assert.doesNotMatch(beyond.display, /[eE+]/);
  assert.equal(beyond.exact, '1,000,000,000,000,000,000,000');
});

test('formatCompact handles negatives without producing "-0"', () => {
  assert.equal(formatCompact(-9999).display, '-9,999');
  assert.equal(formatCompact(-10000).display, '-10K');
  assert.equal(formatCompact(-1500000).display, '-1.5M');
  assert.equal(formatCompact(-0).display, '0');
  assert.equal(formatCompact(0n - 1n).display, '-1');
});

test('formatCompact accepts canonical strings and rejects malformed ones', () => {
  assert.deepEqual(formatCompact('10000'), {
    display: '10K',
    exact: '10,000',
    abbreviated: true,
  });
  assert.equal(formatCompact(INT64_MAX).abbreviated, true);
  const unavailable = { display: '—', exact: '—', abbreviated: false };
  assert.deepEqual(formatCompact('1e5'), unavailable);
  assert.deepEqual(formatCompact('-0'), unavailable);
  assert.deepEqual(formatCompact('007'), unavailable);
});

test('formatCreditsFromMilli rounds half away from zero at ±499/500', () => {
  assert.equal(formatCreditsFromMilli('0').display, '0');
  assert.equal(formatCreditsFromMilli('499').display, '0');
  assert.equal(formatCreditsFromMilli('500').display, '1');
  assert.equal(formatCreditsFromMilli('501').display, '1');
  assert.equal(formatCreditsFromMilli('1499').display, '1');
  assert.equal(formatCreditsFromMilli('1500').display, '2');
  // Negative half rounds away from zero, and never renders as "-0".
  assert.equal(formatCreditsFromMilli('-499').display, '0');
  assert.ok(!formatCreditsFromMilli('-499').display.startsWith('-'));
  assert.equal(formatCreditsFromMilli('-500').display, '-1');
  assert.equal(formatCreditsFromMilli('-1500').display, '-2');
});

test('formatCreditsFromMilli keeps the exact milli-credit value for tooltips', () => {
  assert.deepEqual(formatCreditsFromMilli('7500000'), {
    display: '7,500',
    exact: '7,500,000',
    abbreviated: false,
  });
  assert.equal(formatCreditsFromMilli(INT64_MAX).display, '9,223,372,036,854,776');
  assert.equal(formatCreditsFromMilli(INT64_MAX).exact, '9,223,372,036,854,775,807');
  assert.equal(formatCreditsFromMilli(INT64_MIN).display, '-9,223,372,036,854,776');
  assert.equal(formatCreditsFromMilli(INT64_MIN).exact, '-9,223,372,036,854,775,808');
});

test('formatters share one locale-independent grouping convention (zh/en)', () => {
  // zh-CN and en-US both use comma grouping; the output must be identical no
  // matter which UI language is active and must never contain exponent or
  // plus signs.
  for (const value of [0, 1, 999, 1000, 9999, 10000, 1234567, INT64_MAX]) {
    const compact = formatCompact(value);
    const count = formatCount(value);
    for (const formatted of [compact, count]) {
      assert.doesNotMatch(formatted.display, /[eE+]/);
      assert.doesNotMatch(formatted.exact, /[eE+]/);
      assert.ok(!formatted.display.includes('-0') || formatted.display === '0');
    }
    assert.equal(compact.exact, count.exact);
  }
});

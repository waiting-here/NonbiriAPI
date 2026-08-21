// Unit tests for the admin economy input conversions, run with the Node.js
// built-in test runner (no extra dependencies):
//
//   node --test src/admin/utils/economyInput.test.js
//
// The .ts module is loaded through Node's native TypeScript type stripping.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { displayCreditsToMilliString, milliStringToDisplayInput } from './economyInput.ts';

test('displayCreditsToMilliString converts whole and fractional display credits', () => {
  assert.equal(displayCreditsToMilliString('0'), '0');
  assert.equal(displayCreditsToMilliString('1'), '1000');
  assert.equal(displayCreditsToMilliString('40000'), '40000000');
  assert.equal(displayCreditsToMilliString('250000'), '250000000');
  assert.equal(displayCreditsToMilliString('0.001'), '1');
  assert.equal(displayCreditsToMilliString('0.5'), '500');
  assert.equal(displayCreditsToMilliString('1.5'), '1500');
  assert.equal(displayCreditsToMilliString('12.345'), '12345');
  assert.equal(displayCreditsToMilliString(' 7 '), '7000');
});

test('displayCreditsToMilliString keeps negatives explicit', () => {
  assert.equal(displayCreditsToMilliString('-1'), '-1000');
  assert.equal(displayCreditsToMilliString('-0.001'), '-1');
  assert.equal(displayCreditsToMilliString('-12.345'), '-12345');
  // "-0" in any form is rejected instead of silently dropping the sign.
  assert.equal(displayCreditsToMilliString('-0'), null);
  assert.equal(displayCreditsToMilliString('-0.000'), null);
});

test('displayCreditsToMilliString rejects non-canonical or out-of-range input', () => {
  for (const bad of [
    '',
    ' ',
    '-',
    '1.',
    '.5',
    '+1',
    '01',
    '1,000',
    '1e3',
    '0.0001',
    '1.0000',
    'abc',
    '9223372036854775.809',
    '-9223372036854775.809',
  ]) {
    assert.equal(
      displayCreditsToMilliString(bad),
      null,
      `expected rejection: ${JSON.stringify(bad)}`,
    );
  }
  // The int64 boundaries themselves stay representable.
  assert.equal(displayCreditsToMilliString('9223372036854775.807'), '9223372036854775807');
  assert.equal(displayCreditsToMilliString('-9223372036854775.808'), '-9223372036854775808');
});

test('milliStringToDisplayInput is the exact inverse of the wire form', () => {
  assert.equal(milliStringToDisplayInput('0'), '0');
  assert.equal(milliStringToDisplayInput('1000'), '1');
  assert.equal(milliStringToDisplayInput('1500'), '1.5');
  assert.equal(milliStringToDisplayInput('1'), '0.001');
  assert.equal(milliStringToDisplayInput('40000000'), '40000');
  assert.equal(milliStringToDisplayInput('250000000'), '250000');
  assert.equal(milliStringToDisplayInput('-12345'), '-12.345');
  assert.equal(milliStringToDisplayInput('-9223372036854775808'), '-9223372036854775.808');
});

test('milliStringToDisplayInput degrades to empty on non-canonical wire data', () => {
  for (const bad of ['', '-0', '01', '1e5', 'abc', '1.5', ' 1']) {
    assert.equal(milliStringToDisplayInput(bad), '', `expected empty: ${JSON.stringify(bad)}`);
  }
});

test('conversion round-trips every canonical milli value it accepts', () => {
  for (const milli of ['0', '1', '999', '1000', '1001', '12345', '40000000', '-7654321']) {
    const display = milliStringToDisplayInput(milli);
    assert.ok(display.length > 0);
    assert.equal(displayCreditsToMilliString(display), milli);
  }
});

import { describe, expect, it } from 'vitest';
import {
  parameterPresence,
  parametersForTrace,
  projectParameters,
  stableValueKey,
} from './parameterProjection';

describe('debug parameter projection', () => {
  it('preserves caller order and distinguishes absent/null/false/zero/empty', () => {
    const parameters = projectParameters({
      model: 'public/model',
      stream: false,
      max_tokens: 0,
      stop: null,
      user: '',
      tools: [{ type: 'function' }],
      vendor_extension: [],
    });
    expect(parameters.slice(0, 6).map(({ name }) => name)).toEqual([
      'model',
      'stream',
      'max_tokens',
      'stop',
      'user',
      'vendor_extension',
    ]);
    expect(parameters.slice(0, 6).map(({ presence }) => presence)).toEqual([
      'value',
      'false',
      'zero',
      'null',
      'empty_string',
      'empty_array',
    ]);
    expect(parameters.find(({ name }) => name === 'temperature')).toMatchObject({
      name: 'temperature',
      presence: 'absent',
    });
    expect(parameters.some(({ name }) => name === 'tools')).toBe(false);
  });

  it('keeps unknown fields and falls back to raw request when wire parameters are absent', () => {
    const parameters = parametersForTrace({
      raw_request: { model: 'm', custom_flag: false },
    });
    expect(parameters[0]).toMatchObject({ name: 'model', presence: 'value', value: 'm' });
    expect(parameters[1]).toMatchObject({ name: 'custom_flag', presence: 'false', value: false });
  });

  it('uses the explicit empty classifier for all empty JSON containers', () => {
    expect(parameterPresence('')).toBe('empty_string');
    expect(parameterPresence([])).toBe('empty_array');
    expect(parameterPresence({})).toBe('empty_object');
    expect(parameterPresence(0)).toBe('zero');
    expect(parameterPresence(null)).toBe('null');
    expect(parameterPresence(false)).toBe('false');
  });

  it('compares object values deterministically regardless of key order', () => {
    expect(stableValueKey({ b: 2, a: [true, null] })).toBe(
      stableValueKey({ a: [true, null], b: 2 }),
    );
    expect(stableValueKey({ b: 2, a: [true, null] })).not.toBe(
      stableValueKey({ a: [true, false], b: 2 }),
    );
  });
});

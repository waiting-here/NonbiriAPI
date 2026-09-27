import { describe, expect, it } from 'vitest';
import { conditionReferences, validateCondition } from './conditions';
import { validAmount } from './NodeEditor';
import type { Condition } from './api';

describe('Fat Fish period prerequisites and amounts', () => {
  const ids = new Set(['ffn_one', 'ffn_two']);
  it('collects graph edges from nested conditions', () => {
    const condition: Condition = { all: [{ passed: 'ffn_one' }, { any: [{ stars: { node: 'ffn_two', min: 2 } }, { passed_count: 1 }] }] };
    expect(conditionReferences(condition)).toEqual(['ffn_one', 'ffn_two']);
    expect(validateCondition(condition, ids)).toBeNull();
  });
  it('rejects stale references, unbounded depth and empty groups before save', () => {
    expect(validateCondition({ passed: 'ffn_deleted' }, ids)).toMatch(/unknown node/);
    expect(validateCondition({ all: [] }, ids)).toMatch(/at least one/);
    let nested = {};
    for (let index = 0; index < 9; index++) nested = { all: [nested] };
    expect(validateCondition(nested, ids)).toMatch(/depth 8/);
  });
  it('uses canonical milli-credit strings', () => {
    for (const amount of ['0', '1', '1.2', '0.001', '1.234']) expect(validAmount(amount)).toBe(true);
    for (const amount of ['01', '-1', '1.000', '1.2340', '1e3', '0.0', '']) expect(validAmount(amount)).toBe(false);
  });
});

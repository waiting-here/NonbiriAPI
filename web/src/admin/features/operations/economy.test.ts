import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  gamesConfigPatch,
  normalizeGamesConfig,
  normalizePool,
  thursdayMutationRevision,
  type Period,
} from './economy';

const fixture = JSON.parse(readFileSync(resolve(process.cwd(), '..', 'internal/game/runtime/testdata/contracts/games-config.json'), 'utf8')) as unknown;

describe('administrator game configuration wire', () => {
  it('accepts the canonical Go fixture and emits no read-only queue capacity', () => {
    const config = normalizeGamesConfig(fixture);
    expect(config.rps.modes.quick.pumps_bp.thursday).toBe(100);
    const patch = gamesConfigPatch(config) as { rps: { modes: Record<string, Record<string, unknown>> } };
    expect(patch.rps.modes.quick).not.toHaveProperty('queue_capacity');
    expect(patch.rps.modes.quick.pumps_bp).toEqual({ platform: 100, welfare: 100, thursday: 100 });
  });

  it('rejects the Thursday-only next_pool field on RPS', () => {
    const hostile = structuredClone(fixture) as { rps: { modes: { quick: { pumps_bp: Record<string, unknown> } } } };
    hostile.rps.modes.quick.pumps_bp = { platform: 100, welfare: 100, next_pool: 100 };
    expect(() => normalizeGamesConfig(hostile)).toThrow(/pool split/i);
  });
});

describe('Thursday mutation revision selection', () => {
  const period = (state: Period['state'], revision: string): Period => ({ state, revision } as Period);

  it('uses only a configured period revision', () => {
    expect(thursdayMutationRevision({ revision: '7' } as never, period('configured', '3'))).toBe('3');
  });

  it('uses activities authority for configuration_error or no period', () => {
    expect(thursdayMutationRevision({ revision: '7' } as never, period('configuration_error', '3'))).toBe('7');
    expect(thursdayMutationRevision({ revision: '7' } as never, null)).toBe('7');
    expect(thursdayMutationRevision(undefined, null)).toBeNull();
  });
});

describe('shared pool wire', () => {
  const openPool = {
    id: `pol_${'A'.repeat(22)}`,
    pool_type: 'thursday',
    period_id: null,
    state: 'open',
    revision: '1',
    balance: '0',
    created_at: 1,
    closed_at: null,
  };

  it('accepts the unbound open Thursday pool reserved for the next period', () => {
    expect(normalizePool(openPool)).toEqual(openPool);
    expect(normalizePool({
      ...openPool,
      period_id: `thu_${'B'.repeat(21)}A`,
    }).period_id).toBe(`thu_${'B'.repeat(21)}A`);
  });

  it('keeps welfare pools unbound and open pools without a close time', () => {
    expect(normalizePool({ ...openPool, pool_type: 'welfare' }).period_id).toBeNull();
    expect(() => normalizePool({
      ...openPool,
      pool_type: 'welfare',
      period_id: `thu_${'B'.repeat(21)}A`,
    })).toThrow(/pool state/i);
    expect(() => normalizePool({ ...openPool, closed_at: 2 })).toThrow(/pool state/i);
  });
});

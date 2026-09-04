import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { gamesConfigPatch, normalizeGamesConfig, thursdayMutationRevision, type Period } from './economy';

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

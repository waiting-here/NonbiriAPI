import fs from 'node:fs';
import { createHash } from 'node:crypto';
import { describe, expect, it } from 'vitest';
import { contentHash, parseLevel } from '../../src/shared/fatfish/engine/canonical';
import { replay } from '../../src/shared/fatfish/engine/engine';
import { containsPolygon, polygonIntersectionArea } from '../../src/shared/fatfish/engine/geometry';
import { fishFootprint } from '../../src/shared/fatfish/engine/trig_helpers';
import type { EngineState, InputTuple } from '../../src/shared/fatfish/engine/types';

interface ExampleEntry {
  id: string;
  title: string;
  url: string;
  content_hash: string;
  raw_sha256: string;
  bytes: number;
  source_example: string;
  source_sha256: string;
  adaptation: string;
  coverage: string[];
}

const directory = new URL('../../public/examples/fatfish/', import.meta.url);
const manifest = JSON.parse(fs.readFileSync(new URL('manifest.json', directory), 'utf8')) as {
  format: string;
  version: number;
  license: string;
  examples: ExampleEntry[];
};
const seed = Uint8Array.from({ length: 32 }, (_, index) => index);
const cases: [string, InputTuple[], number, number][] = [
  ['01-first-rice', [[0, 0, 'return', 100]], 8, 308],
  ['02-buffer-pool', [], 10, 1782],
  ['03-narrow-bridge', [], 9, 245],
  ['04-two-turns', [[0, 0, 'return', 100]], 12, 387],
  ['05-last-barrier', [[0, 0, 'return', 100]], 12, 237],
  ['06-power-button', [], 8, 233],
  ['07-one-way-stream', [], 10, 403],
  ['08-rice-buffet', [[0, 0, 'return', 100]], 10, 218],
];

describe('importable example levels', () => {
  it('lists exactly eight pure level imports with distinct canonical hashes and useful attribution', () => {
    expect(manifest.format).toBe('nonbiri-fatfish-examples');
    expect(manifest.version).toBe(1);
    expect(manifest.license).toBe('AGPL-3.0');
    expect(manifest.examples.map((entry) => entry.id)).toEqual(cases.map(([id]) => id));
    expect(new Set(manifest.examples.map((entry) => entry.content_hash)).size).toBe(8);
    expect(fs.readdirSync(directory).filter((name) => name.endsWith('.fatfish.json')).sort()).toEqual(
      cases.map(([id]) => `${id}.fatfish.json`),
    );
    for (const entry of manifest.examples) {
      expect(entry.title.length).toBeGreaterThan(0);
      expect(entry.url).toBe(`/examples/fatfish/${entry.id}.fatfish.json`);
      expect(entry.source_example.length).toBeGreaterThan(0);
      expect(entry.source_sha256).toMatch(/^[0-9a-f]{64}$/);
      expect(entry.adaptation.length).toBeGreaterThan(0);
      expect(entry.coverage.length).toBeGreaterThan(0);
    }
  });

  for (const [id, inputs, expectedFed, expectedTick] of cases) {
    it(id, () => {
      const entry = manifest.examples.find((item) => item.id === id);
      expect(entry).toBeDefined();
      const data = fs.readFileSync(new URL(`${id}.fatfish.json`, directory));
      expect(data.length).toBe(entry!.bytes);
      expect(createHash('sha256').update(data).digest('hex')).toBe(entry!.raw_sha256);
      const level = parseLevel(data.toString('utf8'));
      expect(contentHash(level)).toBe(entry!.content_hash);
      if (id === '03-narrow-bridge') {
        const partialFish = level.fish[0];
        expect(polygonIntersectionArea(fishFootprint(partialFish.x, partialFish.y), level.solids[0].polygon).sign()).toBeGreaterThan(0);
        const islandFish = level.fish[1];
        expect(containsPolygon(level.hazards[0].polygon, { x: islandFish.x, y: islandFish.y })).toBe(false);
      }
      const firstTurns = new Map<number, number>();
      let escapedPartialCover = false;
      let safeIslandLoss = false;
      let gateOpened = false;
      const flows = new Set<number>();
      const observe = (state: EngineState) => {
        if (id === '02-buffer-pool') for (const fish of state.fish.filter((item) => item.id <= 2)) {
          if (fish.turn_dir !== 0 && !firstTurns.has(fish.id)) firstTurns.set(fish.id, fish.turn_dir);
        }
        if (id === '03-narrow-bridge') {
          escapedPartialCover ||= state.fish[0].x < 110 * 64;
          safeIslandLoss ||= state.fish[1].status === 'lost';
        }
        if (id === '06-power-button') gateOpened ||= state.gates[0].open;
        if (id === '07-one-way-stream') for (const fish of state.fish) if (fish.flow_id > 0) flows.add(fish.flow_id);
      };
      const replaySeed = Uint8Array.from(seed);
      if (id === '02-buffer-pool') replaySeed[0] = 3;
      const result = replay(level, replaySeed, inputs, { onTick: observe });
      expect(result.content_hash).toBe(entry!.content_hash);
      expect(result.passed).toBe(true);
      expect(result.fed).toBe(expectedFed);
      expect(result.terminal_tick).toBe(expectedTick);
      expect(result.reason).toBe('all_resolved');
      if (id === '02-buffer-pool') {
        expect(firstTurns.size).toBe(2);
        expect(firstTurns.get(1)).not.toBe(firstTurns.get(2));
      }
      if (id === '03-narrow-bridge') {
        expect(escapedPartialCover).toBe(true);
        expect(safeIslandLoss).toBe(true);
        expect(level.hazards[0].polygon.holes).toHaveLength(1);
      }
      if (id === '06-power-button') expect(gateOpened).toBe(true);
      if (id === '07-one-way-stream') expect([...flows].sort()).toEqual([700, 701]);
      if (id === '08-rice-buffet') expect(result.bowl_counts).toEqual([{ id: 400, count: 4 }, { id: 401, count: 6 }]);
    });
  }
});

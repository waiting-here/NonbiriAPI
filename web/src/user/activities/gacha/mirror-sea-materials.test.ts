import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { materialPointWeight, materialPose, type MaterialPlate } from './romance/material-plates';

const manifest = JSON.parse(
  readFileSync(
    resolve('public/assets/gacha/motion/n-gemini/material-layers/manifest.json'),
    'utf8',
  ),
) as { cycle: number; layers: MaterialPlate[] };
const named = (name: string) => manifest.layers.find((plate) => plate.name === name)!;

describe('mirror-sea free cloth', () => {
  it('moves the visible hems while keeping real attachment cuts pinned', () => {
    for (const [name, free, seam] of [
      ['sleeve-silk', [280, 890], [245, 417]],
      ['train-left', [125, 1370], [420, 1240]],
      ['train-right', [970, 1230], [760, 835]],
    ] as const) {
      const plate = named(name);
      expect(materialPointWeight(plate, free)).toBeGreaterThan(0.8);
      expect(materialPointWeight(plate, seam)).toBeLessThan(0.01);
      const rest = materialPose(plate, 0, manifest.cycle).offset;
      const peak = materialPose(plate, 7, manifest.cycle).offset;
      expect(Math.hypot(peak[0] - rest[0], peak[1] - rest[1])).toBeGreaterThan(25);
      expect(rest[1]).toBeGreaterThan(7);
    }
  });

  it('binds the hanging crystals to their actual cloth and ribbon attachment', () => {
    expect(named('head-crystal-far').parent).toEqual({ name: 'head-ribbon-lower', at: [708, 303] });
    expect(named('sleeve-crystal-upper').parent).toEqual({ name: 'sleeve-silk', at: [177, 531] });
    expect(named('sleeve-crystal-lower').parent).toEqual({ name: 'sleeve-silk', at: [191, 644] });
    for (const name of ['head-crystal-far', 'sleeve-crystal-upper', 'sleeve-crystal-lower']) {
      const plate = named(name);
      expect(plate.rigid).toBe(true);
      expect(plate.pivot).toEqual(typeof plate.parent === 'object' ? plate.parent.at : undefined);
      expect(plate.vector).toEqual([0, 0]);
    }
  });

  it('keeps the cloth mesh orientation through the complete breeze and return', () => {
    for (const name of ['sleeve-silk', 'train-left', 'train-right']) {
      const plate = named(name);
      const [x, y, width, height] = plate.crop;
      const columns = Math.ceil(width / 12),
        rows = Math.ceil(height / 12);
      const project = (col: number, row: number, offset: readonly number[]) => {
        const point = [x + (col * width) / columns, y + (row * height) / rows] as const;
        const weight = materialPointWeight(plate, point);
        return [point[0] + offset[0] * weight, point[1] + offset[1] * weight];
      };
      let minimum = Infinity;
      for (let time = 0; time <= manifest.cycle; time += 0.5) {
        const { offset } = materialPose(plate, time, manifest.cycle);
        for (let row = 0; row < rows; row++)
          for (let col = 0; col < columns; col++) {
            const a = project(col, row, offset),
              b = project(col + 1, row, offset);
            const c = project(col, row + 1, offset),
              d = project(col + 1, row + 1, offset);
            for (const [p, q, r] of [
              [a, b, c],
              [c, b, d],
            ]) {
              const area = (q[0] - p[0]) * (r[1] - p[1]) - (q[1] - p[1]) * (r[0] - p[0]);
              minimum = Math.min(minimum, area / (((width / columns) * height) / rows));
            }
          }
      }
      expect(minimum, name).toBeGreaterThan(0.2);
    }
  });
});

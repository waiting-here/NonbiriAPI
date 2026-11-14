import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, it } from 'vitest';
import { materialPointWeight, materialPose, type MaterialPlate } from '../romance/material-plates';

const manifest = JSON.parse(
  readFileSync(
    resolve('public/assets/gacha/motion/r-chatgpt/material-layers/manifest.json'),
    'utf8',
  ),
) as { cycle: number; layers: MaterialPlate[] };
const plate = (name: string) => manifest.layers.find((layer) => layer.name === name)!;

it('moves the actual free garment contours while keeping their sewn cuts fixed', () => {
  const contours: [string, [number, number], number][] = [
    ['coat-left-tail', [251, 849], 0.7],
    ['coat-right-tail', [867, 998], 0.95],
    ['skirt-front-hem', [500, 800], 0.6],
  ];
  for (const [name, point, minimum] of contours) {
    const layer = plate(name);
    expect(materialPointWeight(layer, point), name).toBeGreaterThan(minimum);
    for (const seam of layer.seams!)
      for (const anchor of seam)
        expect(materialPointWeight(layer, anchor), name + ' attachment').toBe(0);
  }
});

it('keeps skirt coverage and paired cord attachments throughout the breeze', () => {
  const skirt = plate('skirt-front-hem');
  for (let seconds = 0; seconds <= 48; seconds += 0.125) {
    const pose = materialPose(skirt, seconds, manifest.cycle);
    expect(pose.offset[1]).toBeGreaterThanOrEqual(0);
    expect(pose.offset[1]).toBeLessThanOrEqual(12);
  }
  for (const name of ['left-short', 'left-long', 'right']) {
    const cord = plate(name + '-cord');
    const cap = plate(name + '-metal-cap');
    for (const key of ['pivot', 'rotation', 'delay', 'parent'] as const)
      expect(cap[key]).toEqual(cord[key]);
  }
  expect(plate('right-cord').parent).toEqual({
    name: 'coat-right-tail',
    at: [878, 805],
  });
});

it('keeps every cloth triangle unfolded across gust, release and cycle boundaries', () => {
  for (const layer of manifest.layers.filter((part) => part.material.includes('cloth'))) {
    const [x, y, width, height] = layer.crop;
    const columns = Math.ceil(width / 12);
    const rows = Math.ceil(height / 12);
    const dx = width / columns;
    const dy = height / rows;
    let minimum = Infinity;
    for (let seconds = 0; seconds <= 48; seconds += 0.5) {
      const { offset } = materialPose(layer, seconds, manifest.cycle);
      const point = (column: number, row: number) => {
        const px = x + column * dx;
        const py = y + row * dy;
        const weight = materialPointWeight(layer, [px, py]);
        return [px + offset[0] * weight, py + offset[1] * weight];
      };
      for (let row = 0; row < rows; row++)
        for (let column = 0; column < columns; column++) {
          const a = point(column, row);
          const b = point(column + 1, row);
          const c = point(column, row + 1);
          const d = point(column + 1, row + 1);
          for (const [p, q, r] of [
            [a, b, c],
            [c, b, d],
          ])
            minimum = Math.min(
              minimum,
              ((q[0] - p[0]) * (r[1] - p[1]) - (q[1] - p[1]) * (r[0] - p[0])) / (dx * dy),
            );
        }
    }
    expect(minimum, layer.name).toBeGreaterThan(0.3);
  }
});

import { polygonDistance, regionWeight } from '../sweet-hide-and-seek/motion';
import type { MotionRegion, Point } from '../sweet-hide-and-seek/motion';
import { christmasRigs } from './rigs';

export type { Point };
export type ChristmasCard = 't-chatgpt' | 't-claude' | 't-glm' | 't-deepseek' | 't-gemini';
export const christmasCards = ['t-chatgpt', 't-claude', 't-glm', 't-deepseek', 't-gemini'] as const;
export type RibSection = readonly [
  y: number,
  left: number,
  right: number,
  lift: number,
  expansion: number,
  follow: number,
];
export type ChristmasRig = {
  ribs: readonly RibSection[];
  feather: number;
  materials: readonly MotionRegion[];
  fixed: readonly (readonly Point[])[];
  floating?: { polygon: readonly Point[]; amplitude: Point };
};
export const meshStep = 8;
export const meshColumns = 1024 / meshStep + 1;
export const vertexStride = 14;
export type MotionMesh = { vertices: Float32Array; indices: Uint16Array };

const smooth = (a: number, b: number, x: number) => {
  const t = Math.max(0, Math.min(1, (x - a) / (b - a)));
  return t * t * (3 - 2 * t);
};

function ribsAt(rig: ChristmasRig, [x, y]: Point): readonly [number, number, number] {
  for (let i = 1; i < rig.ribs.length; i++) {
    const a = rig.ribs[i - 1],
      b = rig.ribs[i];
    if (y < a[0] || y > b[0]) continue;
    const t = smooth(a[0], b[0], y);
    const left = a[1] + (b[1] - a[1]) * t;
    const right = a[2] + (b[2] - a[2]) * t;
    const weight = smooth(0, rig.feather, Math.min(x - left, right - x));
    return [
      (x - (left + right) / 2) * (a[4] + (b[4] - a[4]) * t) * weight,
      -(a[3] + (b[3] - a[3]) * t) * weight,
      a[5] + (b[5] - a[5]) * t,
    ];
  }
  return [0, 0, 0];
}

export function createMotionMesh(id: ChristmasCard): MotionMesh {
  const rig = christmasRigs[id];
  const rows = 1536 / meshStep + 1;
  const vertices = new Float32Array(meshColumns * rows * vertexStride);
  const indices = new Uint16Array((meshColumns - 1) * (rows - 1) * 6);
  for (let row = 0; row < rows; row++) {
    for (let column = 0; column < meshColumns; column++) {
      const point: Point = [column * meshStep, row * meshStep];
      const index = (row * meshColumns + column) * vertexStride;
      vertices[index] = point[0];
      vertices[index + 1] = point[1];
      let protection = 1,
        bodyProtection = 1;
      for (const polygon of rig.fixed) {
        // Include a complete triangle beyond each contact outline.
        const distance = -polygonDistance(polygon, point);
        protection = Math.min(protection, smooth(12, 52, distance));
        bodyProtection = Math.min(bodyProtection, smooth(12, 36, distance));
      }
      if (protection === 0) continue;
      let totalWeight = 0;
      for (const material of rig.materials) {
        const weight = regionWeight(material, point) * protection;
        if (!weight) continue;
        const dx = material.tip[0] - material.root[0];
        const dy = material.tip[1] - material.root[1];
        const progress = Math.max(
          0,
          Math.min(
            1,
            ((point[0] - material.root[0]) * dx + (point[1] - material.root[1]) * dy) /
              (dx * dx + dy * dy),
          ),
        );
        const lag = progress * 0.95;
        const phase = material.phase - lag;
        const secondary = material.phase * 0.6 - lag * 1.35;
        totalWeight += weight;
        vertices[index + 2] += material.amplitude[0] * weight * 0.82 * Math.cos(phase);
        vertices[index + 3] += material.amplitude[1] * weight * 0.82 * Math.cos(phase + 0.4);
        vertices[index + 4] += material.amplitude[0] * weight * 0.82 * Math.sin(phase);
        vertices[index + 5] += material.amplitude[1] * weight * 0.82 * Math.sin(phase + 0.4);
        vertices[index + 6] += material.amplitude[0] * weight * 0.18 * Math.cos(secondary);
        vertices[index + 7] += material.amplitude[1] * weight * 0.18 * Math.cos(secondary + 0.4);
        vertices[index + 8] += material.amplitude[0] * weight * 0.18 * Math.sin(secondary);
        vertices[index + 9] += material.amplitude[1] * weight * 0.18 * Math.sin(secondary + 0.4);
      }
      if (totalWeight > 1)
        for (let channel = 2; channel < 10; channel++) vertices[index + channel] /= totalWeight;
      const ribs = ribsAt(rig, point);
      vertices[index + 10] = ribs[0] * bodyProtection;
      vertices[index + 11] = ribs[1] * bodyProtection;
      vertices[index + 12] = vertices[index + 10] * ribs[2];
      vertices[index + 13] = vertices[index + 11] * ribs[2];
      if (rig.floating) {
        const weight = smooth(0, 20, polygonDistance(rig.floating.polygon, point));
        // An isolated companion translates rigidly inside its transparent margin.
        vertices[index + 2] += rig.floating.amplitude[0] * weight;
        vertices[index + 5] += rig.floating.amplitude[1] * weight;
      }
    }
  }
  let offset = 0;
  for (let row = 0; row < rows - 1; row++) {
    for (let column = 0; column < meshColumns - 1; column++) {
      const a = row * meshColumns + column,
        b = a + 1,
        c = a + meshColumns,
        d = c + 1;
      indices.set([a, b, c, c, b, d], offset);
      offset += 6;
    }
  }
  return { vertices, indices };
}

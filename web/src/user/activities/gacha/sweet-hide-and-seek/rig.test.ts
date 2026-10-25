import { expect, it } from 'vitest';
import {
  createMotionMesh,
  fixedRegions,
  meshColumns,
  meshStep,
  motionDrivers,
  polygonDistance,
  vertexStride,
} from './motion';
import type { MotionMesh, Point, SweetCard } from './motion';

const meshes = {
  's-chatgpt': createMotionMesh('s-chatgpt'),
  's-claude': createMotionMesh('s-claude'),
};
const ids = ['s-chatgpt', 's-claude'] as const;

// Interpolate the same two triangles used by GL, including vertices across pins.
function coefficients(mesh: MotionMesh, [x, y]: Point): number[] {
  const column = Math.min(meshColumns - 2, Math.floor(x / meshStep));
  const row = Math.min(1536 / meshStep - 1, Math.floor(y / meshStep));
  const u = x / meshStep - column,
    v = y / meshStep - row;
  const a = row * meshColumns + column,
    b = a + 1,
    c = a + meshColumns,
    d = c + 1;
  const triangle = u + v <= 1 ? [a, b, c] : [c, b, d];
  const weights = u + v <= 1 ? [1 - u - v, u, v] : [1 - u, 1 - v, u + v - 1];
  return Array.from({ length: vertexStride - 2 }, (_, channel) =>
    triangle.reduce(
      (sum, vertex, i) => sum + mesh.vertices[vertex * vertexStride + 2 + channel] * weights[i],
      0,
    ),
  );
}
function displacement(values: readonly number[], drivers: Float32Array): Point {
  let x = 0,
    y = 0;
  for (let i = 0; i < drivers.length; i++) {
    x += values[i * 2] * drivers[i];
    y += values[i * 2 + 1] * drivers[i];
  }
  return [x, y];
}

it.each(ids)(
  '%s preserves interpolated contact interiors and boundaries through every driver',
  (id) => {
    const mesh = meshes[id];
    let worst = 0;
    for (const polygon of fixedRegions[id]) {
      for (let i = 0; i < polygon.length; i++) {
        const a = polygon[i],
          b = polygon[(i + 1) % polygon.length];
        const length = Math.hypot(b[0] - a[0], b[1] - a[1]);
        for (let step = 0; step <= length; step += 3) {
          const x = a[0] + ((b[0] - a[0]) * step) / length;
          const y = a[1] + ((b[1] - a[1]) * step) / length;
          if (x >= 0 && x <= 1024 && y >= 0 && y <= 1536)
            worst = Math.max(worst, ...coefficients(mesh, [x, y]).map(Math.abs));
        }
      }
      for (let y = 0; y <= 1536; y += 13)
        for (let x = 0; x <= 1024; x += 13)
          if (polygonDistance(polygon, [x, y]) >= 0)
            worst = Math.max(worst, ...coefficients(mesh, [x, y]).map(Math.abs));
    }
    expect(worst).toBe(0);
  },
);

it('pins source face, candy, fingers, supporting knees and feet independently of rig outlines', () => {
  const contacts: Record<SweetCard, Point[]> = {
    's-chatgpt': [
      [408, 349],
      [542, 290],
      [485, 429],
      [535, 461],
      [824, 440],
      [770, 554],
      [783, 665],
      [768, 817],
      [204, 1021],
      [185, 1080],
      [417, 1446],
      [576, 1471],
      [510, 950],
    ],
    's-claude': [
      [552, 166],
      [608, 143],
      [600, 195],
      [674, 211],
      [697, 260],
      [657, 313],
      [607, 711],
      [577, 783],
      [571, 818],
      [313, 1417],
      [436, 1490],
      [500, 545],
    ],
  };
  for (const id of ids)
    for (const point of contacts[id])
      expect(coefficients(meshes[id], point), id + ' ' + point.join(',')).toEqual(
        Array(vertexStride - 2).fill(0),
      );
});

it('binds the complete ribcage to one lift and expansion with a fixed waist', () => {
  const left = coefficients(meshes['s-chatgpt'], [440, 704]);
  const center = coefficients(meshes['s-chatgpt'], [540, 704]);
  const right = coefficients(meshes['s-chatgpt'], [636, 704]);
  expect(left[9]).toBeCloseTo(center[9], 6);
  expect(right[9]).toBeCloseTo(center[9], 6);
  expect(right[8] - left[8]).toBeCloseTo((636 - 440) * 0.018, 5);
  expect(center[9]).toBeLessThan(-12);
  expect(coefficients(meshes['s-chatgpt'], [510, 952])[9]).toBe(0);
  const shoulder = coefficients(meshes['s-claude'], [562, 296])[9];
  const ribs = coefficients(meshes['s-claude'], [545, 440])[9];
  expect(shoulder).toBeLessThan(0);
  expect(Math.abs(ribs)).toBeGreaterThan(Math.abs(shoulder) * 2);
});

it('lets the lower chest follow the same breath with a slower, bounded return', () => {
  const upper = coefficients(meshes['s-chatgpt'], [540, 560]);
  const lower = coefficients(meshes['s-chatgpt'], [540, 800]);
  expect(upper[11]).toBe(0);
  expect(lower[11] / lower[9]).toBeCloseTo(0.65, 5);
  const drivers = new Float32Array(6);
  motionDrivers(1.2, drivers);
  expect(drivers[5]).toBeLessThan(0);
  const inhale = displacement(lower, drivers)[1];
  expect(inhale).toBeGreaterThan(lower[9] * drivers[4]);
  motionDrivers(4.2, drivers);
  expect(drivers[5]).toBeGreaterThan(0);
  const exhale = displacement(lower, drivers)[1];
  expect(exhale).toBeLessThan(lower[9] * drivers[4]);
  for (let time = 0; time <= 6.8; time += 0.04) {
    motionDrivers(time, drivers);
    const lift = displacement(lower, drivers)[1];
    expect(lift).toBeGreaterThanOrEqual(lower[9] - 0.00001);
    expect(lift).toBeLessThanOrEqual(0.00001);
  }
});

it.each(ids)(
  '%s stays finite and preserves triangle orientation across combined motion phases',
  (id) => {
    const { vertices, indices } = meshes[id];
    const positions = new Float32Array((vertices.length / vertexStride) * 2);
    const drivers = new Float32Array(6);
    let smallestArea = Infinity,
      largestArea = 0,
      largestStretch = 0;
    // The joint period is 163.2 s; include respiratory extrema as well as its beat with wind.
    const times = [2.584, 2.864, 6.8, 7.08, ...Array.from({ length: 103 }, (_, i) => i * 1.6)];
    for (const time of times) {
      motionDrivers(time, drivers);
      for (let i = 0; i < vertices.length / vertexStride; i++) {
        const base = i * vertexStride;
        let x = vertices[base],
          y = vertices[base + 1];
        for (let driver = 0; driver < drivers.length; driver++) {
          x += vertices[base + 2 + driver * 2] * drivers[driver];
          y += vertices[base + 3 + driver * 2] * drivers[driver];
        }
        positions[i * 2] = x;
        positions[i * 2 + 1] = y;
      }
      for (let i = 0; i < indices.length; i += 3) {
        const a = indices[i] * 2,
          b = indices[i + 1] * 2,
          c = indices[i + 2] * 2;
        const bx = positions[b] - positions[a],
          by = positions[b + 1] - positions[a + 1];
        const cx = positions[c] - positions[a],
          cy = positions[c + 1] - positions[a + 1];
        const area = (bx * cy - by * cx) / (meshStep * meshStep);
        smallestArea = Math.min(smallestArea, area);
        largestArea = Math.max(largestArea, area);
        largestStretch = Math.max(largestStretch, Math.hypot(bx, by) / meshStep);
      }
    }
    expect(Number.isFinite(smallestArea + largestArea + largestStretch)).toBe(true);
    expect(smallestArea).toBeGreaterThan(0.4);
    expect(largestArea).toBeLessThan(1.7);
    // One edge of the second triangle is diagonal.
    expect(largestStretch).toBeLessThan(2.05);
  },
);

it('retains a useful displacement budget on real hair and free hem landmarks', () => {
  // Engineering amplitude checks only: normal-speed artistic review is separate.
  const landmarks: [SweetCard, string, Point, number][] = [
    ['s-chatgpt', 'left hair', [91, 1002], 14],
    ['s-chatgpt', 'right hair', [955, 958], 18],
    ['s-chatgpt', 'left sleeve', [238, 874], 5],
    ['s-chatgpt', 'right sleeve', [902, 1062], 12],
    ['s-claude', 'back hair', [283, 603], 18],
    ['s-claude', 'outer curls', [210, 493], 8],
    ['s-claude', 'left free hem', [222, 1211], 14],
    ['s-claude', 'right free hem', [591, 1232], 12],
  ];
  const drivers = new Float32Array(6);
  for (const [id, name, point, minimum] of landmarks) {
    const values = coefficients(meshes[id], point);
    let min = Infinity,
      max = -Infinity;
    for (let t = 0; t <= 9.6; t += 0.1) {
      motionDrivers(t, drivers);
      const [x] = displacement(values, drivers);
      min = Math.min(min, x);
      max = Math.max(max, x);
    }
    expect(max - min, id + ' ' + name).toBeGreaterThan(minimum);
    expect(max - min).toBeLessThan(48);
  }
});

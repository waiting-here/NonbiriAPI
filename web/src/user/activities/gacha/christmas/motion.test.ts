import { expect, it } from 'vitest';
import { motionDrivers, polygonDistance } from '../sweet-hide-and-seek/motion';
import { christmasRigs } from './rigs';
import { christmasCards, createMotionMesh, meshColumns, meshStep, vertexStride } from './motion';
import type { ChristmasCard, MotionMesh, Point } from './motion';
import { renderLayers } from './layers';

const meshes = new Map<ChristmasCard, MotionMesh>();
function meshFor(id: ChristmasCard) {
  let mesh = meshes.get(id);
  if (!mesh) {
    mesh = createMotionMesh(id);
    meshes.set(id, mesh);
  }
  return mesh;
}

// Inspect interpolated triangles, including vertices across the pinned boundary.
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
const inset = ([x, y]: Point): Point => [51.2 + 0.9 * x, 76.8 + 0.9 * y];

it.each(christmasCards)('%s pins complete contact regions and interpolated outlines', (id) => {
  const mesh = meshFor(id);
  let worst = 0;
  for (const polygon of christmasRigs[id].fixed) {
    for (let i = 0; i < polygon.length; i++) {
      const a = polygon[i],
        b = polygon[(i + 1) % polygon.length];
      const length = Math.hypot(b[0] - a[0], b[1] - a[1]);
      for (let d = 0; d <= length; d += 4) {
        const x = a[0] + ((b[0] - a[0]) * d) / length;
        const y = a[1] + ((b[1] - a[1]) * d) / length;
        if (x >= 0 && x <= 1024 && y >= 0 && y <= 1536)
          worst = Math.max(worst, ...coefficients(mesh, [x, y]).map(Math.abs));
      }
    }
    for (let y = 0; y <= 1536; y += 19)
      for (let x = 0; x <= 1024; x += 19)
        if (polygonDistance(polygon, [x, y]) >= 0)
          worst = Math.max(worst, ...coefficients(mesh, [x, y]).map(Math.abs));
  }
  expect(worst).toBe(0);
});

it('anchors the drawn faces, hands, hard details and supports in each composition', () => {
  const contacts: [ChristmasCard, Point[]][] = [
    [
      't-chatgpt',
      [
        [492, 178],
        [582, 164],
        [540, 218],
        [575, 326],
        [240, 490],
        [310, 482],
        [725, 579],
        [760, 581],
        [728, 520],
        [604, 572],
        [425, 1515],
        [600, 1518],
        [385, 1490],
        [620, 1495],
      ],
    ],
    [
      't-claude',
      [
        [434, 201],
        [519, 179],
        [540, 325],
        [628, 543],
        [589, 641],
        [610, 493],
        [544, 728],
        [113, 666],
        [188, 700],
        [136, 1006],
        [505, 1168],
        [309, 1370],
        [819, 1350],
        [599, 1480],
        [716, 1480],
      ],
    ],
    [
      't-glm',
      [
        [491, 177],
        [562, 160],
        [581, 294],
        [415, 550],
        [492, 579],
        [535, 595],
        [673, 603],
        [707, 575],
        [612, 554],
        [550, 520],
        [465, 1480],
        [594, 1470],
      ],
    ],
    [
      't-gemini',
      [
        [457, 194],
        [527, 161],
        [589, 317],
        [270, 239],
        [315, 342],
        [696, 710],
        [517, 565],
        [445, 1495],
        [690, 1490],
      ],
    ],
  ];
  contacts.push([
    't-deepseek',
    [
      [510, 201],
      [580, 178],
      [557, 236],
      [550, 289],
      [450, 344],
      [407, 513],
      [762, 493],
      [781, 581],
      [534, 565],
      [605, 626],
      [378, 1491],
      [631, 1491],
    ],
  ]);
  for (const [id, points] of contacts)
    for (const point of points)
      expect(
        coefficients(meshFor(id), id === 't-claude' ? point : inset(point)),
        id + ' ' + point.join(','),
      ).toEqual(Array(vertexStride - 2).fill(0));
});

it('translates the isolated cloud as one rigid companion', () => {
  const points: Point[] = [
    [222, 217],
    [263, 228],
    [309, 223],
  ];
  const values = points.map((point) => coefficients(meshFor('t-chatgpt'), inset(point)));
  for (const value of values.slice(1))
    value.forEach((channel, i) => expect(channel).toBeCloseTo(values[0][i], 6));
  expect(Math.abs(values[0][0]) + Math.abs(values[0][3])).toBeGreaterThan(8);
  expect(values[0].slice(4)).toEqual(Array(8).fill(0));
});

it.each(christmasCards)(
  '%s preserves triangle orientation across combined breath/wind extrema',
  (id) => {
    for (const layer of renderLayers(id)) {
      const { vertices, indices } = createMotionMesh(id, {
        region: layer.region,
        rect: layer.rect,
        fixed: layer.fixed,
      });
      const positions = new Float32Array((vertices.length / vertexStride) * 2);
      const drivers = new Float32Array(6);
      let minimum = Infinity,
        maximum = 0;
      const times = [0, 2.584, 2.864, 6.8, 7.08, ...Array.from({ length: 43 }, (_, i) => i * 3.9)];
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
          const area =
            ((positions[b] - positions[a]) * (positions[c + 1] - positions[a + 1]) -
              (positions[b + 1] - positions[a + 1]) * (positions[c] - positions[a])) /
            (meshStep * meshStep);
          minimum = Math.min(minimum, area);
          maximum = Math.max(maximum, area);
        }
      }
      expect(Number.isFinite(minimum + maximum)).toBe(true);
      expect(minimum, layer.name).toBeGreaterThan(0.35);
      expect(maximum, layer.name).toBeLessThan(1.8);
    }
  },
);

it('binds the visible torso in each pose to respiration without animating waist contacts', () => {
  const torsos: [ChristmasCard, Point, Point][] = [
    ['t-chatgpt', [570, 435], [604, 572]],
    ['t-claude', [482, 491], [540, 726]],
    ['t-glm', [551, 399], [500, 596]],
    ['t-deepseek', [595, 425], [595, 565]],
    ['t-gemini', [584, 425], [519, 547]],
  ];
  for (const [id, torso, anchor] of torsos) {
    const breath = coefficients(meshFor(id), id === 't-claude' ? torso : inset(torso))[9];
    expect(breath, id).toBeLessThan(-3);
    expect(breath, id).toBeGreaterThan(-9);
    expect(coefficients(meshFor(id), inset(anchor))).toEqual(Array(vertexStride - 2).fill(0));
  }
});

it('retains visible motion at actual free hair, drape and tail landmarks', () => {
  const landmarks: [ChristmasCard, Point][] = [
    ['t-chatgpt', [158, 833]],
    ['t-chatgpt', [263, 1040]],
    ['t-glm', [870, 634]],
    ['t-glm', [786, 854]],
    ['t-deepseek', [186, 505]],
    ['t-deepseek', [270, 950]],
    ['t-deepseek', [102, 630]],
    ['t-gemini', [179, 732]],
    ['t-gemini', [331, 1046]],
  ];
  const drivers = new Float32Array(6);
  for (const [id, point] of landmarks) {
    const values = coefficients(meshFor(id), inset(point));
    let min = Infinity,
      max = -Infinity;
    for (let time = 0; time <= 9.6; time += 0.1) {
      motionDrivers(time, drivers);
      const x = Array.from(drivers).reduce((sum, value, i) => sum + value * values[i * 2], 0);
      min = Math.min(min, x);
      max = Math.max(max, x);
    }
    // An engineering guard against accidentally pinning a complete material.
    // Normal-speed review at the actual display size remains separate.
    expect(max - min, id + ' ' + point.join(',')).toBeGreaterThan(8);
    expect(max - min).toBeLessThan(45);
  }
});

it.each(christmasCards)('%s keeps material flutter out of the body layer', (id) => {
  const body = renderLayers(id).find((layer) => layer.name === 'body')!;
  const mesh = createMotionMesh(id, { fixed: body.fixed });
  for (let i = 0; i < mesh.vertices.length; i += vertexStride)
    expect(Array.from(mesh.vertices.subarray(i + 2, i + 10))).toEqual(Array(8).fill(0));
});

it('moves Claude sheer skirt independently of the legs underneath', () => {
  const layers = renderLayers('t-claude');
  const body = createMotionMesh('t-claude', { fixed: layers[0].fixed });
  const skirt = layers.find((layer) => layer.name === 'front-skirt')!;
  const cloth = createMotionMesh('t-claude', { region: skirt.region, fixed: skirt.fixed });
  const point: Point = [570, 1280];
  expect(coefficients(body, point)).toEqual(Array(vertexStride - 2).fill(0));
  expect(
    coefficients(cloth, point)
      .slice(0, 8)
      .some((value) => Math.abs(value) > 3),
  ).toBe(true);
  // Body, independent translucent gown, then hands/book in the foreground.
  expect(layers.findIndex((layer) => layer.name === 'gown-bodice')).toBeGreaterThan(0);
  expect(layers.at(-1)?.name).toBe('front');
});

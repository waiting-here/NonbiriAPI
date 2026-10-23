import { mask, regionWeight } from './motion';

type Vector = readonly [number, number];
const hairAndCoat = [
  [
    [65, 450],
    [153, 344],
    [245, 261],
    [330, 194],
    [350, 242],
    [320, 334],
    [296, 402],
    [300, 480],
    [260, 530],
    [230, 620],
    [130, 715],
    [58, 645],
    [47, 554],
  ],
  [
    [598, 170],
    [669, 211],
    [718, 298],
    [837, 350],
    [966, 414],
    [1023, 516],
    [1016, 652],
    [929, 741],
    [854, 697],
    [847, 560],
    [800, 499],
    [766, 427],
    [680, 381],
    [640, 311],
  ],
  [
    [350, 541],
    [390, 567],
    [337, 657],
    [263, 739],
    [202, 840],
    [154, 864],
    [145, 748],
    [251, 615],
  ],
  [
    [681, 725],
    [772, 786],
    [853, 861],
    [910, 984],
    [878, 1075],
    [792, 932],
    [724, 889],
    [724, 815],
  ],
] as const;
const environment = [
  [
    [700, 15],
    [1060, 0],
    [1190, 65],
    [1225, 170],
    [1130, 246],
    [900, 260],
    [768, 215],
    [700, 160],
  ],
  [
    [290, 0],
    [545, 0],
    [775, 95],
    [800, 151],
    [674, 227],
    [530, 163],
    [413, 182],
    [343, 92],
  ],
  [
    [1180, 0],
    [1440, 0],
    [1550, 80],
    [1510, 240],
    [1395, 286],
    [1270, 223],
    [1185, 155],
  ],
] as const;

// Authored torso mesh in source pixels. Its boundary and both hands are pinned;
// the neck base follows the breath, while hips and legs remain outside the mesh.
const xs = [280, 350, 405, 470, 535, 600, 660, 770];
const ys = [250, 290, 330, 390, 460, 520, 565, 620];
const zero: Vector = [0, 0];
const row = (...middle: Vector[]) => [zero, zero, ...middle, zero];
const displacement: Vector[][] = [
  xs.map(() => zero),
  row([-0.3, -0.6], [0, -0.2], [0.4, -0.8], [0.6, -0.8], [0, 0]),
  row([-0.8, -1.6], [0, -2.3], [0.8, -2.1], [1.2, -1.8], [0, -0.6]),
  row([-1.2, -2.3], [0, -3.8], [1.2, -3.3], [1.4, -1.9], [0.3, -0.6]),
  row([-0.9, -1.3], [0, -2.8], [1, -2.8], [1, -1.4], [0, 0]),
  row([-0.4, -0.6], [0, -1.3], [0.5, -1.3], [0.4, -0.7], [0, 0]),
  row([0, 0], [0, -0.1], [0.1, -0.2], [0, 0], [0, 0]),
  xs.map(() => zero),
];
export function breathDisplacement(x: number, y: number): Vector {
  const col = xs.findIndex((value, index) => index > 0 && x <= value) - 1;
  const line = ys.findIndex((value, index) => index > 0 && y <= value) - 1;
  if (col < 0 || line < 0 || x < xs[0] || y < ys[0]) return zero;
  const u = (x - xs[col]) / (xs[col + 1] - xs[col]);
  const v = (y - ys[line]) / (ys[line + 1] - ys[line]);
  const a = displacement[line][col],
    b = displacement[line][col + 1];
  const c = displacement[line + 1][col],
    d = displacement[line + 1][col + 1];
  const interpolate = (channel: number) =>
    u + v <= 1
      ? a[channel] + u * (b[channel] - a[channel]) + v * (c[channel] - a[channel])
      : d[channel] + (1 - u) * (c[channel] - d[channel]) + (1 - v) * (b[channel] - d[channel]);
  return [interpolate(0), interpolate(1)];
}
const tail = [
  [801, 756],
  [841, 762],
  [865, 781],
  [858, 794],
  [822, 784],
  [804, 775],
] as const;
const grip = [
  [299, 350],
  [355, 323],
  [410, 334],
  [413, 393],
  [374, 432],
  [360, 484],
  [326, 527],
  [296, 482],
] as const;
const offeredHand = [
  [620, 526],
  [698, 500],
  [772, 513],
  [755, 590],
  [714, 644],
  [639, 646],
  [614, 592],
] as const;
function poseMask() {
  const width = 256,
    height = 384;
  const pixels = new Uint8Array(width * height * 4);
  for (let y = 0; y < height; y++)
    for (let x = 0; x < width; x++) {
      const px = (x + 0.5) * 4,
        py = (y + 0.5) * 4;
      const delta = breathDisplacement(px, py);
      const pinned = Math.max(regionWeight(px, py, grip, 5), regionWeight(px, py, offeredHand, 5));
      const offset = (y * width + x) * 4;
      pixels[offset] = Math.round(128 + delta[0] * 16 * (1 - pinned));
      pixels[offset + 1] = Math.round(128 + delta[1] * 16 * (1 - pinned));
      pixels[offset + 2] = Math.round(255 * regionWeight(px, py, tail, 3));
      pixels[offset + 3] = 255;
    }
  return { width, height, pixels };
}
let cached: ReturnType<typeof createMasks> | undefined;
const createMasks = () => ({
  hero: mask(256, 384, 1024, 1536, hairAndCoat),
  environment: mask(418, 235, 1672, 941, environment),
  pose: poseMask(),
});
export const refinedMotionMasks = () => (cached ??= createMasks());

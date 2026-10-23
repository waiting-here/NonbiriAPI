type Point = readonly [number, number];
export const hairAndCloth = [
  [
    [70, 480],
    [170, 390],
    [306, 300],
    [288, 432],
    [291, 510],
    [215, 622],
    [117, 711],
    [70, 630],
  ],
  [
    [695, 355],
    [816, 408],
    [963, 448],
    [1019, 530],
    [1004, 666],
    [922, 744],
    [850, 710],
    [861, 558],
  ],
  [
    [350, 542],
    [389, 567],
    [337, 656],
    [263, 739],
    [202, 837],
    [153, 852],
    [148, 750],
    [251, 615],
  ],
  [
    [684, 727],
    [769, 787],
    [848, 858],
    [901, 980],
    [874, 1063],
    [795, 932],
    [729, 888],
    [727, 816],
  ],
] as const;
const environment = [
  [
    [710, 35],
    [1060, 0],
    [1170, 65],
    [1225, 170],
    [1130, 246],
    [900, 260],
    [768, 215],
    [700, 160],
  ],
  [
    [295, 0],
    [545, 0],
    [775, 95],
    [795, 150],
    [673, 224],
    [530, 163],
    [413, 182],
    [343, 92],
  ],
  [
    [1330, 20],
    [1460, 0],
    [1580, 28],
    [1582, 150],
    [1504, 240],
    [1392, 285],
    [1315, 205],
    [1250, 109],
  ],
] as const;

export function regionWeight(x: number, y: number, polygon: readonly Point[]) {
  let inside = false,
    distance = Infinity;
  let a = polygon[polygon.length - 1];
  for (const b of polygon) {
    if (a[1] > y !== b[1] > y && x < ((a[0] - b[0]) * (y - b[1])) / (a[1] - b[1]) + b[0])
      inside = !inside;
    const dx = b[0] - a[0],
      dy = b[1] - a[1];
    const t = Math.max(0, Math.min(1, ((x - a[0]) * dx + (y - a[1]) * dy) / (dx * dx + dy * dy)));
    distance = Math.min(distance, Math.hypot(x - a[0] - dx * t, y - a[1] - dy * t));
    a = b;
  }
  const t = Math.min(1, distance / 18);
  return inside ? t * t * (3 - 2 * t) : 0;
}
function mask(
  width: number,
  height: number,
  sourceWidth: number,
  sourceHeight: number,
  polygons: readonly (readonly Point[])[],
) {
  const pixels = new Uint8Array(width * height * 4);
  for (let y = 0; y < height; y++)
    for (let x = 0; x < width; x++) {
      for (const [channel, polygon] of polygons.entries())
        pixels[(y * width + x) * 4 + channel] = Math.round(
          255 *
            regionWeight(
              ((x + 0.5) * sourceWidth) / width,
              ((y + 0.5) * sourceHeight) / height,
              polygon,
            ),
        );
    }
  return { width, height, pixels };
}
let cached: ReturnType<typeof motionMasks> | undefined;
export function motionMasks(): {
  hero: ReturnType<typeof mask>;
  environment: ReturnType<typeof mask>;
} {
  return (cached ??= {
    hero: mask(256, 384, 1024, 1536, hairAndCloth),
    environment: mask(418, 235, 1672, 941, environment),
  });
}

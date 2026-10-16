import { eyelidProfileWidth, type EyelidProfile } from '@shared/animation/illustration';

type Point = readonly [number, number];
function contour(points: Point[], x: number) {
  const next = points.findIndex((point) => point[0] >= x);
  if (next <= 0) return points[next === 0 ? 0 : points.length - 1][1];
  const a = points[next - 1],
    b = points[next];
  return a[1] + ((x - a[0]) / (b[0] - a[0])) * (b[1] - a[1]);
}
function eye(
  box: [number, number, number, number],
  upper: Point[],
  lower: Point[],
  shut: Point[],
): EyelidProfile {
  const rows: [number[], number[]] = [[], []];
  const byte = (y: number) => Math.round(Math.max(0, Math.min(1, (y - box[1]) / box[3])) * 255);
  for (let i = 0; i < eyelidProfileWidth; i++) {
    const x = box[0] + (box[2] * i) / (eyelidProfileWidth - 1);
    const top = contour(upper, x),
      bottom = contour(lower, x),
      closed = contour(shut, x);
    rows[0].push(
      byte(Math.min(top, closed) - 2),
      byte(Math.max(bottom, closed + 5) + 2),
      byte(top),
      byte(top + 4),
    );
    rows[1].push(byte(closed), byte(closed + 4), 255, 255);
  }
  return { bounds: [box[0] / 1024, box[1] / 1536, box[2] / 1024, box[3] / 1536], rows };
}

// Traced in the unplaced portrait plate, before either card or wallpaper composition.
export const mirrorSeaEyelids = [
  eye(
    [379, 150, 55, 39],
    [
      [379, 177],
      [389, 164],
      [402, 157],
      [416, 157],
      [427, 162],
      [434, 165],
    ],
    [
      [379, 179],
      [389, 179],
      [403, 183],
      [416, 179],
      [428, 171],
      [434, 166],
    ],
    [
      [379, 175],
      [390, 178],
      [402, 175],
      [415, 171],
      [434, 166],
    ],
  ),
  eye(
    [458, 124, 53, 40],
    [
      [458, 146],
      [469, 134],
      [481, 128],
      [494, 130],
      [511, 140],
    ],
    [
      [458, 147],
      [469, 154],
      [481, 159],
      [494, 155],
      [511, 141],
    ],
    [
      [458, 154],
      [470, 149],
      [482, 145],
      [494, 141],
      [511, 136],
    ],
  ),
];

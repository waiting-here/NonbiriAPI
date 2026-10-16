import { eyelidProfileWidth, type EyelidProfile } from '@shared/animation/illustration';

type Point = readonly [number, number];
function curve(points: readonly Point[], x: number) {
  for (let i = 1; i < points.length; i++) {
    const [a, b] = [points[i - 1], points[i]];
    if (x <= b[0]) {
      const t = Math.max(0, (x - a[0]) / (b[0] - a[0]));
      return a[1] + (b[1] - a[1]) * t;
    }
  }
  return points[points.length - 1][1];
}
function profile(
  box: [number, number, number, number],
  upper: Point[],
  lower: Point[],
  shut: Point[],
): EyelidProfile {
  const [x, y, width, height] = box;
  const channel = (value: number) =>
    Math.round(Math.max(0, Math.min(1, (value - y) / height)) * 255);
  const opened: number[] = [],
    closed: number[] = [];
  for (let i = 0; i < eyelidProfileWidth; i++) {
    const px = x + (width * i) / (eyelidProfileWidth - 1);
    const top = curve(upper, px),
      bottom = curve(lower, px),
      lid = curve(shut, px);
    opened.push(
      channel(top - 2),
      channel(Math.max(bottom + 2, lid + 4)),
      channel(top),
      channel(top + 3.4),
    );
    const edge = Math.max(
      0,
      Math.min(1, (px - upper[0][0]) / 2.3, (upper[upper.length - 1][0] - px) / 2.3),
    );
    closed.push(channel(lid - 1.6), channel(lid + 2.4), Math.round(edge * 255), 0);
  }
  return { bounds: [x / 1037, y / 1516, width / 1037, height / 1516], rows: [opened, closed] };
}

// Separately measured contours in the new 1037 × 1516 painting.
export const polishedAmberEyelids: [EyelidProfile, EyelidProfile] = [
  profile(
    [466, 114, 40, 32],
    [
      [468, 126],
      [473, 123],
      [478, 120],
      [484, 118.7],
      [490, 119.3],
      [495, 121],
      [500, 124],
      [503, 128],
    ],
    [
      [468, 129],
      [473, 135],
      [478, 139],
      [484, 140],
      [490, 139],
      [495, 136],
      [500, 131],
      [503, 129],
    ],
    [
      [468, 129],
      [473, 132],
      [478, 134],
      [484, 135],
      [490, 135],
      [496, 133],
      [503, 129],
    ],
  ),
  profile(
    [530, 108, 41, 32],
    [
      [532, 124],
      [538, 117.5],
      [544, 114.5],
      [550, 113.5],
      [555, 114.5],
      [560, 116.5],
      [566, 119],
      [568, 121],
    ],
    [
      [532, 125],
      [538, 130],
      [544, 134],
      [550, 134],
      [556, 131],
      [562, 126],
      [568, 122],
    ],
    [
      [532, 125],
      [538, 129],
      [544, 131],
      [550, 131],
      [556, 130],
      [562, 126],
      [568, 122],
    ],
  ),
];

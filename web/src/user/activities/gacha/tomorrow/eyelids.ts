import { eyelidProfileWidth, type EyelidProfile } from '@shared/animation/illustration';
type Point = [number, number];
function curve(points: Point[], x: number) {
  for (let i = 1; i < points.length; i++) {
    const a = points[i - 1],
      b = points[i];
    if (x <= b[0]) return a[1] + (b[1] - a[1]) * Math.max(0, (x - a[0]) / (b[0] - a[0]));
  }
  return points[points.length - 1][1];
}
export function profile(
  box: [number, number, number, number],
  top: Point[],
  bottom: Point[],
  shut: Point[],
  coverageTop?: Point[],
): EyelidProfile {
  const [x, y, width, height] = box;
  const channel = (value: number) =>
    Math.round(Math.max(0, Math.min(1, (value - y) / height)) * 255);
  const opened: number[] = [],
    closed: number[] = [];
  for (let i = 0; i < eyelidProfileWidth; i++) {
    const px = x + (width * i) / (eyelidProfileWidth - 1);
    const a = curve(top, px),
      b = curve(bottom, px),
      c = curve(shut, px);
    opened.push(
      channel(coverageTop ? curve(coverageTop, px) : a - 2),
      channel(Math.max(b + 3, c + 6)),
      channel(a),
      channel(a + 5.5),
    );
    const edge = Math.max(0, Math.min(1, (px - top[0][0]) / 2, (top[top.length - 1][0] - px) / 2));
    closed.push(channel(c - 2.5), channel(c + 4), Math.round(edge * 255), 0);
  }
  return { bounds: [x / 1024, y / 1536, width / 1024, height / 1536], rows: [opened, closed] };
}

export const tomorrowEyelids: [EyelidProfile, EyelidProfile] = [
  profile(
    [385, 179, 63, 43],
    [
      [386, 204],
      [394, 198],
      [402, 189],
      [411, 183],
      [420, 183],
      [430, 185],
      [439, 188],
      [446, 194],
    ],
    [
      [386, 207],
      [395, 208],
      [405, 211],
      [416, 214],
      [427, 210],
      [438, 205],
      [446, 197],
    ],
    [
      [386, 211],
      [395, 213],
      [405, 209],
      [416, 207],
      [427, 204],
      [438, 198],
      [446, 197],
    ],
  ),
  profile(
    [475, 143, 59, 45],
    [
      [476, 165],
      [483, 158],
      [492, 152],
      [501, 148],
      [511, 148],
      [520, 151],
      [528, 154],
      [533, 154],
    ],
    [
      [476, 170],
      [484, 179],
      [494, 184],
      [504, 182],
      [515, 178],
      [524, 168],
      [533, 157],
    ],
    [
      [476, 179],
      [484, 177],
      [494, 175],
      [504, 171],
      [514, 167],
      [524, 163],
      [533, 163],
    ],
  ),
];

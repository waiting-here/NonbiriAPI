import type { MythicPortrait, Point, Rect } from '../activities/gacha/mythic/layers';
export type GardenPart = {
  name: string;
  label: string;
  rect: Rect;
  root: Point;
  reach: number;
  material: 'flower' | 'pendant';
  front: boolean;
  amplitude: number;
  phase: number;
};
const flower = (
  name: string,
  rect: Rect,
  root: Point,
  front: boolean,
  reach: number,
  amplitude = 2,
): GardenPart => ({
  name,
  label: '花叶 · ' + name,
  rect,
  root,
  front,
  reach,
  amplitude,
  phase: root[0] * 0.013,
  material: 'flower',
});
const pendant = (name: string, rect: Rect, root: Point): GardenPart => ({
  name,
  label: '悬挂饰物 · ' + name,
  rect,
  root,
  front: false,
  reach: rect[3] * 0.85,
  amplitude: 1,
  phase: root[0] * 0.017,
  material: 'pendant',
});
export const gardenParts: Record<MythicPortrait, GardenPart[]> = {
  'celestial-light': [
    pendant('upper-star', [194, 0, 126, 150], [266, 0]),
    pendant('upper-globe', [328, 72, 88, 125], [372, 74]),
    pendant('small-star', [410, 32, 42, 100], [429, 34]),
    pendant('bridge-star', [1037, 167, 89, 154], [1082, 211]),
    pendant('far-star', [1187, 0, 75, 133], [1220, 0]),
    pendant('bright-star', [1357, 0, 62, 115], [1384, 0]),
    pendant('right-globe', [1571, 105, 101, 301], [1634, 109]),
    flower('left-near', [0, 581, 500, 360], [96, 941], true, 370, 2.6),
    flower('right-near', [1280, 647, 392, 294], [1555, 941], true, 300, 2.5),
    flower('terrace-low', [670, 625, 670, 316], [1032, 910], false, 330, 1.6),
    flower('terrace-high', [1260, 289, 412, 416], [1530, 598], false, 260, 1.5),
    flower('column-vine', [1390, 0, 145, 355], [1460, 0], false, 320, 1.4),
  ],
  'celestial-dark': [
    pendant('left-star', [139, 0, 57, 205], [168, 0]),
    pendant('left-gem', [211, 0, 50, 112], [236, 0]),
    pendant('top-star', [916, 0, 55, 105], [943, 0]),
    pendant('top-drop', [958, 0, 55, 180], [984, 0]),
    pendant('top-gems', [1052, 0, 49, 127], [1075, 0]),
    pendant('center-star', [1080, 0, 68, 221], [1114, 0]),
    pendant('far-star', [1255, 0, 62, 135], [1286, 0]),
    pendant('orbit-star', [1279, 226, 58, 111], [1307, 230]),
    pendant('lower-star', [1420, 101, 85, 293], [1460, 103]),
    pendant('right-star', [1510, 0, 84, 292], [1549, 0]),
    pendant('right-globe', [1577, 0, 95, 286], [1610, 0]),
    flower('left-near', [0, 677, 306, 264], [90, 941], true, 310, 2.4),
    flower('right-near', [1265, 653, 407, 288], [1544, 941], true, 315, 2.6),
    flower('left-vine', [0, 340, 165, 423], [62, 742], false, 350, 1.5),
    flower('top-left-vine', [0, 0, 112, 355], [25, 0], false, 300, 1.2),
    flower('terrace-low', [792, 639, 670, 302], [1128, 905], false, 320, 1.7),
    flower('terrace-high', [1341, 270, 331, 442], [1609, 669], false, 330, 1.4),
  ],
  'wandering-light': [
    flower('left-near', [0, 575, 684, 366], [150, 941], true, 460, 2.8),
    flower('right-near', [1206, 573, 466, 368], [1543, 941], true, 390, 2.7),
    flower('low-sprigs', [826, 793, 462, 148], [1150, 941], true, 260, 1.8),
  ],
  'wandering-dark': [
    flower('hanging-bough', [0, 0, 255, 370], [16, 1], true, 320, 2.2),
    flower('near-blooms', [773, 766, 899, 175], [1280, 941], true, 430, 2.6),
    flower('ridge-leaves', [1106, 513, 566, 366], [1508, 840], false, 355, 1.8),
  ],
};
export function gardenPath(id: MythicPortrait, name: string, high = false) {
  return (
    '/assets/gacha/appearances/layers/' +
    (high ? 'hires/' : '') +
    id +
    '/environment/' +
    name +
    '.webp'
  );
}
export function gardenMotion(
  part: GardenPart,
  time: number,
  active = true,
): [number, number, number, number] {
  const phase = part.phase,
    pendulum = part.material === 'pendant';
  const speed = pendulum ? 0.77 : 0.59;
  const wave = active ? Math.sin(time * speed + phase) - Math.sin(phase) : 0;
  const delayed = active ? Math.sin(time * speed + phase - 0.55) - Math.sin(phase - 0.55) : 0;
  return [
    (wave * part.amplitude) / 1672,
    pendulum ? 0 : (delayed * part.amplitude * 0.28) / 941,
    wave * (pendulum ? 0.0035 : 0.002),
    part.reach / 941,
  ];
}

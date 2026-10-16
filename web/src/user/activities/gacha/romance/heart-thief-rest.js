const clamp = (x) => Math.max(0, Math.min(1, x));
const smooth = (a, b, x) => {
  const q = clamp((x - a) / (b - a));
  return q * q * (3 - 2 * q);
};
function distance(x, y, poly) {
  let inside = false,
    d = Infinity;
  for (let i = 0, j = poly.length - 1; i < poly.length; j = i++) {
    const [a, b] = poly[j],
      [c, e] = poly[i];
    if (b > y !== e > y && x < ((c - a) * (y - b)) / (e - b) + a) inside = !inside;
    const dx = c - a,
      dy = e - b,
      q = clamp(((x - a) * dx + (y - b) * dy) / (dx * dx + dy * dy));
    d = Math.min(d, Math.hypot(x - a - q * dx, y - b - q * dy));
  }
  return { inside, d };
}
const inside = (x, y, p, f) => {
  const z = distance(x, y, p);
  return z.inside ? smooth(0, f, z.d) : 0;
};
const cover = (x, y, p, f = 8) => {
  const z = distance(x, y, p);
  return z.inside ? 1 : 1 - smooth(0, f, z.d);
};
const clear = (x, y, p, f = 10) => {
  const z = distance(x, y, p);
  return z.inside ? 0 : smooth(2, f, z.d);
};
export const restOutlines = {
  hairLeft: [
    [360, 325],
    [392, 370],
    [381, 430],
    [375, 484],
    [364, 568],
    [326, 654],
    [196, 739],
    [66, 727],
    [54, 561],
    [98, 446],
    [200, 395],
  ],
  hairRight: [
    [666, 279],
    [766, 310],
    [906, 390],
    [1035, 481],
    [1060, 634],
    [1014, 728],
    [874, 757],
    [797, 708],
    [746, 633],
    [826, 615],
    [841, 510],
    [790, 447],
    [729, 406],
  ],
  body: [
    [389, 432],
    [505, 410],
    [574, 465],
    [607, 530],
    [637, 603],
    [642, 652],
    [611, 685],
    [520, 707],
    [438, 697],
    [388, 662],
    [384, 575],
    [407, 491],
  ],
  head: [
    [350, 0],
    [653, 0],
    [676, 113],
    [660, 180],
    [642, 242],
    [612, 293],
    [573, 318],
    [524, 281],
    [485, 248],
    [435, 231],
    [391, 191],
    [367, 130],
  ],
  mask: [
    [351, 139],
    [370, 160],
    [385, 173],
    [409, 204],
    [449, 211],
    [477, 227],
    [495, 223],
    [481, 237],
    [453, 251],
    [421, 249],
    [399, 235],
    [379, 230],
    [355, 220],
    [352, 194],
  ],
  maskHand: [
    [340, 216],
    [381, 215],
    [409, 233],
    [411, 251],
    [389, 274],
    [371, 279],
    [348, 310],
    [330, 330],
    [301, 321],
    [304, 285],
    [320, 250],
  ],
  ribs: [
    [440, 304],
    [507, 272],
    [565, 286],
    [585, 331],
    [579, 381],
    [548, 414],
    [515, 442],
    [481, 472],
    [442, 481],
    [414, 453],
    [391, 421],
    [372, 384],
    [382, 344],
  ],
  leftCoat: [
    [407, 543],
    [366, 568],
    [326, 594],
    [282, 622],
    [240, 647],
    [210, 685],
    [187, 728],
    [168, 770],
    [156, 804],
    [177, 851],
    [218, 906],
    [276, 952],
    [254, 907],
    [245, 870],
    [238, 825],
    [238, 777],
    [250, 749],
    [277, 733],
    [328, 716],
    [363, 693],
    [390, 655],
  ],
  rightCoat: [
    [575, 479],
    [618, 565],
    [678, 633],
    [738, 691],
    [790, 725],
    [862, 751],
    [925, 810],
    [982, 867],
    [1023, 918],
    [1034, 956],
    [1013, 991],
    [971, 1028],
    [925, 1070],
    [878, 1128],
    [895, 1059],
    [904, 994],
    [908, 941],
    [909, 878],
    [830, 872],
    [777, 878],
    [724, 842],
    [702, 805],
    [688, 770],
    [653, 734],
    [629, 700],
    [621, 651],
    [600, 599],
    [575, 550],
  ],
  legs: [
    [394, 649],
    [477, 650],
    [523, 638],
    [578, 615],
    [633, 623],
    [637, 681],
    [613, 756],
    [572, 835],
    [585, 931],
    [639, 1080],
    [675, 1190],
    [705, 1220],
    [705, 1270],
    [677, 1310],
    [635, 1333],
    [597, 1325],
    [603, 1264],
    [569, 1152],
    [536, 1116],
    [536, 1223],
    [496, 1397],
    [447, 1448],
    [400, 1447],
    [416, 1345],
    [434, 1230],
    [444, 1135],
    [446, 1030],
    [443, 959],
    [426, 866],
    [409, 789],
    [397, 732],
  ],
};
export function restPose(t) {
  const elapsed = Math.max(0, t - 9),
    fade = smooth(0, 1.6, elapsed);
  return {
    nod: 0,
    breath: fade * (1 - Math.cos((elapsed * Math.PI * 2) / 5.8)) * 0.5,
    cloth: smooth(0.15, 3.2, elapsed),
    elapsed,
  };
}
export function inspectRestWeight(x, y) {
  const head = 0;
  const ribs = inside(x, y, restOutlines.ribs, 20) * smooth(476, 414, y);
  const legClear =
    clear(x, y, restOutlines.legs, 16) *
    clear(x, y, restOutlines.body, 18) *
    clear(x, y, restOutlines.hairLeft, 36) *
    clear(x, y, restOutlines.hairRight, 40);
  const left = cover(x, y, restOutlines.leftCoat, 72) * smooth(555, 825, y) * legClear;
  const right = cover(x, y, restOutlines.rightCoat, 96) * smooth(575, 945, y) * legClear;
  return { head, ribs, left, right };
}

type Point = readonly [number, number];
type Matrix = number[];
type Weights = number[];

export const rigSize = [832, 1216] as const;
export const boneNames = [
  'ground',
  'chest',
  'head',
  'staff upper arm',
  'staff forearm',
  'book upper arm',
  'book forearm',
  'cape root',
  'cape tip',
  'hair root',
  'hair tip',
  'left hem',
  'right hem',
  'tabard hem',
] as const;

const smooth = (from: number, to: number, value: number) => {
  const t = Math.max(0, Math.min(1, (value - from) / (to - from)));
  return t * t * (3 - 2 * t);
};

// Painted material boundaries, in original source pixels. The staff, hands,
// shoulder armour and book stay rigid within their respective joint chains.
export const regions: Record<string, readonly Point[]> = {
  head: [
    [209, -24],
    [585, -24],
    [586, 177],
    [487, 199],
    [442, 173],
    [382, 178],
    [351, 191],
    [304, 209],
    [226, 200],
  ],
  staffArm: [
    [324, 233],
    [342, 306],
    [318, 409],
    [287, 450],
    [231, 511],
    [195, 545],
    [168, 500],
    [153, 442],
    [85, 415],
    [82, 351],
    [202, 348],
    [258, 307],
  ],
  staff: [
    [-16, 108],
    [79, 102],
    [82, 248],
    [130, 357],
    [173, 370],
    [206, 589],
    [179, 604],
    [136, 439],
    [88, 417],
    [74, 363],
    [36, 275],
    [-16, 240],
  ],
  bookArm: [
    [470, 278],
    [533, 275],
    [569, 337],
    [605, 360],
    [643, 366],
    [678, 410],
    [647, 514],
    [620, 551],
    [577, 554],
    [545, 519],
    [514, 443],
    [483, 389],
  ],
  book: [
    [532, 429],
    [593, 326],
    [663, 303],
    [707, 316],
    [754, 280],
    [852, 280],
    [852, 480],
    [780, 483],
    [689, 500],
    [638, 480],
    [586, 463],
  ],
  cape: [
    [133, 575],
    [157, 548],
    [197, 546],
    [236, 559],
    [239, 628],
    [219, 672],
    [207, 720],
    [202, 779],
    [137, 818],
    [88, 774],
    [85, 692],
    [98, 628],
  ],
  hair: [
    [537, 531],
    [562, 541],
    [592, 541],
    [624, 518],
    [645, 493],
    [660, 502],
    [676, 525],
    [683, 591],
    [643, 607],
    [604, 626],
    [566, 653],
    [548, 642],
    [537, 608],
    [526, 578],
    [521, 555],
  ],
  leftHem: [
    [231, 670],
    [310, 684],
    [314, 956],
    [358, 1034],
    [357, 1070],
    [174, 1061],
    [164, 909],
    [189, 779],
  ],
  rightHem: [
    [466, 670],
    [559, 660],
    [594, 818],
    [635, 985],
    [627, 1048],
    [471, 1071],
    [456, 980],
  ],
  tabard: [
    [317, 716],
    [450, 716],
    [471, 905],
    [304, 916],
  ],
};

function inside([x, y]: Point, polygon: readonly Point[]) {
  let hit = false;
  for (let i = 0, j = polygon.length - 1; i < polygon.length; j = i++) {
    const [ax, ay] = polygon[i],
      [bx, by] = polygon[j];
    if (ay > y !== by > y && x < ((bx - ax) * (y - ay)) / (by - ay) + ax) hit = !hit;
  }
  return hit;
}

function regionWeight(point: Point, polygon: readonly Point[], feather: number) {
  if (!inside(point, polygon)) return 0;
  let distance = Infinity;
  for (let i = 0; i < polygon.length; i++) {
    const a = polygon[i],
      b = polygon[(i + 1) % polygon.length];
    const dx = b[0] - a[0],
      dy = b[1] - a[1];
    const t = Math.max(
      0,
      Math.min(1, ((point[0] - a[0]) * dx + (point[1] - a[1]) * dy) / (dx * dx + dy * dy)),
    );
    distance = Math.min(distance, Math.hypot(point[0] - a[0] - t * dx, point[1] - a[1] - t * dy));
  }
  return smooth(0, feather, distance);
}

function mixBone(weights: Weights, bone: number, amount: number) {
  for (let i = 0; i < weights.length; i++) weights[i] *= 1 - amount;
  weights[bone] += amount;
}

export function weightsAt(point: Point) {
  const [x, y] = point;
  const weights = Array<number>(boneNames.length).fill(0);
  // The waist is the root; fitted chest panels move together, and the
  // transition into the belt is confined to the lower torso.
  weights[1] = 1 - smooth(332, 434, y);
  weights[0] = 1 - weights[1];
  mixBone(weights, 2, regionWeight(point, regions.head, 12) * (1 - smooth(169, 206, y)));
  const staffArm = regionWeight(point, regions.staffArm, 14);
  mixBone(weights, 3, staffArm);
  mixBone(weights, 4, staffArm * (1 - smooth(187, 269, x)));
  const bookArm = regionWeight(point, regions.bookArm, 14);
  mixBone(weights, 5, bookArm);
  mixBone(weights, 6, bookArm * smooth(523, 588, x));
  mixBone(weights, 6, regionWeight(point, regions.book, 20));
  const cape = regionWeight(point, regions.cape, 24) * smooth(551, 610, y);
  mixBone(weights, 7, cape);
  mixBone(weights, 8, cape * smooth(634, 742, y));
  // The solid staff crosses the cape; its grip chain takes precedence there.
  mixBone(weights, 4, regionWeight(point, regions.staff, 7));
  const hair = regionWeight(point, regions.hair, 15) * smooth(524, 558, y);
  mixBone(weights, 9, hair);
  mixBone(weights, 10, hair * smooth(566, 624, y));
  mixBone(weights, 11, regionWeight(point, regions.leftHem, 22) * smooth(713, 1025, y));
  mixBone(weights, 12, regionWeight(point, regions.rightHem, 22) * smooth(713, 1025, y));
  mixBone(weights, 13, regionWeight(point, regions.tabard, 15) * smooth(732, 885, y));
  return weights;
}

const identity = (): Matrix => [1, 0, 0, 0, 1, 0, 0, 0, 1];
function multiply(a: Matrix, b: Matrix): Matrix {
  return Array.from({ length: 9 }, (_, i) => {
    const column = Math.floor(i / 3),
      row = i % 3;
    return a[row] * b[column * 3] + a[row + 3] * b[column * 3 + 1] + a[row + 6] * b[column * 3 + 2];
  });
}
function joint(parent: Matrix, [x, y]: Point, angle: number, dx = 0, dy = 0): Matrix {
  const c = Math.cos(angle),
    s = Math.sin(angle);
  return multiply(parent, [c, s, 0, -s, c, 0, x - c * x + s * y + dx, y - s * x - c * y + dy, 1]);
}

export function poseAt(seconds: number) {
  const phase = ((seconds % 17.6) / 17.6) * Math.PI * 2;
  const wind = (lag: number) => Math.sin(phase * 2 - lag) + Math.sin(lag);
  const bones: Matrix[] = [identity()];
  bones[1] = joint(
    bones[0],
    [398, 421],
    0.0035 * Math.sin(phase * 2),
    0,
    -2.2 * (1 - Math.cos(phase * 4)),
  );
  bones[2] = joint(bones[1], [402, 175], 0.024 * Math.sin(phase * 2));
  bones[3] = joint(bones[1], [318, 248], 0.0045 * Math.sin(phase * 2));
  bones[4] = joint(bones[3], [214, 381], 0.006 * wind(0.35));
  bones[5] = joint(bones[1], [503, 285], -0.014 * Math.sin(phase * 2));
  bones[6] = joint(bones[5], [577, 489], -0.043 * wind(0.3));
  bones[7] = joint(bones[1], [204, 568], 0.026 * wind(0.5));
  bones[8] = joint(bones[7], [181, 665], 0.036 * wind(1.05));
  bones[9] = joint(bones[1], [605, 525], -0.037 * wind(0.65));
  bones[10] = joint(bones[9], [590, 576], -0.06 * wind(1.15));
  bones[11] = joint(bones[0], [299, 717], 0.018 * wind(0.7));
  bones[12] = joint(bones[0], [471, 717], 0.016 * wind(0.95));
  bones[13] = joint(bones[0], [384, 734], 0.013 * wind(1.2));
  return new Float32Array(bones.flat());
}

let cachedMesh: Float32Array | undefined;
export function createRigMesh() {
  if (cachedMesh) return cachedMesh;
  const step = 8,
    columns = Math.ceil(rigSize[0] / step),
    rows = Math.ceil(rigSize[1] / step);
  const vertices: number[][] = [];
  for (let y = 0; y <= rows; y++) {
    for (let x = 0; x <= columns; x++) {
      const point: Point = [(x * rigSize[0]) / columns, (y * rigSize[1]) / rows];
      const weights = weightsAt(point)
        .map((weight, bone) => ({ weight, bone }))
        .sort((a, b) => b.weight - a.weight)
        .slice(0, 4);
      const total = weights.reduce((sum, entry) => sum + entry.weight, 0);
      vertices.push([
        ...point,
        ...weights.map((entry) => entry.bone),
        ...weights.map((entry) => entry.weight / total),
      ]);
    }
  }
  const data = new Float32Array(columns * rows * 6 * 10);
  let offset = 0;
  for (let y = 0; y < rows; y++) {
    for (let x = 0; x < columns; x++) {
      const a = y * (columns + 1) + x,
        b = a + 1,
        c = a + columns + 1,
        d = c + 1;
      for (const index of [a, b, c, b, d, c]) {
        data.set(vertices[index], offset);
        offset += 10;
      }
    }
  }
  cachedMesh = data;
  return data;
}

export const characterVertex = `
attribute vec2 position;
attribute vec4 joints, weights;
uniform mat3 bones[${boneNames.length}];
uniform vec4 view, heroBox;
varying vec2 uv;
void main(){
  uv=position/vec2(832.,1216.);
  mat3 skin=bones[int(joints.x)]*weights.x+bones[int(joints.y)]*weights.y
    +bones[int(joints.z)]*weights.z+bones[int(joints.w)]*weights.w;
  vec2 posed=(skin*vec3(position,1.)).xy/vec2(832.,1216.);
  vec2 screen=(heroBox.xy+posed*heroBox.zw-view.xy)/view.zw;
  gl_Position=vec4(screen.x*2.-1.,1.-screen.y*2.,0.,1.);
}
`;

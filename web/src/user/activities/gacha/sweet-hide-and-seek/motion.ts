export type SweetCard = 's-chatgpt' | 's-claude';
export type Point = readonly [number, number];
export type MotionRegion = {
  /** Original 1024 × 1536 coordinates; all points outside this polygon stay fixed. */
  polygon: readonly Point[];
  root: number;
  tip: number;
  amplitude: Point;
  phase: number;
};

// Only detached outer ends above a complete background plate. Hand contacts,
// body, hat, candy foreground and feet have no motion field.
export const motionRegions: Record<SweetCard, readonly MotionRegion[]> = {
  's-chatgpt': [
    {
      polygon: [
        [20, 785],
        [90, 737],
        [160, 772],
        [163, 875],
        [134, 937],
        [72, 962],
        [7, 916],
      ],
      root: 760,
      tip: 940,
      amplitude: [2.2, 0.45],
      phase: 0,
    },
    {
      polygon: [
        [881, 802],
        [944, 825],
        [1001, 900],
        [1023, 988],
        [989, 1040],
        [932, 1014],
        [885, 937],
      ],
      root: 825,
      tip: 1020,
      amplitude: [2, 0.4],
      phase: 1.1,
    },
  ],
  's-claude': [
    {
      polygon: [
        [191, 444],
        [217, 418],
        [247, 447],
        [245, 497],
        [226, 541],
        [194, 536],
        [176, 503],
      ],
      root: 433,
      tip: 530,
      amplitude: [1.7, 0.35],
      phase: 0.5,
    },
    {
      polygon: [
        [228, 584],
        [273, 574],
        [310, 602],
        [298, 657],
        [277, 697],
        [249, 674],
        [228, 636],
      ],
      root: 590,
      tip: 685,
      amplitude: [1.9, 0.45],
      phase: 1.4,
    },
    {
      polygon: [
        [559, 1118],
        [601, 1099],
        [626, 1161],
        [617, 1220],
        [578, 1261],
        [548, 1238],
        [536, 1184],
      ],
      root: 1120,
      tip: 1248,
      amplitude: [1.3, 0.35],
      phase: 2.1,
    },
  ],
};

const smooth = (a: number, b: number, x: number) => {
  const t = Math.max(0, Math.min(1, (x - a) / (b - a)));
  return t * t * (3 - 2 * t);
};

export function regionWeight(region: MotionRegion, [x, y]: Point): number {
  let inside = false,
    distance = Infinity;
  for (let i = 0; i < region.polygon.length; i++) {
    const a = region.polygon[i],
      b = region.polygon[(i + 1) % region.polygon.length];
    if (a[1] > y !== b[1] > y && x < ((b[0] - a[0]) * (y - a[1])) / (b[1] - a[1]) + a[0])
      inside = !inside;
    const dx = b[0] - a[0],
      dy = b[1] - a[1];
    const t = Math.max(0, Math.min(1, ((x - a[0]) * dx + (y - a[1]) * dy) / (dx * dx + dy * dy)));
    distance = Math.min(distance, Math.hypot(x - a[0] - dx * t, y - a[1] - dy * t));
  }
  return inside ? smooth(0, 12, distance) * smooth(region.root, region.tip, y) : 0;
}

export function motionOffset(id: SweetCard, point: Point, seconds: number): Point {
  let x = 0,
    y = 0;
  for (const region of motionRegions[id]) {
    const weight = regionWeight(region, point);
    const phase = (seconds * Math.PI * 2) / 9.6 + region.phase - (point[1] - region.root) * 0.004;
    x += region.amplitude[0] * Math.sin(phase) * weight;
    y += region.amplitude[1] * Math.sin(phase + 0.6) * weight;
  }
  return [x, y];
}

const f = (n: number) => (Number.isInteger(n) ? `${n}.0` : `${n}`);
const vec = (point: Point) => `vec2(${f(point[0])},${f(point[1])})`;

export function motionShader(id: SweetCard): string {
  const regions = motionRegions[id]
    .map((region, i) => {
      const xs = region.polygon.map(([x]) => x),
        ys = region.polygon.map(([, y]) => y);
      const outside = `p.x<=${f(Math.min(...xs))}||p.x>=${f(Math.max(...xs))}||p.y<=${f(Math.min(...ys))}||p.y>=${f(Math.max(...ys))}`;
      const edges = region.polygon
        .map((a, j) => {
          const b = region.polygon[(j + 1) % region.polygon.length];
          return `edge(p,${vec(a)},${vec(b)},d,inside);`;
        })
        .join('\n');
      return `float region${i}(vec2 p){if(${outside})return 0.;float d=10000.;bool inside=false;${edges}
      return inside?smoothstep(0.,12.,d)*smoothstep(${f(region.root)},${f(region.tip)},p.y):0.;}`;
    })
    .join('\n');
  const offsets = motionRegions[id]
    .map(
      (region, i) => `
    phase=time*.6544984695+${f(region.phase)}-(p.y-${f(region.root)})*.004;
    offset+=${vec(region.amplitude)}*vec2(sin(phase),sin(phase+.6))*region${i}(p);`,
    )
    .join('\n');
  return `
    void edge(vec2 p,vec2 a,vec2 b,inout float d,inout bool inside){
      vec2 v=b-a;float t=clamp(dot(p-a,v)/dot(v,v),0.,1.);d=min(d,length(p-a-v*t));
      if((a.y>p.y)!=(b.y>p.y)){if(p.x<(b.x-a.x)*(p.y-a.y)/(b.y-a.y)+a.x)inside=!inside;}
    }
    ${regions}
    vec2 motion(vec2 p){vec2 offset=vec2(0.);float phase;${offsets}return p-offset;}
  `;
}

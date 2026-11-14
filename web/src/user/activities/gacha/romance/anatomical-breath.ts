type Point = readonly [number, number];
type Cage = {
  size: Point;
  center: Point;
  boundary: readonly Point[];
  lift: Point;
  period: number;
  inhale: number;
};

// Sewn seams/underarms and the lower ribs are fixed landmarks. Only the inner
// ribcage vertex lifts. These are finite triangular bindings, not radial fields.
export const mirrorSeaBreath: Cage = {
  size: [1024, 1536],
  center: [494, 432],
  lift: [0, -2.1],
  period: 6.4,
  inhale: 2.25,
  boundary: [
    [392, 354],
    [431, 377],
    [482, 392],
    [532, 374],
    [590, 348],
    [608, 401],
    [600, 476],
    [563, 525],
    [486, 545],
    [420, 513],
    [395, 451],
  ],
};
export const elopementBreath: Cage = {
  size: [1024, 1536],
  center: [531, 457],
  lift: [-0.25, -1.9],
  period: 6.8,
  inhale: 2.4,
  boundary: [
    [422, 373],
    [474, 402],
    [533, 415],
    [584, 391],
    [627, 382],
    [649, 439],
    [633, 488],
    [584, 515],
    [543, 522],
    [486, 515],
    [431, 494],
    [410, 454],
  ],
};

const number = (value: number) => (Number.isInteger(value) ? value + '.' : String(value));
const vector = (point: Point) => `vec2(${point.map(number).join(',')})`;
export function anatomicalBreathShader(name: string, cage: Cage) {
  const triangles = cage.boundary
    .map(
      (point, i) =>
        `w=max(w,${name}Triangle(p,${vector(cage.center)},${vector(point)},${vector(cage.boundary[(i + 1) % cage.boundary.length])}));`,
    )
    .join('\n');
  return `
float ${name}Triangle(vec2 p,vec2 a,vec2 b,vec2 c){
 float d=(b.y-c.y)*(a.x-c.x)+(c.x-b.x)*(a.y-c.y);
 float u=((b.y-c.y)*(p.x-c.x)+(c.x-b.x)*(p.y-c.y))/d;
 float v=((c.y-a.y)*(p.x-c.x)+(a.x-c.x)*(p.y-c.y))/d;
 float q=1.-u-v;
 return u>=0.&&v>=0.&&q>=0. ? u : 0.;
}
vec2 ${name}(vec2 uv,float seconds){
 vec2 p=uv*${vector(cage.size)};float w=0.;${triangles}
 float t=mod(max(0.,seconds),${number(cage.period)});
 float breath=smoothstep(0.,${number(cage.inhale)},t)*(1.-smoothstep(${number(cage.inhale)},${number(cage.period)},t));
 return ${vector(cage.lift)}*w*breath/${vector(cage.size)};
}`;
}

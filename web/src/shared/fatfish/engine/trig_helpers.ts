import { SIN_TABLE } from "./trig";
import { FIELD_HEIGHT, FIELD_WIDTH, FISH_RADIUS, type Point, type Polygon } from "./types";
import { cross, validatePolygon } from "./geometry";

const SCALE = 1 << 20;
const MAX_SHAPE_DIMENSION = 2 * (FIELD_WIDTH + FIELD_HEIGHT);
const validCenter = (center: Point): boolean => Number.isSafeInteger(center.x) && Number.isSafeInteger(center.y) &&
  !Object.is(center.x, -0) && !Object.is(center.y, -0) && center.x >= 0 && center.x <= FIELD_WIDTH && center.y >= 0 && center.y <= FIELD_HEIGHT;
export function positiveMod(value: number, modulus: number): number { return ((value % modulus) + modulus) % modulus; }
export function sinCos(heading: number): [number, number] {
  return [SIN_TABLE[heading & 4095], SIN_TABLE[(heading + 1024) & 4095]];
}
export const FISH_OFFSETS: readonly Point[] = Array.from({ length: 64 }, (_, index) => {
  const [sine, cosine] = sinCos(index * 64);
  return { x: Math.trunc(FISH_RADIUS * cosine / SCALE), y: Math.trunc(FISH_RADIUS * sine / SCALE) };
});
export function fishFootprint(x: number, y: number): Point[] {
  return FISH_OFFSETS.map((point) => ({ x: x + point.x, y: y + point.y }));
}
function rotateAndTranslate(x: number, y: number, center: Point, heading: number): Point {
  const [sine, cosine] = sinCos(heading);
  return {
    x: center.x + Math.trunc((x * cosine - y * sine) / SCALE),
    y: center.y + Math.trunc((x * sine + y * cosine) / SCALE),
  };
}
function simplifyRing(points: Point[]): Point[] {
  const result = [...points];
  let changed = true;
  while (changed && result.length >= 3) {
    changed = false;
    for (let index = 0; index < result.length; index++) {
      const previous = result[(index + result.length - 1) % result.length];
      const current = result[index];
      const next = result[(index + 1) % result.length];
      if (current.x === previous.x && current.y === previous.y || current.x === next.x && current.y === next.y || cross(previous, current, next) === 0) {
        result.splice(index, 1);
        changed = true;
        break;
      }
    }
  }
  return result;
}
export function compileEllipse(center: Point, radiusX: number, radiusY: number, heading: number): Polygon {
  if (!validCenter(center) || !Number.isSafeInteger(radiusX) || !Number.isSafeInteger(radiusY) || radiusX < 64 || radiusY < 64 || radiusX > MAX_SHAPE_DIMENSION || radiusY > MAX_SHAPE_DIMENSION || !Number.isInteger(heading) || Object.is(heading, -0) || heading < 0 || heading > 4095) throw new Error("ellipse radii or heading are invalid");
  const outer = simplifyRing(Array.from({ length: 64 }, (_, index) => {
    const [sine, cosine] = sinCos(index * 64);
    return rotateAndTranslate(Math.trunc(radiusX * cosine / SCALE), Math.trunc(radiusY * sine / SCALE), center, heading);
  }));
  const polygon = { outer, holes: [] };
  validatePolygon(polygon, false);
  return polygon;
}
export function compileRotatedRectangle(center: Point, width: number, height: number, heading: number): Polygon {
  if (!validCenter(center) || !Number.isSafeInteger(width) || !Number.isSafeInteger(height) || width < 128 || height < 128 || width > MAX_SHAPE_DIMENSION || height > MAX_SHAPE_DIMENSION || !Number.isInteger(heading) || Object.is(heading, -0) || heading < 0 || heading > 4095) throw new Error("rectangle dimensions are invalid");
  const halfWidth = Math.trunc(width / 2), halfHeight = Math.trunc(height / 2);
  const outer = simplifyRing([
    { x: -halfWidth, y: -halfHeight }, { x: halfWidth, y: -halfHeight },
    { x: halfWidth, y: halfHeight }, { x: -halfWidth, y: halfHeight },
  ].map((point) => rotateAndTranslate(point.x, point.y, center, heading)));
  const polygon = { outer, holes: [] };
  validatePolygon(polygon, false);
  return polygon;
}
export function compileRoundedRectangle(center: Point, width: number, height: number, radius: number, heading: number): Polygon {
  if (!validCenter(center) || !Number.isSafeInteger(width) || !Number.isSafeInteger(height) || !Number.isSafeInteger(radius) || width < 128 || height < 128 || width > MAX_SHAPE_DIMENSION || height > MAX_SHAPE_DIMENSION || radius < 64 || radius > MAX_SHAPE_DIMENSION || radius * 2 > width || radius * 2 > height || !Number.isInteger(heading) || Object.is(heading, -0) || heading < 0 || heading > 4095) throw new Error("rounded rectangle dimensions are invalid");
  const halfWidth = Math.trunc(width / 2), halfHeight = Math.trunc(height / 2);
  const corners: Point[] = [
    { x: halfWidth - radius, y: -halfHeight + radius }, { x: halfWidth - radius, y: halfHeight - radius },
    { x: -halfWidth + radius, y: halfHeight - radius }, { x: -halfWidth + radius, y: -halfHeight + radius },
  ];
  const starts = [3072, 0, 1024, 2048];
  const points: Point[] = [];
  for (let cornerIndex = 0; cornerIndex < 4; cornerIndex++) {
    for (let step = 0; step < 16; step++) {
      const [sine, cosine] = sinCos((starts[cornerIndex] + step * 64) & 4095);
      const corner = corners[cornerIndex];
      const point = rotateAndTranslate(corner.x + Math.trunc(radius * cosine / SCALE), corner.y + Math.trunc(radius * sine / SCALE), center, heading);
      const previous = points.at(-1);
      if (!previous || previous.x !== point.x || previous.y !== point.y) points.push(point);
    }
  }
  const polygon = { outer: simplifyRing(points), holes: [] };
  validatePolygon(polygon, false);
  return polygon;
}

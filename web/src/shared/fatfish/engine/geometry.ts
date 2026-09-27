import { DRAG_BUFFER, FIELD_HEIGHT, FIELD_WIDTH, type Point, type Polygon } from "./types";

function absolute(value: bigint): bigint { return value < 0n ? -value : value; }
function gcd(a: bigint, b: bigint): bigint {
  a = absolute(a); b = absolute(b);
  while (b !== 0n) { const remainder = a % b; a = b; b = remainder; }
  return a === 0n ? 1n : a;
}
export class Rat {
  readonly n: bigint;
  readonly d: bigint;
  constructor(n: bigint, d = 1n) {
    if (d === 0n) throw new Error("zero rational denominator");
    if (d < 0n) { n = -n; d = -d; }
    const common = gcd(n, d);
    this.n = n / common; this.d = d / common;
  }
  static int(value: number): Rat { return new Rat(BigInt(value)); }
  add(other: Rat): Rat { return new Rat(this.n * other.d + other.n * this.d, this.d * other.d); }
  sub(other: Rat): Rat { return new Rat(this.n * other.d - other.n * this.d, this.d * other.d); }
  mul(other: Rat): Rat { return new Rat(this.n * other.n, this.d * other.d); }
  div(other: Rat): Rat { return new Rat(this.n * other.d, this.d * other.n); }
  cmp(other: Rat): number { const difference = this.n * other.d - other.n * this.d; return difference < 0n ? -1 : difference > 0n ? 1 : 0; }
  sign(): number { return this.n < 0n ? -1 : this.n > 0n ? 1 : 0; }
  key(): string { return `${this.n}/${this.d}`; }
}
export interface RatPoint { x: Rat; y: Rat }
export const ratPoint = (point: Point): RatPoint => ({ x: Rat.int(point.x), y: Rat.int(point.y) });

export function cross(a: Point, b: Point, c: Point): number {
  return (b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x);
}
function sign(value: number): number { return value < 0 ? -1 : value > 0 ? 1 : 0; }
export function onSegment(a: Point, b: Point, point: Point): boolean {
  return cross(a, b, point) === 0 && Math.min(a.x, b.x) <= point.x && point.x <= Math.max(a.x, b.x) && Math.min(a.y, b.y) <= point.y && point.y <= Math.max(a.y, b.y);
}
export function segmentsIntersect(a: Point, b: Point, c: Point, d: Point): boolean {
  const abC = cross(a, b, c), abD = cross(a, b, d), cdA = cross(c, d, a), cdB = cross(c, d, b);
  if (abC === 0 && onSegment(a, b, c) || abD === 0 && onSegment(a, b, d) || cdA === 0 && onSegment(c, d, a) || cdB === 0 && onSegment(c, d, b)) return true;
  return sign(abC) !== sign(abD) && sign(cdA) !== sign(cdB);
}
export function pointInRing(ring: Point[], point: Point): -1 | 0 | 1 {
  let inside = false;
  for (let index = 0; index < ring.length; index++) {
    const a = ring[index], b = ring[(index + 1) % ring.length];
    if (onSegment(a, b, point)) return 0;
    if ((a.y > point.y) !== (b.y > point.y) && (cross(a, b, point) > 0) === (b.y > a.y)) inside = !inside;
  }
  return inside ? 1 : -1;
}
export function containsPolygon(polygon: Polygon, point: Point): boolean {
  if (pointInRing(polygon.outer, point) < 0) return false;
  return !(polygon.holes ?? []).some((hole) => pointInRing(hole, point) >= 0);
}
export function ringsIntersect(first: Point[], second: Point[]): boolean {
  for (let firstIndex = 0; firstIndex < first.length; firstIndex++) {
    const a = first[firstIndex], b = first[(firstIndex + 1) % first.length];
    for (let secondIndex = 0; secondIndex < second.length; secondIndex++) {
      const c = second[secondIndex], d = second[(secondIndex + 1) % second.length];
      if (segmentsIntersect(a, b, c, d)) return true;
    }
  }
  return false;
}
export function translatePolygon(polygon: Polygon, dx: number, dy: number): Polygon {
  const translate = (point: Point): Point => ({ x: point.x + dx, y: point.y + dy });
  return { outer: polygon.outer.map(translate), holes: (polygon.holes ?? []).map((hole) => hole.map(translate)) };
}
export interface Bounds { minX: number; minY: number; maxX: number; maxY: number }
export function ringBounds(ring: Point[]): Bounds {
  let minX = ring[0].x, maxX = minX, minY = ring[0].y, maxY = minY;
  for (const point of ring) {
    minX = Math.min(minX, point.x); maxX = Math.max(maxX, point.x);
    minY = Math.min(minY, point.y); maxY = Math.max(maxY, point.y);
  }
  return { minX, minY, maxX, maxY };
}
export function boundsOverlap(a: Bounds, b: Bounds): boolean {
  return a.minX <= b.maxX && b.minX <= a.maxX && a.minY <= b.maxY && b.minY <= a.maxY;
}
export interface Segment { a: Point; b: Point }
export function ringSegments(ring: Point[]): Segment[] { return ring.map((point, index) => ({ a: point, b: ring[(index + 1) % ring.length] })); }
export function polygonSegments(polygon: Polygon): Segment[] { return [...ringSegments(polygon.outer), ...(polygon.holes ?? []).flatMap(ringSegments)]; }

export function validatePolygon(polygon: Polygon, local: boolean): void {
  const holes = polygon?.holes ?? [];
  if (!polygon || !Array.isArray(polygon.outer) || polygon.outer.length < 3 || polygon.outer.length > 128 || !Array.isArray(holes) || holes.length > 8) throw new Error("outer contour or hole count exceeds bounds");
  validateRing(polygon.outer, local);
  for (let index = 0; index < holes.length; index++) {
    const hole = holes[index];
    if (!Array.isArray(hole) || hole.length < 3 || hole.length > 64) throw new Error(`hole[${index}] vertex count exceeds bounds`);
    validateRing(hole, local);
    for (let vertex = 0; vertex < hole.length; vertex++) if (pointInRing(polygon.outer, hole[vertex]) !== 1) throw new Error(`hole[${index}] vertex[${vertex}] is outside the outer contour`);
    if (ringsIntersect(polygon.outer, hole)) throw new Error(`hole[${index}] touches the outer contour`);
    if (ringsTooClose(polygon.outer, hole)) throw new Error(`hole[${index}] leaves a feature thinner than two pixels`);
    for (let previous = 0; previous < index; previous++) {
      const earlier = holes[previous];
      if (ringsIntersect(earlier, hole) || pointInRing(earlier, hole[0]) >= 0 || pointInRing(hole, earlier[0]) >= 0) throw new Error(`hole[${index}] overlaps hole[${previous}]`);
      if (ringsTooClose(earlier, hole)) throw new Error(`hole[${index}] leaves a feature thinner than two pixels beside hole[${previous}]`);
    }
  }
}
function validateRing(ring: Point[], local: boolean): void {
  let area2 = 0;
  for (let index = 0; index < ring.length; index++) {
    const point = ring[index], next = ring[(index + 1) % ring.length], previous = ring[(index + ring.length - 1) % ring.length];
    const minimum = local ? -DRAG_BUFFER : 0;
    const maxX = local ? DRAG_BUFFER : FIELD_WIDTH, maxY = local ? DRAG_BUFFER : FIELD_HEIGHT;
    if (!Number.isSafeInteger(point.x) || !Number.isSafeInteger(point.y) || Object.is(point.x, -0) || Object.is(point.y, -0) || point.x < minimum || point.x > maxX || point.y < minimum || point.y > maxY) throw new Error(`vertex[${index}] is outside the field or local bounds`);
    if (point.x === next.x && point.y === next.y) throw new Error(`edge[${index}] has zero length`);
    if (cross(previous, point, next) === 0) throw new Error(`vertex[${index}] is collinear`);
    area2 += point.x * next.y - next.x * point.y;
  }
  if (area2 === 0) throw new Error("contour has zero area");
  for (let first = 0; first < ring.length; first++) {
    for (let second = first + 1; second < ring.length; second++) {
      if (second === first + 1 || first === 0 && second === ring.length - 1) continue;
      if (segmentsIntersect(ring[first], ring[(first + 1) % ring.length], ring[second], ring[(second + 1) % ring.length])) throw new Error(`edge[${first}] intersects edge[${second}]`);
      const a = ring[first], b = ring[(first + 1) % ring.length], c = ring[second], d = ring[(second + 1) % ring.length];
      if ((b.x - a.x) * (d.x - c.x) + (b.y - a.y) * (d.y - c.y) < 0 && segmentsCloserThanTwoPixels(a, b, c, d)) throw new Error(`edge[${first}] faces edge[${second}] across a feature thinner than two pixels`);
    }
  }
  if (convexRing(ring) && convexWidthBelowTwoPixels(ring)) throw new Error("convex contour is thinner than two pixels");
  if (triangulate(ring).length !== ring.length - 2) throw new Error("contour cannot be decomposed into convex triangles");
}

function convexRing(ring: Point[]): boolean {
  const orientation = sign(cross(ring[ring.length - 1], ring[0], ring[1]));
  return ring.every((point, index) => sign(cross(point, ring[(index + 1) % ring.length], ring[(index + 2) % ring.length])) === orientation);
}
function convexWidthBelowTwoPixels(ring: Point[]): boolean {
  for (let index = 0; index < ring.length; index++) {
    const a = ring[index], b = ring[(index + 1) % ring.length];
    const dx = b.x - a.x, dy = b.y - a.y, lengthSquared = dx * dx + dy * dy;
    let largest = 0;
    for (const point of ring) largest = Math.max(largest, Math.abs(cross(a, b, point)));
    if (BigInt(largest) ** 2n < BigInt(lengthSquared) * BigInt((2 * 64) ** 2)) return true;
  }
  return false;
}

function ringsTooClose(first: Point[], second: Point[]): boolean {
  for (const a of ringSegments(first)) for (const b of ringSegments(second)) if (segmentsCloserThanTwoPixels(a.a, a.b, b.a, b.b)) return true;
  return false;
}
function segmentsCloserThanTwoPixels(a: Point, b: Point, c: Point, d: Point): boolean {
  const first = ringBounds([a, b]), second = ringBounds([c, d]), limit = 2 * 64;
  if (first.maxX + limit <= second.minX || second.maxX + limit <= first.minX || first.maxY + limit <= second.minY || second.maxY + limit <= first.minY) return false;
  return pointCloserThanTwoPixels(a, c, d) || pointCloserThanTwoPixels(b, c, d) || pointCloserThanTwoPixels(c, a, b) || pointCloserThanTwoPixels(d, a, b);
}
function pointCloserThanTwoPixels(point: Point, a: Point, b: Point): boolean {
  const dx = b.x - a.x, dy = b.y - a.y, dot = (point.x - a.x) * dx + (point.y - a.y) * dy, lengthSquared = dx * dx + dy * dy;
  const threshold = (2 * 64) ** 2;
  if (dot <= 0) return (point.x - a.x) ** 2 + (point.y - a.y) ** 2 < threshold;
  if (dot >= lengthSquared) return (point.x - b.x) ** 2 + (point.y - b.y) ** 2 < threshold;
  const crossValue = dx * (point.y - a.y) - dy * (point.x - a.x);
  return BigInt(crossValue) ** 2n < BigInt(lengthSquared) * BigInt(threshold);
}

function pointInTriangle(a: Point, b: Point, c: Point, point: Point): boolean {
  const ab = cross(a, b, point), bc = cross(b, c, point), ca = cross(c, a, point);
  return ab >= 0 && bc >= 0 && ca >= 0 || ab <= 0 && bc <= 0 && ca <= 0;
}
export function triangulate(ring: Point[]): [Point, Point, Point][] {
  if (ring.length < 3) return [];
  let orientation = 0;
  for (let index = 0; index < ring.length; index++) orientation += ring[index].x * ring[(index + 1) % ring.length].y - ring[(index + 1) % ring.length].x * ring[index].y;
  const indices = ring.map((_, index) => index);
  const triangles: [Point, Point, Point][] = [];
  while (indices.length > 3) {
    let found = false;
    for (let position = 0; position < indices.length; position++) {
      const previous = indices[(position + indices.length - 1) % indices.length], middle = indices[position], next = indices[(position + 1) % indices.length];
      const a = ring[previous], b = ring[middle], c = ring[next];
      const turn = cross(a, b, c);
      if (turn === 0 || sign(turn) !== sign(orientation)) continue;
      if (indices.some((candidate) => candidate !== previous && candidate !== middle && candidate !== next && pointInTriangle(a, b, c, ring[candidate]))) continue;
      triangles.push([a, b, c]);
      indices.splice(position, 1);
      found = true;
      break;
    }
    if (!found) throw new Error("polygon cannot be decomposed into convex triangles");
  }
  triangles.push([ring[indices[0]], ring[indices[1]], ring[indices[2]]]);
  return triangles;
}

function rationalCross(a: Point, b: Point, point: RatPoint): Rat {
  return Rat.int(b.x - a.x).mul(point.y.sub(Rat.int(a.y))).sub(Rat.int(b.y - a.y).mul(point.x.sub(Rat.int(a.x))));
}
function clipAgainstEdge(subject: RatPoint[], a: Point, b: Point, orientation: number): RatPoint[] {
  if (subject.length === 0) return [];
  const output: RatPoint[] = [];
  let previous = subject[subject.length - 1], previousSide = rationalCross(a, b, previous);
  for (const current of subject) {
    const currentSide = rationalCross(a, b, current);
    const previousInside = previousSide.sign() * orientation >= 0, currentInside = currentSide.sign() * orientation >= 0;
    if (previousInside !== currentInside) {
      const fraction = previousSide.div(previousSide.sub(currentSide));
      output.push({ x: previous.x.add(current.x.sub(previous.x).mul(fraction)), y: previous.y.add(current.y.sub(previous.y).mul(fraction)) });
    }
    if (currentInside) output.push(current);
    previous = current; previousSide = currentSide;
  }
  return output;
}
export function rationalArea(points: RatPoint[]): Rat {
  let area2 = Rat.int(0);
  for (let index = 0; index < points.length; index++) {
    const point = points[index], next = points[(index + 1) % points.length];
    area2 = area2.add(point.x.mul(next.y).sub(next.x.mul(point.y)));
  }
  return new Rat(absolute(area2.n), area2.d * 2n);
}
export function triangleIntersectionArea(subject: Point[], triangle: [Point, Point, Point]): Rat {
  if (!boundsOverlap(ringBounds(subject), ringBounds(triangle))) return Rat.int(0);
  let polygon = subject.map(ratPoint);
  const orientation = sign(cross(triangle[0], triangle[1], triangle[2]));
  for (let index = 0; index < 3; index++) {
    polygon = clipAgainstEdge(polygon, triangle[index], triangle[(index + 1) % 3], orientation);
    if (polygon.length < 3) return Rat.int(0);
  }
  return rationalArea(polygon);
}
export function polygonIntersectionArea(subject: Point[], polygon: Polygon): Rat {
  if (!boundsOverlap(ringBounds(subject), ringBounds(polygon.outer))) return Rat.int(0);
  let area = Rat.int(0);
  for (const triangle of triangulate(polygon.outer)) area = area.add(triangleIntersectionArea(subject, triangle));
  for (const hole of polygon.holes ?? []) for (const triangle of triangulate(hole)) area = area.sub(triangleIntersectionArea(subject, triangle));
  return area;
}
export function polygonsInteriorOverlap(first: Polygon, second: Polygon): boolean {
  let area = Rat.int(0);
  for (const triangle of triangulate(first.outer)) area = area.add(polygonIntersectionArea(triangle, second));
  for (const hole of first.holes ?? []) for (const triangle of triangulate(hole)) area = area.sub(polygonIntersectionArea(triangle, second));
  return area.sign() > 0;
}
export function projectPointToSegment(point: Point, a: Point, b: Point): RatPoint {
  const dx = b.x - a.x, dy = b.y - a.y, length2 = dx * dx + dy * dy;
  if (length2 === 0) return ratPoint(a);
  const numerator = (point.x - a.x) * dx + (point.y - a.y) * dy;
  if (numerator <= 0) return ratPoint(a);
  if (numerator >= length2) return ratPoint(b);
  const fraction = new Rat(BigInt(numerator), BigInt(length2));
  return { x: Rat.int(a.x).add(Rat.int(dx).mul(fraction)), y: Rat.int(a.y).add(Rat.int(dy).mul(fraction)) };
}

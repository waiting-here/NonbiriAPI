import { boundsOverlap, onSegment, polygonSegments, Rat, ringBounds, ringSegments, type RatPoint, type Segment } from "./geometry";
import type { Bowl, BowlState, Point, Polygon, Shape } from "./types";

function pointAt(a: Point, b: Point, fraction: Rat): RatPoint {
  return { x: Rat.int(a.x).add(Rat.int(b.x - a.x).mul(fraction)), y: Rat.int(a.y).add(Rat.int(b.y - a.y).mul(fraction)) };
}
function rationalCross(a: Point, b: Point, point: RatPoint): Rat {
  return Rat.int(b.x - a.x).mul(point.y.sub(Rat.int(a.y))).sub(Rat.int(b.y - a.y).mul(point.x.sub(Rat.int(a.x))));
}
function pointInRingRat(ring: Point[], point: RatPoint): -1 | 0 | 1 {
  let inside = false;
  for (const { a, b } of ringSegments(ring)) {
    const orientation = rationalCross(a, b, point);
    if (orientation.sign() === 0 && point.x.cmp(Rat.int(Math.min(a.x, b.x))) >= 0 && point.x.cmp(Rat.int(Math.max(a.x, b.x))) <= 0 && point.y.cmp(Rat.int(Math.min(a.y, b.y))) >= 0 && point.y.cmp(Rat.int(Math.max(a.y, b.y))) <= 0) return 0;
    if ((point.y.cmp(Rat.int(a.y)) < 0) !== (point.y.cmp(Rat.int(b.y)) < 0) && (orientation.sign() > 0) === (b.y > a.y)) inside = !inside;
  }
  return inside ? 1 : -1;
}
function containsPolygonRat(polygon: Polygon, point: RatPoint): boolean {
  return pointInRingRat(polygon.outer, point) >= 0 && !polygon.holes.some((hole) => pointInRingRat(hole, point) >= 0);
}
function segmentCrossT(first: Segment, second: Segment): Rat | null {
  const { a, b } = first, { a: c, b: d } = second;
  if (!boundsOverlap(ringBounds([a, b]), ringBounds([c, d]))) return null;
  const rx = b.x - a.x, ry = b.y - a.y, sx = d.x - c.x, sy = d.y - c.y;
  let denominator = rx * sy - ry * sx;
  if (denominator === 0) return null;
  const qx = c.x - a.x, qy = c.y - a.y;
  let tNumerator = qx * sy - qy * sx, uNumerator = qx * ry - qy * rx;
  if (denominator < 0) { denominator = -denominator; tNumerator = -tNumerator; uNumerator = -uNumerator; }
  if (tNumerator < 0 || tNumerator > denominator || uNumerator < 0 || uNumerator > denominator) return null;
  return new Rat(BigInt(tNumerator), BigInt(denominator));
}
function pointParameter(path: Segment, point: Point): Rat {
  if (path.a.x !== path.b.x) return new Rat(BigInt(point.x - path.a.x), BigInt(path.b.x - path.a.x));
  if (path.a.y !== path.b.y) return new Rat(BigInt(point.y - path.a.y), BigInt(path.b.y - path.a.y));
  return Rat.int(0);
}
function contourParameters(path: Segment, polygon: Polygon): Rat[] {
  const parameters: Rat[] = [];
  for (const edge of polygonSegments(polygon)) {
    const crossing = segmentCrossT(path, edge);
    if (crossing) parameters.push(crossing);
    if (onSegment(path.a, path.b, edge.a)) parameters.push(pointParameter(path, edge.a));
    if (onSegment(path.a, path.b, edge.b)) parameters.push(pointParameter(path, edge.b));
  }
  return parameters;
}
export interface SweptContact { kind: "fed" | "lost" | ""; id: number }
export function firstSweptContact(from: Point, to: Point, hazards: Shape[], bowls: Bowl[], counts: BowlState[], solids: Polygon[]): SweptContact {
  const empty: SweptContact = { kind: "", id: 0 };
  const path: Segment = { a: from, b: to }, pathBounds = ringBounds([from, to]);
  const possibleHazards = hazards.filter((item) => boundsOverlap(pathBounds, ringBounds(item.polygon.outer)));
  const possibleBowls = bowls.filter((item) => boundsOverlap(pathBounds, ringBounds(item.polygon.outer)));
  if (possibleHazards.length === 0 && possibleBowls.length === 0) return empty;
  const possibleSolids = solids.filter((polygon) => boundsOverlap(pathBounds, ringBounds(polygon.outer)));
  const parameters = new Map<string, Rat>([[Rat.int(0).key(), Rat.int(0)], [Rat.int(1).key(), Rat.int(1)]]);
  for (const polygon of [...possibleHazards.map((item) => item.polygon), ...possibleBowls.map((item) => item.polygon), ...possibleSolids]) {
    for (const parameter of contourParameters(path, polygon)) parameters.set(parameter.key(), parameter);
  }
  const ordered = Array.from(parameters.values()).sort((a, b) => a.cmp(b));
  function evaluate(at: Rat): SweptContact {
    const point = pointAt(from, to, at);
    if (possibleSolids.some((polygon) => containsPolygonRat(polygon, point))) return empty;
    for (const hazard of possibleHazards) if (containsPolygonRat(hazard.polygon, point)) return { kind: "lost", id: hazard.id };
    for (const bowl of possibleBowls) {
      const count = counts.find((item) => item.id === bowl.id)?.count ?? 0;
      if (count < bowl.capacity && containsPolygonRat(bowl.polygon, point)) return { kind: "fed", id: bowl.id };
    }
    return empty;
  }
  for (let index = 0; index < ordered.length; index++) {
    const contact = evaluate(ordered[index]);
    if (contact.kind) return contact;
    if (index + 1 < ordered.length && ordered[index].cmp(ordered[index + 1]) < 0) {
      const interior = evaluate(ordered[index].add(ordered[index + 1]).div(Rat.int(2)));
      if (interior.kind) return interior;
    }
  }
  return empty;
}

import { boundsOverlap, polygonIntersectionArea, polygonSegments, Rat, rationalArea, ratPoint, ringBounds, ringSegments, type Segment } from "./geometry";
import type { Point, Polygon } from "./types";

interface Interval { low: Rat; high: Rat }
function segmentCrossX(first: Segment, second: Segment): Rat | null {
  const { a, b } = first, { a: c, b: d } = second;
  if (!boundsOverlap(ringBounds([a, b]), ringBounds([c, d]))) return null;
  const rx = b.x - a.x, ry = b.y - a.y, sx = d.x - c.x, sy = d.y - c.y;
  let denominator = rx * sy - ry * sx;
  if (denominator === 0) return null;
  const qx = c.x - a.x, qy = c.y - a.y;
  let tNumerator = qx * sy - qy * sx, uNumerator = qx * ry - qy * rx;
  if (denominator < 0) { denominator = -denominator; tNumerator = -tNumerator; uNumerator = -uNumerator; }
  if (tNumerator < 0 || tNumerator > denominator || uNumerator < 0 || uNumerator > denominator) return null;
  return Rat.int(a.x).add(Rat.int(rx).mul(new Rat(BigInt(tNumerator), BigInt(denominator))));
}
function ringIntervalsAtX(ring: Point[], x: Rat): Interval[] {
  const values: Rat[] = [];
  for (const { a, b } of ringSegments(ring)) {
    if (a.x === b.x || x.cmp(Rat.int(Math.min(a.x, b.x))) <= 0 || x.cmp(Rat.int(Math.max(a.x, b.x))) >= 0) continue;
    const fraction = x.sub(Rat.int(a.x)).div(Rat.int(b.x - a.x));
    values.push(Rat.int(a.y).add(fraction.mul(Rat.int(b.y - a.y))));
  }
  values.sort((a, b) => a.cmp(b));
  const intervals: Interval[] = [];
  for (let index = 0; index + 1 < values.length; index += 2) {
    if (values[index].cmp(values[index + 1]) < 0) intervals.push({ low: values[index], high: values[index + 1] });
  }
  return intervals;
}
function subtractIntervals(source: Interval[], cuts: Interval[]): Interval[] {
  let result = source;
  for (const cut of cuts) {
    const remaining: Interval[] = [];
    for (const part of result) {
      if (cut.high.cmp(part.low) <= 0 || cut.low.cmp(part.high) >= 0) { remaining.push(part); continue; }
      if (cut.low.cmp(part.low) > 0) remaining.push({ low: part.low, high: cut.low });
      if (cut.high.cmp(part.high) < 0) remaining.push({ low: cut.high, high: part.high });
    }
    result = remaining;
  }
  return result;
}
function polygonIntervalsAtX(polygon: Polygon, x: Rat): Interval[] {
  let intervals = ringIntervalsAtX(polygon.outer, x);
  for (const hole of polygon.holes) intervals = subtractIntervals(intervals, ringIntervalsAtX(hole, x));
  return intervals;
}
function unionIntervals(parts: Interval[]): Interval[] {
  if (parts.length < 2) return parts;
  parts.sort((a, b) => a.low.cmp(b.low) || a.high.cmp(b.high));
  const merged: Interval[] = [{ ...parts[0] }];
  for (const part of parts.slice(1)) {
    const last = merged[merged.length - 1];
    if (part.low.cmp(last.high) <= 0) { if (part.high.cmp(last.high) > 0) last.high = part.high; }
    else merged.push({ ...part });
  }
  return merged;
}

// Exact positive-area polygon difference emptiness. Every vertex and crossing
// x-coordinate partitions the input into slabs with fixed interval ordering.
export function unionCoversFish(fish: Point[], solids: Polygon[]): boolean {
  if (solids.length === 0) return false;
  const fishBounds = ringBounds(fish);
  const edges = ringSegments(fish);
  const breaks = new Map<string, Rat>();
  const addBreak = (x: Rat) => { if (x.cmp(Rat.int(fishBounds.minX)) >= 0 && x.cmp(Rat.int(fishBounds.maxX)) <= 0) breaks.set(x.key(), x); };
  addBreak(Rat.int(fishBounds.minX)); addBreak(Rat.int(fishBounds.maxX));
  for (const solid of solids) {
    if (!boundsOverlap(fishBounds, ringBounds(solid.outer))) continue;
    for (const edge of polygonSegments(solid)) { edges.push(edge); addBreak(Rat.int(edge.a.x)); addBreak(Rat.int(edge.b.x)); }
  }
  for (const point of fish) addBreak(Rat.int(point.x));
  for (let first = 0; first < edges.length; first++) {
    for (let second = first + 1; second < edges.length; second++) {
      const x = segmentCrossX(edges[first], edges[second]);
      if (x) addBreak(x);
    }
  }
  const values = Array.from(breaks.values()).sort((a, b) => a.cmp(b));
  for (let index = 0; index + 1 < values.length; index++) {
    if (values[index].cmp(values[index + 1]) === 0) continue;
    const midpoint = values[index].add(values[index + 1]).div(Rat.int(2));
    const fishSections = ringIntervalsAtX(fish, midpoint);
    if (fishSections.length === 0) continue;
    const solidSections: Interval[] = [];
    for (const solid of solids) {
      const bounds = ringBounds(solid.outer);
      if (midpoint.cmp(Rat.int(bounds.minX)) > 0 && midpoint.cmp(Rat.int(bounds.maxX)) < 0) solidSections.push(...polygonIntervalsAtX(solid, midpoint));
    }
    if (subtractIntervals(fishSections, unionIntervals(solidSections)).length !== 0) return false;
  }
  return true;
}

export interface OverlapResult { areas: Rat[]; full: boolean }
export function footprintOverlap(fish: Point[], solids: Polygon[]): OverlapResult {
  const areas = solids.map((solid) => polygonIntersectionArea(fish, solid));
  const fishArea = rationalArea(fish.map(ratPoint));
  let sum = Rat.int(0);
  const intersecting: Polygon[] = [];
  for (let index = 0; index < areas.length; index++) {
    if (areas[index].sign() > 0) { sum = sum.add(areas[index]); intersecting.push(solids[index]); }
  }
  if (sum.cmp(fishArea) < 0) return { areas, full: false };
  if (intersecting.length === 1) return { areas, full: areas.some((area) => area.cmp(fishArea) === 0) };
  return { areas, full: unionCoversFish(fish, intersecting) };
}

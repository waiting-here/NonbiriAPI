import { boundsOverlap, Rat, ringBounds, ringsIntersect, triangleIntersectionArea, triangulate, type Bounds } from "./geometry";
import { unionCoversFish, type OverlapResult } from "./geometry_union";
import { FISH_OFFSETS } from "./trig_helpers";
import { FISH_RADIUS, type Point, type Polygon } from "./types";

export interface PreparedPolygon {
  polygon: Polygon;
  bounds: Bounds;
  outer: [Point, Point, Point][];
  holes: [Point, Point, Point][][];
  rect: boolean;
}

export function preparePolygon(polygon: Polygon): PreparedPolygon {
  const bounds = ringBounds(polygon.outer);
  const corners = new Set(polygon.outer.map((point) => `${point.x},${point.y}`));
  const rect = polygon.holes.length === 0 && polygon.outer.length === 4 && corners.size === 4 &&
    [`${bounds.minX},${bounds.minY}`, `${bounds.maxX},${bounds.minY}`, `${bounds.maxX},${bounds.maxY}`, `${bounds.minX},${bounds.maxY}`].every((corner) => corners.has(corner));
  return { polygon, bounds, outer: triangulate(polygon.outer), holes: polygon.holes.map(triangulate), rect };
}

function projection(points: Point[], axisX: number, axisY: number): [number, number] {
  let minimum = points[0].x * axisX + points[0].y * axisY, maximum = minimum;
  for (const point of points.slice(1)) {
    const value = point.x * axisX + point.y * axisY;
    minimum = Math.min(minimum, value); maximum = Math.max(maximum, value);
  }
  return [minimum, maximum];
}

function convexOverlap(first: Point[], second: Point[], positiveOnly: boolean): boolean {
  if (!boundsOverlap(ringBounds(first), ringBounds(second))) return false;
  for (const polygon of [first, second]) {
    for (let index = 0; index < polygon.length; index++) {
      const a = polygon[index], b = polygon[(index + 1) % polygon.length];
      const axisX = b.y - a.y, axisY = a.x - b.x;
      const [firstMin, firstMax] = projection(first, axisX, axisY), [secondMin, secondMax] = projection(second, axisX, axisY);
      if (positiveOnly ? firstMax <= secondMin || secondMax <= firstMin : firstMax < secondMin || secondMax < firstMin) return false;
    }
  }
  return true;
}

function rectIntersectsFish(fish: Point[], rectangle: Bounds, positiveOnly: boolean): boolean {
  const fishBounds = ringBounds(fish);
  if (positiveOnly ? fishBounds.maxX <= rectangle.minX || rectangle.maxX <= fishBounds.minX || fishBounds.maxY <= rectangle.minY || rectangle.maxY <= fishBounds.minY : !boundsOverlap(fishBounds, rectangle)) return false;
  for (let index = 0; index < fish.length; index++) {
    const a = fish[index], b = fish[(index + 1) % fish.length];
    const dx = b.x - a.x, dy = b.y - a.y;
    const x = dy > 0 ? rectangle.minX : rectangle.maxX, y = dx > 0 ? rectangle.maxY : rectangle.minY;
    const value = dx * (y - a.y) - dy * (x - a.x);
    if (positiveOnly ? value <= 0 : value < 0) return false;
  }
  return true;
}

export function rectIntersectsCenter(x: number, y: number, rectangle: Bounds, positiveOnly: boolean): boolean {
  const fishBounds = { minX: x - FISH_RADIUS, minY: y - FISH_RADIUS, maxX: x + FISH_RADIUS, maxY: y + FISH_RADIUS };
  if (positiveOnly ? fishBounds.maxX <= rectangle.minX || rectangle.maxX <= fishBounds.minX || fishBounds.maxY <= rectangle.minY || rectangle.maxY <= fishBounds.minY : !boundsOverlap(fishBounds, rectangle)) return false;
  for (let index = 0; index < FISH_OFFSETS.length; index++) {
    const a = FISH_OFFSETS[index], b = FISH_OFFSETS[(index + 1) % FISH_OFFSETS.length];
    const dx = b.x - a.x, dy = b.y - a.y;
    const xProbe = (dy > 0 ? rectangle.minX : rectangle.maxX) - x;
    const yProbe = (dx > 0 ? rectangle.maxY : rectangle.minY) - y;
    const value = dx * (yProbe - a.y) - dy * (xProbe - a.x);
    if (positiveOnly ? value <= 0 : value < 0) return false;
  }
  return true;
}

export function preparedIntersectionArea(fish: Point[], solid: PreparedPolygon): Rat {
  if (!boundsOverlap(ringBounds(fish), solid.bounds)) return Rat.int(0);
  let area = Rat.int(0);
  for (const triangle of solid.outer) area = area.add(triangleIntersectionArea(fish, triangle));
  for (const hole of solid.holes) for (const triangle of hole) area = area.sub(triangleIntersectionArea(fish, triangle));
  return area;
}

export function preparedIntersectsFish(fish: Point[], fishBounds: Bounds, solid: PreparedPolygon, positiveOnly: boolean): boolean {
  if (!boundsOverlap(fishBounds, solid.bounds)) return false;
  if (solid.rect) return rectIntersectsFish(fish, solid.bounds, positiveOnly);
  for (const triangle of solid.outer) {
    if (convexOverlap(fish, triangle, positiveOnly)) {
      if (solid.holes.length === 0) return true;
      if (preparedIntersectionArea(fish, solid).sign() > 0) return true;
      return !positiveOnly && ringsIntersect(fish, solid.polygon.outer);
    }
  }
  return false;
}

export function preparedFootprintOverlap(fish: Point[], solids: PreparedPolygon[]): OverlapResult {
  const fishBounds = ringBounds(fish), areas = solids.map(() => Rat.int(0)), intersecting: Polygon[] = [];
  let total = Rat.int(0);
  for (let index = 0; index < solids.length; index++) {
    if (!preparedIntersectsFish(fish, fishBounds, solids[index], true)) continue;
    areas[index] = preparedIntersectionArea(fish, solids[index]);
    if (areas[index].sign() > 0) { total = total.add(areas[index]); intersecting.push(solids[index].polygon); }
  }
  if (intersecting.length === 0) return { areas, full: false };
  let twiceArea = 0;
  for (let index = 0; index < fish.length; index++) twiceArea += fish[index].x * fish[(index + 1) % fish.length].y - fish[(index + 1) % fish.length].x * fish[index].y;
  const fishArea = new Rat(BigInt(Math.abs(twiceArea)), 2n);
  if (total.cmp(fishArea) < 0) return { areas, full: false };
  if (intersecting.length === 1) return { areas, full: total.cmp(fishArea) === 0 };
  return { areas, full: unionCoversFish(fish, intersecting) };
}

export function extendBounds(current: Bounds | null, next: Bounds): Bounds {
  if (current === null) return next;
  return { minX: Math.min(current.minX, next.minX), minY: Math.min(current.minY, next.minY), maxX: Math.max(current.maxX, next.maxX), maxY: Math.max(current.maxY, next.maxY) };
}

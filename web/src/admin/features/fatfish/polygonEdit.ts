import { validatePolygon } from '@shared/fatfish/engine/geometry';
import type { Level, Point, Polygon } from '@shared/fatfish/engine/types';
import { cloneLevel, type Selection } from './draft';

export interface Vertex { hole: number | null; index: number }
export function selectedPolygon(level: Level, selection: Selection | null): Polygon | null {
  if (!selection || selection.kind === 'fish') return null;
  return level[selection.kind].find((shape) => shape.id === selection.id)?.polygon ?? null;
}
export function polygonEdit(level: Level, selection: Selection, edit: (polygon: Polygon) => void): Level {
  const next = cloneLevel(level), polygon = selectedPolygon(next, selection);
  if (!polygon) throw new Error('Select a shape first.');
  edit(polygon);
  return next;
}
export function polygonError(level: Level, selection: Selection): string | null {
  const polygon = selectedPolygon(level, selection);
  if (!polygon) return 'Select a shape first.';
  try { validatePolygon(polygon, selection.kind === 'tools'); return null; }
  catch (error) { return error instanceof Error ? error.message : String(error); }
}
export function ringFor(polygon: Polygon, vertex: Vertex): Point[] {
  return vertex.hole === null ? polygon.outer : polygon.holes[vertex.hole];
}
export function insertVertex(level: Level, selection: Selection, vertex: Vertex, point: Point): Level {
  return polygonEdit(level, selection, (polygon) => {
    const ring = ringFor(polygon, vertex);
    if (ring.length >= (vertex.hole === null ? 128 : 64)) throw new Error('The vertex limit has been reached.');
    ring.splice(vertex.index + 1, 0, point);
  });
}
export function removeVertex(level: Level, selection: Selection, vertex: Vertex): Level {
  return polygonEdit(level, selection, (polygon) => {
    const ring = ringFor(polygon, vertex);
    if (ring.length <= 3) throw new Error('A contour needs at least three vertices.');
    ring.splice(vertex.index, 1);
  });
}

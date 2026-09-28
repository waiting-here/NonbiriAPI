import { DRAG_BUFFER, FIELD_HEIGHT, FIELD_WIDTH, type Level, type Point, type ToolState } from './engine/types';

export const WORKSPACE_LEFT = -DRAG_BUFFER;
export const WORKSPACE_TOP = -DRAG_BUFFER;
export const WORKSPACE_WIDTH = FIELD_WIDTH + DRAG_BUFFER * 2;
export const WORKSPACE_HEIGHT = FIELD_HEIGHT + DRAG_BUFFER * 2;
export const WORKSPACE_VIEW_BOX = [WORKSPACE_LEFT, WORKSPACE_TOP, WORKSPACE_WIDTH, WORKSPACE_HEIGHT].map((n) => n / 64).join(' ');

export function workspacePoint(clientX: number, clientY: number, rect: Pick<DOMRect, 'left' | 'top' | 'width' | 'height'>): Point {
  return {
    x: Math.round((clientX - rect.left) * WORKSPACE_WIDTH / rect.width + WORKSPACE_LEFT),
    y: Math.round((clientY - rect.top) * WORKSPACE_HEIGHT / rect.height + WORKSPACE_TOP),
  };
}

export function clampWorkspace(point: Point): Point {
  return { x: Math.max(-DRAG_BUFFER, Math.min(FIELD_WIDTH + DRAG_BUFFER, point.x)),
    y: Math.max(-DRAG_BUFFER, Math.min(FIELD_HEIGHT + DRAG_BUFFER, point.y)) };
}

export function inField(point: Point): boolean {
  return point.x >= 0 && point.x <= FIELD_WIDTH && point.y >= 0 && point.y <= FIELD_HEIGHT;
}

// Older unplaced pieces have field coordinates, although they were displayed
// in a separate tray. Give only those pieces a deterministic initial bench spot.
export function toolPosition(tool: Level['tools'][number], index: number, current?: ToolState): Point {
  if (current?.placed) return { x: current.x, y: current.y };
  if ((!current && tool.placed) || (!tool.placed && !inField(tool))) return { x: tool.x, y: tool.y };
  if (index < 5) return { x: (32 + index * 104) * 64, y: 624 * 64 };
  if (index < 10) return { x: (32 + (index - 5) * 104) * 64, y: -64 * 64 };
  const side = index - 10;
  return { x: (side < 7 ? -64 : 544) * 64, y: (32 + side % 7 * 80) * 64 };
}

export function laidOutLevel(level: Level): Level {
  const ordered = [...level.tools].sort((a, b) => a.id - b.id);
  return { ...level, tools: level.tools.map((tool) => ({ ...tool, ...toolPosition(tool, ordered.indexOf(tool)) })) };
}

import { containsPolygon, translatePolygon } from '@shared/fatfish/engine/geometry';
import { compileEllipse, compileRoundedRectangle } from '@shared/fatfish/engine/trig_helpers';
import {
  ENGINE_VERSION, FIELD_HEIGHT, FIELD_WIDTH, FISH_RADIUS, MAX_LEVEL_BYTES,
  type Level, type Point, type Polygon,
} from '@shared/fatfish/engine/types';
import { validateLevel } from '@shared/fatfish/engine/validate';
import { clampWorkspace } from '@shared/fatfish/workspace';

export type ObjectKind = 'fish' | 'tools' | 'solids' | 'hazards' | 'bowls' | 'switches' | 'gates' | 'directions';
export interface Selection { kind: ObjectKind; id: number }
export const GRID = 8 * 64;
export const px = (value: number) => Math.round(value / 64);
export const unit = (value: number) => Math.round(value * 64);
export const utf8Bytes = (value: string) => new TextEncoder().encode(value).byteLength;
export const snap = (value: number, enabled: boolean) => enabled ? Math.round(value / GRID) * GRID : Math.round(value);
export const rect = (x: number, y: number, width: number, height: number): Polygon => ({
  outer: [
    { x: x - width / 2, y: y - height / 2 },
    { x: x + width / 2, y: y - height / 2 },
    { x: x + width / 2, y: y + height / 2 },
    { x: x - width / 2, y: y + height / 2 },
  ].map((point) => ({ x: Math.round(point.x), y: Math.round(point.y) })), holes: [],
});
export function toolPolygon(resourceKey: string): Polygon {
  const center = { x: unit(240), y: unit(280) };
  const dimensions: Record<string, [number, number, number, number]> = {
    barrier: [122, 50, 12, 0], memory: [50, 122, 8, 0],
    fan: [122, 42, 8, 512], light: [122, 42, 8, 3584],
  };
  if (resourceKey === 'cup') return translatePolygon(compileEllipse(center, unit(31), unit(25), 0), -center.x, -center.y);
  const [width, height, radius, heading] = dimensions[resourceKey] ?? dimensions.barrier;
  return translatePolygon(compileRoundedRectangle(center, unit(width), unit(height), unit(radius), heading), -center.x, -center.y);
}
export function blankLevel(): Level {
  return {
    format: 'nonbiri-fatfish-level', format_version: 1, engine_version: ENGINE_VERSION, scoring_version: 1,
    duration_seconds: 90, speed_pixels_per_second: 72, thresholds: [1, 1, 1],
    fish: [{ id: 1, x: unit(88), y: unit(400), heading: 0 }],
    tools: [], solids: [], hazards: [],
    bowls: [{ id: 2, polygon: rect(unit(380), unit(180), unit(56), unit(48)), required: 0, capacity: 1 }],
    switches: [], gates: [], directions: [],
  };
}
export function cloneLevel(level: Level): Level { return structuredClone(level); }
export function convertToCurrentDraft(level: Level): Level {
  return { ...cloneLevel(level), engine_version: ENGINE_VERSION };
}
export function allIDs(level: Level): number[] {
  return [...level.fish, ...level.tools, ...level.solids, ...level.hazards,
    ...level.bowls, ...level.switches, ...level.gates, ...level.directions].map((item) => item.id);
}
export function nextID(level: Level): number {
  const used = new Set(allIDs(level));
  for (let id = 1; id <= 65535; id++) if (!used.has(id)) return id;
  throw new Error('Object ID limit reached');
}
export function shapeAt(level: Level, selection: Selection): Polygon | null {
  if (selection.kind === 'fish') return null;
  const shape = level[selection.kind].find((item) => item.id === selection.id);
  if (!shape) return null;
  if (selection.kind === 'tools') {
    const tool = level.tools.find((item) => item.id === selection.id);
    return tool ? translatePolygon(tool.polygon, tool.x, tool.y) : null;
  }
  return shape.polygon;
}
export function hitTest(level: Level, position: Point): Selection | null {
  for (let index = level.fish.length - 1; index >= 0; index--) {
    const fish = level.fish[index];
    if ((fish.x - position.x) ** 2 + (fish.y - position.y) ** 2 <= FISH_RADIUS ** 2)
      return { kind: 'fish', id: fish.id };
  }
  const kinds: Exclude<ObjectKind, 'fish'>[] = ['tools', 'directions', 'switches', 'gates', 'bowls', 'hazards', 'solids'];
  for (const kind of kinds) {
    for (let index = level[kind].length - 1; index >= 0; index--) {
      const shape = level[kind][index];
      const polygon = kind === 'tools'
        ? (() => { const tool = level.tools[index]; return translatePolygon(tool.polygon, tool.x, tool.y); })()
        : shape.polygon;
      if (containsPolygon(polygon, position)) return { kind, id: shape.id };
    }
  }
  return null;
}
export function moveSelection(level: Level, selection: Selection, dx: number, dy: number, grid: boolean): Level {
  const copy = cloneLevel(level);
  if (selection.kind === 'fish') {
    const fish = copy.fish.find((item) => item.id === selection.id);
    if (fish) { fish.x = snap(fish.x + dx, grid); fish.y = snap(fish.y + dy, grid); }
  } else if (selection.kind === 'tools') {
    const tool = copy.tools.find((item) => item.id === selection.id);
    if (tool) {
      const point = clampWorkspace({ x: snap(tool.x + dx, grid), y: snap(tool.y + dy, grid) });
      tool.x = point.x; tool.y = point.y; tool.placed = true;
    }
  } else {
    const item = copy[selection.kind].find((shape) => shape.id === selection.id);
    if (item) {
      item.polygon.outer = item.polygon.outer.map((point) => ({ x: snap(point.x + dx, grid), y: snap(point.y + dy, grid) }));
      item.polygon.holes = item.polygon.holes.map((hole) => hole.map((point) => ({ x: snap(point.x + dx, grid), y: snap(point.y + dy, grid) })));
    }
  }
  return copy;
}
export function localValidation(level: Level): string | null {
  if (new TextEncoder().encode(JSON.stringify(level)).byteLength > MAX_LEVEL_BYTES) return 'level: 256 KiB limit exceeded';
  try { validateLevel(level); return null; } catch (error) { return error instanceof Error ? error.message : 'level: invalid'; }
}
export function fitField(position: Point): Point {
  return { x: Math.max(0, Math.min(FIELD_WIDTH, position.x)), y: Math.max(0, Math.min(FIELD_HEIGHT, position.y)) };
}

interface LegacyShape { id?: string; type?: string; x?: number; y?: number; width?: number; height?: number; rx?: number; ry?: number; radius?: number; required?: number; capacity?: number; }
function legacyPolygon(shape: LegacyShape, location: string): Polygon {
  const { x, y } = shape;
  if (!Number.isFinite(x) || !Number.isFinite(y)) throw new Error(`${location}: center is missing`);
  try {
    if (shape.type === 'rect' || shape.width !== undefined && shape.height !== undefined) {
      if (!Number.isFinite(shape.width) || !Number.isFinite(shape.height)) throw new Error('dimensions are missing');
      return shape.radius && shape.radius >= 1
        ? compileRoundedRectangle({ x: unit(x!), y: unit(y!) }, unit(shape.width!), unit(shape.height!), unit(shape.radius), 0)
        : rect(unit(x!), unit(y!), unit(shape.width!), unit(shape.height!));
    }
    if (!Number.isFinite(shape.rx) || !Number.isFinite(shape.ry)) throw new Error('radii are missing');
    return compileEllipse({ x: unit(x!), y: unit(y!) }, unit(shape.rx!), unit(shape.ry!), 0);
  } catch (cause) { throw new Error(`${location}: ${cause instanceof Error ? cause.message : 'invalid geometry'}`, { cause }); }
}
export interface ImportedDraft { title: string; description: string; level: Level; pendingFishCount: number; warning?: string }
export function importDraft(text: string): ImportedDraft {
  if (new TextEncoder().encode(text).byteLength > MAX_LEVEL_BYTES) throw new Error('Import exceeds 256 KiB');
  const root: unknown = JSON.parse(text.replace(/^\uFEFF/, ''));
  if (!root || typeof root !== 'object' || Array.isArray(root)) throw new Error('Import must be an object');
  const data = root as Record<string, unknown>;
  if (data.format === 'nonbiri-fatfish-level') {
    const level = data as unknown as Level;
    const error = localValidation(level);
    if (error) throw new Error(error);
    return { title: 'Imported level', description: '', level, pendingFishCount: 0 };
  }
  if ('draft' in data && typeof data.title === 'string' && typeof data.description === 'string') {
    const level = data.draft as Level;
    const error = localValidation(level);
    if (error) throw new Error(error);
    return { title: data.title, description: data.description, level, pendingFishCount: 0 };
  }
  const envelope = data as { format?: unknown; version?: unknown; level?: unknown };
  if (envelope.format !== 'fat-fish-level' || ![1, 2].includes(Number(envelope.version))) throw new Error('Unsupported import format');
  if (!envelope.level || typeof envelope.level !== 'object' || Array.isArray(envelope.level)) throw new Error('Legacy level is missing');
  const old = envelope.level as Record<string, unknown>;
  const count = Number(old.total);
  if (!Number.isInteger(count) || count < 1 || count > 40) throw new Error('Legacy total must be 1–40');
  const level = blankLevel();
  level.fish = [];
  level.thresholds = [Number(old.target), Number(old.target), Number(old.target)];
  level.duration_seconds = Number(old.duration);
  level.speed_pixels_per_second = Number(old.speed);
  let id = 1;
  const oldToNew = new Map<string, number>();
  const mapShapes = (name: string, limit: number) => {
    const items = old[name] ?? [];
    if (!Array.isArray(items) || items.length > limit) throw new Error(`${name}: too many objects`);
    return items.map((raw, index) => {
      if (!raw || typeof raw !== 'object') throw new Error(`${name}[${index}]: invalid object`);
      const shape = raw as LegacyShape;
      const number = id++;
      if (typeof shape.id === 'string') oldToNew.set(shape.id, number);
      return { id: number, polygon: legacyPolygon(shape, `${name}[${index}]`) };
    });
  };
  const goals = [old.goal, ...(Array.isArray(old.extraGoals) ? old.extraGoals : [])];
  if (goals.length < 1 || goals.length > 8) throw new Error('Legacy goals must be 1–8');
  level.bowls = goals.map((raw, index) => {
    if (!raw || typeof raw !== 'object') throw new Error(`goals[${index}]: invalid`);
    const shape = raw as LegacyShape;
    const number = id++;
    return { id: number, polygon: legacyPolygon(shape, `goals[${index}]`), required: Number(shape.required ?? 0), capacity: Number(shape.capacity || count) };
  });
  level.solids = mapShapes('solids', 24);
  level.hazards = mapShapes('hazards', 24);
  const switches = old.switches ?? [];
  if (!Array.isArray(switches) || switches.length > 16) throw new Error('switches: too many objects');
  level.switches = switches.map((raw, index) => {
    const shape = raw as LegacyShape & { mode?: 'latch' | 'hold' };
    const number = id++;
    if (typeof shape.id === 'string') oldToNew.set(shape.id, number);
    return { id: number, polygon: legacyPolygon(shape, `switches[${index}]`), mode: shape.mode ?? 'latch' };
  });
  const gates = old.gates ?? [];
  if (!Array.isArray(gates) || gates.length > 16) throw new Error('gates: too many objects');
  level.gates = gates.map((raw, index) => {
    const shape = raw as LegacyShape & { initiallyOpen?: boolean; logic?: 'all' | 'any' };
    const number = id++;
    if (typeof shape.id === 'string') oldToNew.set(shape.id, number);
    return { id: number, polygon: legacyPolygon(shape, `gates[${index}]`), initially_open: !!shape.initiallyOpen, mode: shape.logic ?? 'any', switch_ids: [] };
  });
  switches.forEach((raw, index) => {
    const targets = (raw as { targets?: unknown }).targets;
    if (!Array.isArray(targets)) return;
    for (const target of targets) {
      const gate = level.gates.find((item) => item.id === oldToNew.get(String(target)));
      if (!gate) throw new Error(`switches[${index}].targets: unknown gate ${String(target)}`);
      gate.switch_ids.push(level.switches[index].id);
    }
  });
  const flows = old.flows ?? [];
  if (!Array.isArray(flows) || flows.length > 24) throw new Error('flows: too many objects');
  level.directions = flows.map((raw, index) => {
    const shape = raw as LegacyShape & { mode?: 'entry' | 'oneway'; heading?: number };
    return { id: id++, polygon: legacyPolygon(shape, `flows[${index}]`), mode: shape.mode ?? 'entry', heading: Math.round((((shape.heading ?? 0) / (2 * Math.PI)) % 1 + 1) % 1 * 4096) % 4096 };
  });
  const tools = old.tools ?? [];
  if (!Array.isArray(tools) || tools.length > 24) throw new Error('tools: too many objects');
  const keys = { keycap: 'barrier', memory: 'memory', accelerator: 'fan', cooler: 'light', puck: 'cup' } as const;
  level.tools = tools.map((raw, index) => {
    const tool = raw as { kind?: keyof typeof keys; placed?: boolean; locked?: boolean; x?: number; y?: number };
    if (tool.locked) throw new Error(`tools[${index}]: locked legacy tools need manual remodeling`);
    if (!tool.kind || !keys[tool.kind]) throw new Error(`tools[${index}]: unknown kind`);
    return { id: id++, resource_key: keys[tool.kind], placed: !!tool.placed,
      x: unit(tool.x ?? 0), y: unit(tool.y ?? 0), polygon: toolPolygon(keys[tool.kind]) };
  });
  if (!Number.isInteger(level.thresholds[0]) || level.thresholds[0] < 1 || level.thresholds[0] > count) throw new Error('Legacy target is invalid');
  return {
    title: typeof old.title === 'string' ? old.title : 'Converted legacy level',
    description: typeof old.brief === 'string' ? old.brief : '',
    level, pendingFishCount: count,
    warning: 'Place each fish individually, review all converted shapes and star thresholds, then validate. Old passed results are not carried over.',
  };
}

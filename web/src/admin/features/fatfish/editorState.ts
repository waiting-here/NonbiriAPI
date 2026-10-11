import type { Level, Point } from '@shared/fatfish/engine/types';
import { cloneLevel, localValidation, shapeAt, utf8Bytes, type Selection } from './draft';
import { polygonError } from './polygonEdit';
import type { useFatFishText } from './copy';

export const objectLabels = {
  fish: 'fish',
  tools: 'movable_tool',
  solids: 'fixed_obstacle',
  hazards: 'hazard',
  bowls: 'bowl',
  switches: 'switch',
  gates: 'gate',
  directions: 'direction_zone',
} as const;
export type EditorText = ReturnType<typeof useFatFishText>;
export interface GeometryIssue {
  raw: string;
  selection: Selection | null;
}
export function issueSelection(
  raw: string,
  level: Level,
  selected: Selection | null,
): Selection | null {
  const match = raw.match(/(fish|tools|solids|hazards|bowls|switches|gates|directions)\[(\d+)\]/);
  if (match) {
    const kind = match[1] as Selection['kind'],
      item = level[kind][Number(match[2])];
    if (item) return { kind, id: item.id };
  }
  return /hole|edge|vertex|contour|polygon/i.test(raw) ? selected : null;
}
export function geometryIssues(level: Level): GeometryIssue[] {
  const issues: GeometryIssue[] = [];
  for (const kind of Object.keys(objectLabels) as Selection['kind'][]) {
    if (kind === 'fish') continue;
    for (const item of level[kind]) {
      const selection = { kind, id: item.id },
        raw = polygonError(level, selection);
      if (raw) issues.push({ raw, selection });
    }
  }
  const raw = localValidation(level);
  if (raw && !issues.some((issue) => issue.raw === raw))
    issues.push({ raw, selection: issueSelection(raw, level, null) });
  return issues;
}
export function readiness(
  level: Level,
  title: string,
  description: string,
  requiredFish: number,
  pending: boolean,
) {
  const issues = geometryIssues(level);
  const missingTitle = !title.trim();
  const textTooLong = utf8Bytes(title) > 128 || utf8Bytes(description) > 4096;
  const missingFish = Math.max(0, requiredFish - level.fish.length);
  return {
    issues,
    missingTitle,
    textTooLong,
    missingFish,
    pending,
    ready: !missingTitle && !textTooLong && !missingFish && !pending && !issues.length,
  };
}
export function describeIssue(raw: string, text: EditorText): string {
  const cases: [RegExp, Parameters<EditorText>[0]][] = [
    [/intersects|overlaps/i, 'issue_overlap'],
    [/center begins inside|collision/i, 'issue_fish_collision'],
    [/zero length|collinear|zero area/i, 'issue_flat'],
    [/thinner than two pixels/i, 'issue_thin'],
    [/outside.*bounds|outside.*field/i, 'issue_bounds'],
    [/at least three|vertex count|vertex limit/i, 'issue_vertices'],
    [/hole/i, 'issue_hole'],
    [/threshold/i, 'issue_threshold'],
    [/fish count/i, 'issue_fish_count'],
    [/quotas|quota exceeds|capacities/i, 'issue_bowl'],
    [/switch ID|gate control/i, 'issue_switch'],
    [/duration_seconds/i, 'issue_duration'],
    [/speed_pixels/i, 'issue_speed'],
    [/out of range/i, 'issue_range'],
    [/Select a shape/i, 'select_shape'],
    [/decomposed/i, 'issue_contour'],
    [/object count/i, 'issue_objects'],
    [/format|version/i, 'issue_version'],
  ];
  return text(cases.find(([pattern]) => pattern.test(raw))?.[1] ?? 'issue_unknown');
}
export function selectionCenter(level: Level, selection: Selection): Point {
  const polygon = shapeAt(level, selection);
  if (polygon) {
    const xs = polygon.outer.map((point) => point.x),
      ys = polygon.outer.map((point) => point.y);
    return {
      x: (Math.min(...xs) + Math.max(...xs)) / 2,
      y: (Math.min(...ys) + Math.max(...ys)) / 2,
    };
  }
  const fish = level.fish.find((item) => item.id === selection.id);
  return fish ? { x: fish.x, y: fish.y } : { x: 240 * 64, y: 280 * 64 };
}

export function nearestPlacement(level: Level, selection: Selection, center: Point): Level {
  const original = selectionCenter(level, selection);
  const occupied = Object.keys(objectLabels).flatMap((kind) =>
    level[kind as Selection['kind']]
      .filter((item) => item.id !== selection.id)
      .map((item) => selectionCenter(level, { kind: kind as Selection['kind'], id: item.id })),
  );
  const offsets = [{ x: 0, y: 0 }];
  for (let radius = 32; radius <= 544; radius += 32) {
    for (let y = -radius; y <= radius; y += 32)
      for (let x = -radius; x <= radius; x += 32)
        if (Math.max(Math.abs(x), Math.abs(y)) === radius) offsets.push({ x: x * 64, y: y * 64 });
  }
  for (const offset of offsets) {
    const x = Math.round(center.x + offset.x),
      y = Math.round(center.y + offset.y);
    if (occupied.some((point) => Math.hypot(x - point.x, y - point.y) < 40 * 64)) continue;
    const next = cloneLevel(level),
      dx = x - original.x,
      dy = y - original.y;
    if (selection.kind === 'fish' || selection.kind === 'tools') {
      const item = next[selection.kind].find((item) => item.id === selection.id)!;
      item.x += dx;
      item.y += dy;
    } else {
      const polygon = next[selection.kind].find((item) => item.id === selection.id)!.polygon;
      for (const ring of [polygon.outer, ...polygon.holes])
        for (const point of ring) {
          point.x += dx;
          point.y += dy;
        }
    }
    if (!localValidation(next)) return next;
  }
  return level;
}

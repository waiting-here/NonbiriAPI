import { useRef, useState, type PointerEvent } from 'react';
import { containsPolygon, polygonsInteriorOverlap, translatePolygon } from '@shared/fatfish/engine/geometry';
import { fishFootprint } from '@shared/fatfish/engine/trig_helpers';
import { type Level, type Point, type Polygon } from '@shared/fatfish/engine/types';
import { type ObjectKind, type Selection, GRID, hitTest, moveSelection, px, snap, unit } from './draft';

const colors: Record<ObjectKind, string> = {
  fish: '#227bb2', tools: '#c47715', solids: '#64748b', hazards: '#c33954',
  bowls: '#258665', switches: '#a454c8', gates: '#5659a8', directions: '#1c9bb0',
};
const shapeKinds: Exclude<ObjectKind, 'fish'>[] = ['solids', 'hazards', 'bowls', 'gates', 'switches', 'directions', 'tools'];

export function polygonPath(polygon: Polygon): string {
  const ring = (points: Point[]) => points.map((point, index) => `${index ? 'L' : 'M'}${point.x / 64} ${point.y / 64}`).join(' ') + ' Z';
  return [ring(polygon.outer), ...polygon.holes.map(ring)].join(' ');
}
function fishBody(fish: Point): Polygon {
  return { outer: fishFootprint(fish.x, fish.y), holes: [] };
}
const fishPath = polygonPath(fishBody({ x: 0, y: 0 }));
export function fishCollision(level: Level, fish: Point): boolean {
  const body = fishBody(fish);
  return level.hazards.some((item) => polygonsInteriorOverlap(body, item.polygon))
    || level.solids.some((item) => polygonsInteriorOverlap(body, item.polygon))
    || level.gates.some((item) => !item.initially_open && polygonsInteriorOverlap(body, item.polygon))
    || level.tools.some((item) => item.placed && polygonsInteriorOverlap(body, translatePolygon(item.polygon, item.x, item.y)))
    || level.bowls.some((item) => containsPolygon(item.polygon, fish));
}

interface Props {
  level: Level; selected: Selection | null; grid: boolean;
  onSelect(selection: Selection | null): void; onCommit(level: Level): void;
  onPlaceFish?(position: Point): void;
}
export function LevelCanvas({ level, selected, grid, onSelect, onCommit, onPlaceFish }: Props) {
  const svg = useRef<SVGSVGElement>(null);
  const drag = useRef<{ selection: Selection; start: Point; level: Level } | null>(null);
  const [preview, setPreview] = useState<Level | null>(null);
  const shown = preview ?? level;
  const point = (event: PointerEvent<SVGSVGElement>): Point => {
    const rect = svg.current?.getBoundingClientRect();
    if (!rect) return { x: 0, y: 0 };
    return { x: unit((event.clientX - rect.left) * 480 / rect.width), y: unit((event.clientY - rect.top) * 560 / rect.height) };
  };
  const down = (event: PointerEvent<SVGSVGElement>) => {
    const start = point(event), hit = hitTest(shown, start);
    if (!hit && onPlaceFish) { onPlaceFish(start); return; }
    onSelect(hit);
    if (!hit) return;
    drag.current = { selection: hit, start, level };
    event.currentTarget.setPointerCapture(event.pointerId);
  };
  const move = (event: PointerEvent<SVGSVGElement>) => {
    const active = drag.current;
    if (!active) return;
    const now = point(event);
    const dx = snap(now.x - active.start.x, grid), dy = snap(now.y - active.start.y, grid);
    setPreview(moveSelection(active.level, active.selection, dx, dy, false));
  };
  const end = () => {
    if (preview) onCommit(preview);
    setPreview(null);
    drag.current = null;
  };
  return (
    <svg ref={svg} role="img" aria-label="Fat Fish level map" className="fatfish-level-map"
      viewBox="0 0 480 560" onPointerDown={down} onPointerMove={move}
      onPointerUp={end} onPointerCancel={() => { drag.current = null; setPreview(null); }}>
      <defs><pattern id="fatfish-grid" width={GRID / 64} height={GRID / 64} patternUnits="userSpaceOnUse"><path d={`M ${GRID / 64} 0 L 0 0 0 ${GRID / 64}`} fill="none" stroke="#d7e2e7" strokeWidth="0.5" /></pattern></defs>
      <rect width="480" height="560" fill="#f6fafb" />
      {grid ? <rect width="480" height="560" fill="url(#fatfish-grid)" /> : null}
      {shapeKinds.flatMap((kind) => shown[kind].map((item) => {
        const polygon = kind === 'tools'
          ? (() => { const tool = shown.tools.find((candidate) => candidate.id === item.id)!; return translatePolygon(tool.polygon, tool.x, tool.y); })()
          : item.polygon;
        return <path key={`${kind}-${item.id}`} d={polygonPath(polygon)} fillRule="evenodd"
          fill={colors[kind]} fillOpacity={kind === 'tools' && !shown.tools.some((tool) => tool.id === item.id && tool.placed) ? 0.15 : selected?.kind === kind && selected.id === item.id ? 0.66 : 0.38}
          stroke={colors[kind]} strokeWidth={selected?.kind === kind && selected.id === item.id ? 3 : 1.4} />;
      }))}
      {shown.fish.map((fish) => {
        const collision = fishCollision(shown, fish);
        return <g key={fish.id} transform={`translate(${px(fish.x)} ${px(fish.y)})`}>
          <path d={fishPath} fill={collision ? '#d2223b' : colors.fish}
            stroke={selected?.kind === 'fish' && selected.id === fish.id ? '#122d44' : '#fff'} strokeWidth="2" />
          <path d="M 0 0 L 14 0" transform={`rotate(${fish.heading * 360 / 4096})`}
            stroke="white" strokeWidth="2" />
        </g>;
      })}
    </svg>
  );
}

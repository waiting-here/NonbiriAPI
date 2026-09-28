import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react';
import { containsPolygon, polygonsInteriorOverlap, translatePolygon } from '@shared/fatfish/engine/geometry';
import { fishFootprint } from '@shared/fatfish/engine/trig_helpers';
import { type Level, type Point, type Polygon } from '@shared/fatfish/engine/types';
import { FatFishCanvas } from '@shared/fatfish/FatFishPlayer';
import { useActivityText } from '@shared/limitedactivities/copy';
import { inField, workspacePoint, WORKSPACE_VIEW_BOX } from '@shared/fatfish/workspace';
import { type ObjectKind, type Selection, GRID, hitTest, moveSelection, px, snap } from './draft';

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
  onDelete?(): void; onUndo?(): void; onRedo?(): void; onCancelPlacement?(): void;
}
export function LevelCanvas({ level, selected, grid, onSelect, onCommit, onPlaceFish, onDelete, onUndo, onRedo, onCancelPlacement }: Props) {
  const t = useActivityText();
  const svg = useRef<SVGSVGElement>(null);
  const viewport = useRef<HTMLDivElement>(null);
  const pan = useRef<{ pointer: number; x: number; y: number; left: number; top: number } | null>(null);
  const [zoomed, setZoomed] = useState(false);
  const drag = useRef<{ pointer: number; selection: Selection; start: Point; level: Level } | null>(null);
  const pending = useRef<Level | null>(null);
  const [preview, setPreview] = useState<Level | null>(null);
  const cancelGesture = () => { drag.current = null; pan.current = null; pending.current = null; setPreview(null); };
  useEffect(() => {
    const cancel = () => { drag.current = null; pan.current = null; pending.current = null; setPreview(null); };
    window.addEventListener('blur', cancel);
    return () => window.removeEventListener('blur', cancel);
  }, []);
  const shown = preview ?? level;
  const point = (event: PointerEvent<SVGSVGElement>): Point => {
    const rect = svg.current?.getBoundingClientRect();
    if (!rect) return { x: 0, y: 0 };
    return workspacePoint(event.clientX, event.clientY, rect);
  };
  const down = (event: PointerEvent<SVGSVGElement>) => {
    if (!event.isPrimary || event.button !== 0 || drag.current || pan.current) return;
    event.currentTarget.focus({ preventScroll: true });
    const start = point(event);
    const selectedTool = selected?.kind === 'tools' ? shown.tools.find((tool) => tool.id === selected.id) : null;
    const hit = selectedTool && containsPolygon(translatePolygon(selectedTool.polygon, selectedTool.x, selectedTool.y), start)
      ? selected : hitTest(shown, start);
    if (!hit && onPlaceFish && inField(start)) { onPlaceFish(start); return; }
    onSelect(hit);
    if (!hit) {
      if (zoomed && event.pointerType === 'touch' && viewport.current) {
        pan.current = { pointer: event.pointerId, x: event.clientX, y: event.clientY,
          left: viewport.current.scrollLeft, top: viewport.current.scrollTop };
        event.currentTarget.setPointerCapture(event.pointerId);
      }
      return;
    }
    drag.current = { pointer: event.pointerId, selection: hit, start, level };
    event.currentTarget.setPointerCapture(event.pointerId);
  };
  const move = (event: PointerEvent<SVGSVGElement>) => {
    const moving = pan.current;
    if (moving?.pointer === event.pointerId && viewport.current) {
      viewport.current.scrollLeft = moving.left + moving.x - event.clientX;
      viewport.current.scrollTop = moving.top + moving.y - event.clientY;
      return;
    }
    const active = drag.current;
    if (!active || active.pointer !== event.pointerId) return;
    const now = point(event);
    const dx = snap(now.x - active.start.x, grid), dy = snap(now.y - active.start.y, grid);
    pending.current = moveSelection(active.level, active.selection, dx, dy, false);
    setPreview(pending.current);
  };
  const end = (event: PointerEvent<SVGSVGElement>, cancelled = false) => {
    if (drag.current?.pointer !== event.pointerId && pan.current?.pointer !== event.pointerId) return;
    pan.current = null;
    if (!cancelled && pending.current) onCommit(pending.current);
    pending.current = null;
    setPreview(null);
    drag.current = null;
  };
  const keyDown = (event: KeyboardEvent<SVGSVGElement>) => {
    if (event.target !== event.currentTarget || event.altKey) return;
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'z') {
      event.preventDefault(); cancelGesture();
      if (event.shiftKey) onRedo?.(); else onUndo?.();
      return;
    }
    if (event.ctrlKey || event.metaKey) return;
    if (event.key === 'Escape') {
      event.preventDefault(); cancelGesture(); onSelect(null); onCancelPlacement?.(); return;
    }
    if (!selected) return;
    if (event.key === 'Delete' || event.key === 'Backspace') {
      event.preventDefault(); cancelGesture(); onDelete?.(); return;
    }
    const step = (event.shiftKey ? 1 : 5) * 64;
    const delta: Record<string, [number, number]> = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] };
    const movement = delta[event.key];
    if (!movement) return;
    event.preventDefault(); cancelGesture();
    onCommit(moveSelection(level, selected, movement[0], movement[1], false));
  };
  return (
    <div className="fatfish-editor-workspace">
    <div className="fatfish-player__workspace-heading"><div><strong>{t('场地与操作台', 'Field and workbench')}</strong>
      <span>{t('道具可在整片区域内自由摆放；场内道具构成开局场景。', 'Place pieces anywhere on the workspace, including the initial scene inside the field.')}</span></div>
      <button type="button" onClick={() => setZoomed((value) => !value)}>{zoomed ? t('适应屏幕', 'Fit screen') : t('放大场地', 'Enlarge field')}</button></div>
    <div className="fatfish-player__viewport" data-zoomed={zoomed} ref={viewport}>
    <div className="fatfish-level-stage fatfish-player__board">
    <FatFishCanvas level={shown} selectedTool={selected?.kind === 'tools' ? selected.id : null} decorative />
    <svg ref={svg} role="img" aria-label="Fat Fish level map" className="fatfish-level-map" tabIndex={0}
      viewBox={WORKSPACE_VIEW_BOX} onKeyDown={keyDown} onPointerDown={down} onPointerMove={move}
      onPointerUp={end} onPointerCancel={(event) => end(event, true)}>
      <defs><pattern id="fatfish-grid" width={GRID / 64} height={GRID / 64} patternUnits="userSpaceOnUse"><path d={`M ${GRID / 64} 0 L 0 0 0 ${GRID / 64}`} fill="none" stroke="#476b7938" strokeWidth="0.4" /></pattern></defs>
      {grid ? <rect x="-128" y="-128" width="736" height="816" fill="url(#fatfish-grid)" /> : null}
      {shapeKinds.flatMap((kind) => shown[kind].map((item) => {
        const polygon = kind === 'tools'
          ? (() => { const tool = shown.tools.find((candidate) => candidate.id === item.id)!; return translatePolygon(tool.polygon, tool.x, tool.y); })()
          : item.polygon;
        return <path key={`${kind}-${item.id}`} d={polygonPath(polygon)} fillRule="evenodd"
          fill={colors[kind]} fillOpacity={selected?.kind === kind && selected.id === item.id ? 0.12 : 0}
          stroke={selected?.kind === kind && selected.id === item.id ? colors[kind] : 'transparent'} strokeWidth={2} />;
      }))}
      {shown.fish.map((fish) => {
        const collision = fishCollision(shown, fish);
        return <g key={fish.id} transform={`translate(${px(fish.x)} ${px(fish.y)})`}>
          <path d={fishPath} fill={collision ? '#d2223b88' : '#227bb216'}
            stroke={selected?.kind === 'fish' && selected.id === fish.id ? '#122d44' : collision ? '#d2223b' : '#24577788'}
            strokeWidth={selected?.kind === 'fish' && selected.id === fish.id ? 2.5 : 1} />
          <path d="M 0 0 L 14 0" transform={`rotate(${fish.heading * 360 / 4096})`}
            stroke="white" strokeWidth="2" />
        </g>;
      })}
    </svg>
    </div></div>
    <p className="muted">{t('选中后用方向键移动，按住 Shift 微调；Delete 删除，Esc 取消选择，Ctrl/⌘+Z 撤销，Ctrl/⌘+Shift+Z 重做。', 'Arrow keys move the selection; hold Shift for fine movement. Delete removes it, Esc clears it, Ctrl/⌘+Z undoes, and Ctrl/⌘+Shift+Z redoes.')}</p>
    </div>
  );
}

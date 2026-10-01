import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react';
import { containsPolygon, polygonsInteriorOverlap, translatePolygon } from '@shared/fatfish/engine/geometry';
import { fishFootprint } from '@shared/fatfish/engine/trig_helpers';
import { type Level, type Point, type Polygon } from '@shared/fatfish/engine/types';
import { FatFishCanvas } from '@shared/fatfish/FatFishPlayer';
import { useFatFishText } from './copy';
import { inField, workspacePoint, WORKSPACE_VIEW_BOX } from '@shared/fatfish/workspace';
import { type ObjectKind, type Selection, GRID, localValidation, hitTest, moveSelection, px, snap } from './draft';

import { insertVertex, polygonEdit, polygonError, removeVertex, ringFor, selectedPolygon, type Vertex } from './polygonEdit';

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
  onPendingChange?(pending: boolean): void;
  onDelete?(): void; onUndo?(): void; onRedo?(): void; onCancelPlacement?(): void;
}
export function LevelCanvas({ level, selected, grid, onSelect, onCommit, onPlaceFish, onDelete, onUndo, onRedo, onCancelPlacement, onPendingChange }: Props) {
  const text = useFatFishText();
  const svg = useRef<SVGSVGElement>(null);
  const viewport = useRef<HTMLDivElement>(null);
  const pan = useRef<{ pointer: number; x: number; y: number; left: number; top: number } | null>(null);
  const [zoomed, setZoomed] = useState(false);
  const drag = useRef<{ pointer: number; selection: Selection; start: Point; level: Level; vertex?: Vertex; moveHole?: boolean } | null>(null);
  const pending = useRef<Level | null>(null);
  const [mode, setMode] = useState<'move' | 'vertices' | 'outer' | 'hole'>('move');
  const [moveHole, setMoveHole] = useState(false);
  const [drawing, setDrawing] = useState<Point[]>([]);
  const [vertex, setVertex] = useState<Vertex | null>(null);
  const [geometryError, setGeometryError] = useState<string | null>(null);
  const [preview, setPreview] = useState<Level | null>(null);
  const cancelGesture = () => { drag.current = null; pan.current = null; pending.current = null; setPreview(null); setDrawing([]); setVertex(null); setMoveHole(false); setGeometryError(null); };
  useEffect(() => {
    const cancel = () => { drag.current = null; pan.current = null; pending.current = null; setPreview(null); setDrawing([]); setVertex(null); setMoveHole(false); setGeometryError(null); };
    window.addEventListener('blur', cancel);
    return () => window.removeEventListener('blur', cancel);
  }, []);
  useEffect(() => { onPendingChange?.(!!preview || drawing.length > 0); }, [preview, drawing.length, onPendingChange]);
  const shown = preview ?? level;
  const point = (event: PointerEvent<SVGElement>): Point => {
    const rect = svg.current?.getBoundingClientRect();
    if (!rect) return { x: 0, y: 0 };
    return workspacePoint(event.clientX, event.clientY, rect);
  };
  const down = (event: PointerEvent<SVGSVGElement>) => {
    if (!event.isPrimary || event.button !== 0 || drag.current || pan.current) return;
    event.currentTarget.focus({ preventScroll: true });
    const start = point(event);
    if ((mode === 'outer' || mode === 'hole') && selected && selected.kind !== 'fish') {
      const tool = selected.kind === 'tools' ? shown.tools.find((item) => item.id === selected.id) : null;
      if (drawing.length >= (mode === 'outer' ? 128 : 64)) return;
      setDrawing([...drawing, { x: snap(start.x - (tool?.x ?? 0), grid), y: snap(start.y - (tool?.y ?? 0), grid) }]);
      return;
    }
    const selectedTool = selected?.kind === 'tools' ? shown.tools.find((tool) => tool.id === selected.id) : null;
    const hit = selectedTool && containsPolygon(translatePolygon(selectedTool.polygon, selectedTool.x, selectedTool.y), start)
      ? selected : hitTest(shown, start);
    if (!hit && onPlaceFish && inField(start)) { onPlaceFish(start); return; }
    const changedSelection = hit?.kind !== selected?.kind || hit?.id !== selected?.id;
    if (changedSelection) cancelGesture();
    onSelect(hit);
    if (!hit) {
      if (zoomed && event.pointerType === 'touch' && viewport.current) {
        pan.current = { pointer: event.pointerId, x: event.clientX, y: event.clientY,
          left: viewport.current.scrollLeft, top: viewport.current.scrollTop };
        event.currentTarget.setPointerCapture(event.pointerId);
      }
      return;
    }
    drag.current = { pointer: event.pointerId, selection: hit, start, level: changedSelection ? level : shown };
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
    pending.current = active.vertex ? polygonEdit(active.level, active.selection, (polygon) => {
      const ring = ringFor(polygon, active.vertex!);
      if (active.moveHole) {
        for (const pointValue of ring) { pointValue.x += dx; pointValue.y += dy; }
        return;
      }
      ring[active.vertex!.index] = { x: ring[active.vertex!.index].x + dx, y: ring[active.vertex!.index].y + dy };
    }) : moveSelection(active.level, active.selection, dx, dy, false);
    setPreview(pending.current);
  };
  const end = (event: PointerEvent<SVGSVGElement>, cancelled = false) => {
    if (drag.current?.pointer !== event.pointerId && pan.current?.pointer !== event.pointerId) return;
    pan.current = null;
    if (!cancelled && pending.current) {
      const problem = drag.current?.selection.kind !== 'fish' && drag.current ? (polygonError(pending.current, drag.current.selection) ?? localValidation(pending.current)) : null;
      setGeometryError(problem);
      if (!problem) { onCommit(pending.current); setPreview(null); pending.current = null; }
    } else { pending.current = null; setPreview(null); setGeometryError(null); }
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
      event.preventDefault();
      if (mode === 'vertices' && vertex) {
        try {
          const next = removeVertex(shown, selected, vertex), problem = polygonError(next, selected) ?? localValidation(next);
          setGeometryError(problem);
          if (problem) { pending.current = next; setPreview(next); } else { onCommit(next); cancelGesture(); }
        } catch (error) { setGeometryError(String(error)); }
      } else { cancelGesture(); onDelete?.(); }
      return;
    }
    const step = (event.shiftKey ? 1 : 5) * 64;
    const delta: Record<string, [number, number]> = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] };
    const movement = delta[event.key];
    if (!movement) return;
    event.preventDefault(); cancelGesture();
    onCommit(moveSelection(level, selected, movement[0], movement[1], false));
  };
  const polygon = selectedPolygon(shown, selected);
  const center = selected?.kind === 'tools' ? shown.tools.find((item) => item.id === selected.id) : null;
  const finishContour = () => {
    if (!selected || !polygon || drawing.length < 3) return;
    const next = polygonEdit(shown, selected, (value) => {
      if (mode === 'outer') value.outer = drawing;
      else value.holes.push(drawing);
    });
    const problem = polygonError(next, selected) ?? localValidation(next); setGeometryError(problem);
    if (problem) return;
    onCommit(next); cancelGesture(); setMode('vertices');
  };
  return (
    <div className="fatfish-editor-workspace">
    <div className="fatfish-player__workspace-heading"><div><strong>{text('field_and_workbench')}</strong>
      <span>{text('place_pieces_anywhere_on_the_workspace_including_the_initial_scene_ins')}</span></div>
      <button type="button" onClick={() => setZoomed((value) => !value)}>{zoomed ? text('fit_screen') : text('enlarge_field')}</button></div>
    <div className="fatfish-actions">
      <button type="button" onClick={() => { cancelGesture(); setMode('move'); }}>{text('move_object')}</button>
      <button type="button" disabled={!polygon} onClick={() => { cancelGesture(); setMode('vertices'); }}>{text('edit_vertices')}</button>
      <button type="button" disabled={!polygon} onClick={() => { cancelGesture(); setMode('outer'); }}>{text('draw_outer_contour')}</button>
      <button type="button" disabled={!polygon || polygon.holes.length >= 8} onClick={() => { cancelGesture(); setMode('hole'); }}>{text('draw_hole')}</button>
      {mode === 'vertices' ? <label><input type="checkbox" checked={moveHole} onChange={(event) => setMoveHole(event.target.checked)} />{text('move_selected_hole')}</label> : null}
      {mode === 'outer' || mode === 'hole' ? <>
        <button type="button" disabled={drawing.length < 3} onClick={finishContour}>{text('finish_contour')}</button>
        <button type="button" onClick={() => { cancelGesture(); setMode('vertices'); }}>{text('cancel_contour')}</button>
      </> : null}
    </div>
    {geometryError ? <p role="alert">{text('the_contour_is_unfinished_or_invalid_adjust_its_vertices_esc_restores_')} {geometryError}</p> : null}
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
      {polygon && mode === 'vertices' ? [polygon.outer, ...polygon.holes].flatMap((ring, ringIndex) => ring.map((pointValue, index) => {
        const address = { hole: ringIndex === 0 ? null : ringIndex - 1, index };
        const next = ring[(index + 1) % ring.length];
        return <line key={ringIndex + ':' + index}
          x1={(pointValue.x + (center?.x ?? 0)) / 64} y1={(pointValue.y + (center?.y ?? 0)) / 64}
          x2={(next.x + (center?.x ?? 0)) / 64} y2={(next.y + (center?.y ?? 0)) / 64}
          stroke="transparent" strokeWidth={12} onPointerDown={(event) => {
            event.stopPropagation();
            const at = point(event);
            try {
              const changed = insertVertex(shown, selected!, address, { x: at.x - (center?.x ?? 0), y: at.y - (center?.y ?? 0) });
              const problem = polygonError(changed, selected!) ?? localValidation(changed);
              setVertex({ ...address, index: index + 1 }); setGeometryError(problem);
              if (problem) { pending.current = changed; setPreview(changed); }
              else { onCommit(changed); pending.current = null; setPreview(null); }
            } catch (error) { setGeometryError(String(error)); }
          }} />;
      })) : null}
      {polygon && mode === 'vertices' ? [polygon.outer, ...polygon.holes].flatMap((ring, ringIndex) => ring.map((pointValue, index) => {
        const address = { hole: ringIndex === 0 ? null : ringIndex - 1, index };
        return <circle key={ringIndex + ':' + index}
          cx={(pointValue.x + (center?.x ?? 0)) / 64} cy={(pointValue.y + (center?.y ?? 0)) / 64}
          r={5} fill={vertex?.hole === address.hole && vertex.index === index ? '#fba928' : '#fff'} stroke="#145f85"
          aria-label={text('vertex') + ' ' + (ringIndex + 1) + ':' + (index + 1)}
          onPointerDown={(event) => {
            event.stopPropagation(); svg.current?.focus(); setVertex(address);
            drag.current = { pointer: event.pointerId, selection: selected!, start: point(event), level: shown, vertex: address, moveHole: moveHole && address.hole !== null };
            svg.current?.setPointerCapture(event.pointerId);
          }} />;
      })) : null}
      {drawing.length ? <polyline points={drawing.map((value) => ((value.x + (center?.x ?? 0)) / 64) + ',' + ((value.y + (center?.y ?? 0)) / 64)).join(' ')} fill="none" stroke="#db5d34" strokeWidth={2} /> : null}
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
    <p className="muted">{text('arrow_keys_move_the_selection_hold_shift_for_fine_movement_delete_remo')}</p>
    </div>
  );
}

import {
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type PointerEvent,
  type Ref,
} from 'react';
import {
  containsPolygon,
  polygonsInteriorOverlap,
  translatePolygon,
} from '@shared/fatfish/engine/geometry';
import { fishFootprint } from '@shared/fatfish/engine/trig_helpers';
import {
  FISH_RADIUS,
  FIELD_WIDTH,
  FIELD_HEIGHT,
  type Level,
  type Point,
  type Polygon,
} from '@shared/fatfish/engine/types';
import { FatFishCanvas } from '@shared/fatfish/FatFishPlayer';
import { useFatFishText } from './copy';
import { inField, workspacePoint, WORKSPACE_VIEW_BOX } from '@shared/fatfish/workspace';
import {
  type ObjectKind,
  type Selection,
  GRID,
  localValidation,
  hitTest,
  moveSelection,
  px,
  snap,
} from './draft';
import { describeIssue, geometryIssues, objectLabels, selectionCenter } from './editorState';
import {
  insertVertex,
  polygonEdit,
  polygonError,
  removeVertex,
  ringFor,
  selectedPolygon,
  type Vertex,
} from './polygonEdit';

const colors: Record<ObjectKind, string> = {
  fish: '#227bb2',
  tools: '#c47715',
  solids: '#64748b',
  hazards: '#c33954',
  bowls: '#258665',
  switches: '#a454c8',
  gates: '#5659a8',
  directions: '#1c9bb0',
};
const shapeKinds: Exclude<ObjectKind, 'fish'>[] = [
  'solids',
  'hazards',
  'bowls',
  'gates',
  'switches',
  'directions',
  'tools',
];
type Mode = 'move' | 'vertices' | 'outer' | 'hole';
export function polygonPath(polygon: Polygon): string {
  const ring = (points: Point[]) =>
    points
      .map((point, index) => (index ? 'L' : 'M') + point.x / 64 + ' ' + point.y / 64)
      .join(' ') + ' Z';
  return [ring(polygon.outer), ...polygon.holes.map(ring)].join(' ');
}
function fishBody(fish: Point): Polygon {
  return { outer: fishFootprint(fish.x, fish.y), holes: [] };
}
const fishPath = polygonPath(fishBody({ x: 0, y: 0 }));
export function fishCollision(level: Level, fish: Point): boolean {
  const body = fishBody(fish);
  return (
    level.hazards.some((item) => polygonsInteriorOverlap(body, item.polygon)) ||
    level.solids.some((item) => polygonsInteriorOverlap(body, item.polygon)) ||
    level.gates.some(
      (item) => !item.initially_open && polygonsInteriorOverlap(body, item.polygon),
    ) ||
    level.tools.some(
      (item) =>
        item.placed &&
        polygonsInteriorOverlap(body, translatePolygon(item.polygon, item.x, item.y)),
    ) ||
    level.bowls.some((item) => containsPolygon(item.polygon, fish))
  );
}
export function fishPosition(position: Point, grid: boolean): Point {
  return {
    x: Math.max(FISH_RADIUS, Math.min(FIELD_WIDTH - FISH_RADIUS, snap(position.x, grid))),
    y: Math.max(FISH_RADIUS, Math.min(FIELD_HEIGHT - FISH_RADIUS, snap(position.y, grid))),
  };
}
export interface LevelCanvasHandle {
  cancelGesture(): void;
  center(): Point;
  locate(selection: Selection): void;
}
interface Props {
  ref?: Ref<LevelCanvasHandle>;
  level: Level;
  selected: Selection | null;
  grid: boolean;
  onSelect(selection: Selection | null): void;
  onCommit(level: Level): void;
  onPlaceFish?(position: Point): void;
  fishHeading?: number;
  onPendingChange?(pending: boolean): void;
  onDelete?(): void;
  onUndo?(): void;
  onRedo?(): void;
  onCancelPlacement?(): void;
  canUndo?: boolean;
  canRedo?: boolean;
  onGridChange?(enabled: boolean): void;
}
export function LevelCanvas({
  ref,
  level,
  selected,
  grid,
  onSelect,
  onCommit,
  onPlaceFish,
  fishHeading = 0,
  onDelete,
  onUndo,
  onRedo,
  onCancelPlacement,
  onPendingChange,
  canUndo,
  canRedo,
  onGridChange,
}: Props) {
  const text = useFatFishText();
  const svg = useRef<SVGSVGElement>(null),
    viewport = useRef<HTMLDivElement>(null);
  const pan = useRef<{ pointer: number; x: number; y: number; left: number; top: number } | null>(
    null,
  );
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const pinch = useRef<{ distance: number; zoom: number } | null>(null);
  const space = useRef(false),
    frame = useRef<number | null>(null),
    zoomRef = useRef(1);
  const [zoom, setZoom] = useState(1);
  const drag = useRef<{
    pointer: number;
    selection: Selection;
    start: Point;
    level: Level;
    vertex?: Vertex;
    moveHole?: boolean;
  } | null>(null);
  const pending = useRef<Level | null>(null);
  const [mode, setMode] = useState<Mode>('move');
  const [moveHole, setMoveHole] = useState(false),
    [labels, setLabels] = useState(true);
  const [drawing, setDrawing] = useState<Point[]>([]),
    [vertex, setVertex] = useState<Vertex | null>(null);
  const [geometryError, setGeometryError] = useState<string | null>(null),
    [hint, setHint] = useState('');
  const [preview, setPreview] = useState<Level | null>(null);
  const [hover, setHover] = useState<Selection | null>(null),
    [cursor, setCursor] = useState<Point | null>(null);
  const issues = useMemo(() => geometryIssues(level), [level]);
  const shown = preview ?? level;
  const cancelGesture = useCallback(() => {
    if (frame.current !== null) cancelAnimationFrame(frame.current);
    frame.current = null;
    drag.current = null;
    pan.current = null;
    pending.current = null;
    setPreview(null);
    setDrawing([]);
    setVertex(null);
    setMoveHole(false);
    setGeometryError(null);
    setHint('');
  }, []);
  const scale = useCallback((value: number, client?: { x: number; y: number }) => {
    const box = viewport.current;
    if (!box) return;
    const rect = box.getBoundingClientRect();
    const x = client ? client.x - rect.left : box.clientWidth / 2;
    const y = client ? client.y - rect.top : box.clientHeight / 2;
    const next = Math.max(1, Math.min(4, value)),
      ratio = next / zoomRef.current;
    const left = (box.scrollLeft + x) * ratio - x,
      top = (box.scrollTop + y) * ratio - y;
    zoomRef.current = next;
    setZoom(next);
    requestAnimationFrame(() => {
      box.scrollLeft = left;
      box.scrollTop = top;
    });
  }, []);
  useImperativeHandle(
    ref,
    () => ({
      cancelGesture,
      center: () => {
        const box = viewport.current?.getBoundingClientRect(),
          map = svg.current?.getBoundingClientRect();
        return box && map
          ? workspacePoint(box.left + box.width / 2, box.top + box.height / 2, map)
          : { x: 240 * 64, y: 280 * 64 };
      },
      locate: (selection) => {
        const box = viewport.current,
          map = svg.current?.getBoundingClientRect();
        if (!box || !map) return;
        const center = selectionCenter(level, selection),
          bounds = box.getBoundingClientRect();
        box.scrollLeft +=
          ((center.x / 64 + 128) / 736) * map.width + map.left - bounds.left - bounds.width / 2;
        box.scrollTop +=
          ((center.y / 64 + 128) / 816) * map.height + map.top - bounds.top - bounds.height / 2;
        box.scrollIntoView({ block: 'nearest' });
        svg.current?.focus({ preventScroll: true });
      },
    }),
    [level, cancelGesture],
  );
  useEffect(() => {
    const blur = () => {
      cancelGesture();
      space.current = false;
      pointers.current.clear();
      pinch.current = null;
    };
    const keyUp = (event: globalThis.KeyboardEvent) => {
      if (event.code === 'Space') space.current = false;
    };
    const wheel = (event: WheelEvent) => {
      if (!event.ctrlKey) return;
      event.preventDefault();
      scale(zoomRef.current * Math.exp(-event.deltaY * 0.002), {
        x: event.clientX,
        y: event.clientY,
      });
    };
    const box = viewport.current;
    box?.addEventListener('wheel', wheel, { passive: false });
    window.addEventListener('blur', blur);
    window.addEventListener('keyup', keyUp);
    return () => {
      box?.removeEventListener('wheel', wheel);
      window.removeEventListener('blur', blur);
      window.removeEventListener('keyup', keyUp);
      if (frame.current !== null) cancelAnimationFrame(frame.current);
    };
  }, [cancelGesture, scale]);
  useEffect(() => {
    onPendingChange?.(!!preview || drawing.length > 0);
  }, [preview, drawing.length, onPendingChange]);
  const point = (event: PointerEvent<SVGElement>): Point => {
    const rect = svg.current?.getBoundingClientRect();
    return rect ? workspacePoint(event.clientX, event.clientY, rect) : { x: 0, y: 0 };
  };
  const beginPan = (event: PointerEvent<SVGSVGElement>) => {
    if (!viewport.current) return;
    pan.current = {
      pointer: event.pointerId,
      x: event.clientX,
      y: event.clientY,
      left: viewport.current.scrollLeft,
      top: viewport.current.scrollTop,
    };
    event.currentTarget.setPointerCapture(event.pointerId);
  };
  const down = (event: PointerEvent<SVGSVGElement>) => {
    if (event.button !== 0) return;
    pointers.current.set(event.pointerId, { x: event.clientX, y: event.clientY });
    if (pointers.current.size === 2) {
      cancelGesture();
      const [a, b] = [...pointers.current.values()];
      pinch.current = {
        distance: Math.max(1, Math.hypot(a.x - b.x, a.y - b.y)),
        zoom: zoomRef.current,
      };
      event.currentTarget.setPointerCapture(event.pointerId);
      return;
    }
    if (!event.isPrimary || drag.current || pan.current) return;
    event.currentTarget.focus({ preventScroll: true });
    if (space.current) {
      beginPan(event);
      return;
    }
    const start = point(event);
    setCursor(start);
    if (onPlaceFish) {
      if (!inField(start)) {
        setHint(text('placement_outside'));
        return;
      }
      const at = fishPosition(start, grid);
      if (fishCollision(shown, at)) {
        setHint(text('placement_collision'));
        return;
      }
      onPlaceFish(at);
      setHint('');
      return;
    }
    const editingMode =
      event.pointerType === 'touch' && window.matchMedia?.('(width < 40rem)').matches
        ? 'move'
        : mode;
    if (
      (editingMode === 'outer' || editingMode === 'hole') &&
      selected &&
      selected.kind !== 'fish'
    ) {
      const tool =
        selected.kind === 'tools' ? shown.tools.find((item) => item.id === selected.id) : null;
      if (drawing.length >= (editingMode === 'outer' ? 128 : 64)) return;
      setDrawing([
        ...drawing,
        { x: snap(start.x - (tool?.x ?? 0), grid), y: snap(start.y - (tool?.y ?? 0), grid) },
      ]);
      return;
    }
    const selectedTool =
      selected?.kind === 'tools' ? shown.tools.find((tool) => tool.id === selected.id) : null;
    const hit =
      selectedTool &&
      containsPolygon(translatePolygon(selectedTool.polygon, selectedTool.x, selectedTool.y), start)
        ? selected
        : hitTest(shown, start);
    const changed = hit?.kind !== selected?.kind || hit?.id !== selected?.id;
    if (changed) cancelGesture();
    onSelect(hit);
    if (!hit) {
      if (event.pointerType === 'touch') beginPan(event);
      return;
    }
    drag.current = {
      pointer: event.pointerId,
      selection: hit,
      start,
      level: changed ? level : shown,
    };
    event.currentTarget.setPointerCapture(event.pointerId);
  };
  const move = (event: PointerEvent<SVGSVGElement>) => {
    const now = point(event);
    setCursor(now);
    if (pointers.current.has(event.pointerId))
      pointers.current.set(event.pointerId, { x: event.clientX, y: event.clientY });
    if (pinch.current && pointers.current.size >= 2) {
      const [a, b] = [...pointers.current.values()];
      scale((pinch.current.zoom * Math.hypot(a.x - b.x, a.y - b.y)) / pinch.current.distance, {
        x: (a.x + b.x) / 2,
        y: (a.y + b.y) / 2,
      });
      return;
    }
    const moving = pan.current;
    if (moving?.pointer === event.pointerId && viewport.current) {
      viewport.current.scrollLeft = moving.left + moving.x - event.clientX;
      viewport.current.scrollTop = moving.top + moving.y - event.clientY;
      return;
    }
    const active = drag.current;
    if (!active || active.pointer !== event.pointerId) {
      setHover(hitTest(shown, now));
      return;
    }
    const dx = snap(now.x - active.start.x, grid),
      dy = snap(now.y - active.start.y, grid);
    pending.current = active.vertex
      ? polygonEdit(active.level, active.selection, (polygon) => {
          const ring = ringFor(polygon, active.vertex!);
          if (active.moveHole) {
            for (const value of ring) {
              value.x += dx;
              value.y += dy;
            }
            return;
          }
          ring[active.vertex!.index] = {
            x: ring[active.vertex!.index].x + dx,
            y: ring[active.vertex!.index].y + dy,
          };
        })
      : moveSelection(active.level, active.selection, dx, dy, false);
    if (frame.current === null)
      frame.current = requestAnimationFrame(() => {
        frame.current = null;
        if (!pending.current || !drag.current) return;
        setPreview(pending.current);
        setGeometryError(
          drag.current.selection.kind === 'fish'
            ? null
            : polygonError(pending.current, drag.current.selection),
        );
      });
  };
  const end = (event: PointerEvent<SVGSVGElement>, cancelled = false) => {
    pointers.current.delete(event.pointerId);
    if (pinch.current) {
      if (pointers.current.size < 2) pinch.current = null;
      return;
    }
    if (drag.current?.pointer !== event.pointerId && pan.current?.pointer !== event.pointerId)
      return;
    if (frame.current !== null) cancelAnimationFrame(frame.current);
    frame.current = null;
    pan.current = null;
    if (!cancelled && pending.current) {
      const problem =
        (drag.current?.selection.kind !== 'fish' && drag.current
          ? polygonError(pending.current, drag.current.selection)
          : null) ?? localValidation(pending.current);
      setGeometryError(problem);
      if (!problem) {
        onCommit(pending.current);
        setPreview(null);
        pending.current = null;
      } else setPreview(pending.current);
    } else {
      pending.current = null;
      setPreview(null);
      setGeometryError(null);
    }
    drag.current = null;
  };
  const keyDown = (event: KeyboardEvent<SVGSVGElement>) => {
    if (event.target !== event.currentTarget || event.altKey) return;
    if (event.code === 'Space') {
      event.preventDefault();
      space.current = true;
      return;
    }
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'z') {
      event.preventDefault();
      cancelGesture();
      if (event.shiftKey) onRedo?.();
      else onUndo?.();
      return;
    }
    if (event.ctrlKey || event.metaKey) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      cancelGesture();
      onSelect(null);
      onCancelPlacement?.();
      return;
    }
    if (!selected) return;
    if (event.key === 'Delete' || event.key === 'Backspace') {
      event.preventDefault();
      if (mode === 'vertices') {
        if (!vertex) {
          setHint(text('select_vertex'));
          return;
        }
        try {
          const next = removeVertex(shown, selected, vertex),
            problem = polygonError(next, selected) ?? localValidation(next);
          setGeometryError(problem);
          if (problem) {
            pending.current = next;
            setPreview(next);
          } else {
            onCommit(next);
            cancelGesture();
          }
        } catch (error) {
          setGeometryError(String(error));
        }
      } else {
        cancelGesture();
        onDelete?.();
      }
      return;
    }
    const step = (event.shiftKey ? 1 : grid ? 8 : 5) * 64;
    const movement: Record<string, [number, number]> = {
      ArrowLeft: [-step, 0],
      ArrowRight: [step, 0],
      ArrowUp: [0, -step],
      ArrowDown: [0, step],
    };
    const delta = movement[event.key];
    if (!delta) return;
    event.preventDefault();
    cancelGesture();
    onCommit(moveSelection(level, selected, delta[0], delta[1], false));
  };
  const polygon = selectedPolygon(shown, selected);
  const center =
    selected?.kind === 'tools' ? shown.tools.find((item) => item.id === selected.id) : null;
  const finishContour = () => {
    if (!selected || !polygon || drawing.length < 3) return;
    const next = polygonEdit(shown, selected, (value) => {
      if (mode === 'outer') value.outer = drawing;
      else value.holes.push(drawing);
    });
    const problem = polygonError(next, selected) ?? localValidation(next);
    setGeometryError(problem);
    if (problem) return;
    onCommit(next);
    cancelGesture();
    setMode('vertices');
  };
  const linkedGates = shown.gates.filter((gate) =>
    selected?.kind === 'gates'
      ? selected.id === gate.id
      : selected?.kind === 'switches' && gate.switch_ids.includes(selected.id),
  );
  const ghost = cursor && onPlaceFish ? fishPosition(cursor, grid) : null;
  const ghostInvalid = !!ghost && ((!!cursor && !inField(cursor)) || fishCollision(shown, ghost));
  const name = (value: Selection) => text(objectLabels[value.kind]) + ' ' + value.id;
  return (
    <div className="fatfish-editor-workspace">
      <div className="fatfish-canvas-toolbar">
        <div className="fatfish-modes" role="group" aria-label={text('editor_modes')}>
          {(
            [
              ['move', 'move_object'],
              ['vertices', 'edit_vertices'],
              ['outer', 'draw_outer_contour'],
              ['hole', 'draw_hole'],
            ] as const
          ).map(([value, key]) => (
            <button
              type="button"
              key={value}
              className={value === 'move' ? '' : 'fatfish-geometry-mode'}
              aria-pressed={mode === value}
              disabled={
                value !== 'move' && (!polygon || (value === 'hole' && polygon.holes.length >= 8))
              }
              title={!polygon && value !== 'move' ? text('select_shape') : undefined}
              onClick={() => {
                cancelGesture();
                setMode(value);
              }}
            >
              {text(key)}
            </button>
          ))}
        </div>
        <div className="fatfish-actions">
          <button type="button" disabled={!canUndo} onClick={onUndo}>
            {text('undo')}
          </button>
          <button type="button" disabled={!canRedo} onClick={onRedo}>
            {text('redo')}
          </button>
          <label>
            <input
              type="checkbox"
              checked={grid}
              onChange={(event) => onGridChange?.(event.target.checked)}
            />
            {text('snap_to_8_pixel_grid')}
          </label>
          <label>
            <input
              type="checkbox"
              checked={labels}
              onChange={(event) => setLabels(event.target.checked)}
            />
            {text('show_labels')}
          </label>
        </div>
        <div className="fatfish-actions fatfish-zoom">
          <button
            type="button"
            aria-label={text('zoom_out')}
            disabled={zoom <= 1}
            onClick={() => scale(zoom / 1.25)}
          >
            −
          </button>
          <button type="button" onClick={() => scale(1)}>
            {text('fit_screen')}
          </button>
          <span>{Math.round(zoom * 100)}%</span>
          <button
            type="button"
            aria-label={text('zoom_in')}
            disabled={zoom >= 4}
            onClick={() => scale(zoom * 1.25)}
          >
            +
          </button>
        </div>
      </div>
      {!polygon && <p className="muted fatfish-geometry-mode">{text('select_shape')}</p>}
      <p className="fatfish-mobile-only muted">{text('mobile_geometry_help')}</p>
      {mode === 'vertices' && (
        <label className="fatfish-geometry-mode">
          <input
            type="checkbox"
            checked={moveHole}
            onChange={(event) => setMoveHole(event.target.checked)}
          />
          {text('move_selected_hole')}
        </label>
      )}
      {(mode === 'outer' || mode === 'hole') && (
        <div className="fatfish-actions fatfish-geometry-mode">
          <button type="button" disabled={drawing.length < 3} onClick={finishContour}>
            {text('finish_contour')}
          </button>
          <button
            type="button"
            onClick={() => {
              cancelGesture();
              setMode('vertices');
            }}
          >
            {text('cancel_contour')}
          </button>
        </div>
      )}
      <div className="fatfish-canvas-wrap">
        {onPlaceFish && (
          <div className="fatfish-placement-notice" role="status">
            {text('place_fish_mode')}{' '}
            <button type="button" onClick={onCancelPlacement}>
              {text('cancel_contour')}
            </button>
          </div>
        )}
        <div className="fatfish-player__viewport fatfish-editor-viewport" ref={viewport}>
          <div className="fatfish-level-stage" style={{ width: `calc(min(100%, 70dvh * 736 / 816) * ${zoom})` }}>
            <span className="fatfish-workbench-label">{text('workbench')}</span>
            <FatFishCanvas
              level={shown}
              selectedTool={selected?.kind === 'tools' ? selected.id : null}
              decorative
              bowlLabel={(required, capacity) => text('bowl_capacity', { required, capacity })}
            />
            <svg
              ref={svg}
              role="img"
              aria-label={text('canvas_label')}
              className="fatfish-level-map"
              tabIndex={0}
              data-mode={onPlaceFish ? 'place' : mode}
              viewBox={WORKSPACE_VIEW_BOX}
              onKeyDown={keyDown}
              onPointerDown={down}
              onPointerMove={move}
              onPointerLeave={() => {
                if (!drag.current) {
                  setHover(null);
                  setCursor(null);
                }
              }}
              onPointerUp={end}
              onPointerCancel={(event) => end(event, true)}
            >
              <defs>
                <pattern
                  id="fatfish-grid"
                  width={GRID / 64}
                  height={GRID / 64}
                  patternUnits="userSpaceOnUse"
                >
                  <path
                    d={'M ' + GRID / 64 + ' 0 L 0 0 0 ' + GRID / 64}
                    fill="none"
                    stroke="#476b7938"
                    strokeWidth="0.4"
                  />
                </pattern>
              </defs>
              {grid && (
                <rect x="-128" y="-128" width="736" height="816" fill="url(#fatfish-grid)" />
              )}
              {linkedGates.flatMap((gate) =>
                gate.switch_ids.map((id) => {
                  const from = selectionCenter(shown, { kind: 'gates', id: gate.id }),
                    to = selectionCenter(shown, { kind: 'switches', id });
                  return (
                    <line
                      key={gate.id + ':' + id}
                      className="fatfish-control-link"
                      x1={px(from.x)}
                      y1={px(from.y)}
                      x2={px(to.x)}
                      y2={px(to.y)}
                      stroke="#7554a5"
                      strokeWidth="2"
                      strokeDasharray="6 5"
                      pointerEvents="none"
                    />
                  );
                }),
              )}
              {shapeKinds.flatMap((kind) =>
                shown[kind].map((item) => {
                  const poly =
                    kind === 'tools'
                      ? (() => {
                          const tool = shown.tools.find((candidate) => candidate.id === item.id)!;
                          return translatePolygon(tool.polygon, tool.x, tool.y);
                        })()
                      : item.polygon;
                  const active = selected?.kind === kind && selected.id === item.id,
                    over = hover?.kind === kind && hover.id === item.id;
                  const invalid =
                    (active && !!geometryError) ||
                    issues.some(
                      (issue) => issue.selection?.kind === kind && issue.selection.id === item.id,
                    );
                  const linked =
                    kind === 'gates' && linkedGates.some((gate) => gate.id === item.id);
                  const x = Math.min(...poly.outer.map((p) => px(p.x))),
                    y = Math.min(...poly.outer.map((p) => px(p.y)));
                  return (
                    <g key={kind + '-' + item.id} data-invalid={invalid || undefined}>
                      <path
                        d={polygonPath(poly)}
                        fillRule="evenodd"
                        fill={invalid ? '#cf263d' : colors[kind]}
                        fillOpacity={active || invalid || linked ? 0.16 : 0}
                        stroke={
                          invalid
                            ? '#cf263d'
                            : active || over || linked
                              ? colors[kind]
                              : 'transparent'
                        }
                        strokeWidth={active || invalid ? 3 : 1.5}
                      />
                      {labels && (
                        <g
                          className="fatfish-object-label"
                          pointerEvents="none"
                          transform={'translate(' + x + ' ' + (y - 5) + ')'}
                        >
                          <text>{name({ kind, id: item.id })}</text>
                        </g>
                      )}
                    </g>
                  );
                }),
              )}
              {polygon &&
                mode === 'vertices' &&
                [polygon.outer, ...polygon.holes].flatMap((ring, ringIndex) =>
                  ring.map((value, index) => {
                    const address = { hole: ringIndex === 0 ? null : ringIndex - 1, index },
                      next = ring[(index + 1) % ring.length];
                    return (
                      <line
                        key={ringIndex + ':' + index}
                        className="fatfish-geometry-mode"
                        x1={px(value.x + (center?.x ?? 0))}
                        y1={px(value.y + (center?.y ?? 0))}
                        x2={px(next.x + (center?.x ?? 0))}
                        y2={px(next.y + (center?.y ?? 0))}
                        stroke="transparent"
                        strokeWidth={12}
                        onPointerDown={(event) => {
                          event.stopPropagation();
                          const at = point(event);
                          try {
                            const changed = insertVertex(shown, selected!, address, {
                              x: at.x - (center?.x ?? 0),
                              y: at.y - (center?.y ?? 0),
                            });
                            const problem =
                              polygonError(changed, selected!) ?? localValidation(changed);
                            setVertex({ ...address, index: index + 1 });
                            setGeometryError(problem);
                            if (problem) {
                              pending.current = changed;
                              setPreview(changed);
                            } else {
                              onCommit(changed);
                              pending.current = null;
                              setPreview(null);
                            }
                          } catch (error) {
                            setGeometryError(String(error));
                          }
                        }}
                      />
                    );
                  }),
                )}
              {polygon &&
                mode === 'vertices' &&
                [polygon.outer, ...polygon.holes].flatMap((ring, ringIndex) =>
                  ring.map((value, index) => {
                    const address = { hole: ringIndex === 0 ? null : ringIndex - 1, index };
                    return (
                      <circle
                        key={ringIndex + ':' + index}
                        className="fatfish-geometry-mode"
                        cx={px(value.x + (center?.x ?? 0))}
                        cy={px(value.y + (center?.y ?? 0))}
                        r={5}
                        fill={
                          vertex?.hole === address.hole && vertex.index === index
                            ? '#fba928'
                            : '#fff'
                        }
                        stroke="#145f85"
                        aria-label={text('vertex') + ' ' + (ringIndex + 1) + ':' + (index + 1)}
                        onPointerDown={(event) => {
                          event.stopPropagation();
                          svg.current?.focus({ preventScroll: true });
                          setVertex(address);
                          pointers.current.set(event.pointerId, {
                            x: event.clientX,
                            y: event.clientY,
                          });
                          drag.current = {
                            pointer: event.pointerId,
                            selection: selected!,
                            start: point(event),
                            level: shown,
                            vertex: address,
                            moveHole: moveHole && address.hole !== null,
                          };
                          svg.current?.setPointerCapture(event.pointerId);
                        }}
                      />
                    );
                  }),
                )}
              {!!drawing.length && (
                <polyline
                  points={drawing
                    .map(
                      (value) =>
                        px(value.x + (center?.x ?? 0)) + ',' + px(value.y + (center?.y ?? 0)),
                    )
                    .join(' ')}
                  fill="none"
                  stroke={geometryError ? '#cf263d' : '#db5d34'}
                  strokeWidth={2}
                />
              )}
              {shown.fish.map((fish) => (
                <g key={fish.id} transform={'translate(' + px(fish.x) + ' ' + px(fish.y) + ')'}>
                  <path
                    d={fishPath}
                    fill={fishCollision(shown, fish) ? '#d2223b88' : '#227bb216'}
                    stroke={
                      selected?.kind === 'fish' && selected.id === fish.id
                        ? '#122d44'
                        : fishCollision(shown, fish)
                          ? '#d2223b'
                          : '#24577788'
                    }
                    strokeWidth={2}
                  />
                  <path
                    d="M 0 0 L 16 0 M 12 -4 L 16 0 L 12 4"
                    transform={'rotate(' + (fish.heading * 360) / 4096 + ')'}
                    stroke="white"
                    fill="none"
                    strokeWidth={2}
                  />
                  {labels && (
                    <text className="fatfish-object-label" y="-18">
                      {name({ kind: 'fish', id: fish.id })}
                    </text>
                  )}
                </g>
              ))}
              {ghost && (
                <g
                  className="fatfish-placement-ghost"
                  data-invalid={ghostInvalid}
                  pointerEvents="none"
                  transform={'translate(' + px(ghost.x) + ' ' + px(ghost.y) + ')'}
                >
                  <path
                    d={fishPath}
                    fill={ghostInvalid ? '#e1495577' : '#397fbc88'}
                    stroke={ghostInvalid ? '#b21635' : '#174f81'}
                    strokeWidth={2}
                  />
                  <path
                    d="M -6 0 L 20 0 M 14 -5 L 20 0 L 14 5"
                    transform={'rotate(' + (fishHeading * 360) / 4096 + ')'}
                    fill="none"
                    stroke="white"
                    strokeWidth={2}
                  />
                </g>
              )}
              {cursor && (geometryError || ghostInvalid || (hover && !onPlaceFish)) && (
                <text
                  className={
                    'fatfish-cursor-hint' + (geometryError || ghostInvalid ? ' is-error' : '')
                  }
                  pointerEvents="none"
                  x={Math.max(-115, Math.min(360, px(cursor.x) + 12))}
                  y={Math.max(-110, px(cursor.y) - 20)}
                >
                  {geometryError
                    ? describeIssue(geometryError, text)
                    : ghostInvalid
                      ? text('placement_collision')
                      : hover
                        ? name(hover)
                        : ''}
                </text>
              )}
            </svg>
          </div>
        </div>
      </div>
      {hint && <p role="status">{hint}</p>}
      {geometryError && (
        <div className="fatfish-error" role="alert">
          <p>{describeIssue(geometryError, text)}</p>
          <details>
            <summary>{text('technical_details')}</summary>
            {geometryError}
          </details>
        </div>
      )}
      <details className="fatfish-shortcuts">
        <summary>{text('shortcuts')}</summary>
        <p>{text('editor_shortcuts')}</p>
      </details>
    </div>
  );
}

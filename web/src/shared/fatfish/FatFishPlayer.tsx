import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { useActivityText } from '@shared/limitedactivities/copy';
import { normalizeLevel } from './engine/canonical';
import { containsPolygon, pointInRing, translatePolygon } from './engine/geometry';
import { DRAG_BUFFER, FIELD_HEIGHT, FIELD_WIDTH, type EngineState, type Level, type Point, type Polygon } from './engine/types';
import { fishFootprint } from './engine/trig_helpers';
import { formatScoreUnits, type FatFishChallenge } from './api';
import { loadFatFishArt, toolIcon, type FatFishArt } from './art';
import type { FatFishSessionController } from './session';
import { useFatFishMusic } from './useFatFishMusic';
import './player.css';

const unit = 64;
const width = FIELD_WIDTH / unit;
const height = FIELD_HEIGHT / unit;
const toolNames: Readonly<Record<string, readonly [string, string]>> = {
  barrier: ['键帽挡板', 'Keycap barrier'], memory: ['缓存圆墩', 'Cache puck'],
  fan: ['加班小风扇', 'Desk fan'], light: ['今日供饭屏', 'Rice monitor'],
  cup: ['白饭储备', 'Rice reserve'],
};
interface ToolDrag {
  pointerID: number;
  toolID: number;
  startX: number;
  startY: number;
  dx: number;
  dy: number;
  mode: 'board' | 'tray' | 'place';
  moved: boolean;
  hadPlacement: boolean;
}
interface Placement { toolID: number; x: number; y: number }
function nearOutline(polygon: Polygon, point: Point, radius: number): boolean {
  if (polygon.holes.some((hole) => pointInRing(hole, point) >= 0)) return false;
  const limit = radius * radius;
  return polygon.outer.some((a, index) => {
    const b = polygon.outer[(index + 1) % polygon.outer.length];
    const dx = b.x - a.x, dy = b.y - a.y;
    const fraction = dx || dy ? Math.max(0, Math.min(1,
      ((point.x - a.x) * dx + (point.y - a.y) * dy) / (dx * dx + dy * dy))) : 0;
    const x = a.x + fraction * dx - point.x, y = a.y + fraction * dy - point.y;
    return x * x + y * y <= limit;
  });
}

function polygonPath(ctx: CanvasRenderingContext2D, polygon: Polygon, offsetX = 0, offsetY = 0): void {
  const ring = (points: Point[]) => {
    points.forEach((point, index) => {
      const x = (point.x + offsetX) / unit, y = (point.y + offsetY) / unit;
      if (index === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    });
    ctx.closePath();
  };
  ctx.beginPath();
  ring(polygon.outer);
  polygon.holes.forEach(ring);
}
function shapeBounds(polygon: Polygon, offsetX = 0, offsetY = 0) {
  const xs = polygon.outer.map((point) => (point.x + offsetX) / unit);
  const ys = polygon.outer.map((point) => (point.y + offsetY) / unit);
  return { x: Math.min(...xs), y: Math.min(...ys), width: Math.max(...xs) - Math.min(...xs),
    height: Math.max(...ys) - Math.min(...ys) };
}
function drawShape(ctx: CanvasRenderingContext2D, polygon: Polygon, fill: string,
  stroke: string, icon?: HTMLImageElement, offsetX = 0, offsetY = 0,
  minimumWidth = 52, minimumHeight = 52, rotation = 0, toolBody = false): void {
  const box = shapeBounds(polygon, offsetX, offsetY);
  polygonPath(ctx, polygon, offsetX, offsetY);
  ctx.fillStyle = fill;
  ctx.fill('evenodd');
  if (toolBody) {
    ctx.save();
    polygonPath(ctx, polygon, offsetX, offsetY);
    ctx.clip('evenodd');
    ctx.beginPath();
    for (let x = box.x - box.height; x < box.x + box.width; x += 10) {
      ctx.moveTo(x, box.y);
      ctx.lineTo(x + box.height, box.y + box.height);
    }
    ctx.strokeStyle = '#f8fbf58c'; ctx.lineWidth = 2; ctx.stroke();
    ctx.restore();
  }
  if (icon && icon.naturalWidth > 0 && icon.naturalHeight > 0) {
    const detached = toolBody && (box.width < 32 || box.height < 32 || polygon.holes.length > 0);
    const targetWidth = detached ? 42 : Math.min(180, Math.max(minimumWidth, box.width + 12));
    const targetHeight = detached ? 42 : Math.min(160, Math.max(minimumHeight, box.height + 12));
    const scale = Math.min(targetWidth / icon.naturalWidth, targetHeight / icon.naturalHeight);
    const displayWidth = icon.naturalWidth * scale, displayHeight = icon.naturalHeight * scale;
    ctx.save();
    if (detached) {
      const right = box.x + box.width + displayWidth + 12 <= width;
      const iconX = right ? box.x + box.width + 8 + displayWidth / 2 : box.x - 8 - displayWidth / 2;
      const iconY = Math.max(displayHeight / 2 + 4, Math.min(height - displayHeight / 2 - 4,
        box.y + Math.min(box.height / 2, 28)));
      ctx.beginPath();
      ctx.moveTo(box.x + box.width / 2, iconY);
      ctx.lineTo(iconX, iconY);
      ctx.strokeStyle = stroke; ctx.lineWidth = 1; ctx.setLineDash([3, 3]); ctx.stroke(); ctx.setLineDash([]);
      ctx.translate(iconX, iconY);
      ctx.fillStyle = '#fff9e5e8'; ctx.strokeStyle = stroke; ctx.lineWidth = 1;
      ctx.beginPath(); ctx.roundRect(-displayWidth / 2 - 3, -displayHeight / 2 - 3,
        displayWidth + 6, displayHeight + 6, 7); ctx.fill(); ctx.stroke();
    } else ctx.translate(box.x + box.width / 2, box.y + box.height / 2);
    ctx.rotate(rotation);
    ctx.shadowColor = '#233f4670';
    ctx.shadowBlur = 5;
    ctx.shadowOffsetY = 3;
    ctx.drawImage(icon, -displayWidth / 2, -displayHeight / 2, displayWidth, displayHeight);
    ctx.restore();
  }
  polygonPath(ctx, polygon, offsetX, offsetY);
  ctx.strokeStyle = stroke;
  ctx.lineWidth = 1.7;
  ctx.stroke();
}
function drawFatFishScene(ctx: CanvasRenderingContext2D, level: Level, state?: EngineState | null,
  art?: FatFishArt | null): void {
  ctx.clearRect(0, 0, width, height);
  ctx.fillStyle = '#faf2df';
  ctx.fillRect(0, 0, width, height);
  const floor = art?.icons.get('floor-tile');
  if (floor) {
    ctx.save(); ctx.globalAlpha = .56;
    for (let y = 0; y < height; y += 160) for (let x = 0; x < width; x += 160) {
      ctx.drawImage(floor, x, y, 160, 160);
    }
    ctx.restore();
  }
  level.directions.forEach((item) => drawShape(ctx, item.polygon, '#62b9c535', '#2d7486',
    art?.icons.get('rice-arrow'), 0, 0, 52, 52, item.heading * Math.PI * 2 / 4096));
  level.solids.forEach((item) => {
    const box = shapeBounds(item.polygon);
    const icon = box.width > box.height * 1.4 ? 'server-wall' : box.height > box.width * 1.4 ? 'server-rack' : 'router-wedge';
    drawShape(ctx, item.polygon, '#5e829955', '#345c75', art?.icons.get(icon), 0, 0, 42, 42);
  });
  level.hazards.forEach((item) => drawShape(ctx, item.polygon, '#e8867960', '#ae4e52',
    art?.icons.get('offline-pool'), 0, 0, 58, 48));
  level.bowls.forEach((item) => {
    const count = state?.bowls.find((entry) => entry.id === item.id)?.count ?? 0;
    drawShape(ctx, item.polygon, '#f9cf8c66', '#9a693e',
      art?.icons.get(count >= item.capacity ? 'rice-goal-full' : 'rice-goal'), 0, 0, 68, 62);
  });
  level.switches.forEach((item) => {
    const active = state?.switches.find((entry) => entry.id === item.id)?.active;
    drawShape(ctx, item.polygon, active ? '#8bd19f66' : '#f1db9766', '#64866d',
      art?.icons.get(active ? 'switch-on' : 'switch-off'));
  });
  level.gates.forEach((item) => {
    const open = state?.gates.find((entry) => entry.id === item.id)?.open ?? item.initially_open;
    drawShape(ctx, item.polygon, open ? '#8bd19f55' : '#59697855', open ? '#519b70' : '#354c5b',
      art?.icons.get(open ? 'gate-open' : 'gate-closed'), 0, 0, 60, 48);
  });
  level.tools.forEach((item) => {
    const current = state?.tools.find((entry) => entry.id === item.id) ?? item;
    if (current.placed) {
      const box = shapeBounds(item.polygon);
      const icon = item.resource_key === 'barrier' && box.height > box.width * 1.4
        ? 'keycap-barrier-vertical' : toolIcon(item.resource_key) ?? '';
      drawShape(ctx, item.polygon, '#3e98c799', '#285d83', art?.icons.get(icon), current.x, current.y,
        item.resource_key === 'barrier' ? 58 : 64, item.resource_key === 'barrier' ? 58 : 64, 0, true);
    }
  });
  const atlas = art?.atlas;
  const elapsed = (state?.tick ?? 0) * 1000 / 60;
  for (const fish of state?.fish ?? level.fish) {
    const status = 'status' in fish ? fish.status : 'walking';
    const x = fish.x / unit, y = fish.y / unit;
    const heading = ((fish.heading + 512) % 4096) >> 10;
    const animationName = status === 'fed' ? 'eat' : status === 'lost' || state?.terminal ? 'idle' :
      ['walk-right', 'walk-down', 'walk-left', 'walk-up'][heading];
    const animation = atlas?.animations[animationName];
    let frameID: string | undefined;
    if (animation?.frames.length && animation.frames.length === animation.durationsMs.length) {
      const duration = animation.durationsMs.reduce((sum, value) => sum + value, 0);
      let cursor = duration > 0 ? elapsed % duration : 0;
      for (let index = 0; index < animation.frames.length; index++) {
        cursor -= animation.durationsMs[index];
        if (cursor < 0) { frameID = animation.frames[index]; break; }
      }
    }
    const frame = frameID ? atlas?.frames[frameID] : undefined;
    const sprite = frame ? art?.sources[frame.source] : undefined;
    ctx.save(); ctx.translate(x, y);
    ctx.fillStyle = '#254d5660';
    ctx.beginPath(); ctx.ellipse(0, 1, 12, 5, 0, 0, Math.PI * 2); ctx.fill();
    if (status === 'lost') ctx.globalAlpha = .5;
    if (frame && sprite && frame.referenceHeight > 0) {
      const scale = (atlas?.defaultDisplayHeight ?? 48) / frame.referenceHeight;
      const [sx, sy, sw, sh] = frame.rect;
      ctx.drawImage(sprite, sx, sy, sw, sh, -frame.pivot[0] * scale,
        -frame.pivot[1] * scale, sw * scale, sh * scale);
    } else {
      ctx.rotate(fish.heading * Math.PI * 2 / 4096);
      ctx.fillStyle = '#426f9b';
      ctx.beginPath(); ctx.ellipse(0, 0, 8, 5.5, 0, 0, Math.PI * 2); ctx.fill();
      ctx.beginPath(); ctx.moveTo(-6, 0); ctx.lineTo(-13, -5); ctx.lineTo(-13, 5); ctx.closePath(); ctx.fill();
      ctx.fillStyle = '#f8faf6'; ctx.beginPath(); ctx.arc(3, -2, 1.2, 0, Math.PI * 2); ctx.fill();
    }
    ctx.restore();
    polygonPath(ctx, { outer: fishFootprint(fish.x, fish.y), holes: [] });
    ctx.lineWidth = 1; ctx.strokeStyle = status === 'lost' ? '#aa4444' : status === 'fed' ? '#3d925d' : '#184f71b0'; ctx.stroke();
    if (status === 'lost') {
      const bubble = art?.icons.get('offline-bubble');
      if (bubble) ctx.drawImage(bubble, x - 10, y - 18, 20, 20);
    } else if (status === 'walking' && 'turn_dir' in fish && fish.turn_dir !== 0) {
      ctx.beginPath(); ctx.arc(x, y, 11, -.6, .6);
      ctx.lineWidth = 1.3; ctx.strokeStyle = '#277b91'; ctx.stroke();
    }
  }
}

export function FatFishCanvas({ level, state, decorative = false, keyboardHelpID,
  onPointerDown, onPointerMove, onPointerEnd, onPointerCancel, onKeyDown }: {
  level: Level;
  state?: EngineState | null;
  decorative?: boolean;
  keyboardHelpID?: string;
  onPointerDown?: (point: Point, event: React.PointerEvent<HTMLCanvasElement>) => boolean | void;
  onPointerMove?: (point: Point, event: React.PointerEvent<HTMLCanvasElement>) => void;
  onPointerEnd?: (event: React.PointerEvent<HTMLCanvasElement>) => void;
  onPointerCancel?: (event: React.PointerEvent<HTMLCanvasElement>) => void;
  onKeyDown?: (key: string, shift: boolean) => void;
}) {
  const ref = useRef<HTMLCanvasElement>(null);
  const normalized = useMemo(() => normalizeLevel(level), [level]);
  const [art, setArt] = useState<FatFishArt | null>(null);
  useEffect(() => {
    let live = true;
    void loadFatFishArt().then((value) => { if (live) setArt(value); });
    return () => { live = false; };
  }, []);
  useEffect(() => {
    const ctx = ref.current?.getContext('2d');
    if (ctx) drawFatFishScene(ctx, normalized, state, art);
  }, [normalized, state, art]);
  const point = (event: React.PointerEvent<HTMLCanvasElement>): Point => {
    const rect = event.currentTarget.getBoundingClientRect();
    return {
      x: Math.round((event.clientX - rect.left) * FIELD_WIDTH / rect.width),
      y: Math.round((event.clientY - rect.top) * FIELD_HEIGHT / rect.height),
    };
  };
  return <canvas ref={ref} className="fatfish-canvas" width={width} height={height}
    data-fish-board={onPointerDown ? '' : undefined}
    role={decorative ? 'presentation' : 'img'} aria-label={decorative ? undefined : 'Fat fish playfield'}
    aria-describedby={onKeyDown ? keyboardHelpID : undefined}
    aria-hidden={decorative || undefined} tabIndex={!decorative && onKeyDown ? 0 : undefined}
    style={{ touchAction: 'none', pointerEvents: decorative ? 'none' : undefined }}
    onKeyDown={(event) => {
      if (['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Backspace', 'Delete', 'Escape'].includes(event.key) ||
          event.key.toLowerCase() === 'n') {
        event.preventDefault(); onKeyDown?.(event.key, event.shiftKey);
      }
    }}
    onPointerDown={(event) => {
      if (!onPointerDown || !event.isPrimary || event.button !== 0) return;
      if (onPointerDown(point(event), event) !== false) event.currentTarget.setPointerCapture?.(event.pointerId);
    }}
    onPointerMove={(event) => { if (onPointerMove && event.isPrimary) onPointerMove(point(event), event); }}
    onPointerUp={(event) => onPointerEnd?.(event)}
    onPointerCancel={(event) => onPointerCancel?.(event)} />;
}

export function FatFishPlayer({ controller, onTerminal, mode = 'user' }: {
  controller: FatFishSessionController;
  onTerminal?: (view: FatFishChallenge) => void;
  mode?: 'user' | 'playtest';
}) {
  const t = useActivityText();
  const keyboardHelpID = useId();
  const [observed, setObserved] = useState(() => ({ controller, snapshot: controller.snapshot() }));
  const snapshot = observed.controller === controller ? observed.snapshot : controller.snapshot();
  const [selectedTool, setSelectedTool] = useState<number | null>(null);
  const [draggingTool, setDraggingTool] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const boardRef = useRef<HTMLDivElement>(null);
  const yardRef = useRef<HTMLDivElement>(null);
  const drag = useRef<ToolDrag | null>(null);
  const pendingPlacement = useRef<Placement | null>(null);
  const placementFrame = useRef<number | null>(null);
  const lastPlacement = useRef<Placement | null>(null);
  useEffect(() => controller.subscribe(() => setObserved({ controller, snapshot: controller.snapshot() })), [controller]);
  useEffect(() => {
    let frame = 0;
    const run = () => { controller.advance(); frame = requestAnimationFrame(run); };
    frame = requestAnimationFrame(run);
    const save = window.setInterval(() => {
      if (controller.snapshot().canPlay) void controller.flush().catch((error: unknown) =>
        setActionError(error instanceof Error ? error.message : String(error)));
    }, 1000);
    const preserve = () => {
      if (controller.snapshot().canPlay) void controller.flush().catch((error: unknown) =>
        setActionError(error instanceof Error ? error.message : String(error)));
    };
    const visibility = () => { if (document.visibilityState === 'hidden') preserve(); };
    document.addEventListener('visibilitychange', visibility);
    window.addEventListener('pagehide', preserve);
    const poll = window.setInterval(() => {
      if (['prepared', 'verifying'].includes(controller.snapshot().phase)) void controller.poll().then((view) => {
        if (view && ['settled_pass', 'settled_fail', 'abandoned', 'expired', 'cancelled_refunded'].includes(view.state)) onTerminal?.(view);
      }).catch((error: unknown) => setActionError(error instanceof Error ? error.message : String(error)));
    }, 2000);
    return () => { cancelAnimationFrame(frame); clearInterval(save); clearInterval(poll);
      document.removeEventListener('visibilitychange', visibility); window.removeEventListener('pagehide', preserve); };
  }, [controller, onTerminal]);
  useEffect(() => {
    const blur = () => {
      drag.current = null;
      pendingPlacement.current = null;
      if (placementFrame.current !== null) cancelAnimationFrame(placementFrame.current);
      placementFrame.current = null;
      setDraggingTool(null);
    };
    window.addEventListener('blur', blur);
    return () => { window.removeEventListener('blur', blur);
      pendingPlacement.current = null;
      if (placementFrame.current !== null) cancelAnimationFrame(placementFrame.current);
      placementFrame.current = null;
      drag.current = null;
    };
  }, [controller]);
  const level = snapshot.challenge?.level;
  const { enabled: musicEnabled, unavailable: musicUnavailable, toggle: toggleMusic,
    host: musicHost } = useFatFishMusic(controller, Boolean(level) && snapshot.phase !== 'read_only');
  const normalizedLevel = useMemo(() => level ? normalizeLevel(level) : null, [level]);
  const submit = async () => {
    setBusy(true); setActionError(null);
    try { const view = await controller.submit(); if (view.state !== 'verifying') onTerminal?.(view); }
    catch (error) { setActionError(error instanceof Error ? error.message : String(error)); }
    finally { setBusy(false); }
  };
  const tools = normalizedLevel?.tools ?? [];
  const currentTool = (id: number) => snapshot.state?.tools.find((item) => item.id === id) ??
    tools.find((item) => item.id === id);
  const boardPoint = (clientX: number, clientY: number): Point | null => {
    const rect = boardRef.current?.getBoundingClientRect();
    if (!rect?.width || !rect.height) return null;
    const point = { x: Math.round((clientX - rect.left) * FIELD_WIDTH / rect.width),
      y: Math.round((clientY - rect.top) * FIELD_HEIGHT / rect.height) };
    return point.x >= -DRAG_BUFFER && point.x <= FIELD_WIDTH + DRAG_BUFFER &&
      point.y >= -DRAG_BUFFER && point.y <= FIELD_HEIGHT + DRAG_BUFFER ? point : null;
  };
  const placementAt = (active: ToolDrag, point: Point): Placement => ({ toolID: active.toolID,
    x: Math.max(-DRAG_BUFFER, Math.min(FIELD_WIDTH + DRAG_BUFFER, point.x + active.dx)),
    y: Math.max(-DRAG_BUFFER, Math.min(FIELD_HEIGHT + DRAG_BUFFER, point.y + active.dy)) });
  const inYard = (clientX: number, clientY: number) => {
    const rect = yardRef.current?.getBoundingClientRect();
    return !!rect && clientX >= rect.left && clientX <= rect.right && clientY >= rect.top && clientY <= rect.bottom;
  };
  const sendPlacement = (placement: Placement) => {
    const previous = lastPlacement.current;
    if (previous?.toolID === placement.toolID && previous.x === placement.x && previous.y === placement.y) return;
    lastPlacement.current = placement;
    void controller.place(placement.toolID, placement.x, placement.y).catch((error: unknown) => {
      if (lastPlacement.current === placement) lastPlacement.current = null;
      setActionError(error instanceof Error ? error.message : String(error));
    });
  };
  const flushPlacement = () => {
    if (placementFrame.current !== null) cancelAnimationFrame(placementFrame.current);
    placementFrame.current = null;
    const placement = pendingPlacement.current;
    pendingPlacement.current = null;
    if (placement) sendPlacement(placement);
  };
  const queuePlacement = (placement: Placement) => {
    pendingPlacement.current = placement;
    if (drag.current) drag.current.hadPlacement = true;
    if (placementFrame.current === null) placementFrame.current = requestAnimationFrame(flushPlacement);
  };
  const cancelPending = () => {
    if (placementFrame.current !== null) cancelAnimationFrame(placementFrame.current);
    placementFrame.current = null;
    pendingPlacement.current = null;
  };
  const pickTool = (point: Point, pointerType: string) => {
    const radius = (pointerType === 'touch' ? 18 : 8) * unit;
    return [...tools].reverse().find((tool) => {
      const current = currentTool(tool.id);
      if (!current?.placed) return false;
      const polygon = translatePolygon(tool.polygon, current.x, current.y);
      return containsPolygon(polygon, point) || nearOutline(polygon, point, radius);
    });
  };
  const moveDrag = (clientX: number, clientY: number, pointerID: number) => {
    const active = drag.current;
    if (!active || active.pointerID !== pointerID || !snapshot.canPlay) return;
    if (Math.hypot(clientX - active.startX, clientY - active.startY) > 4) active.moved = true;
    if (!active.moved && active.mode !== 'place') return;
    if (inYard(clientX, clientY)) { cancelPending(); return; }
    const point = boardPoint(clientX, clientY);
    if (point) queuePlacement(placementAt(active, point));
  };
  const endDrag = (clientX: number, clientY: number, pointerID: number, cancelled = false) => {
    const active = drag.current;
    if (!active || active.pointerID !== pointerID) return;
    if (!cancelled && snapshot.canPlay) {
      if (inYard(clientX, clientY)) {
        cancelPending();
        if (active.mode === 'board' || active.hadPlacement) {
          void controller.returnTool(active.toolID).catch((error: unknown) =>
            setActionError(error instanceof Error ? error.message : String(error)));
        }
      } else if (active.moved || active.mode === 'place') {
        const point = boardPoint(clientX, clientY);
        if (point) queuePlacement(placementAt(active, point));
        flushPlacement();
      }
    } else cancelPending();
    drag.current = null;
    setDraggingTool(null);
  };
  const startBoard = (point: Point, event: React.PointerEvent<HTMLCanvasElement>) => {
    if (drag.current || !snapshot.canPlay) return false;
    const hit = pickTool(point, event.pointerType);
    const toolID = hit?.id ?? selectedTool;
    if (toolID === null || toolID === undefined || !currentTool(toolID)) return false;
    const current = currentTool(toolID)!;
    drag.current = { pointerID: event.pointerId, toolID, startX: event.clientX, startY: event.clientY,
      dx: hit ? current.x - point.x : 0, dy: hit ? current.y - point.y : 0,
      mode: hit ? 'board' : 'place', moved: false, hadPlacement: false };
    lastPlacement.current = null;
    setSelectedTool(toolID); setDraggingTool(toolID);
    event.currentTarget.focus({ preventScroll: true });
    if (!hit) queuePlacement({ toolID, x: point.x, y: point.y });
    return true;
  };
  const startTray = (event: React.PointerEvent<HTMLDivElement>) => {
    if (!event.isPrimary || event.button !== 0 || drag.current || !snapshot.canPlay) return;
    const element = event.target instanceof Element ? event.target.closest<HTMLButtonElement>('[data-tool-id]') : null;
    const toolID = Number(element?.dataset.toolId);
    if (!element || !Number.isSafeInteger(toolID) || currentTool(toolID)?.placed) return;
    drag.current = { pointerID: event.pointerId, toolID, startX: event.clientX, startY: event.clientY,
      dx: 0, dy: 0, mode: 'tray', moved: false, hadPlacement: false };
    lastPlacement.current = null;
    setSelectedTool(toolID); setDraggingTool(toolID);
    event.currentTarget.setPointerCapture?.(event.pointerId);
  };
  const rescued = snapshot.state?.fish.filter((fish) => fish.status === 'fed').length ?? 0;
  const lost = snapshot.state?.fish.filter((fish) => fish.status === 'lost').length ?? 0;
  const stagedTools = tools.filter((tool) => !currentTool(tool.id)?.placed);
  const remainingSeconds = Math.max(0, Math.ceil(((level?.duration_seconds ?? 0) * 60 - (snapshot.state?.tick ?? 0)) / 60));
  const timeLeft = `${Math.floor(remainingSeconds / 60)}:${String(remainingSeconds % 60).padStart(2, '0')}`;
  return <section className="fatfish-player" aria-label={t('大肥鱼游玩', 'Fat fish play')}
    data-can-play={snapshot.canPlay} data-dragging={draggingTool !== null}>
    <div className="fatfish-player__banner">
      <img src="/assets/fatfish/svg/rice-goal.svg" alt="" aria-hidden="true" />
      <div><strong>{t('大肥鱼，开饭啦！', 'Fat Fish, dinner is ready!')}</strong>
        <span>{t('带小鱼去吃饭', 'Guide the fish to rice')}</span></div>
    </div>
    <div className="fatfish-player__music">
      <button type="button" data-fatfish-music-toggle="" aria-pressed={musicEnabled}
        disabled={!level || snapshot.phase === 'read_only'}
        onClick={toggleMusic}>{musicEnabled ? t('关闭背景音乐', 'Mute music') : t('开启背景音乐', 'Play music')}</button>
      <span className="fatfish-player__music-credit">
        Monkeys Spinning Monkeys · Kevin MacLeod (<a href="https://incompetech.com/">incompetech.com</a>) · <a
          href="https://creativecommons.org/licenses/by/4.0/">CC BY 4.0</a>
      </span>
      <span ref={musicHost} hidden aria-hidden="true" />
      {musicUnavailable ? <p role="alert">{t('音乐无法播放，请重试开启；游戏会继续运行。',
        'Music could not play. Try turning it on again; the game will continue.')}</p> : null}
    </div>
    <div className="fatfish-player__status" role="status" data-fish-tick={snapshot.state?.tick} data-fish-fed={rescued}>
      {snapshot.phase === 'read_only' ? <span className="fatfish-player__notice">{t('本局正在另一标签页游玩；此页只能查看。', 'This challenge is active in another tab. This page is read-only.')}</span> : null}
      {snapshot.phase === 'prepared' ? <span className="fatfish-player__notice">{t('先看看布局，开始后才能移动道具。', 'Preview the layout; tools can move after start.')}</span> : null}
      {snapshot.phase === 'waiting' ? <span className="fatfish-player__notice">{t('即将开始', 'Starting shortly')}</span> : null}
      {snapshot.phase === 'verifying' ? <span className="fatfish-player__notice">{t('服务端正在核验成绩。', 'The server is verifying the result.')}</span> : null}
      {snapshot.phase === 'terminal' ? <span className="fatfish-player__notice">{t('本局已结束。', 'This challenge has ended.')}</span> : null}
      {snapshot.state ? <span className="fatfish-player__stat"><small>{t('剩余时间', 'Time left')}</small><strong>{timeLeft}</strong></span> : null}
      {snapshot.state ? <span className="fatfish-player__stat"><small>{t('已救', 'Rescued')}</small><strong>{rescued}/{snapshot.state.fish.length}</strong></span> : null}
      {snapshot.state ? <span className="fatfish-player__stat"><small>{t('失去', 'Lost')}</small><strong>{lost}</strong></span> : null}
    </div>
    {level ? <div className="fatfish-player__board" ref={boardRef}>
      <FatFishCanvas level={level} state={snapshot.state}
      keyboardHelpID={keyboardHelpID}
      onPointerDown={startBoard}
      onPointerMove={(_point, event) => moveDrag(event.clientX, event.clientY, event.pointerId)}
      onPointerEnd={(event) => endDrag(event.clientX, event.clientY, event.pointerId)}
      onPointerCancel={(event) => endDrag(event.clientX, event.clientY, event.pointerId, true)}
      onKeyDown={(key, shift) => {
        if (!snapshot.canPlay) return;
        if (key.toLowerCase() === 'n') {
          if (tools.length) {
            const current = tools.findIndex((item) => item.id === selectedTool);
            setSelectedTool(tools[(current + 1) % tools.length].id);
          }
          return;
        }
        if (key === 'Escape') { setSelectedTool(null); return; }
        if (selectedTool === null) return;
        if (key === 'Backspace' || key === 'Delete') {
          void controller.returnTool(selectedTool).catch((failure: unknown) =>
            setActionError(failure instanceof Error ? failure.message : String(failure)));
          return;
        }
        const tool = snapshot.state?.tools.find((item) => item.id === selectedTool) ??
          normalizedLevel?.tools.find((item) => item.id === selectedTool);
        if (!tool) return;
        const step = (shift ? 2 : 10) * unit;
        const dx = key === 'ArrowLeft' ? -step : key === 'ArrowRight' ? step : 0;
        const dy = key === 'ArrowUp' ? -step : key === 'ArrowDown' ? step : 0;
        const x = Math.max(-DRAG_BUFFER, Math.min(FIELD_WIDTH + DRAG_BUFFER, tool.x + dx));
        const y = Math.max(-DRAG_BUFFER, Math.min(FIELD_HEIGHT + DRAG_BUFFER, tool.y + dy));
        void controller.place(selectedTool, x, y).catch((failure: unknown) =>
          setActionError(failure instanceof Error ? failure.message : String(failure)));
      }} />
    </div> : null}
    {level ? <div className="fatfish-player__yard" ref={yardRef} data-staging-area
      onPointerDown={startTray}
      onPointerMove={(event) => moveDrag(event.clientX, event.clientY, event.pointerId)}
      onPointerUp={(event) => endDrag(event.clientX, event.clientY, event.pointerId)}
      onPointerCancel={(event) => endDrag(event.clientX, event.clientY, event.pointerId, true)}>
      <div className="fatfish-player__yard-heading"><strong>{t('道具台', 'Tool bench')}</strong>
        <span>{snapshot.canPlay ? t('拖到场内摆放；场上的道具可拖回来。', 'Drag pieces into the field, or return them here.')
          : t('开始后才能摆放。', 'Pieces unlock after start.')}</span></div>
      <div className="fatfish-player__yard-pieces">
        {stagedTools.length ? stagedTools.map((tool) => {
          const name = toolNames[tool.resource_key];
          const icon = toolIcon(tool.resource_key);
          const copies = tools.filter((candidate) => candidate.resource_key === tool.resource_key);
          const copyNumber = copies.findIndex((candidate) => candidate.id === tool.id) + 1;
          return <button type="button" key={tool.id} data-tool-id={tool.id}
            className={`fatfish-player__piece${selectedTool === tool.id ? ' is-selected' : ''}`}
            aria-pressed={selectedTool === tool.id} disabled={!snapshot.canPlay}
            onDragStart={(event) => event.preventDefault()}
            onClick={() => { setSelectedTool(tool.id); boardRef.current?.querySelector('canvas')?.focus({ preventScroll: true }); }}>
            {icon ? <img src={`/assets/fatfish/svg/${icon}.svg`} alt="" aria-hidden="true" draggable={false} /> : null}
            <span>{name ? t(name[0], name[1]) : t('道具', 'Piece')}
              {copies.length > 1 ? <small>{copyNumber}/{copies.length}</small> : null}</span>
          </button>;
        }) : <span className="fatfish-player__yard-empty">{t('道具都在场上', 'All pieces are on the field')}</span>}
      </div>
    </div> : null}
    {level ? <p className="fatfish-player__keyboard-hint" id={keyboardHelpID}>
      {t('键盘：聚焦场地后按 N 切换道具，方向键移动，Delete 收回。',
        'Keyboard: focus the field, press N to choose a piece, arrows to move, Delete to return.')}
    </p> : null}
    <div className="fatfish-player__controls">
      <button type="button" disabled={!snapshot.canPlay || selectedTool === null} onClick={() => {
        if (selectedTool !== null) void controller.returnTool(selectedTool).catch((error: unknown) =>
          setActionError(error instanceof Error ? error.message : String(error)));
      }}>{t('收回工具', 'Return tool')}</button>
      <button type="button" className="fatfish-player__finish" disabled={!snapshot.canPlay || !snapshot.state || snapshot.state.terminal}
        onClick={() => {
          const remaining = snapshot.state?.fish.filter((fish) => fish.status === 'walking').length ?? 0;
          if (window.confirm(t(`现在按已救数量结束？剩余 ${remaining} 条鱼不计入成绩；服务端仍会核验。`,
            `Finish with the current rescued count? ${remaining} remaining fish will not count; the server will verify.`)))
            void controller.finish().catch((error: unknown) => setActionError(error instanceof Error ? error.message : String(error)));
        }}>{t('完成', 'Finish')}</button>
      {snapshot.provisional && snapshot.challenge?.state === 'active' ? <button type="button" className="fatfish-player__submit" disabled={busy}
        onClick={() => void submit()}>{t('提交核验', 'Submit for verification')}</button> : null}
      {mode === 'user' && snapshot.phase !== 'terminal' ? <button type="button" disabled={busy || !snapshot.challenge}
        onClick={() => {
          if (window.confirm(t('放弃本局？正式开局的门票不退还。', 'Abandon this challenge? A started ticket is not refunded.')))
            void controller.abandon().then(onTerminal).catch((error: unknown) => setActionError(error instanceof Error ? error.message : String(error)));
        }}>{t('放弃', 'Abandon')}</button> : null}
    </div>
    {snapshot.provisional ? <p>{t('本地预估，仅供参考', 'Local estimate only')} · {snapshot.provisional.fed}/{snapshot.provisional.total} · {snapshot.provisional.stars}★ · {formatScoreUnits(snapshot.provisional.score_units)}</p> : null}
    {snapshot.challenge?.result ? <p>{t('已核验', 'Verified')} · {snapshot.challenge.result.fed}/{snapshot.challenge.result.total} · {snapshot.challenge.result.stars}★ · {formatScoreUnits(snapshot.challenge.result.score_units)}</p> : null}
    {actionError || snapshot.error ? <p role="alert">{actionError ?? snapshot.error}</p> : null}
  </section>;
}

const toolLabels = {
  barrier: 'common.spaceKeycap',
  memory: 'common.memoryModule',
  fan: 'common.inferenceCard',
  light: 'common.coolingFins',
  cup: 'common.cachePuck',
} as const;
import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { useActivityText } from '@shared/limitedactivities/copy';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { normalizeLevel } from './engine/canonical';
import { containsPolygon, pointInRing, translatePolygon } from './engine/geometry';
import {
  DRAG_BUFFER,
  FIELD_HEIGHT,
  FIELD_WIDTH,
  type EngineState,
  type Level,
  type Point,
  type Polygon,
} from './engine/types';
import { drawableDraft, drawFatFishScene } from './scene';
import { workspacePoint, toolPosition, WORKSPACE_WIDTH, WORKSPACE_HEIGHT } from './workspace';
import { formatScoreUnits, type FatFishChallenge } from './api';
import { loadFatFishArt, type FatFishArt } from './art';
import type { FatFishSessionController } from './session';
import { useFatFishMusic } from './useFatFishMusic';
import './player.css';
const unit = 64;
const width = WORKSPACE_WIDTH / unit;
const height = WORKSPACE_HEIGHT / unit;
interface ToolDrag {
  pointerID: number;
  toolID: number;
  startX: number;
  startY: number;
  dx: number;
  dy: number;
  moved: boolean;
}
interface Placement {
  toolID: number;
  x: number;
  y: number;
}
function nearOutline(polygon: Polygon, point: Point, radius: number): boolean {
  if (polygon.holes.some((hole) => pointInRing(hole, point) >= 0)) return false;
  const limit = radius * radius;
  return polygon.outer.some((a, index) => {
    const b = polygon.outer[(index + 1) % polygon.outer.length];
    const dx = b.x - a.x,
      dy = b.y - a.y;
    const fraction =
      dx || dy
        ? Math.max(
            0,
            Math.min(1, ((point.x - a.x) * dx + (point.y - a.y) * dy) / (dx * dx + dy * dy)),
          )
        : 0;
    const x = a.x + fraction * dx - point.x,
      y = a.y + fraction * dy - point.y;
    return x * x + y * y <= limit;
  });
}
export function FatFishCanvas({
  level,
  state,
  selectedTool,
  decorative = false,
  keyboardHelpID,
  onPointerDown,
  onPointerMove,
  onPointerEnd,
  onPointerCancel,
  onKeyDown,
}: {
  level: Level;
  state?: EngineState | null;
  selectedTool?: number | null;
  decorative?: boolean;
  keyboardHelpID?: string;
  onPointerDown?: (point: Point, event: React.PointerEvent<HTMLCanvasElement>) => boolean | void;
  onPointerMove?: (point: Point, event: React.PointerEvent<HTMLCanvasElement>) => void;
  onPointerEnd?: (event: React.PointerEvent<HTMLCanvasElement>) => void;
  onPointerCancel?: (event: React.PointerEvent<HTMLCanvasElement>) => void;
  onKeyDown?: (key: string, shift: boolean) => void;
}) {
  const ref = useRef<HTMLCanvasElement>(null);
  // Draft previews remain editable while validation errors are being fixed.
  // A playable level must still pass the canonical protocol validator.
  const normalized = useMemo(
    () => (decorative ? drawableDraft(level) : normalizeLevel(level)),
    [level, decorative],
  );
  const [art, setArt] = useState<FatFishArt | null>(null);
  useEffect(() => {
    let live = true;
    void loadFatFishArt().then((value) => {
      if (live) setArt(value);
    });
    return () => {
      live = false;
    };
  }, []);
  const [pixels, setPixels] = useState({ width, height });
  useEffect(() => {
    const canvas = ref.current;
    if (!canvas || typeof ResizeObserver === 'undefined') return;
    const resize = () => {
      const rect = canvas.getBoundingClientRect();
      const density = Math.min(2, Math.max(1, window.devicePixelRatio || 1));
      if (rect.width > 0)
        setPixels({
          width: Math.round(rect.width * density),
          height: Math.round(((rect.width * height) / width) * density),
        });
    };
    const observer = new ResizeObserver(resize);
    observer.observe(canvas);
    resize();
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    const ctx = ref.current?.getContext('2d');
    if (ctx) {
      const sx = pixels.width / width,
        sy = pixels.height / height;
      ctx.setTransform(sx, 0, 0, sy, 128 * sx, 128 * sy);
      drawFatFishScene(ctx, normalized, state, art, selectedTool);
    }
  }, [normalized, state, art, selectedTool, pixels]);
  const point = (event: React.PointerEvent<HTMLCanvasElement>): Point => {
    const rect = event.currentTarget.getBoundingClientRect();
    return workspacePoint(event.clientX, event.clientY, rect);
  };
  return (
    <canvas
      ref={ref}
      className="fatfish-canvas"
      width={pixels.width}
      height={pixels.height}
      data-fish-board={onPointerDown ? '' : undefined}
      role={decorative ? 'presentation' : 'img'}
      aria-label={decorative ? undefined : 'Fat fish playfield'}
      aria-describedby={onKeyDown ? keyboardHelpID : undefined}
      aria-hidden={decorative || undefined}
      tabIndex={!decorative && onKeyDown ? 0 : undefined}
      style={{ touchAction: 'none', pointerEvents: decorative ? 'none' : undefined }}
      onKeyDown={(event) => {
        if (
          [
            'ArrowUp',
            'ArrowDown',
            'ArrowLeft',
            'ArrowRight',
            'Backspace',
            'Delete',
            'Escape',
          ].includes(event.key) ||
          event.key.toLowerCase() === 'n'
        ) {
          event.preventDefault();
          onKeyDown?.(event.key, event.shiftKey);
        }
      }}
      onPointerDown={(event) => {
        if (!onPointerDown || !event.isPrimary || event.button !== 0) return;
        if (onPointerDown(point(event), event) !== false)
          event.currentTarget.setPointerCapture?.(event.pointerId);
      }}
      onPointerMove={(event) => {
        if (onPointerMove && event.isPrimary) onPointerMove(point(event), event);
      }}
      onPointerUp={(event) => onPointerEnd?.(event)}
      onPointerCancel={(event) => onPointerCancel?.(event)}
    />
  );
}
export function FatFishPlayer({
  controller,
  onTerminal,
  mode = 'user',
}: {
  controller: FatFishSessionController;
  onTerminal?: (view: FatFishChallenge) => void;
  mode?: 'user' | 'playtest';
}) {
  const text = useActivityText();
  const keyboardHelpID = useId();
  const [observed, setObserved] = useState(() => ({ controller, snapshot: controller.snapshot() }));
  const snapshot = observed.controller === controller ? observed.snapshot : controller.snapshot();
  const [selectedTool, setSelectedTool] = useState<number | null>(null);
  const [draggingTool, setDraggingTool] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [confirmAction, setConfirmAction] = useState<'finish' | 'abandon' | null>(null);
  const boardRef = useRef<HTMLDivElement>(null);
  const viewportRef = useRef<HTMLDivElement>(null);
  const [zoomed, setZoomed] = useState(false);
  const pan = useRef<{
    pointerID: number;
    x: number;
    y: number;
    left: number;
    top: number;
  } | null>(null);
  const drag = useRef<ToolDrag | null>(null);
  const pendingPlacement = useRef<Placement | null>(null);
  const placementFrame = useRef<number | null>(null);
  const lastPlacement = useRef<Placement | null>(null);
  useEffect(
    () => controller.subscribe(() => setObserved({ controller, snapshot: controller.snapshot() })),
    [controller],
  );
  useEffect(() => {
    let frame = 0;
    const run = () => {
      controller.advance();
      frame = requestAnimationFrame(run);
    };
    frame = requestAnimationFrame(run);
    const save = window.setInterval(() => {
      if (controller.snapshot().canPlay)
        void controller
          .flush()
          .catch((error: unknown) =>
            setActionError(error instanceof Error ? error.message : String(error)),
          );
    }, 1000);
    const preserve = () => {
      if (controller.snapshot().canPlay)
        void controller
          .flush()
          .catch((error: unknown) =>
            setActionError(error instanceof Error ? error.message : String(error)),
          );
    };
    const visibility = () => {
      if (document.visibilityState === 'hidden') preserve();
    };
    document.addEventListener('visibilitychange', visibility);
    window.addEventListener('pagehide', preserve);
    const poll = window.setInterval(() => {
      if (['prepared', 'verifying'].includes(controller.snapshot().phase))
        void controller
          .poll()
          .then((view) => {
            if (
              view &&
              [
                'settled_pass',
                'settled_fail',
                'abandoned',
                'expired',
                'cancelled_refunded',
              ].includes(view.state)
            )
              onTerminal?.(view);
          })
          .catch((error: unknown) =>
            setActionError(error instanceof Error ? error.message : String(error)),
          );
    }, 2000);
    return () => {
      cancelAnimationFrame(frame);
      clearInterval(save);
      clearInterval(poll);
      document.removeEventListener('visibilitychange', visibility);
      window.removeEventListener('pagehide', preserve);
    };
  }, [controller, onTerminal]);
  useEffect(() => {
    const blur = () => {
      drag.current = null;
      pan.current = null;
      pendingPlacement.current = null;
      if (placementFrame.current !== null) cancelAnimationFrame(placementFrame.current);
      placementFrame.current = null;
      setDraggingTool(null);
    };
    window.addEventListener('blur', blur);
    return () => {
      window.removeEventListener('blur', blur);
      pendingPlacement.current = null;
      if (placementFrame.current !== null) cancelAnimationFrame(placementFrame.current);
      placementFrame.current = null;
      drag.current = null;
    };
  }, [controller]);
  const level = snapshot.challenge?.level;
  const {
    enabled: musicEnabled,
    unavailable: musicUnavailable,
    toggle: toggleMusic,
    host: musicHost,
  } = useFatFishMusic(controller, Boolean(level) && snapshot.phase !== 'read_only');
  const normalizedLevel = useMemo(() => (level ? normalizeLevel(level) : null), [level]);
  const submit = async () => {
    setBusy(true);
    setActionError(null);
    try {
      const view = await controller.submit();
      if (view.state !== 'verifying') onTerminal?.(view);
    } catch (error) {
      setActionError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  };
  const completeAction = async (action: 'finish' | 'abandon') => {
    setConfirmAction(null);
    setBusy(true);
    setActionError(null);
    try {
      if (action === 'finish') await controller.finish();
      else {
        const view = await controller.abandon();
        onTerminal?.(view);
      }
    } catch (error) {
      setActionError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  };
  const tools = normalizedLevel?.tools ?? [];
  const currentTool = (id: number) =>
    snapshot.state?.tools.find((item) => item.id === id) ?? tools.find((item) => item.id === id);
  const boardPoint = (clientX: number, clientY: number): Point | null => {
    const rect = boardRef.current?.getBoundingClientRect();
    if (!rect?.width || !rect.height) return null;
    const point = workspacePoint(clientX, clientY, rect);
    return point.x >= -DRAG_BUFFER &&
      point.x <= FIELD_WIDTH + DRAG_BUFFER &&
      point.y >= -DRAG_BUFFER &&
      point.y <= FIELD_HEIGHT + DRAG_BUFFER
      ? point
      : null;
  };
  const placementAt = (active: ToolDrag, point: Point): Placement => ({
    toolID: active.toolID,
    x: Math.max(-DRAG_BUFFER, Math.min(FIELD_WIDTH + DRAG_BUFFER, point.x + active.dx)),
    y: Math.max(-DRAG_BUFFER, Math.min(FIELD_HEIGHT + DRAG_BUFFER, point.y + active.dy)),
  });
  const sendPlacement = (placement: Placement) => {
    const previous = lastPlacement.current;
    if (
      previous?.toolID === placement.toolID &&
      previous.x === placement.x &&
      previous.y === placement.y
    )
      return;
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
    if (placementFrame.current === null)
      placementFrame.current = requestAnimationFrame(flushPlacement);
  };
  const cancelPending = () => {
    if (placementFrame.current !== null) cancelAnimationFrame(placementFrame.current);
    placementFrame.current = null;
    pendingPlacement.current = null;
  };
  const pickTool = (point: Point, pointerType: string) => {
    const radius = (pointerType === 'touch' ? 18 : 8) * unit;
    return [...tools]
      .sort((a, b) => Number(a.id === selectedTool) - Number(b.id === selectedTool))
      .reverse()
      .find((tool) => {
        const current = currentTool(tool.id);
        const position = toolPosition(tool, tools.indexOf(tool), current);
        const polygon = translatePolygon(tool.polygon, position.x, position.y);
        return containsPolygon(polygon, point) || nearOutline(polygon, point, radius);
      });
  };
  const moveDrag = (clientX: number, clientY: number, pointerID: number) => {
    const movingView = pan.current;
    if (movingView?.pointerID === pointerID && viewportRef.current) {
      viewportRef.current.scrollLeft = movingView.left + movingView.x - clientX;
      viewportRef.current.scrollTop = movingView.top + movingView.y - clientY;
      return;
    }
    const active = drag.current;
    if (!active || active.pointerID !== pointerID || !controller.snapshot().canPlay) return;
    if (Math.hypot(clientX - active.startX, clientY - active.startY) > 4) active.moved = true;
    if (!active.moved) return;
    const point = boardPoint(clientX, clientY);
    if (point) queuePlacement(placementAt(active, point));
  };
  const endDrag = (clientX: number, clientY: number, pointerID: number, cancelled = false) => {
    if (pan.current?.pointerID === pointerID) {
      pan.current = null;
      return;
    }
    const active = drag.current;
    if (!active || active.pointerID !== pointerID) return;
    if (!cancelled && controller.snapshot().canPlay && active.moved) {
      const point = boardPoint(clientX, clientY);
      if (point) queuePlacement(placementAt(active, point));
      flushPlacement();
    } else {
      cancelPending();
    }
    drag.current = null;
    setDraggingTool(null);
  };
  const startBoard = (point: Point, event: React.PointerEvent<HTMLCanvasElement>) => {
    if (drag.current || pan.current) return false;
    const hit = pickTool(point, event.pointerType);
    if (!hit || !controller.snapshot().canPlay) {
      if (event.pointerType === 'touch' && zoomed && viewportRef.current) {
        pan.current = {
          pointerID: event.pointerId,
          x: event.clientX,
          y: event.clientY,
          left: viewportRef.current.scrollLeft,
          top: viewportRef.current.scrollTop,
        };
        return true;
      }
      if (!hit) setSelectedTool(null);
      return false;
    }
    const current = currentTool(hit.id)!;
    const position = toolPosition(hit, tools.indexOf(hit), current);
    drag.current = {
      pointerID: event.pointerId,
      toolID: hit.id,
      startX: event.clientX,
      startY: event.clientY,
      dx: position.x - point.x,
      dy: position.y - point.y,
      moved: false,
    };
    lastPlacement.current = null;
    setSelectedTool(hit.id);
    setDraggingTool(hit.id);
    event.currentTarget.focus({ preventScroll: true });
    return true;
  };
  const rescued = snapshot.state?.fish.filter((fish) => fish.status === 'fed').length ?? 0;
  const lost = snapshot.state?.fish.filter((fish) => fish.status === 'lost').length ?? 0;
  const remainingSeconds = Math.max(
    0,
    Math.ceil(((level?.duration_seconds ?? 0) * 60 - (snapshot.state?.tick ?? 0)) / 60),
  );
  const timeLeft = `${Math.floor(remainingSeconds / 60)}:${String(remainingSeconds % 60).padStart(2, '0')}`;
  return (
    <section
      className="fatfish-player"
      aria-label={text('common.fatFishPlay')}
      data-can-play={snapshot.canPlay}
      data-dragging={draggingTool !== null}
    >
      <div className="fatfish-player__banner">
        <img src="/assets/fatfish/svg/rice-goal.svg" alt="" aria-hidden="true" />
        <div>
          <strong>{text('common.fatFishDinnerIsReady')}</strong>
          <span>{text('common.guideTheFishToRice')}</span>
        </div>
      </div>
      <div className="fatfish-player__music">
        <button
          type="button"
          data-fatfish-music-toggle=""
          aria-pressed={musicEnabled}
          disabled={!level || snapshot.phase === 'read_only'}
          onClick={toggleMusic}
        >
          {musicEnabled ? text('common.muteMusic') : text('common.playMusic')}
        </button>
        <span className="fatfish-player__music-credit">
          Monkeys Spinning Monkeys · Kevin MacLeod (
          <a href="https://incompetech.com/">incompetech.com</a>) ·{' '}
          <a href="https://creativecommons.org/licenses/by/4.0/">CC BY 4.0</a>
        </span>
        <span ref={musicHost} hidden aria-hidden="true" />
        {musicUnavailable ? (
          <p role="alert">{text('common.musicCouldNotPlayTryTurningIt')}</p>
        ) : null}
      </div>
      <div
        className="fatfish-player__status"
        role="status"
        data-fish-tick={snapshot.state?.tick}
        data-fish-fed={rescued}
      >
        {snapshot.phase === 'read_only' ? (
          <span className="fatfish-player__notice">
            {text('common.thisChallengeIsActiveInAnotherTab')}
          </span>
        ) : null}
        {snapshot.phase === 'prepared' ? (
          <span className="fatfish-player__notice">
            {text('common.previewTheLayoutToolsCanMoveAfter')}
          </span>
        ) : null}
        {snapshot.phase === 'waiting' ? (
          <span className="fatfish-player__notice">{text('common.startingShortly')}</span>
        ) : null}
        {snapshot.phase === 'verifying' ? (
          <span className="fatfish-player__notice">
            {text('common.theServerIsVerifyingTheResult')}
          </span>
        ) : null}
        {snapshot.phase === 'terminal' ? (
          <span className="fatfish-player__notice">{text('common.thisChallengeHasEnded')}</span>
        ) : null}
        {snapshot.state ? (
          <span className="fatfish-player__stat">
            <small>{text('common.timeLeft')}</small>
            <strong>{timeLeft}</strong>
          </span>
        ) : null}
        {snapshot.state ? (
          <span className="fatfish-player__stat">
            <small>{text('common.rescued')}</small>
            <strong>
              {rescued}/{snapshot.state.fish.length}
            </strong>
          </span>
        ) : null}
        {snapshot.state ? (
          <span className="fatfish-player__stat">
            <small>{text('common.lost')}</small>
            <strong>{lost}</strong>
          </span>
        ) : null}
      </div>
      {level ? (
        <div className="fatfish-player__workspace">
          <div className="fatfish-player__workspace-heading">
            <div>
              <strong>{text('common.dinnerWorkshop')}</strong>
              <span>{text('common.dragPiecesFreelyBetweenTheFieldAnd')}</span>
            </div>
            <button type="button" onClick={() => setZoomed((value) => !value)}>
              {zoomed ? text('common.fitScreen') : text('common.enlargeField')}
            </button>
          </div>
          <div className="fatfish-player__viewport" ref={viewportRef} data-zoomed={zoomed}>
            <div className="fatfish-player__board" ref={boardRef}>
              <FatFishCanvas
                level={level}
                state={snapshot.state}
                selectedTool={selectedTool}
                keyboardHelpID={keyboardHelpID}
                onPointerDown={startBoard}
                onPointerMove={(_point, event) =>
                  moveDrag(event.clientX, event.clientY, event.pointerId)
                }
                onPointerEnd={(event) => endDrag(event.clientX, event.clientY, event.pointerId)}
                onPointerCancel={(event) =>
                  endDrag(event.clientX, event.clientY, event.pointerId, true)
                }
                onKeyDown={(key, shift) => {
                  if (!snapshot.canPlay) return;
                  if (key.toLowerCase() === 'n') {
                    if (tools.length) {
                      const current = tools.findIndex((item) => item.id === selectedTool);
                      setSelectedTool(tools[(current + 1) % tools.length].id);
                    }
                    return;
                  }
                  if (key === 'Escape') {
                    const active = drag.current;
                    if (active) endDrag(active.startX, active.startY, active.pointerID, true);
                    setSelectedTool(null);
                    return;
                  }
                  if (selectedTool === null) return;
                  if (key === 'Backspace' || key === 'Delete') {
                    void controller
                      .returnTool(selectedTool)
                      .catch((failure: unknown) =>
                        setActionError(
                          failure instanceof Error ? failure.message : String(failure),
                        ),
                      );
                    return;
                  }
                  const source = tools.find((item) => item.id === selectedTool);
                  if (!source) return;
                  const tool = toolPosition(source, tools.indexOf(source), currentTool(source.id));
                  const step = (shift ? 2 : 10) * unit;
                  const dx = key === 'ArrowLeft' ? -step : key === 'ArrowRight' ? step : 0;
                  const dy = key === 'ArrowUp' ? -step : key === 'ArrowDown' ? step : 0;
                  const x = Math.max(
                    -DRAG_BUFFER,
                    Math.min(FIELD_WIDTH + DRAG_BUFFER, tool.x + dx),
                  );
                  const y = Math.max(
                    -DRAG_BUFFER,
                    Math.min(FIELD_HEIGHT + DRAG_BUFFER, tool.y + dy),
                  );
                  void controller
                    .place(selectedTool, x, y)
                    .catch((failure: unknown) =>
                      setActionError(failure instanceof Error ? failure.message : String(failure)),
                    );
                }}
              />
            </div>
          </div>
          {zoomed ? (
            <p className="fatfish-player__pan-hint">
              {text('common.onTouchScreensDragEmptySpaceTo')}
            </p>
          ) : null}
          <label className="fatfish-player__selection">
            {text('common.chooseAPieceOrDragItDirectly')}
            <select
              value={selectedTool ?? ''}
              onChange={(event) =>
                setSelectedTool(event.target.value === '' ? null : Number(event.target.value))
              }
            >
              <option value="">{text('common.noneSelected')}</option>
              {tools.map((tool, index) => {
                const name = toolLabels[tool.resource_key as keyof typeof toolLabels];
                return (
                  <option key={tool.id} value={tool.id}>
                    {index + 1}. {name ? text(name) : text('common.piece')}
                  </option>
                );
              })}
            </select>
          </label>
        </div>
      ) : null}
      {level ? (
        <p className="fatfish-player__keyboard-hint" id={keyboardHelpID}>
          {text('common.keyboardFocusTheFieldPressNTo')}
        </p>
      ) : null}
      <div className="fatfish-player__controls">
        <button
          type="button"
          disabled={!snapshot.canPlay || selectedTool === null}
          onClick={() => {
            if (selectedTool !== null)
              void controller
                .returnTool(selectedTool)
                .catch((error: unknown) =>
                  setActionError(error instanceof Error ? error.message : String(error)),
                );
          }}
        >
          {text('common.returnTool')}
        </button>
        <button
          type="button"
          className="fatfish-player__finish"
          disabled={busy || !snapshot.canPlay || !snapshot.state || snapshot.state.terminal}
          onClick={() => {
            if (mode === 'playtest') void completeAction('finish');
            else setConfirmAction('finish');
          }}
        >
          {text('common.finish')}
        </button>
        {snapshot.provisional && snapshot.challenge?.state === 'active' ? (
          <button
            type="button"
            className="fatfish-player__submit"
            disabled={busy}
            onClick={() => void submit()}
          >
            {text('common.submitForVerification')}
          </button>
        ) : null}
        {mode === 'user' && snapshot.phase !== 'terminal' ? (
          <button
            type="button"
            disabled={busy || !snapshot.challenge}
            onClick={() => setConfirmAction('abandon')}
          >
            {text('common.abandon')}
          </button>
        ) : null}
      </div>
      {snapshot.provisional ? (
        <p>
          {text('common.localEstimateOnly')} · {snapshot.provisional.fed}/
          {snapshot.provisional.total} · {snapshot.provisional.stars}★ ·{' '}
          {formatScoreUnits(snapshot.provisional.score_units)}
        </p>
      ) : null}
      {snapshot.challenge?.result ? (
        <p>
          {text('common.verified')} · {snapshot.challenge.result.fed}/
          {snapshot.challenge.result.total} · {snapshot.challenge.result.stars}★ ·{' '}
          {formatScoreUnits(snapshot.challenge.result.score_units)}
        </p>
      ) : null}
      {actionError || snapshot.error ? <p role="alert">{actionError ?? snapshot.error}</p> : null}
      <ConfirmDialog
        open={confirmAction !== null}
        title={text(confirmAction === 'abandon' ? 'common.abandon' : 'common.finish')}
        description={
          confirmAction === 'abandon'
            ? text('common.abandonThisChallengeAStartedTicketIs')
            : text('common.finishWithTheCurrentRescuedCountRemaining', {
                remaining:
                  snapshot.state?.fish.filter((fish) => fish.status === 'walking').length ?? 0,
              })
        }
        confirmLabel={text(confirmAction === 'abandon' ? 'common.abandon' : 'common.finish')}
        danger={confirmAction === 'abandon'}
        busy={busy}
        onCancel={() => setConfirmAction(null)}
        onConfirm={() => {
          if (confirmAction) void completeAction(confirmAction);
        }}
      />
    </section>
  );
}

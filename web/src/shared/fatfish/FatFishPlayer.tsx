import { useEffect, useMemo, useRef, useState } from 'react';
import { useActivityText } from '@shared/limitedactivities/copy';
import { normalizeLevel } from './engine/canonical';
import { FIELD_HEIGHT, FIELD_WIDTH, type EngineState, type Level, type Point, type Polygon } from './engine/types';
import { fishFootprint } from './engine/trig_helpers';
import { formatScoreUnits, type FatFishChallenge } from './api';
import { loadFatFishArt, toolIcon, type FatFishArt } from './art';
import type { FatFishSessionController } from './session';
import './player.css';

const unit = 64;
const width = FIELD_WIDTH / unit;
const height = FIELD_HEIGHT / unit;

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
function fillShape(ctx: CanvasRenderingContext2D, polygon: Polygon, fill: string,
  stroke: string, offsetX = 0, offsetY = 0, icon?: HTMLImageElement): void {
  polygonPath(ctx, polygon, offsetX, offsetY);
  ctx.fillStyle = fill;
  ctx.fill('evenodd');
  if (icon) {
    const xs = polygon.outer.map((point) => (point.x + offsetX) / unit);
    const ys = polygon.outer.map((point) => (point.y + offsetY) / unit);
    ctx.save();
    ctx.clip('evenodd');
    ctx.drawImage(icon, Math.min(...xs), Math.min(...ys), Math.max(...xs) - Math.min(...xs), Math.max(...ys) - Math.min(...ys));
    ctx.restore();
  }
  polygonPath(ctx, polygon, offsetX, offsetY);
  ctx.strokeStyle = stroke;
  ctx.lineWidth = 1.5;
  ctx.stroke();
}
function drawFatFishScene(ctx: CanvasRenderingContext2D, level: Level, state?: EngineState | null,
  art?: FatFishArt | null): void {
  ctx.clearRect(0, 0, width, height);
  ctx.fillStyle = '#d7eff1';
  ctx.fillRect(0, 0, width, height);
  ctx.fillStyle = '#e7f7f5';
  for (let y = 20; y < height; y += 42) for (let x = 20 + (y % 84); x < width; x += 84) {
    ctx.beginPath(); ctx.arc(x, y, 3, 0, Math.PI * 2); ctx.fill();
  }
  level.directions.forEach((item) => fillShape(ctx, item.polygon, '#a8d9e3a0', '#5b94a8', 0, 0,
    art?.icons.get('rice-arrow')));
  level.solids.forEach((item) => fillShape(ctx, item.polygon, '#537a81', '#345359'));
  level.hazards.forEach((item) => fillShape(ctx, item.polygon, '#dd806b', '#9d5148', 0, 0,
    art?.icons.get('offline-pool')));
  level.bowls.forEach((item) => {
    const count = state?.bowls.find((entry) => entry.id === item.id)?.count ?? 0;
    fillShape(ctx, item.polygon, '#f4c980', '#9c7336', 0, 0,
      art?.icons.get(count >= item.capacity ? 'rice-goal-full' : 'rice-goal'));
  });
  level.switches.forEach((item) => fillShape(ctx, item.polygon,
    state?.switches.find((entry) => entry.id === item.id)?.active ? '#8bd19f' : '#f1db97', '#64866d', 0, 0,
    art?.icons.get(state?.switches.find((entry) => entry.id === item.id)?.active ? 'switch-on' : 'switch-off')));
  level.gates.forEach((item) => {
    const open = state?.gates.find((entry) => entry.id === item.id)?.open ?? item.initially_open;
    fillShape(ctx, item.polygon, open ? '#8bd19f55' : '#596978', open ? '#519b70' : '#354c5b', 0, 0,
      art?.icons.get(open ? 'gate-open' : 'gate-closed'));
  });
  level.tools.forEach((item) => {
    const current = state?.tools.find((entry) => entry.id === item.id) ?? item;
    if (current.placed) fillShape(ctx, item.polygon, '#729bbb', '#365772', current.x, current.y,
      art?.icons.get(toolIcon(item.resource_key) ?? ''));
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
    if (status === 'lost') ctx.globalAlpha = .5;
    if (frame && sprite && frame.referenceHeight > 0) {
      const scale = 16 / frame.referenceHeight;
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
    ctx.lineWidth = .8; ctx.strokeStyle = status === 'lost' ? '#aa4444' : status === 'fed' ? '#3d925d' : '#184f7199'; ctx.stroke();
    if (status === 'lost') {
      const bubble = art?.icons.get('offline-bubble');
      if (bubble) ctx.drawImage(bubble, x - 10, y - 18, 20, 20);
    } else if (status === 'walking' && 'turn_dir' in fish && fish.turn_dir !== 0) {
      ctx.beginPath(); ctx.arc(x, y, 11, -.6, .6);
      ctx.lineWidth = 1.3; ctx.strokeStyle = '#277b91'; ctx.stroke();
    }
  }
}

export function FatFishCanvas({ level, state, onPointerDown, onPointerMove, onPointerEnd, onKeyDown }: {
  level: Level;
  state?: EngineState | null;
  onPointerDown?: (point: Point, pointerID: number) => void;
  onPointerMove?: (point: Point, pointerID: number) => void;
  onPointerEnd?: (pointerID: number) => void;
  onKeyDown?: (key: string) => void;
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
    role="img" aria-label="Fat fish playfield" tabIndex={onKeyDown ? 0 : undefined}
    style={{ touchAction: 'none' }}
    onKeyDown={(event) => {
      if (['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Backspace', 'Delete'].includes(event.key)) {
        event.preventDefault(); onKeyDown?.(event.key);
      }
    }}
    onPointerDown={(event) => {
      if (!onPointerDown || !event.isPrimary || event.button !== 0) return;
      event.currentTarget.setPointerCapture(event.pointerId);
      onPointerDown(point(event), event.pointerId);
    }}
    onPointerMove={(event) => { if (onPointerMove && event.isPrimary) onPointerMove(point(event), event.pointerId); }}
    onPointerUp={(event) => onPointerEnd?.(event.pointerId)}
    onPointerCancel={(event) => onPointerEnd?.(event.pointerId)} />;
}

export function FatFishPlayer({ controller, onTerminal, mode = 'user' }: {
  controller: FatFishSessionController;
  onTerminal?: (view: FatFishChallenge) => void;
  mode?: 'user' | 'playtest';
}) {
  const t = useActivityText();
  const [observed, setObserved] = useState(() => ({ controller, snapshot: controller.snapshot() }));
  const snapshot = observed.controller === controller ? observed.snapshot : controller.snapshot();
  const [selectedTool, setSelectedTool] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const pointer = useRef<number | null>(null);
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
    const blur = () => { pointer.current = null; };
    window.addEventListener('blur', blur);
    return () => window.removeEventListener('blur', blur);
  }, []);
  const level = snapshot.challenge?.level;
  const normalizedLevel = useMemo(() => level ? normalizeLevel(level) : null, [level]);
  const submit = async () => {
    setBusy(true); setActionError(null);
    try { const view = await controller.submit(); if (view.state !== 'verifying') onTerminal?.(view); }
    catch (error) { setActionError(error instanceof Error ? error.message : String(error)); }
    finally { setBusy(false); }
  };
  const applyPlace = (point: Point) => {
    if (selectedTool === null || !snapshot.canPlay) return;
    void controller.place(selectedTool, point.x, point.y).catch((error: unknown) =>
      setActionError(error instanceof Error ? error.message : String(error)));
  };
  const tools = normalizedLevel?.tools ?? [];
  return <section className="fatfish-player" aria-label={t('大肥鱼游玩', 'Fat fish play')}>
    <div className="fatfish-player__status" role="status">
      {snapshot.phase === 'read_only' ? t('本局正在另一标签页游玩；此页只能查看。', 'This challenge is active in another tab. This page is read-only.') : null}
      {snapshot.phase === 'waiting' ? t('即将开始', 'Starting shortly') : null}
      {snapshot.phase === 'verifying' ? t('服务端正在核验成绩。', 'The server is verifying the result.') : null}
      {snapshot.phase === 'terminal' ? t('本局已结束。', 'This challenge has ended.') : null}
      {snapshot.state ? ` ${t('进度', 'Tick')} ${snapshot.state.tick} / ${(level?.duration_seconds ?? 0) * 60}` : null}
      {snapshot.state ? ` · ${t('已救', 'Rescued')}: ${snapshot.state.fish.filter((fish) => fish.status === 'fed').length}/${snapshot.state.fish.length}` : null}
      {snapshot.state ? ` · ${t('失去', 'Lost')}: ${snapshot.state.fish.filter((fish) => fish.status === 'lost').length}` : null}
    </div>
    {level ? <FatFishCanvas level={level} state={snapshot.state}
      onPointerDown={(point, id) => { if (pointer.current !== null || !snapshot.canPlay) return; pointer.current = id; applyPlace(point); }}
      onPointerMove={(point, id) => { if (pointer.current === id) applyPlace(point); }}
      onPointerEnd={(id) => { if (pointer.current === id) pointer.current = null; }}
      onKeyDown={(key) => {
        if (selectedTool === null || !snapshot.canPlay) return;
        if (key === 'Backspace' || key === 'Delete') {
          void controller.returnTool(selectedTool).catch((failure: unknown) =>
            setActionError(failure instanceof Error ? failure.message : String(failure)));
          return;
        }
        const tool = snapshot.state?.tools.find((item) => item.id === selectedTool) ??
          normalizedLevel?.tools.find((item) => item.id === selectedTool);
        if (!tool) return;
        const dx = key === 'ArrowLeft' ? -unit : key === 'ArrowRight' ? unit : 0;
        const dy = key === 'ArrowUp' ? -unit : key === 'ArrowDown' ? unit : 0;
        void controller.place(selectedTool, tool.x + dx, tool.y + dy).catch((failure: unknown) =>
          setActionError(failure instanceof Error ? failure.message : String(failure)));
      }} /> : null}
    <div className="fatfish-player__controls">
      {tools.map((tool) => <button type="button" key={tool.id} className={selectedTool === tool.id ? 'is-selected' : ''}
        disabled={!snapshot.canPlay} onClick={() => setSelectedTool(tool.id)}>{t('工具', 'Tool')} {tool.id}</button>)}
      <button type="button" disabled={!snapshot.canPlay || selectedTool === null} onClick={() => {
        if (selectedTool !== null) void controller.returnTool(selectedTool).catch((error: unknown) =>
          setActionError(error instanceof Error ? error.message : String(error)));
      }}>{t('收回工具', 'Return tool')}</button>
      <button type="button" disabled={!snapshot.canPlay || !snapshot.state || snapshot.state.terminal}
        onClick={() => {
          const remaining = snapshot.state?.fish.filter((fish) => fish.status === 'walking').length ?? 0;
          if (window.confirm(t(`现在按已救数量结束？剩余 ${remaining} 条鱼不计入成绩；服务端仍会核验。`,
            `Finish with the current rescued count? ${remaining} remaining fish will not count; the server will verify.`)))
            void controller.finish().catch((error: unknown) => setActionError(error instanceof Error ? error.message : String(error)));
        }}>{t('完成', 'Finish')}</button>
      {snapshot.provisional && snapshot.challenge?.state === 'active' ? <button type="button" disabled={busy}
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

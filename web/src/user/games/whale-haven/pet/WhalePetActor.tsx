import {
  useLayoutEffect,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from 'react';
import { getSceneAssetUrl, getSpriteUrl } from '../game/catalog';
import type { WhaleExpression } from '../game/types';
import type { RigActivity } from '../rig/animator';
import type { RigImpulseEvent } from '../rig/WhaleRigActor';
import { requestMotion, loadCodexPetAssets, resolveOutfitPetId } from './loader';
import { loadOriginalGaze, type OriginalGaze } from './originalGaze';
import { CODEX_PET_ROWS, type CodexPetStateId, type LoadedCodexPetAssets } from './types';

const motionPreference = '(prefers-reduced-motion: reduce)';
const prefersReducedMotion = () => window.matchMedia(motionPreference).matches;
function subscribeMotionPreference(update: () => void) {
  const query = window.matchMedia(motionPreference);
  query.addEventListener('change', update);
  return () => query.removeEventListener('change', update);
}

export type WhalePetOutfitProp =
  | 'maid_classic'
  | 'winter_snow'
  | 'knit_beret'
  | 'whale_hoodie'
  | 'sailor_breeze'
  | 'coral_pajama'
  | string;

export interface WhalePetActorProps {
  alt: string;
  expression: WhaleExpression;
  outfit?: WhalePetOutfitProp;
  petId?: string;
  isWorkerUniform?: boolean;
  pinnedPetState?: CodexPetStateId | null;
  lookTrackingEnabled?: boolean;
  roamEnabled?: boolean;
  roamMaxOffsetPx?: number;
  activity: RigActivity;
  alwaysEatingWhenActive?: boolean;
  impulse?: RigImpulseEvent | null;
  displayWidth?: number;
  reloadToken?: number;
  className?: string;
  children?: ReactNode;
}

interface PetRuntime {
  activeState: CodexPetStateId;
  frameIndex: number;
  frameElapsedMs: number;
  impulseState: CodexPetStateId | null;
  impulseUntilMs: number;
  lastImpulseSeq: number;
  roamX: number;
  roamTargetX: number;
  roamPauseState: 'idle' | 'review' | 'running';
  nextRoamDecisionAtMs: number;
  nextSnackAtMs: number;
  snackUntilMs: number;
  pointerInside: boolean;
  pointerClientX: number;
  pointerClientY: number;
  lastPointerMoveAtMs: number;
  /** 眼神瞥向鼠标的活跃截止时间（短窗口瞥视后自动归位播放 24fps 鲜活待机动画） */
  gazeActiveUntilMs: number;
  /** 一次瞥视结束后的冷却截止时间，冷却期内不再因鼠标悬停/微动反复锁死静态凝视 */
  gazeCooldownUntilMs: number;
  currentLookIndex: number | null;
  smoothLookDeg: number | null;
  gazeX: number;
  gazeY: number;
  gazeBlend: number;
}

/** 单次触发眼神跟随的最长持续时间（毫秒） */
const GAZE_GLANCE_DURATION_MS = 2200;
/** 鼠标停住不动多久后提前结束瞥视（毫秒） */
const GAZE_IDLE_RELEASE_MS = 1100;
/** 一次瞥视结束后的冷却时间（毫秒），保证绝大部分时间播放 24fps 鲜活动作 */
const GAZE_COOLDOWN_MS = 8500;
/** 仅当鼠标进入鲸鱼娘角色附近该半径倍率（相对角色显示宽度）且正在移动时才触发瞥视 */
const GAZE_PROXIMITY_WIDTH_MULT = 1.45;

function easeGaze(current: number, target: number, dtMs: number): number {
  const remaining = (current - target) * Math.exp(-dtMs / 70);
  return Math.abs(remaining) < 0.004 ? target : target + remaining;
}

function stepCircularAngleDeg(currentDeg: number | null, targetDeg: number, dtMs: number): number {
  const target = ((targetDeg % 360) + 360) % 360;
  if (currentDeg === null || !Number.isFinite(currentDeg)) {
    return target;
  }
  const delta = ((((target - currentDeg) % 360) + 540) % 360) - 180;
  if (Math.abs(delta) < 0.75) {
    return target;
  }
  const safeDt = Math.max(0, dtMs);
  const alpha = 1 - Math.pow(0.0005, safeDt / 1000);
  const maxStep = (480 * safeDt) / 1000;
  const move =
    Math.sign(delta) * Math.min(Math.abs(delta), Math.max(Math.abs(delta) * alpha, 0), maxStep);
  return (((currentDeg + move) % 360) + 360) % 360;
}

function computeClockwiseLookIndex(
  dx: number,
  dy: number,
  prevIndex: number | null,
): number | null {
  const dist = Math.hypot(dx, dy);
  // Neutral front deadzone right around her nose/face center
  if (dist < 26) return null;

  // 000 deg = 12 o'clock (up: dx=0, dy<0), 090 = screen-right, 180 = down, 270 = screen-left
  const deg = ((Math.atan2(dx, -dy) * 180) / Math.PI + 360) % 360;
  const rawIndex = Math.round(deg / 22.5) % 16;

  if (prevIndex === null || prevIndex === rawIndex) {
    return rawIndex;
  }

  // +/-4.5 degree hysteresis so hovering near a 22.5-deg boundary never jitter-flips
  const prevCenter = prevIndex * 22.5;
  let delta = Math.abs(deg - prevCenter);
  if (delta > 180) delta = 360 - delta;
  if (delta <= 11.25 + 4.5) {
    return prevIndex;
  }
  return rawIndex;
}

export function WhalePetActor({
  alt,
  expression,
  outfit,
  petId,
  isWorkerUniform = false,
  pinnedPetState = null,
  lookTrackingEnabled = true,
  roamEnabled = false,
  roamMaxOffsetPx = 48,
  activity,
  alwaysEatingWhenActive = false,
  impulse = null,
  displayWidth = 252,
  reloadToken = 0,
  className,
  children,
}: WhalePetActorProps) {
  const reduced = useSyncExternalStore(subscribeMotionPreference, prefersReducedMotion, () => true);
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const originalGazeRef = useRef<OriginalGaze | null>(null);
  const [assets, setAssets] = useState<LoadedCodexPetAssets | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);

  const targetPetId = petId ?? resolveOutfitPetId(outfit, isWorkerUniform);
  const displayHeight = Math.round(displayWidth * (208 / 192));

  const propsRef = useRef({
    targetPetId,
    expression,
    isWorkerUniform,
    pinnedPetState,
    lookTrackingEnabled,
    roamEnabled,
    roamMaxOffsetPx,
    activity,
    alwaysEatingWhenActive,
  });
  useLayoutEffect(() => {
    propsRef.current = {
      targetPetId,
      expression,
      isWorkerUniform,
      pinnedPetState,
      lookTrackingEnabled,
      roamEnabled,
      roamMaxOffsetPx,
      activity,
      alwaysEatingWhenActive,
    };
  });

  const [initialRuntime] = useState<PetRuntime>(() => ({
    activeState: 'idle',
    frameIndex: 0,
    frameElapsedMs: 0,
    impulseState: null,
    impulseUntilMs: 0,
    lastImpulseSeq: 0,
    roamX: 0,
    roamTargetX: 0,
    roamPauseState: 'idle',
    nextRoamDecisionAtMs: performance.now() + 5500,
    nextSnackAtMs: performance.now() + 9000,
    snackUntilMs: 0,
    pointerInside: false,
    pointerClientX: 0,
    pointerClientY: 0,
    lastPointerMoveAtMs: 0,
    gazeActiveUntilMs: 0,
    gazeCooldownUntilMs: 0,
    currentLookIndex: null,
    smoothLookDeg: null,
    gazeX: 0,
    gazeY: 0,
    gazeBlend: 0,
  }));
  const runtimeRef = useRef(initialRuntime);

  useEffect(() => {
    if (isWorkerUniform || reduced || !lookTrackingEnabled) return;
    let cancelled = false;
    loadOriginalGaze(targetPetId)
      .then((art) => {
        if (!cancelled) originalGazeRef.current = art;
      })
      .catch(() => {
        // Keep atlas-based tracking when the original artwork cannot be decoded.
      });
    return () => {
      cancelled = true;
      originalGazeRef.current = null;
    };
  }, [targetPetId, isWorkerUniform, reduced, lookTrackingEnabled]);

  // Load the selected pet only when motion is enabled.
  useEffect(() => {
    let cancelled = false;
    if (reduced) return;
    loadCodexPetAssets(targetPetId).then(
      (loaded) => {
        if (!cancelled) {
          setAssets(loaded);
          setLoadFailed(false);
        }
      },
      () => {
        if (!cancelled) setLoadFailed(true);
      },
    );
    return () => {
      cancelled = true;
    };
  }, [targetPetId, reloadToken, reduced]);

  // A gesture starts on input. Its full action loop starts when its own sheet is decoded.
  useLayoutEffect(() => {
    if (reduced || !impulse || !assets || assets.manifest.id !== targetPetId) return;
    const { kind, seq } = impulse;
    const rt = runtimeRef.current;
    if (seq === rt.lastImpulseSeq) return;
    const state =
      kind === 'headpat'
        ? 'waving'
        : kind === 'poke'
          ? 'failed'
          : kind === 'feed'
            ? 'running'
            : 'jumping';
    const motion = isWorkerUniform
      ? state === 'running'
        ? 'workerFeast'
        : 'workerIdle'
      : alwaysEatingWhenActive && state === 'running'
        ? 'feast'
        : state;
    const slice =
      motion === 'workerIdle' || motion === 'workerFeast' || motion === 'feast'
        ? assets.highFpsLayout?.[motion]
        : assets.highFpsLayout?.states[state];
    if (kind === 'headpat' || kind === 'poke') {
      canvasRef.current?.animate?.(
        [
          { translate: '0 0' },
          { translate: kind === 'headpat' ? '0 2px' : '2px 0' },
          { translate: '0 0' },
        ],
        { duration: 160 },
      );
    }
    let current = true;
    void requestMotion(assets, motion, true).then((loaded) => {
      if (!current) return;
      rt.lastImpulseSeq = seq;
      if (!loaded) return;
      rt.impulseState = state;
      rt.impulseUntilMs = performance.now() + (slice ? slice.frames * slice.frameMs : 1650);
      rt.frameIndex = 0;
      rt.frameElapsedMs = 0;
      if (kind === 'teacake') {
        const run = assets.highFpsLayout?.states.running;
        rt.snackUntilMs = rt.impulseUntilMs + (run ? run.frames * run.frameMs : 3400);
      }
    });
    return () => {
      current = false;
    };
  }, [impulse, assets, targetPetId, alwaysEatingWhenActive, isWorkerUniform, reduced]);

  // Pointer gaze listener on parent .ocean-stage
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const stageEl = (canvas.closest('.ocean-stage') as HTMLElement | null) ?? window;

    const onMove = (ev: Event) => {
      const e = ev as PointerEvent;
      const rt = runtimeRef.current;
      rt.pointerInside = true;
      rt.pointerClientX = e.clientX;
      rt.pointerClientY = e.clientY;
      rt.lastPointerMoveAtMs = performance.now();
    };

    const onLeave = () => {
      const rt = runtimeRef.current;
      rt.pointerInside = false;
      if (rt.gazeActiveUntilMs > 0) {
        rt.gazeActiveUntilMs = 0;
        rt.gazeCooldownUntilMs = performance.now() + GAZE_COOLDOWN_MS * 0.5;
      }
      rt.currentLookIndex = null;
      rt.smoothLookDeg = null;
    };

    const onRelease = (ev: Event) => {
      if ((ev as PointerEvent).pointerType !== 'mouse') onLeave();
    };

    stageEl.addEventListener('pointermove', onMove, { passive: true });
    stageEl.addEventListener('pointerleave', onLeave, { passive: true });
    stageEl.addEventListener('pointercancel', onLeave, { passive: true });
    stageEl.addEventListener('pointerup', onRelease, { passive: true });
    return () => {
      stageEl.removeEventListener('pointermove', onMove);
      stageEl.removeEventListener('pointerleave', onLeave);
      stageEl.removeEventListener('pointercancel', onLeave);
      stageEl.removeEventListener('pointerup', onRelease);
    };
  }, [assets, reduced]);

  // Main 60fps Codex V2 Sprite Atlas & Autonomous Roam Loop
  useEffect(() => {
    if (!assets || reduced) return;
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    let rafId = 0;
    let prevNow = performance.now();
    let previousImage: HTMLImageElement | undefined;
    let previousRow = -1;
    let previousColumn = -1;
    let previousGaze = false;
    const idleStart = prevNow;
    let drawnImpulse = 0;

    const tick = (now: number) => {
      const dtMs = Math.min(250, Math.max(0, now - prevNow));
      prevNow = now;

      const rt = runtimeRef.current;
      const p = propsRef.current;

      // 1. Update autonomous seabed roaming offset
      let isRoamingMove: 'running-right' | 'running-left' | null = null;
      if (
        p.roamEnabled &&
        !p.pinnedPetState &&
        (!rt.impulseState || now >= rt.impulseUntilMs) &&
        p.expression !== 'panicked' &&
        p.expression !== 'pout' &&
        p.expression !== 'teary'
      ) {
        if (now >= rt.nextRoamDecisionAtMs && Math.abs(rt.roamTargetX - rt.roamX) < 1.5) {
          const maxOff = p.roamMaxOffsetPx;
          // 65% chance to stroll to a new spot, 35% chance to pause & inspect/snack in place
          if (Math.random() < 0.65 && maxOff >= 16) {
            const candidate = (Math.random() * 2 - 1) * maxOff;
            rt.roamTargetX =
              Math.abs(candidate - rt.roamX) < 18
                ? Math.max(-maxOff, Math.min(maxOff, -rt.roamX * 0.8 + (Math.random() - 0.5) * 20))
                : candidate;
          }
          rt.roamPauseState =
            p.activity === 'eating' && Math.random() < 0.5
              ? 'running'
              : Math.random() < 0.38
                ? 'review'
                : 'idle';
          rt.nextRoamDecisionAtMs = now + 9500 + Math.random() * 8500;
        }

        const diffX = rt.roamTargetX - rt.roamX;
        if (Math.abs(diffX) >= 1.2) {
          const stepPx = (38 * dtMs) / 1000;
          if (Math.abs(diffX) <= stepPx) {
            rt.roamX = rt.roamTargetX;
          } else {
            rt.roamX += Math.sign(diffX) * stepPx;
            isRoamingMove = diffX > 0 ? 'running-right' : 'running-left';
          }
        }
      } else if (!p.roamEnabled || p.pinnedPetState) {
        // Ease gently back to center if roam disabled or state pinned
        rt.roamX *= Math.pow(0.9, dtMs / 16.67);
        if (Math.abs(rt.roamX) < 0.4) rt.roamX = 0;
        rt.roamTargetX = rt.roamX;
      }

      if (wrapRef.current) {
        const transform =
          Math.abs(rt.roamX) > 0.2 ? `translate3d(${rt.roamX.toFixed(1)}px, 0, 0)` : '';
        if (wrapRef.current.style.transform !== transform)
          wrapRef.current.style.transform = transform;
      }

      // Periodic snack-munch cycle when trough has food at home
      if (p.activity === 'eating' && !p.alwaysEatingWhenActive && now >= rt.nextSnackAtMs) {
        const eatSl = assets.highFpsLayout?.states.running;
        const eatLoopMs = eatSl ? eatSl.frames * eatSl.frameMs : 3800;
        rt.snackUntilMs = now + eatLoopMs;
        rt.nextSnackAtMs = now + eatLoopMs + 12000 + Math.random() * 7000;
      }

      // 2. Resolve target Codex V2 state
      if (rt.impulseState && now >= rt.impulseUntilMs) {
        rt.impulseState = null;
      }

      let desiredState: CodexPetStateId;
      if (p.pinnedPetState) {
        desiredState = p.pinnedPetState;
      } else if (rt.impulseState) {
        desiredState = rt.impulseState;
      } else if (p.expression === 'panicked' || p.expression === 'pout') {
        desiredState = 'failed';
      } else if (p.expression === 'teary') {
        desiredState = 'waiting';
      } else if (p.activity === 'swimming') {
        desiredState = 'running-right';
      } else if (p.alwaysEatingWhenActive && p.activity === 'eating') {
        desiredState = 'running';
      } else if (isRoamingMove) {
        desiredState = isRoamingMove;
      } else if (now < rt.snackUntilMs && p.activity === 'eating') {
        desiredState = 'running';
      } else if (p.expression === 'calm') {
        desiredState = 'review';
      } else if (rt.roamPauseState !== 'idle' && now < rt.nextRoamDecisionAtMs - 5500) {
        desiredState = rt.roamPauseState;
      } else {
        desiredState = 'idle';
      }

      if (desiredState !== rt.activeState) {
        rt.activeState = desiredState;
        rt.frameIndex = 0;
        rt.frameElapsedMs = 0;
      }

      // 3. Advance frame timer (High-FPS I2V layout if present, else official Codex V2 durations)
      const rowSpec = CODEX_PET_ROWS[rt.activeState];
      const i2vLayout = assets.highFpsLayout;
      const motion = p.isWorkerUniform
        ? rt.activeState === 'running'
          ? 'workerFeast'
          : 'workerIdle'
        : p.alwaysEatingWhenActive && rt.activeState === 'running'
          ? 'feast'
          : rt.activeState;
      void requestMotion(assets, motion);
      const useWorkerFeast =
        p.isWorkerUniform &&
        rt.activeState === 'running' &&
        Boolean(i2vLayout?.workerFeast && assets.workerFeastSheet);
      const useWorkerIdle =
        p.isWorkerUniform &&
        !useWorkerFeast &&
        Boolean(i2vLayout?.workerIdle && assets.workerIdleSheet);
      const useFeastVariant =
        !useWorkerFeast &&
        !useWorkerIdle &&
        rt.activeState === 'running' &&
        p.alwaysEatingWhenActive &&
        Boolean(i2vLayout?.feast && assets.feastSheet);
      const i2vSlice = useWorkerFeast
        ? i2vLayout?.workerFeast
        : useWorkerIdle
          ? i2vLayout?.workerIdle
          : useFeastVariant
            ? i2vLayout?.feast
            : i2vLayout?.states[rt.activeState];
      const totalFrames = i2vSlice ? i2vSlice.frames : rowSpec.frames;
      const frameDuration = () =>
        Math.max(1, i2vSlice?.frameMs ?? rowSpec.durationsMs[rt.frameIndex] ?? 140);

      const firstImpulseFrame =
        rt.impulseState === rt.activeState && drawnImpulse !== rt.lastImpulseSeq;
      if (firstImpulseFrame) {
        drawnImpulse = rt.lastImpulseSeq;
        rt.frameElapsedMs = 0;
        rt.frameIndex = 0;
      } else {
        rt.frameElapsedMs += dtMs;
      }
      while (rt.frameElapsedMs >= frameDuration()) {
        rt.frameElapsedMs -= frameDuration();
        rt.frameIndex = (rt.frameIndex + 1) % totalFrames;
      }

      // 4. Resolve the action frame, then update the idle gaze.
      let drawImage: HTMLImageElement = assets.atlasImage;
      let drawCellW = assets.cellWidth;
      let drawCellH = assets.cellHeight;
      let drawRow: number;
      let drawCol: number;
      let isWideI2vFrame: boolean;

      const stateSheet = useWorkerFeast
        ? assets.workerFeastSheet
        : useWorkerIdle
          ? assets.workerIdleSheet
          : useFeastVariant
            ? assets.feastSheet
            : assets.stateSheets?.[rt.activeState];
      if (stateSheet && i2vSlice) {
        drawImage = stateSheet.image;
        drawCellW = stateSheet.cellWidth;
        drawCellH = stateSheet.cellHeight;
        const absSlot = i2vSlice.startSlot + (rt.frameIndex % i2vSlice.frames);
        drawRow = Math.floor(absSlot / stateSheet.columns);
        drawCol = absSlot % stateSheet.columns;
        isWideI2vFrame = true;
      } else {
        const idle = i2vLayout?.states.idle;
        const slot = idle
          ? idle.startSlot + (Math.floor((now - idleStart) / idle.frameMs) % idle.frames)
          : 0;
        const columns = assets.stateSheets?.idle?.columns ?? 1;
        drawRow = Math.floor(slot / columns);
        drawCol = slot % columns;
        isWideI2vFrame = true;
      }

      const art = originalGazeRef.current;
      const liveGaze =
        art?.petId === p.targetPetId &&
        assets.manifest.id === art.petId &&
        assets.stateSheets?.idle &&
        rt.activeState === 'idle' &&
        !p.pinnedPetState &&
        !p.isWorkerUniform
          ? art
          : null;

      // 计算低频偶发「瞥一眼鼠标 + 冷却回正」状态机，防止常驻盯鼠标压制 24fps 动作表现
      let gazeEngaged = false;
      let gazeRect: DOMRect | null = null;

      if (
        p.lookTrackingEnabled &&
        rt.activeState === 'idle' &&
        !p.pinnedPetState &&
        rt.pointerInside
      ) {
        gazeRect = canvas.getBoundingClientRect();
        const anchorX = gazeRect.left + gazeRect.width * (liveGaze?.centerX ?? 0.5);
        const anchorY = gazeRect.top + gazeRect.height * (liveGaze?.centerY ?? 0.44);
        const distToAnchor = Math.hypot(rt.pointerClientX - anchorX, rt.pointerClientY - anchorY);
        const triggerRadius = Math.max(150, displayWidth * GAZE_PROXIMITY_WIDTH_MULT);
        const recentlyMoved = now - rt.lastPointerMoveAtMs < GAZE_IDLE_RELEASE_MS;

        if (now < rt.gazeActiveUntilMs) {
          // 若在瞥视期间鼠标早已停下或移出较远区域，提前收起目光并进入冷却
          if (!recentlyMoved || distToAnchor > triggerRadius * 1.3) {
            rt.gazeActiveUntilMs = 0;
            rt.gazeCooldownUntilMs = now + GAZE_COOLDOWN_MS;
          } else {
            gazeEngaged = true;
          }
        } else if (rt.gazeActiveUntilMs > 0) {
          // 刚满 2.2s 单次瞥视上限 -> 强制切回 24fps 待机动作并开启 8.5s 冷却
          rt.gazeActiveUntilMs = 0;
          rt.gazeCooldownUntilMs = now + GAZE_COOLDOWN_MS;
        } else if (
          now >= rt.gazeCooldownUntilMs &&
          now - rt.lastPointerMoveAtMs < 260 &&
          distToAnchor <= triggerRadius &&
          distToAnchor >= 14
        ) {
          // 冷却已结束，且鼠标刚在鲸鱼娘身边划过 -> 触发一次灵动短瞥！
          rt.gazeActiveUntilMs = now + GAZE_GLANCE_DURATION_MS;
          gazeEngaged = true;
        }
      } else if (rt.gazeActiveUntilMs > 0) {
        rt.gazeActiveUntilMs = 0;
        rt.gazeCooldownUntilMs = Math.max(rt.gazeCooldownUntilMs, now + 3500);
      }

      if (liveGaze) {
        let aimX = 0;
        let aimY = 0;
        if (gazeEngaged) {
          const rect = gazeRect ?? canvas.getBoundingClientRect();
          const dx = rt.pointerClientX - (rect.left + rect.width * liveGaze.centerX);
          const dy = rt.pointerClientY - (rect.top + rect.height * liveGaze.centerY);
          const distance = Math.hypot(dx, dy);
          const range = Math.max(48, Math.min(rect.width, rect.height) * 0.42);
          const amount = Math.min(1, Math.max(0, (distance - 8) / range));
          aimX = (dx / Math.max(1, distance)) * amount;
          aimY = (dy / Math.max(1, distance)) * amount;
        }
        rt.gazeX = easeGaze(rt.gazeX, aimX, dtMs);
        rt.gazeY = easeGaze(rt.gazeY, aimY, dtMs);
        rt.gazeBlend = easeGaze(rt.gazeBlend, gazeEngaged ? 1 : 0, dtMs);
      } else {
        rt.gazeX = 0;
        rt.gazeY = 0;
        rt.gazeBlend = 0;
      }

      if (gazeEngaged && !liveGaze && i2vLayout?.look360) void requestMotion(assets, 'look360');
      const canTrackLook = !liveGaze && Boolean(assets.look360Sheet) && gazeEngaged;

      if (canTrackLook) {
        const rect = (wrapRef.current ?? canvas).getBoundingClientRect();
        const headCenterX = rect.left + rect.width * 0.5;
        const headCenterY = rect.top + rect.height * 0.44;
        const dx = rt.pointerClientX - headCenterX;
        const dy = rt.pointerClientY - headCenterY;
        const dist = Math.hypot(dx, dy);

        if (
          dist >= 26 &&
          assets.look360Sheet &&
          i2vLayout?.look360 &&
          i2vLayout.look360.frames > 0
        ) {
          const targetDeg = ((Math.atan2(dx, -dy) * 180) / Math.PI + 360) % 360;
          rt.smoothLookDeg = stepCircularAngleDeg(rt.smoothLookDeg, targetDeg, dtMs);
          const nFrames = i2vLayout.look360.frames;
          const lookFrame = Math.round((rt.smoothLookDeg / 360) * nFrames) % nFrames;
          const absSlot = i2vLayout.look360.startSlot + lookFrame;
          drawImage = assets.look360Sheet.image;
          drawCellW = assets.look360Sheet.cellWidth;
          drawCellH = assets.look360Sheet.cellHeight;
          drawRow = Math.floor(absSlot / assets.look360Sheet.columns);
          drawCol = absSlot % assets.look360Sheet.columns;
          isWideI2vFrame = true;
          rt.currentLookIndex = null;
        } else if (dist < 26) {
          rt.smoothLookDeg = null;
          rt.currentLookIndex = null;
        } else {
          rt.smoothLookDeg = null;
          const lookIdx = computeClockwiseLookIndex(dx, dy, rt.currentLookIndex);
          rt.currentLookIndex = lookIdx;
          if (lookIdx !== null) {
            if (
              assets.lookAtlasImage &&
              assets.lookColumns &&
              assets.lookCellWidth &&
              assets.lookCellHeight
            ) {
              drawImage = assets.lookAtlasImage;
              drawCellW = assets.lookCellWidth;
              drawCellH = assets.lookCellHeight;
              drawRow = 9 + Math.floor(lookIdx / assets.lookColumns);
              drawCol = lookIdx % assets.lookColumns;
              isWideI2vFrame = false;
            } else if (i2vLayout && typeof i2vLayout.lookStartSlot === 'number') {
              drawImage = assets.atlasImage;
              drawCellW = assets.cellWidth;
              drawCellH = assets.cellHeight;
              const lookSlot = i2vLayout.lookStartSlot + lookIdx;
              drawRow = Math.floor(lookSlot / i2vLayout.columns);
              drawCol = lookSlot % i2vLayout.columns;
              isWideI2vFrame = false;
            } else {
              drawImage = assets.atlasImage;
              drawCellW = assets.cellWidth;
              drawCellH = assets.cellHeight;
              drawRow = 9 + Math.floor(lookIdx / 8);
              drawCol = lookIdx % 8;
              isWideI2vFrame = false;
            }
          }
        }
      } else {
        rt.smoothLookDeg = null;
        rt.currentLookIndex = null;
      }

      // 5. Crisp HiDPI Canvas Blit (with expanded side/top bleed box so wagging tail never clips!)
      const cBox = assets.highFpsLayout?.canvasBox;
      const scaleX = cBox?.scaleX ?? 1;
      const scaleY = cBox?.scaleY ?? 1;
      const offsetX = cBox?.offsetX ?? 0;
      const offsetY = cBox?.offsetY ?? 0;

      const cssCanvasW = displayWidth * scaleX;
      const cssCanvasH = displayHeight * scaleY;
      const dpr = Math.min(window.devicePixelRatio || 1, 2.5);
      const targetW = Math.round(cssCanvasW * dpr);
      const targetH = Math.round(cssCanvasH * dpr);
      const resized = canvas.width !== targetW || canvas.height !== targetH;
      if (resized) {
        canvas.width = targetW;
        canvas.height = targetH;
      }

      const drawingGaze = Boolean(liveGaze && rt.gazeBlend > 0);
      if (
        !resized &&
        previousImage === drawImage &&
        previousRow === drawRow &&
        previousColumn === drawCol &&
        !drawingGaze &&
        !previousGaze
      ) {
        rafId = window.requestAnimationFrame(tick);
        return;
      }
      previousImage = drawImage;
      previousRow = drawRow;
      previousColumn = drawCol;
      previousGaze = drawingGaze;
      ctx.clearRect(0, 0, targetW, targetH);
      ctx.imageSmoothingEnabled = true;
      ctx.imageSmoothingQuality = 'high';

      const sx = drawCol * drawCellW;
      const sy = drawRow * drawCellH;
      if (rt.gazeBlend < 1) {
        const logicalFrame = !isWideI2vFrame && cBox;
        const dstX = logicalFrame ? (-offsetX / scaleX) * targetW : 0;
        const dstY = logicalFrame ? (-offsetY / scaleY) * targetH : 0;
        const dstW = logicalFrame ? targetW / scaleX : targetW;
        const dstH = logicalFrame ? targetH / scaleY : targetH;
        ctx.globalAlpha = 1 - rt.gazeBlend;
        ctx.drawImage(drawImage, sx, sy, drawCellW, drawCellH, dstX, dstY, dstW, dstH);
        ctx.globalAlpha = 1;
      }
      if (liveGaze && rt.gazeBlend > 0) liveGaze.draw(ctx, rt.gazeX, rt.gazeY, rt.gazeBlend, now);

      rafId = window.requestAnimationFrame(tick);
    };

    rafId = window.requestAnimationFrame(tick);
    return () => window.cancelAnimationFrame(rafId);
  }, [assets, displayWidth, displayHeight, reduced]);

  if (reduced || !assets || assets.manifest.id !== targetPetId || loadFailed) {
    return (
      <div className={className} style={{ position: 'relative', display: 'inline-block' }}>
        <img
          className="whale-sprite"
          src={
            isWorkerUniform
              ? getSceneAssetUrl('sprites/whale-worker.webp')
              : outfit === 'winter_snow'
                ? getSceneAssetUrl('sprites/whale-winter.webp')
                : outfit === 'knit_beret'
                  ? getSceneAssetUrl('sprites/whale-knit.webp')
                  : outfit === 'whale_hoodie'
                    ? getSceneAssetUrl('sprites/whale-hoodie.webp')
                    : getSpriteUrl(expression)
          }
          alt={alt}
          draggable={false}
          style={{ width: displayWidth, height: 'auto' }}
        />
        {children}
      </div>
    );
  }

  const cBox = assets.highFpsLayout?.canvasBox;
  const cssLeft = (cBox?.offsetX ?? 0) * displayWidth;
  const cssTop = (cBox?.offsetY ?? 0) * displayHeight;
  const cssW = (cBox?.scaleX ?? 1) * displayWidth;
  const cssH = (cBox?.scaleY ?? 1) * displayHeight;

  return (
    <div
      ref={wrapRef}
      className={className}
      style={{
        width: displayWidth,
        height: displayHeight,
        position: 'relative',
        overflow: 'visible',
        willChange: roamEnabled ? 'transform' : undefined,
      }}
    >
      <canvas
        ref={canvasRef}
        role="img"
        aria-label={alt}
        className="whale-rig-canvas"
        style={{
          position: 'absolute',
          left: cssLeft,
          top: cssTop,
          width: cssW,
          height: cssH,
          maxWidth: 'none',
          display: 'block',
          pointerEvents: 'none',
        }}
      />
      {children}
    </div>
  );
}

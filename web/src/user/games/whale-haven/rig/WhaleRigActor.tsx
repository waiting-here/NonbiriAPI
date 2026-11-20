import { useLayoutEffect, useEffect, useMemo, useRef, useState } from 'react';
import { getSpriteUrl } from '../game/catalog';
import type { WhaleExpression, WhaleOutfitId, WhaleRenderMode } from '../game/types';
import {
  IDENTITY_MATRIX,
  createRigRuntimeState,
  makeBoneLocalMatrix,
  multiplyMatrix,
  stepAndEvaluateRig,
  triggerRigImpulse,
  type Matrix2D,
  type RigActivity,
} from './animator';
import { DEFAULT_FACE_CALIBRATION, type FaceCalibrationConfig } from './calibrator';
import { loadWhaleRigBundle, type LoadedRigBundle } from './loader';

export interface RigImpulseEvent {
  seq: number;
  kind: 'headpat' | 'poke' | 'teacake' | 'celebrate' | 'feed';
}

interface WhaleRigActorProps {
  alt: string;
  outfit: WhaleOutfitId;
  expression: WhaleExpression;
  pinnedExpression?: string | null;
  renderMode?: WhaleRenderMode;
  activity: RigActivity;
  heldProp?: 'none' | 'silver_fish' | 'berry_cake' | 'pearl';
  impulse?: RigImpulseEvent | null;
  calibration?: FaceCalibrationConfig;
  showBonesDebug?: boolean;
  displayWidth?: number;
  reloadToken?: number;
  className?: string;
}

// 聚焦 1024x1024 母版中角色有效活动区（兼顾头顶呆毛弹跳与右侧大鲸鱼摆尾）
const VIEW_X = 148;
const VIEW_Y = 34;
const VIEW_W = 768;
const VIEW_H = 910;

// 实测 whale-cheerful.webp (680x727) 映射到 1024x1024 母版的精确对齐框
const REF_OVERLAY_RECT = { x: 157.5, y: 41.5, w: 835.9, h: 893.3 };

export function WhaleRigActor({
  alt,
  outfit,
  expression,
  pinnedExpression = null,
  renderMode = 'rig',
  activity,
  heldProp,
  impulse = null,
  calibration = DEFAULT_FACE_CALIBRATION,
  showBonesDebug = false,
  displayWidth = 252,
  reloadToken = 0,
  className,
}: WhaleRigActorProps) {
  const [bundle, setBundle] = useState<LoadedRigBundle | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const overlayImgRef = useRef<HTMLImageElement | null>(null);

  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const [initialRuntime] = useState(() => createRigRuntimeState(Math.random() * 6));
  const runtimeRef = useRef(initialRuntime);
  const pointerNormRef = useRef({ x: 0, y: 0 });
  const lastImpulseSeqRef = useRef<number>(0);

  const displayHeight = Math.round((displayWidth * VIEW_H) / VIEW_W);

  useEffect(() => {
    if (renderMode === 'sprite') return;
    let cancelled = false;
    loadWhaleRigBundle(reloadToken > 0)
      .then((loaded) => {
        if (!cancelled) {
          setBundle(loaded);
          setLoadFailed(false);
        }
      })
      .catch(() => {
        if (!cancelled) setLoadFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, [renderMode, reloadToken]);

  // 预加载半透明叠底原画参考图
  useEffect(() => {
    if (!calibration.overlayEnabled) return;
    const img = new Image();
    img.src = getSpriteUrl('cheerful');
    img.onload = () => {
      overlayImgRef.current = img;
    };
  }, [calibration.overlayEnabled]);

  useEffect(() => {
    if (!impulse || !bundle) return;
    if (impulse.seq !== lastImpulseSeqRef.current) {
      lastImpulseSeqRef.current = impulse.seq;
      const mappedKind =
        impulse.kind === 'celebrate'
          ? 'headpat'
          : impulse.kind === 'feed'
            ? 'teacake'
            : impulse.kind;
      triggerRigImpulse(runtimeRef.current, bundle.manifest, mappedKind);
    }
  }, [impulse, bundle]);

  useEffect(() => {
    if (renderMode === 'sprite') return;
    const onPointerMove = (ev: PointerEvent) => {
      const canvas = canvasRef.current;
      if (!canvas) return;
      const rect = canvas.getBoundingClientRect();
      const cx = rect.left + rect.width * 0.48;
      const cy = rect.top + rect.height * 0.45;
      const nx = Math.max(-1, Math.min(1, (ev.clientX - cx) / 260));
      const ny = Math.max(-1, Math.min(1, (ev.clientY - cy) / 220));
      pointerNormRef.current = { x: nx, y: ny };
    };
    window.addEventListener('pointermove', onPointerMove, { passive: true });
    return () => window.removeEventListener('pointermove', onPointerMove);
  }, [renderMode]);

  const sortedSlots = useMemo(() => {
    if (!bundle) return [];
    return [...bundle.manifest.slots].sort((a, b) => a.zIndex - b.zIndex);
  }, [bundle]);

  const latestPropsRef = useRef({
    outfit,
    expression,
    pinnedExpression,
    activity,
    heldProp,
    calibration,
    showBonesDebug,
  });
  useLayoutEffect(() => {
    latestPropsRef.current = {
      outfit,
      expression,
      pinnedExpression,
      activity,
      heldProp,
      calibration,
      showBonesDebug,
    };
  });

  useEffect(() => {
    if (renderMode === 'sprite' || !bundle || !canvasRef.current) return;

    const canvas = canvasRef.current;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    let rafId = 0;
    let lastTime = performance.now();

    const applyAndDraw = (
      img: HTMLImageElement,
      m: Matrix2D,
      scaleX: number,
      scaleY: number,
      sx = 0,
      sy = 0,
      sw = 1024,
      sh = 1024,
    ) => {
      ctx.setTransform(
        scaleX * m.a,
        scaleY * m.b,
        scaleX * m.c,
        scaleY * m.d,
        scaleX * (m.e - VIEW_X),
        scaleY * (m.f - VIEW_Y),
      );
      ctx.drawImage(img, sx, sy, sw, sh, sx, sy, sw, sh);
    };

    const renderFrame = (now: number) => {
      const rawDt = (now - lastTime) / 1000;
      lastTime = now;

      const dpr = Math.min(window.devicePixelRatio || 1, 2);
      const targetW = Math.round(displayWidth * dpr);
      const targetH = Math.round(displayHeight * dpr);
      if (canvas.width !== targetW || canvas.height !== targetH) {
        canvas.width = targetW;
        canvas.height = targetH;
      }

      const p = latestPropsRef.current;
      const cal = p.calibration;
      const frame = stepAndEvaluateRig(runtimeRef.current, bundle.manifest, {
        dtSec: cal.freezeMotion ? 0.0001 : rawDt,
        outfitId: p.outfit,
        expressionKey: p.expression,
        pinnedExpressionId: p.pinnedExpression,
        activity: cal.freezeMotion ? 'idle' : p.activity,
        heldProp: p.heldProp,
        pointerNormX: cal.freezeMotion ? 0 : pointerNormRef.current.x,
        pointerNormY: cal.freezeMotion ? 0 : pointerNormRef.current.y,
      });

      // 当开启静止对齐开关时，使用全部单位矩阵锁死中立姿态方便 1:1 叠底比对
      const getBoneMat = (boneId: keyof typeof frame.boneMatrices): Matrix2D =>
        cal.freezeMotion ? IDENTITY_MATRIX : (frame.boneMatrices[boneId] ?? IDENTITY_MATRIX);

      ctx.setTransform(1, 0, 0, 1, 0, 0);
      ctx.clearRect(0, 0, canvas.width, canvas.height);
      ctx.imageSmoothingEnabled = true;
      ctx.imageSmoothingQuality = 'high';

      const scaleX = canvas.width / VIEW_W;
      const scaleY = canvas.height / VIEW_H;

      for (const slot of sortedSlots) {
        const variantKey = frame.slotVariants[slot.id] ?? slot.defaultVariant;
        const entry = slot.variants[variantKey] ?? slot.variants[slot.defaultVariant];
        if (!entry) continue;
        const img = bundle.images.get(entry.file);
        if (!img) continue;

        let m = getBoneMat(slot.bone);

        if (slot.id === 'head_base') {
          if (cal.faceScaleX !== 1 || cal.faceScaleY !== 1 || cal.faceOffsetY !== 0) {
            m = multiplyMatrix(
              m,
              makeBoneLocalMatrix(512, 420, 0, 0, cal.faceOffsetY, cal.faceScaleX, cal.faceScaleY),
            );
          }
          applyAndDraw(img, m, scaleX, scaleY);
          continue;
        }

        if (slot.id === 'hair_front' || slot.id === 'headwear') {
          if (cal.bangsOffsetY !== 0) {
            m = multiplyMatrix(m, makeBoneLocalMatrix(512, 320, 0, 0, cal.bangsOffsetY, 1, 1));
          }
          applyAndDraw(img, m, scaleX, scaleY);
          continue;
        }

        if (slot.id === 'mouth') {
          if (cal.mouthScale !== 1 || cal.mouthOffsetY !== 0) {
            m = multiplyMatrix(
              m,
              makeBoneLocalMatrix(512, 535, 0, 0, cal.mouthOffsetY, cal.mouthScale, cal.mouthScale),
            );
          }
          applyAndDraw(img, m, scaleX, scaleY);
          continue;
        }

        if (
          slot.id === 'eyes_iris' ||
          slot.id === 'eyes_highlight' ||
          slot.id === 'eyelids' ||
          slot.id === 'brows'
        ) {
          const gazeX =
            !cal.freezeMotion && (slot.id === 'eyes_iris' || slot.id === 'eyes_highlight')
              ? frame.irisOffsetPx.x
              : 0;
          const gazeY =
            !cal.freezeMotion && (slot.id === 'eyes_iris' || slot.id === 'eyes_highlight')
              ? frame.irisOffsetPx.y
              : 0;
          const isEye = slot.id !== 'brows';
          const sc = isEye ? cal.eyeScale : 1;
          const dy = cal.eyeOffsetY + gazeY;
          const gapHalf = cal.eyeGapOffset * 0.5;

          if (sc !== 1 || dy !== 0 || gapHalf !== 0 || gazeX !== 0) {
            const mLeft = multiplyMatrix(
              m,
              makeBoneLocalMatrix(424, 464, 0, -gapHalf + gazeX, dy, sc, sc),
            );
            const mRight = multiplyMatrix(
              m,
              makeBoneLocalMatrix(600, 464, 0, gapHalf + gazeX, dy, sc, sc),
            );
            applyAndDraw(img, mLeft, scaleX, scaleY, 0, 0, 512, 1024);
            applyAndDraw(img, mRight, scaleX, scaleY, 512, 0, 512, 1024);
            continue;
          }
        }

        applyAndDraw(img, m, scaleX, scaleY);
      }

      // 半透明原画叠图微调对照层 (Onion-Skin Reference Overlay)
      if (cal.overlayEnabled && overlayImgRef.current && cal.overlayOpacity > 0) {
        ctx.save();
        ctx.globalAlpha = Math.max(0, Math.min(1, cal.overlayOpacity));
        ctx.setTransform(scaleX, 0, 0, scaleY, -VIEW_X * scaleX, -VIEW_Y * scaleY);
        ctx.drawImage(
          overlayImgRef.current,
          REF_OVERLAY_RECT.x,
          REF_OVERLAY_RECT.y,
          REF_OVERLAY_RECT.w,
          REF_OVERLAY_RECT.h,
        );
        ctx.restore();
      }

      // 实时骨骼连线与弹簧关节调试叠层
      if (p.showBonesDebug) {
        ctx.setTransform(scaleX, 0, 0, scaleY, -VIEW_X * scaleX, -VIEW_Y * scaleY);
        const boneWorldPos = new Map<string, { x: number; y: number }>();
        for (const b of bundle.manifest.bones) {
          const m = getBoneMat(b.id);
          const wx = m.a * b.pivotX + m.c * b.pivotY + m.e;
          const wy = m.b * b.pivotX + m.d * b.pivotY + m.f;
          boneWorldPos.set(b.id, { x: wx, y: wy });
        }

        ctx.lineWidth = 2.4;
        for (const b of bundle.manifest.bones) {
          if (!b.parent) continue;
          const p0 = boneWorldPos.get(b.parent);
          const p1 = boneWorldPos.get(b.id);
          if (!p0 || !p1) continue;
          ctx.strokeStyle = b.spring ? 'rgba(92, 242, 214, 0.88)' : 'rgba(255, 198, 92, 0.85)';
          ctx.beginPath();
          ctx.moveTo(p0.x, p0.y);
          ctx.lineTo(p1.x, p1.y);
          ctx.stroke();
        }

        for (const b of bundle.manifest.bones) {
          const pt = boneWorldPos.get(b.id);
          if (!pt) continue;
          ctx.fillStyle = b.spring ? '#42f5d4' : '#ff9f43';
          ctx.beginPath();
          ctx.arc(pt.x, pt.y, b.spring ? 5.5 : 6.5, 0, Math.PI * 2);
          ctx.fill();
          ctx.strokeStyle = '#0d2230';
          ctx.lineWidth = 1.8;
          ctx.stroke();
        }
      }

      rafId = window.requestAnimationFrame(renderFrame);
    };

    rafId = window.requestAnimationFrame(renderFrame);
    return () => window.cancelAnimationFrame(rafId);
  }, [renderMode, bundle, sortedSlots, displayWidth, displayHeight]);

  if (renderMode === 'sprite' || !bundle || loadFailed) {
    return (
      <img
        className={className ?? 'whale-sprite'}
        src={getSpriteUrl(expression)}
        alt={alt}
        draggable={false}
        style={{ width: displayWidth, height: 'auto' }}
      />
    );
  }

  return (
    <canvas
      ref={canvasRef}
      aria-label={alt}
      className={className ?? 'whale-rig-canvas'}
      style={{
        width: displayWidth,
        height: displayHeight,
        display: 'block',
        pointerEvents: 'none',
        filter: 'drop-shadow(0 14px 20px rgba(4, 18, 30, 0.42))',
      }}
    />
  );
}

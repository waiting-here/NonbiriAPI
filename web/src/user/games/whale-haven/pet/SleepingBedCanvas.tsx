import { useEffect, useRef, useState } from 'react';
import {
  invalidateCodexPetCache,
  requestMotion,
  loadCodexPetAssets,
  resolveOutfitPetId,
} from './loader';
import type { LoadedCodexPetAssets, LoadedPetSheet, PetHighFpsStateSlice } from './types';
import type { WhalePetOutfitProp } from './WhalePetActor';

export interface SleepingBedCanvasProps {
  outfit?: WhalePetOutfitProp;
  petId?: string;
  reloadToken?: number;
  onReadyChange?: (ready: boolean) => void;
}

export function SleepingBedCanvas({
  outfit,
  petId,
  reloadToken = 0,
  onReadyChange,
}: SleepingBedCanvasProps) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const [sleepData, setSleepData] = useState<{
    sheet: LoadedPetSheet;
    slice: PetHighFpsStateSlice;
  } | null>(null);

  const targetPetId = petId ?? resolveOutfitPetId(outfit);

  useEffect(() => {
    if (reloadToken > 0) {
      invalidateCodexPetCache(targetPetId);
    }
    let cancelled = false;

    const applyLoaded = (loaded: LoadedCodexPetAssets) => {
      if (loaded.sleepingSheet && loaded.highFpsLayout?.sleeping) {
        setSleepData({
          sheet: loaded.sleepingSheet,
          slice: loaded.highFpsLayout.sleeping,
        });
        onReadyChange?.(true);
        return true;
      }
      return false;
    };

    const loadFallback = () => {
      loadCodexPetAssets('whale-maid')
        .then(async (assets) => {
          await requestMotion(assets, 'sleeping');
          return assets;
        })
        .then((fallbackLoaded) => {
          if (cancelled) return;
          if (!applyLoaded(fallbackLoaded)) {
            setSleepData(null);
            onReadyChange?.(false);
          }
        })
        .catch(() => {
          if (!cancelled) {
            setSleepData(null);
            onReadyChange?.(false);
          }
        });
    };

    loadCodexPetAssets(targetPetId)
      .then(async (assets) => {
        await requestMotion(assets, 'sleeping');
        return assets;
      })
      .then((loaded) => {
        if (cancelled) return;
        if (!applyLoaded(loaded)) {
          if (targetPetId !== 'whale-maid') {
            loadFallback();
          } else {
            setSleepData(null);
            onReadyChange?.(false);
          }
        }
      })
      .catch((err) => {
        if (cancelled) return;
        console.warn(`[SleepingBedCanvas] Failed to load sleeping bed "${targetPetId}":`, err);
        if (targetPetId !== 'whale-maid') {
          loadFallback();
        } else {
          setSleepData(null);
          onReadyChange?.(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [targetPetId, reloadToken, onReadyChange]);

  useEffect(() => {
    if (!sleepData) return;
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    const { sheet, slice } = sleepData;
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    const cw = sheet.cellWidth;
    const ch = sheet.cellHeight;
    canvas.width = Math.round(cw * dpr);
    canvas.height = Math.round(ch * dpr);

    let rafId = 0;
    let lastDrawnFrame = -1;
    const startPerf = performance.now();
    const frameMs = Math.max(24, slice.frameMs || 42);
    const totalFrames = Math.max(1, slice.frames);

    const tick = (now: number) => {
      const frameIdx = Math.floor((now - startPerf) / frameMs) % totalFrames;
      if (frameIdx !== lastDrawnFrame) {
        lastDrawnFrame = frameIdx;
        const slot = (slice.startSlot || 0) + frameIdx;
        const col = slot % sheet.columns;
        const row = Math.floor(slot / sheet.columns);
        const sx = col * cw;
        const sy = row * ch;

        ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
        ctx.clearRect(0, 0, cw, ch);
        ctx.imageSmoothingEnabled = true;
        ctx.imageSmoothingQuality = 'high';
        ctx.drawImage(sheet.image, sx, sy, cw, ch, 0, 0, cw, ch);
      }
      if (!window.matchMedia('(prefers-reduced-motion: reduce)').matches)
        rafId = window.requestAnimationFrame(tick);
    };

    rafId = window.requestAnimationFrame(tick);
    return () => window.cancelAnimationFrame(rafId);
  }, [sleepData]);

  if (!sleepData) return null;

  return (
    <canvas
      ref={canvasRef}
      className="cabin-bed__sleep-canvas"
      aria-label="小鲸鱼正趴在蓝鲸软垫窝里呼呼大睡"
    />
  );
}

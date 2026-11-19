import { artworkActivity, type ArtworkPlayback } from '@shared/animation/artwork-playback';
import { blinkAt, PaintCadence } from '@shared/animation/illustration';
import type { MomentExpansion } from '../moment-frame';
import { createClassicScene } from './scene';
import type { ClassicCard } from './types';

export type ClassicOptions = {
  frozenTime?: number;
  paused?: () => boolean;
  playback?: ArtworkPlayback;
  highResolution?: boolean;
  characterOnly?: boolean;
  signal?: AbortSignal;
  momentFrame?: MomentExpansion;
};

export async function renderClassicSSR(
  canvas: HTMLCanvasElement,
  id: string,
  options: ClassicOptions = {},
) {
  const activity = artworkActivity(options.playback);
  const { signal, frozenTime, paused, highResolution } = options;
  signal?.throwIfAborted();
  const preference = activity.systemMotion;
  const cadence = new PaintCadence(undefined, !!options.playback, options.playback?.highest);
  const frame =
    options.characterOnly && options.momentFrame?.mode === 'expanded-layer'
      ? options.momentFrame
      : undefined;
  let queued = 0;
  let elapsed = frozenTime ?? 0;
  let previous: number | undefined;
  let disposed = false;
  const scene = await createClassicScene(canvas, id as ClassicCard, options, frame, () => {
    options.playback?.setFailed(true);
    dispose();
  });
  if (signal?.aborted) {
    scene.dispose();
    signal.throwIfAborted();
  }

  const paint = () => {
    const motion = !preference?.matches;
    scene.paint(elapsed, motion ? blinkAt(elapsed) : 0, motion);
  };
  const measure = () => {
    if (disposed) return;
    const size = canvas.getBoundingClientRect().width || canvas.width;
    const cap = highResolution ? (frame?.highResolutionWidth ?? 2560) : (frame?.canvas[0] ?? 1024);
    const width = Math.max(1, Math.min(cap, Math.round(size * (window.devicePixelRatio || 1))));
    const height = Math.max(
      1,
      Math.round(width * (frame ? frame.canvas[1] / frame.canvas[0] : 1.5)),
    );
    if (canvas.width !== width || canvas.height !== height) {
      canvas.width = width;
      canvas.height = height;
    }
    scene.resize();
    paint();
  };
  const tick = (now: number) => {
    queued = 0;
    if (disposed || preference?.matches || activity.hidden) return;
    if (!paused?.()) {
      elapsed += previous === undefined ? 0 : Math.min(0.05, Math.max(0, (now - previous) / 1000));
      if (cadence.due(now)) paint();
    }
    previous = now;
    queued = requestAnimationFrame(tick);
  };
  const reset = () => {
    cancelAnimationFrame(queued);
    queued = 0;
    previous = undefined;
    cadence.reset();
    if (!disposed && !activity.hidden && !preference?.matches && frozenTime === undefined)
      queued = requestAnimationFrame(tick);
  };
  const preferenceChanged = () => {
    reset();
    if (!disposed) paint();
  };
  const observer = new ResizeObserver(measure);
  function dispose() {
    if (disposed) return;
    disposed = true;
    cancelAnimationFrame(queued);
    observer.disconnect();
    activity.unlisten(reset);
    window.removeEventListener('resize', measure);
    preference?.removeEventListener('change', preferenceChanged);
    signal?.removeEventListener('abort', dispose);
    scene.dispose();
    canvas.dataset.ready = 'false';
  }
  try {
    measure();
    canvas.style.removeProperty('visibility');
    canvas.dataset.ready = 'true';
    observer.observe(canvas);
    activity.listen(reset);
    window.addEventListener('resize', measure);
    preference?.addEventListener('change', preferenceChanged);
    signal?.addEventListener('abort', dispose, { once: true });
    reset();
  } catch (error) {
    dispose();
    throw error;
  }
  return dispose;
}

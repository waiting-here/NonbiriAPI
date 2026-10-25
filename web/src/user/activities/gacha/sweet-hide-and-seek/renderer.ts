import { blinkAt, PaintCadence } from '@shared/animation/illustration';
import { createSweetScene } from './scene';
import type { SceneOptions } from './scene';
import type { SweetCard } from './motion';

export type SweetOptions = SceneOptions & {
  frozenTime?: number;
  paused?: () => boolean;
};

export async function renderSweetHideAndSeek(
  canvas: HTMLCanvasElement,
  id: SweetCard,
  options: SweetOptions = {},
) {
  if (options.signal?.aborted) throw new DOMException('Aborted', 'AbortError');
  let stopScene = () => {};
  const scene = await createSweetScene(canvas, id, options, () => stopScene());
  if (options.signal?.aborted) {
    scene.dispose();
    throw new DOMException('Aborted', 'AbortError');
  }
  const preference = window.matchMedia('(prefers-reduced-motion: reduce)');
  const cadence = new PaintCadence();
  let request = 0,
    elapsed = options.frozenTime ?? 0,
    previous: number | undefined,
    disposed = false;
  const paint = () => {
    const motion = !preference.matches;
    const blink = motion ? blinkAt(elapsed) : 0;
    scene.paint(elapsed, blink, motion);
  };
  const measure = () => {
    if (disposed) return;
    const width = canvas.getBoundingClientRect().width || canvas.width;
    const cap = options.highResolution ? 2560 : 1024;
    const next = Math.max(1, Math.min(cap, Math.round(width * (window.devicePixelRatio || 1))));
    canvas.width = next;
    canvas.height = Math.round(next * 1.5);
    scene.resize();
    paint();
  };
  const tick = (now: number) => {
    request = 0;
    if (disposed || document.hidden || preference.matches) return;
    if (!options.paused?.()) {
      elapsed += previous === undefined ? 0 : Math.max(0, Math.min((now - previous) / 1000, 0.05));
      if (cadence.due(now)) paint();
    }
    previous = now;
    request = requestAnimationFrame(tick);
  };
  const reset = () => {
    cancelAnimationFrame(request);
    request = 0;
    previous = undefined;
    cadence.reset();
    if (!disposed && !document.hidden && !preference.matches && options.frozenTime === undefined)
      request = requestAnimationFrame(tick);
  };
  const preferenceChanged = () => {
    reset();
    if (!disposed) paint();
  };
  const observer = new ResizeObserver(measure);
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    cancelAnimationFrame(request);
    observer.disconnect();
    document.removeEventListener('visibilitychange', reset);
    window.removeEventListener('resize', measure);
    preference.removeEventListener('change', preferenceChanged);
    options.signal?.removeEventListener('abort', dispose);
    scene.dispose();
    canvas.dataset.ready = 'false';
  };
  stopScene = dispose;
  try {
    measure();
    canvas.style.removeProperty('visibility');
    canvas.dataset.ready = 'true';
    observer.observe(canvas);
    window.addEventListener('resize', measure);
    document.addEventListener('visibilitychange', reset);
    preference.addEventListener('change', preferenceChanged);
    options.signal?.addEventListener('abort', dispose, { once: true });
    reset();
  } catch (error) {
    dispose();
    throw error;
  }
  return dispose;
}

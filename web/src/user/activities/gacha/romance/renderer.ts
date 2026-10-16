import { createScene } from './compositor.js';
import { PaintCadence } from '@shared/animation/illustration';

export async function renderRomance(
  canvas: HTMLCanvasElement,
  id: 'elopement' | 'heart-thief',
  options: {
    polished?: boolean;
    exportSize?: boolean;
    blink?: number;
    variant?: 'card' | 'cover' | 'illustration' | 'appearance';
    landscape?: boolean;
    dark?: boolean;
    highResolution?: boolean;
    characterOnly?: boolean;
    backgroundOnly?: boolean;
    frozenTime?: number;
    paused?: () => boolean;
    signal?: AbortSignal;
  } = {},
) {
  const scene = await createScene(id, options.polished);
  if (options.signal?.aborted) {
    scene.dispose();
    throw new DOMException('Cancelled', 'AbortError');
  }
  const landscape = options.landscape;
  const buffer = document.createElement('canvas');
  const cadence = new PaintCadence();
  let request = 0,
    elapsed = options.frozenTime ?? 0,
    previous = 0,
    disposed = false;
  const measure = () => {
    if (options.exportSize) {
      buffer.width = canvas.width;
      buffer.height = canvas.height;
      return;
    }
    const width = canvas.getBoundingClientRect().width || 768;
    const ratio = window.devicePixelRatio || 1;
    const cap = options.highResolution ? 2560 : 1280;
    canvas.width = Math.max(256, Math.min(cap, Math.round(width * ratio)));
    canvas.height = landscape
      ? Math.round((canvas.width * (canvas.getBoundingClientRect().height || innerHeight)) / width)
      : Math.round(canvas.width * 1.5);
    buffer.width = landscape
      ? Math.min(1920, Math.max(canvas.width, (canvas.height * 16) / 9))
      : canvas.width;
    buffer.height = landscape ? Math.round((buffer.width * 9) / 16) : canvas.height;
  };
  const paint = () => {
    scene.paint(
      buffer,
      elapsed,
      options.variant ?? (landscape ? 'appearance' : 'card'),
      options.dark,
      options.characterOnly ? 'hero' : options.backgroundOnly ? 'background' : 'complete',
      true,
      options.blink,
    );
    const context = canvas.getContext('2d')!;
    context.imageSmoothingQuality = 'high';
    const scale = Math.max(canvas.width / buffer.width, canvas.height / buffer.height);
    const x = (canvas.width - buffer.width * scale) * 0.78;
    context.clearRect(0, 0, canvas.width, canvas.height);
    context.drawImage(buffer, x, 0, buffer.width * scale, buffer.height * scale);
    canvas.dataset.ready = 'true';
    canvas.dataset.time = elapsed.toFixed(3);
    canvas.dataset.blink = buffer.dataset.blink;
  };
  const tick = (now: number) => {
    if (disposed) return;
    if (!document.hidden && !options.paused?.()) {
      elapsed += previous ? Math.min((now - previous) / 1000, 0.05) : 0;
      if (cadence.due(now)) paint();
    }
    previous = now;
    if (!document.hidden) request = requestAnimationFrame(tick);
  };
  const visibility = () => {
    cancelAnimationFrame(request);
    previous = 0;
    cadence.reset();
    if (!document.hidden && !disposed && options.frozenTime === undefined)
      request = requestAnimationFrame(tick);
  };
  measure();
  paint();
  const observer = new ResizeObserver(() => {
    measure();
    paint();
  });
  observer.observe(canvas);
  if (options.frozenTime === undefined && !document.hidden) request = requestAnimationFrame(tick);
  document.addEventListener('visibilitychange', visibility);
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    cancelAnimationFrame(request);
    observer.disconnect();
    document.removeEventListener('visibilitychange', visibility);
    options.signal?.removeEventListener('abort', dispose);
    scene.dispose();
    canvas.dataset.ready = 'false';
  };
  options.signal?.addEventListener('abort', dispose, { once: true });
  return dispose;
}

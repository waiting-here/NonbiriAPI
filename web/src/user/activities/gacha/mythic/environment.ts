import { createWanderingAir } from './wandering-air';
import { celestialParts, paintCelestialPart } from './celestial-parts';
import { loadMythicImage } from './character';
import type { MythicCard } from './layers';
import { createWhalePlane } from './whale';
export async function createMythicEnvironment(
  id: MythicCard,
  options: { highResolution?: boolean; signal?: AbortSignal } = {},
) {
  const prefix =
    '/assets/gacha/' + (options.highResolution ? 'hires/' : '') + 'motion/' + id + '/parts/';
  const controller = new AbortController(),
    abort = () => controller.abort();
  options.signal?.addEventListener('abort', abort, { once: true });
  if (options.signal?.aborted) abort();
  const images: HTMLImageElement[] = [];
  let air: Awaited<ReturnType<typeof createWanderingAir>> | null = null;
  const names =
    id === 'm-deepseek'
      ? ['m-air-scenery', 'm-air-companion', 'm-air-cover', 'water-mask']
      : [
          'scenery',
          'companion',
          'depth-cover',
          'water-mask',
          'moon',
          'earth',
          ...celestialParts.map((part) => part.name),
        ];
  const failed = (error: unknown): never => {
    controller.abort();
    throw error;
  };
  const platePromise = Promise.all(
    names.map(async (name, index) => {
      images[index] = await loadMythicImage(prefix + name + '.webp', controller.signal);
    }),
  ).catch(failed);
  const airPromise = (
    id === 'm-deepseek'
      ? createWanderingAir({ ...options, signal: controller.signal })
      : Promise.resolve(null)
  ).catch(failed);
  try {
    const results = await Promise.allSettled([platePromise, airPromise]);
    if (results[1].status === 'fulfilled') air = results[1].value;
    const failure = results.find((result) => result.status === 'rejected');
    if (failure?.status === 'rejected') throw failure.reason;
    controller.signal.throwIfAborted();
  } catch (error) {
    controller.abort();
    air?.dispose();
    images.forEach((image) => (image.src = ''));
    throw error;
  } finally {
    options.signal?.removeEventListener('abort', abort);
  }
  let swimmer: ReturnType<typeof createWhalePlane> | null = null;
  const water = document.createElement('canvas');
  let disposed = false;
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    swimmer?.dispose();
    air?.dispose();
    images.forEach((image) => (image.src = ''));
    water.width = water.height = 0;
  };
  let wc: CanvasRenderingContext2D;
  try {
    swimmer = id === 'm-deepseek' ? createWhalePlane(images[1]) : null;
    water.width = 512;
    water.height = 768;
    const context = water.getContext('2d');
    if (!context) throw new Error('renderer-unavailable');
    wc = context;
  } catch (error) {
    dispose();
    throw error;
  }
  return {
    surface: swimmer?.canvas,
    surfaces: [swimmer?.canvas, air?.canvas].filter(
      (canvas): canvas is HTMLCanvasElement => !!canvas,
    ),
    paint(ctx: CanvasRenderingContext2D, t: number, width: number, height: number, motion = true) {
      if (disposed) return;
      ctx.save();
      ctx.scale(width / 1024, height / 1536);
      ctx.drawImage(images[0], 0, 0, 1024, 1536);
      if (id === 'm-deepseek') {
        swimmer!.paint(t, motion, width, height);
        ctx.drawImage(swimmer!.canvas, 0, 0, 1024, 1536);
        ctx.drawImage(images[2], 0, 0, 1024, 1536);
        if (motion) {
          wc.clearRect(0, 0, 512, 768);
          wc.globalCompositeOperation = 'source-over';
          wc.strokeStyle = '#c3ffff';
          wc.lineWidth = 2;
          for (let n = 0; n < 17; n++) {
            const y = ((n * 67 + t * 20) % 850) - 40;
            wc.globalAlpha = 0.18 + 0.08 * Math.sin(n * 2.1 + t * 0.4);
            wc.beginPath();
            wc.moveTo(-30, y);
            wc.bezierCurveTo(125, y + 42, 318, y - 34, 550, y + 62);
            wc.stroke();
          }
          wc.globalAlpha = 1;
          wc.globalCompositeOperation = 'destination-in';
          wc.drawImage(images[3], 0, 0, 512, 768);
          ctx.globalCompositeOperation = 'screen';
          ctx.drawImage(water, 0, 0, 1024, 1536);
          ctx.globalCompositeOperation = 'source-over';
        }
        air?.paint(ctx, t, width, height, motion);
      } else {
        for (const [index, cx, cy, period, radius, tilt] of [
          [4, 382, 256, 95, 3.4, 0.0017],
          [5, 954, 684, 133, 2.6, 0.0011],
        ]) {
          const phase = motion ? t / period : 0;
          ctx.save();
          ctx.translate(cx + Math.sin(phase) * radius, cy + (Math.cos(phase) - 1) * radius * 0.55);
          ctx.rotate(Math.sin(phase * 0.7) * tilt);
          ctx.drawImage(images[index], -cx, -cy, 1024, 1536);
          ctx.restore();
        }
        celestialParts.forEach((part, index) => {
          if (part.kind === 'cloud' || part.kind === 'glow')
            paintCelestialPart(ctx, images[index + 6], part, t, motion);
        });
        ctx.drawImage(images[2], 0, 0, 1024, 1536);
        ctx.drawImage(
          images[1],
          motion ? 4 * Math.sin(t * 0.57) : 0,
          motion ? 5 * Math.sin(t * 0.74) : 0,
          1024,
          1536,
        );
        celestialParts.forEach((part, index) => {
          if (part.kind === 'chain' || part.kind === 'crescent')
            paintCelestialPart(ctx, images[index + 6], part, t, motion);
        });
        if (motion) {
          ctx.globalCompositeOperation = 'screen';
          for (const [i, p] of [
            [901, 334],
            [888, 491],
            [159, 490],
            [414, 669],
          ].entries()) {
            const strength = Math.pow(Math.max(0, Math.sin(t * 0.65 + i * 1.73)), 9) * 0.38;
            ctx.globalAlpha = strength;
            ctx.strokeStyle = '#ffe9b4';
            ctx.lineWidth = 1.3;
            ctx.beginPath();
            ctx.moveTo(p[0] - 8, p[1]);
            ctx.lineTo(p[0] + 8, p[1]);
            ctx.moveTo(p[0], p[1] - 12);
            ctx.lineTo(p[0], p[1] + 12);
            ctx.stroke();
          }
          const event = (t + 9) % 31;
          if (event < 1.4) {
            const u = event / 1.4;
            ctx.globalAlpha = Math.sin(Math.PI * u) * 0.5;
            const x = 660 - u * 250,
              y = 56 + u * 160;
            const trail = ctx.createLinearGradient(x, y, x + 98, y - 64);
            trail.addColorStop(0, '#e4fbff');
            trail.addColorStop(1, 'rgba(179,220,255,0)');
            ctx.strokeStyle = trail;
            ctx.lineWidth = 1.4;
            ctx.beginPath();
            ctx.moveTo(x, y);
            ctx.lineTo(x + 98, y - 64);
            ctx.stroke();
          }
        }
      }
      ctx.restore();
    },
    dispose,
  };
}

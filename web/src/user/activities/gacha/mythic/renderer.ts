import { artworkActivity, type ArtworkPlayback } from '@shared/animation/artwork-playback';
import { blinkAt, PaintCadence } from '@shared/animation/illustration';
import { cardEyelids } from '../card-eyelids';
import type { MomentExpansion } from '../moment-frame';
import { createMythicCharacter, loadMythicImage } from './character';
import { createMythicEnvironment } from './environment';
import { createMythicMoment } from './moment';
import type { MythicCard } from './layers';

export type MythicOptions = {
  frozenTime?: number;
  paused?: () => boolean;
  playback?: ArtworkPlayback;
  highResolution?: boolean;
  characterOnly?: boolean;
  signal?: AbortSignal;
  momentFrame?: MomentExpansion;
};
export async function renderMythicSSR(
  canvas: HTMLCanvasElement,
  id: MythicCard,
  options: MythicOptions = {},
) {
  const activity = artworkActivity(options.playback);
  if (options.signal?.aborted) return () => {};
  const expansion =
    options.characterOnly &&
    options.momentFrame?.mode === 'expanded-layer' &&
    options.momentFrame.hero
      ? options.momentFrame
      : undefined;
  const controller = new AbortController(),
    abort = () => controller.abort();
  options.signal?.addEventListener('abort', abort, { once: true });
  const characterPromise = createMythicCharacter(id, cardEyelids[id], {
    ...options,
    signal: controller.signal,
  });
  const environmentPromise = options.characterOnly
    ? Promise.resolve(null)
    : createMythicEnvironment(id, { ...options, signal: controller.signal });
  const expandedPromise = expansion
    ? loadMythicImage(
        '/assets/gacha/' +
          (options.highResolution ? 'hires/' : '') +
          'motion/' +
          id +
          '/parts/expanded-base.webp',
        controller.signal,
      )
    : Promise.resolve(null);
  const guarded = <T>(promise: Promise<T>) =>
    promise.catch((error: unknown) => {
      controller.abort();
      throw error;
    });
  const results = await Promise.allSettled([
    guarded(characterPromise),
    guarded(environmentPromise),
    guarded(expandedPromise),
  ]);
  options.signal?.removeEventListener('abort', abort);
  const failure = results.find((r) => r.status === 'rejected');
  if (failure || options.signal?.aborted) {
    controller.abort();
    if (results[0].status === 'fulfilled') results[0].value.dispose();
    if (results[1].status === 'fulfilled') results[1].value?.dispose();
    if (results[2].status === 'fulfilled' && results[2].value) results[2].value.src = '';
    if (failure?.status === 'rejected') throw failure.reason;
    return () => {};
  }
  const character = (results[0] as PromiseFulfilledResult<Awaited<typeof characterPromise>>).value;
  const environment = (results[1] as PromiseFulfilledResult<Awaited<typeof environmentPromise>>)
    .value;
  const expanded = (results[2] as PromiseFulfilledResult<Awaited<typeof expandedPromise>>).value;
  const ctx = canvas.getContext('2d', { alpha: true });
  if (!ctx) {
    character.dispose();
    environment?.dispose();
    if (expanded) expanded.src = '';
    throw new Error('renderer-unavailable');
  }
  let moment: ReturnType<typeof createMythicMoment> | undefined;
  try {
    if (expanded) moment = createMythicMoment();
  } catch (error) {
    character.dispose();
    environment?.dispose();
    if (expanded) expanded.src = '';
    throw error;
  }
  const reduced = activity.systemMotion,
    cadence = new PaintCadence(undefined, !!options.playback, options.playback?.highest);
  let raf = 0,
    previous: number | undefined,
    time = options.frozenTime ?? 0,
    disposed = false;
  const frameSize = expansion?.canvas ?? [1024, 1536];
  const paint = () => {
    if (disposed) return;
    const motion = !reduced?.matches;
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    environment?.paint(ctx, time, canvas.width, canvas.height, motion);
    const region = expansion?.originalRegion;
    const x = region ? (region.x / frameSize[0]) * canvas.width : 0,
      y = region ? (region.y / frameSize[1]) * canvas.height : 0;
    const w = region ? (region.width / frameSize[0]) * canvas.width : canvas.width,
      h = region ? (region.height / frameSize[1]) * canvas.height : canvas.height;
    if (motion) character.paint(time, blinkAt(time));
    const source = motion ? character.surface : character.still;
    if (expanded && moment)
      moment.paint(ctx, expanded, source, x, y, w, h, canvas.width, canvas.height);
    else ctx.drawImage(source, x, y, w, h);
    canvas.dataset.ready = 'true';
    canvas.dataset.scene = id;
    canvas.dataset.motionTime = time.toFixed(3);
  };
  const measure = () => {
    if (disposed) return;
    const width = canvas.getBoundingClientRect().width || canvas.width;
    const cap =
      (options.highResolution ? 2560 : 1024) *
      (expansion ? frameSize[0] / expansion.originalRegion.width : 1);
    const next = Math.max(1, Math.min(cap, Math.round(width * (window.devicePixelRatio || 1))));
    canvas.width = next;
    canvas.height = Math.round((next * frameSize[1]) / frameSize[0]);
    const ratio = expansion ? expansion.originalRegion.width / frameSize[0] : 1;
    character.resize(
      Math.max(1, Math.round(next * ratio)),
      Math.max(1, Math.round(next * ratio * 1.5)),
    );
    paint();
  };
  const tick = (now: number) => {
    raf = 0;
    if (disposed || activity.hidden || reduced?.matches) return;
    if (!options.paused?.()) {
      time += previous === undefined ? 0 : Math.max(0, Math.min((now - previous) / 1000, 0.05));
      if (cadence.due(now)) {
        try {
          paint();
        } catch {
          lost();
          return;
        }
      }
    }
    previous = now;
    raf = requestAnimationFrame(tick);
  };
  const resume = () => {
    cancelAnimationFrame(raf);
    previous = undefined;
    cadence.reset();
    if (!disposed && !activity.hidden && !reduced?.matches && options.frozenTime === undefined)
      raf = requestAnimationFrame(tick);
  };
  const change = () => {
    resume();
    try {
      paint();
    } catch {
      lost();
    }
  };
  const onResize = () => {
    try {
      measure();
    } catch {
      lost();
    }
  };
  const observer = new ResizeObserver(onResize);
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    cancelAnimationFrame(raf);
    observer.disconnect();
    window.removeEventListener('resize', onResize);
    activity.unlisten(resume);
    reduced?.removeEventListener('change', change);
    options.signal?.removeEventListener('abort', dispose);
    character.surface.removeEventListener('webglcontextlost', lost);
    environment?.surfaces.forEach((surface) =>
      surface.removeEventListener('webglcontextlost', lost),
    );
    character.dispose();
    environment?.dispose();
    moment?.dispose();
    if (expanded) expanded.src = '';
    canvas.dataset.ready = 'false';
  };
  function lost(event?: Event) {
    options.playback?.setFailed(true);
    event?.preventDefault();
    dispose();
    canvas.style.visibility = 'hidden';
  }
  try {
    canvas.style.removeProperty('visibility');
    character.surface.addEventListener('webglcontextlost', lost);
    environment?.surfaces.forEach((surface) => surface.addEventListener('webglcontextlost', lost));
    measure();
    observer.observe(canvas);
    window.addEventListener('resize', onResize);
    activity.listen(resume);
    reduced?.addEventListener('change', change);
    options.signal?.addEventListener('abort', dispose, { once: true });
    if (options.signal?.aborted) {
      dispose();
      return () => {};
    }
    resume();
  } catch (error) {
    dispose();
    throw error;
  }
  return dispose;
}

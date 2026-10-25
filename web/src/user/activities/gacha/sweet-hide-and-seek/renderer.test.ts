import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { renderSweetHideAndSeek as createRenderer } from './renderer';
import { createSweetScene } from './scene';

const scene = vi.hoisted(() => ({ paint: vi.fn(), resize: vi.fn(), dispose: vi.fn() }));
vi.mock('./scene', () => ({ createSweetScene: vi.fn(async () => scene) }));

let callback: FrameRequestCallback | undefined;
let reduced = false;
let preference: EventTarget;
let resize: ResizeObserverCallback;
const cleanups: (() => void)[] = [];
const paintedTime = () => Number(scene.paint.mock.lastCall?.[0]);
const renderSweetHideAndSeek: typeof createRenderer = async (...args) => {
  const dispose = await createRenderer(...args);
  cleanups.push(dispose);
  return dispose;
};
beforeEach(() => {
  callback = undefined;
  reduced = false;
  preference = new EventTarget();
  Object.defineProperty(preference, 'matches', { get: () => reduced });
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => preference),
  );
  vi.stubGlobal(
    'requestAnimationFrame',
    vi.fn((next: FrameRequestCallback) => {
      callback = next;
      return 1;
    }),
  );
  vi.stubGlobal(
    'cancelAnimationFrame',
    vi.fn(() => {
      callback = undefined;
    }),
  );
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(next: ResizeObserverCallback) {
        resize = next;
      }
      observe() {}
      disconnect() {}
    },
  );
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false);
});
afterEach(() => {
  cleanups.splice(0).forEach((dispose) => dispose());
  vi.clearAllMocks();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it('caps 144 Hz paints, freezes paused/hidden time and resumes without a jump', async () => {
  let paused = false;
  const canvas = document.createElement('canvas');
  const dispose = await renderSweetHideAndSeek(canvas, 's-chatgpt', { paused: () => paused });
  callback?.(0);
  for (let t = 1; t <= 1000; t += 1000 / 144) callback?.(t);
  expect(scene.paint.mock.calls.length).toBeGreaterThanOrEqual(60);
  expect(scene.paint.mock.calls.length).toBeLessThanOrEqual(63);
  const before = paintedTime();
  paused = true;
  callback?.(2000);
  callback?.(3000);
  expect(paintedTime()).toBe(before);
  paused = false;
  callback?.(3017);
  // The previous unpainted fraction of a 60 Hz frame may also become visible.
  expect(paintedTime() - before).toBeLessThan(0.03);
  const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
  document.dispatchEvent(new Event('visibilitychange'));
  expect(callback).toBeUndefined();
  const visibleTime = paintedTime();
  hidden.mockReturnValue(false);
  document.dispatchEvent(new Event('visibilitychange'));
  callback?.(60000);
  expect(paintedTime()).toBe(visibleTime);
  dispose();
  dispose();
  expect(callback).toBeUndefined();
  expect(scene.dispose).toHaveBeenCalledOnce();
});

it('renders a frozen blink at the requested resolution and keeps it on resize', async () => {
  vi.stubGlobal('devicePixelRatio', 3);
  const canvas = document.createElement('canvas');
  vi.spyOn(canvas, 'getBoundingClientRect').mockReturnValue({ width: 1200 } as DOMRect);
  const dispose = await renderSweetHideAndSeek(canvas, 's-claude', {
    frozenTime: 2.35,
    highResolution: true,
    characterOnly: true,
  });
  expect(canvas.width).toBe(2560);
  expect(canvas.height).toBe(3840);
  expect(scene.paint).toHaveBeenLastCalledWith(2.35, 1, true);
  expect(callback).toBeUndefined();
  resize([], {} as ResizeObserver);
  expect(scene.paint).toHaveBeenLastCalledWith(2.35, 1, true);
  expect(createSweetScene).toHaveBeenCalledWith(
    canvas,
    's-claude',
    expect.objectContaining({ characterOnly: true, highResolution: true }),
    expect.any(Function),
  );
  dispose();
});

it('uses the unwarped open pose for reduced motion, including preference changes', async () => {
  reduced = true;
  const canvas = document.createElement('canvas');
  const dispose = await renderSweetHideAndSeek(canvas, 's-chatgpt');
  expect(scene.paint).toHaveBeenLastCalledWith(0, 0, false);
  expect(callback).toBeUndefined();
  reduced = false;
  preference.dispatchEvent(new Event('change'));
  callback?.(0);
  callback?.(100);
  expect(callback).toBeDefined();
  const time = paintedTime();
  reduced = true;
  preference.dispatchEvent(new Event('change'));
  expect(callback).toBeUndefined();
  expect(scene.paint).toHaveBeenLastCalledWith(time, 0, false);
  dispose();
});

it('disposes immediately on abort and cannot revive after visibility/resize changes', async () => {
  const controller = new AbortController();
  const canvas = document.createElement('canvas');
  const dispose = await renderSweetHideAndSeek(canvas, 's-claude', { signal: controller.signal });
  controller.abort();
  const calls = scene.paint.mock.calls.length;
  document.dispatchEvent(new Event('visibilitychange'));
  resize([], {} as ResizeObserver);
  expect(callback).toBeUndefined();
  expect(scene.paint).toHaveBeenCalledTimes(calls);
  expect(scene.dispose).toHaveBeenCalledOnce();
  dispose();
});

it('disposes an async stale card before it can paint', async () => {
  const controller = new AbortController();
  let resolve: ((value: typeof scene) => void) | undefined;
  vi.mocked(createSweetScene).mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  const result = renderSweetHideAndSeek(document.createElement('canvas'), 's-chatgpt', {
    signal: controller.signal,
  });
  controller.abort();
  resolve?.(scene);
  await expect(result).rejects.toMatchObject({ name: 'AbortError' });
  expect(scene.dispose).toHaveBeenCalledOnce();
  expect(scene.paint).not.toHaveBeenCalled();
});

it('propagates loading failure without marking the outer static illustration ready', async () => {
  vi.mocked(createSweetScene).mockRejectedValueOnce(new Error('image-unavailable'));
  const canvas = document.createElement('canvas');
  await expect(renderSweetHideAndSeek(canvas, 's-claude')).rejects.toThrow('image-unavailable');
  expect(canvas.dataset.ready).toBeUndefined();
  expect(callback).toBeUndefined();
});

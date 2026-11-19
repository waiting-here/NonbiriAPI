import { afterEach, expect, it, vi } from 'vitest';
import { ArtworkPlayback } from '@shared/animation/artwork-playback';
import { renderRomance } from './renderer';
import { createScene } from './compositor.js';

const scene = vi.hoisted(() => ({
  backgroundSize: [3840, 2560],
  paint: vi.fn(),
  dispose: vi.fn(),
}));
vi.mock('./compositor.js', () => ({ createScene: vi.fn(async () => scene) }));
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  scene.paint.mockClear();
  scene.dispose.mockClear();
  vi.mocked(createScene).mockClear();
});

it.each([
  { mode: 'game card', highest: false, min: 60, max: 62 },
  { mode: 'highest appearance', highest: true, min: 144, max: 146 },
])(
  'preserves the $mode cadence and excludes time spent hidden at 144 Hz',
  async ({ highest, min, max }) => {
    let callback: FrameRequestCallback | undefined;
    vi.stubGlobal(
      'requestAnimationFrame',
      vi.fn((next: FrameRequestCallback) => {
        callback = next;
        return 1;
      }),
    );
    const cancel = vi.fn(() => {
      callback = undefined;
    });
    vi.stubGlobal('cancelAnimationFrame', cancel);
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe() {}
        disconnect() {}
      },
    );
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
      clearRect: vi.fn(),
      drawImage: vi.fn(),
    } as unknown as CanvasRenderingContext2D);
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false);
    const canvas = document.createElement('canvas');
    const dispose = await renderRomance(
      canvas,
      'elopement',
      highest
        ? {
            polished: true,
            landscape: true,
            variant: 'appearance',
            highResolution: true,
            playback: new ArtworkPlayback(true),
          }
        : undefined,
    );
    for (let time = 1; time <= 1000; time += 1000 / 144) callback?.(time);
    expect(scene.paint.mock.calls.length).toBeGreaterThanOrEqual(min);
    expect(scene.paint.mock.calls.length).toBeLessThanOrEqual(max);
    const time = Number(canvas.dataset.time);
    hidden.mockReturnValue(true);
    document.dispatchEvent(new Event('visibilitychange'));
    expect(callback).toBeUndefined();
    hidden.mockReturnValue(false);
    document.dispatchEvent(new Event('visibilitychange'));
    callback?.(10000);
    expect(Number(canvas.dataset.time) - time).toBeLessThan(1 / 60);
    dispose();
    expect(callback).toBeUndefined();
    expect(scene.dispose).toHaveBeenCalledOnce();
  },
);

it('renders a frozen export once without scheduling animation', async () => {
  const raf = vi.fn();
  vi.stubGlobal('requestAnimationFrame', raf);
  vi.stubGlobal('cancelAnimationFrame', vi.fn());
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
    clearRect: vi.fn(),
    drawImage: vi.fn(),
  } as unknown as CanvasRenderingContext2D);
  const canvas = document.createElement('canvas');
  canvas.width = 1024;
  canvas.height = 1536;
  const dispose = await renderRomance(canvas, 'heart-thief', {
    exportSize: true,
    frozenTime: 2.5,
    blink: 0,
  });
  expect(raf).not.toHaveBeenCalled();
  expect(scene.paint).toHaveBeenCalledOnce();
  expect(canvas.dataset.time).toBe('2.500');
  dispose();
});

it.each(['elopement', 'heart-thief'] as const)(
  'uses the actual mobile viewport for %s appearance framing at high density',
  async (theme) => {
    vi.stubGlobal('devicePixelRatio', 3);
    vi.stubGlobal('requestAnimationFrame', vi.fn());
    vi.stubGlobal('cancelAnimationFrame', vi.fn());
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe() {}
        disconnect() {}
      },
    );
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
      clearRect: vi.fn(),
      drawImage: vi.fn(),
    } as unknown as CanvasRenderingContext2D);
    const canvas = document.createElement('canvas');
    vi.spyOn(canvas, 'getBoundingClientRect').mockReturnValue({
      width: 390,
      height: 844,
    } as DOMRect);
    const dispose = await renderRomance(canvas, theme, {
      polished: true,
      landscape: true,
      variant: 'appearance',
      highResolution: true,
      dark: true,
      frozenTime: 0,
    });
    expect(createScene).toHaveBeenCalledWith(
      theme,
      true,
      expect.objectContaining({ highResolution: true, dark: true }),
    );
    const frame = scene.paint.mock.calls[0];
    expect(frame[0].width).toBe(1170);
    expect(frame[0].height).toBe(2532);
    expect(frame[7]).toBe(390);
    dispose();
  },
);

it.each([false, true])(
  'limits the expanded output to a character-only moment: %s',
  async (characterOnly) => {
    vi.stubGlobal('requestAnimationFrame', vi.fn());
    vi.stubGlobal('cancelAnimationFrame', vi.fn());
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe() {}
        disconnect() {}
      },
    );
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
      clearRect: vi.fn(),
      drawImage: vi.fn(),
    } as unknown as CanvasRenderingContext2D);
    const canvas = document.createElement('canvas');
    vi.spyOn(canvas, 'getBoundingClientRect').mockReturnValue({
      width: 600,
      height: 600,
    } as DOMRect);
    const frame = {
      canvas: [1536, 1536] as [number, number],
      originalRegion: { x: 256, y: 0, width: 1024, height: 1536 },
    };
    const dispose = await renderRomance(canvas, 'heart-thief', {
      characterOnly,
      momentFrame: frame,
      frozenTime: 0,
    });
    expect(canvas.width).toBe(600);
    expect(canvas.height).toBe(characterOnly ? 600 : 900);
    expect(scene.paint.mock.calls[0][7]).toBe(characterOnly ? 400 : 600);
    dispose();
  },
);

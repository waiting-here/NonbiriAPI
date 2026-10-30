import { act, fireEvent, render, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { ArtworkPolicyProvider } from '@shared/artwork/policy';
import { MomentBackdrop } from './MomentBackdrop';
import { renderSSR } from '../activities/gacha/ssr-renderer';

vi.mock('../activities/gacha/ssr-renderer', () => ({ renderSSR: vi.fn() }));
vi.mock('../activities/gacha/moment-registration', () => ({
  useMomentExpansion: (id?: string) => ({
    ready: true,
    scene:
      id === 's-claude'
        ? {
            path: 'moment/s-claude/background.webp',
            canvas: [2731, 1536],
            originalRegion: { x: 1707, y: 0, width: 1024, height: 1536 },
            highResolutionWidth: 3840,
          }
        : undefined,
    expansion:
      id === 'q-glm'
        ? {
            mode: 'native-camera',
            canvas: [1536, 1536],
            originalRegion: { x: 256, y: 0, width: 1024, height: 1536 },
          }
        : id === 'c-gemini'
          ? {
              mode: 'expanded-layer',
              canvas: [1536, 1536],
              originalRegion: { x: 256, y: 0, width: 1024, height: 1536 },
              hero: 'moment/c-gemini/hero.webp',
              highResolutionWidth: 3840,
            }
          : undefined,
  }),
}));
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.clearAllMocks();
});
function preference() {
  const query = new EventTarget() as MediaQueryList;
  Object.defineProperty(query, 'matches', { configurable: true, value: false });
  vi.stubGlobal('matchMedia', () => query);
  return (value: boolean) => {
    Object.defineProperty(query, 'matches', { configurable: true, value });
    query.dispatchEvent(new Event('change'));
  };
}
it('uses a cover by default and independent static scene and character for ordinary cards', () => {
  const view = render(<MomentBackdrop />);
  expect(view.container.querySelector('img')).toHaveAttribute(
    'src',
    '/assets/gacha/covers/permanent.webp',
  );
  expect(view.container.querySelector('canvas')).toBeNull();
  view.rerender(<MomentBackdrop moment="card:a-chatgpt" />);
  expect(view.container.querySelector('.nb-moment-backdrop__scene')).toHaveAttribute(
    'src',
    '/assets/gacha/backgrounds/a.webp',
  );
  expect(view.container.querySelector('.nb-moment-character img')).toHaveAttribute(
    'src',
    '/assets/gacha/layers/a-chatgpt.webp',
  );
  expect(view.container.querySelector('canvas')).toBeNull();
  view.rerender(<MomentBackdrop moment="illustration:a" />);
  expect(view.container.querySelector('img')).toHaveAttribute('src', '/assets/gacha/groups/a.webp');
  expect(view.container.querySelector('.nb-moment-character')).toBeNull();
  expect(renderSSR).not.toHaveBeenCalled();
});
it.each(['g-chatgpt', 'l-chatgpt'])(
  'animates only %s character and releases it for reduced motion and unmount',
  async (id) => {
    const change = preference();
    const stop = vi.fn();
    vi.mocked(renderSSR).mockResolvedValue(stop);
    const view = render(<MomentBackdrop moment={'card:' + id} />);
    await waitFor(() => expect(renderSSR).toHaveBeenCalledOnce());
    const options = vi.mocked(renderSSR).mock.calls[0][5];
    expect(options?.characterOnly).toBe(true);
    expect(view.container.querySelector('canvas')).toHaveAttribute('data-ready', 'true');
    await act(async () => change(true));
    expect(options?.signal?.aborted).toBe(true);
    expect(stop).toHaveBeenCalledOnce();
    expect(view.container.querySelector('canvas')).toHaveAttribute('data-ready', 'false');
    await act(async () => change(false));
    await waitFor(() => expect(renderSSR).toHaveBeenCalledTimes(2));
    view.unmount();
    expect(stop).toHaveBeenCalledTimes(2);
  },
);
it('releases a late character renderer after selection changes', async () => {
  preference();
  let resolve!: (stop: () => void) => void;
  vi.mocked(renderSSR).mockReturnValue(
    new Promise((done) => {
      resolve = done;
    }),
  );
  const view = render(<MomentBackdrop moment="card:m-deepseek" />);
  await waitFor(() => expect(renderSSR).toHaveBeenCalledOnce());
  view.rerender(<MomentBackdrop moment="cover:permanent" />);
  const stop = vi.fn();
  await act(async () => resolve(stop));
  expect(stop).toHaveBeenCalledOnce();
  expect(view.container.querySelector('canvas')).toBeNull();
});

it('updates the artwork hue after loading and clears it on selection changes and unmount', () => {
  const context = {
    drawImage: vi.fn(),
    getImageData: vi.fn(() => ({ data: new Uint8ClampedArray([255, 128, 0, 255]) })),
  };
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(
    context as unknown as CanvasRenderingContext2D,
  );
  const view = render(<MomentBackdrop moment="cover:permanent" />);
  const scene = view.container.querySelector('img')!;
  Object.defineProperty(scene, 'naturalWidth', { value: 100, configurable: true });
  Object.defineProperty(scene, 'complete', { value: false, configurable: true });
  fireEvent.load(scene);
  expect(document.documentElement.style.getPropertyValue('--nb-moment-hue')).toBe('30deg');
  view.rerender(<MomentBackdrop moment="card:a-chatgpt" />);
  expect(document.documentElement.style.getPropertyValue('--nb-moment-hue')).toBe('');
  context.getImageData.mockReturnValue({ data: new Uint8ClampedArray([0, 128, 255, 255]) });
  fireEvent.load(scene);
  expect(document.documentElement.style.getPropertyValue('--nb-moment-hue')).toBe('210deg');
  view.unmount();
  expect(document.documentElement.style.getPropertyValue('--nb-moment-hue')).toBe('');
});

it('expands only a registered moment canvas while preserving the original static frame and cleanup', async () => {
  preference();
  vi.mocked(renderSSR).mockResolvedValue(vi.fn());
  const view = render(<MomentBackdrop moment="card:q-glm" />);
  await waitFor(() => expect(renderSSR).toHaveBeenCalledOnce());
  expect(vi.mocked(renderSSR).mock.calls[0][5]?.momentFrame?.originalRegion).toEqual({
    x: 256,
    y: 0,
    width: 1024,
    height: 1536,
  });
  const canvas = view.container.querySelector('canvas')!;
  expect(canvas.style.left).toBe('-25%');
  expect(canvas.style.width).toBe('150%');
  expect(
    view.container.querySelector('.nb-moment-character img')!.getAttribute('style'),
  ).toBeNull();
  expect(document.documentElement.style.getPropertyValue('--nb-moment-extra-reserve')).toContain(
    '0.5',
  );
  expect(document.documentElement.style.getPropertyValue('--nb-moment-right-reserve')).toContain(
    '0.25',
  );
  view.rerender(<MomentBackdrop moment="card:a-chatgpt" />);
  expect(view.container.querySelector('.nb-moment-character')).toHaveAttribute(
    'data-expanded',
    'false',
  );
  expect(document.documentElement.style.getPropertyValue('--nb-moment-extra-reserve')).toBe('');
  expect(document.documentElement.style.getPropertyValue('--nb-moment-right-reserve')).toBe('');
  expect(
    view.container.querySelector('.nb-moment-character img')!.getAttribute('style'),
  ).toBeNull();
});

it('anchors an expanded scene to the original character instead of covering the viewport independently', async () => {
  preference();
  vi.mocked(renderSSR).mockResolvedValue(vi.fn());
  const view = render(
    <ArtworkPolicyProvider disabled={false}>
      <MomentBackdrop moment="card:s-claude" />
    </ArtworkPolicyProvider>,
  );
  await waitFor(() => expect(renderSSR).toHaveBeenCalledOnce());
  expect(view.container.querySelector('.nb-moment-backdrop__scene')).toHaveAttribute(
    'src',
    '/assets/gacha/backgrounds/s.webp',
  );
  expect(view.container.querySelector('.nb-moment-backdrop')).toHaveAttribute(
    'data-registered',
    'false',
  );
  const scene = view.container.querySelector<HTMLImageElement>('.nb-moment-character__scene')!;
  expect(scene.src).toContain('/assets/gacha/moment/s-claude/background.webp');
  expect(scene.style.left).toBe('-166.69921875%');
  expect(scene.style.width).toBe('266.69921875%');
  expect(scene.style.height).toBe('100%');
  expect(view.container.querySelector('.nb-moment-character__plate')).toBeNull();
  expect(
    view.container.querySelector('.nb-moment-character > img:not(.nb-moment-character__scene)'),
  ).toHaveAttribute('src', '/assets/gacha/motion/s-claude/hero.webp');
  expect(vi.mocked(renderSSR).mock.calls[0][5]?.momentFrame).toBeUndefined();
  expect(document.documentElement.style.getPropertyValue('--nb-moment-extra-reserve')).toBe('');
  expect(
    document.documentElement.style.getPropertyValue('--nb-moment-scene-frame-width'),
  ).toContain('dvh');
  view.rerender(<MomentBackdrop moment="cover:permanent" />);
  expect(document.documentElement.style.getPropertyValue('--nb-moment-scene-frame-width')).toBe('');
});

it('uses the adopted static moment source and expanded responsive widths without animating an R card', () => {
  const view = render(<MomentBackdrop moment="card:c-gemini" />);
  const hero = view.container.querySelector<HTMLImageElement>('.nb-moment-character img')!;
  expect(hero.src).toContain('/assets/gacha/moment/c-gemini/hero.webp');
  expect(hero.srcset).toBe(
    '/assets/gacha/moment/c-gemini/hero.webp 1536w, /assets/gacha/hires/moment/c-gemini/hero.webp 3840w',
  );
  expect(hero.sizes).toContain('150vw');
  expect(hero.sizes).toContain('100vh');
  expect(hero.style.width).toBe('150%');
  expect(hero.style.left).toBe('-25%');
  expect(view.container.querySelector('canvas')).toBeNull();
  expect(renderSSR).not.toHaveBeenCalled();
});

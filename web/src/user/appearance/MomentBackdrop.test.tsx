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
            path: 'moment/s-claude/viewport-background.webp',
            canvas: [3072, 1024],
            originalRegion: { x: 2555, y: 175, width: 512, height: 768 },
            highResolutionWidth: 6144,
            viewport: { shadow: 'motion/s-claude/moment-shadow.webp' },
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

it('rechecks resolution after the canvas layout changes and disconnects its observer', async () => {
  preference();
  let resized: ResizeObserverCallback = () => {};
  const disconnect = vi.fn();
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(callback: ResizeObserverCallback) {
        resized = callback;
      }
      observe() {}
      disconnect = disconnect;
    },
  );
  let width = 700;
  vi.spyOn(HTMLCanvasElement.prototype, 'getBoundingClientRect').mockImplementation(
    () => ({ width, height: width * 1.5 }) as DOMRect,
  );
  vi.mocked(renderSSR).mockResolvedValue(vi.fn());
  const view = render(<MomentBackdrop moment="card:g-chatgpt" />);
  await waitFor(() => expect(renderSSR).toHaveBeenCalledOnce());
  expect(vi.mocked(renderSSR).mock.calls[0][4]).toBe(false);
  width = 1200;
  await act(async () => resized([], {} as ResizeObserver));
  await waitFor(() => expect(renderSSR).toHaveBeenCalledTimes(2));
  expect(vi.mocked(renderSSR).mock.calls[1][4]).toBe(true);
  view.unmount();
  expect(disconnect).toHaveBeenCalledOnce();
});

it('uses the registered palette on the first frame and releases it when leaving', () => {
  const hue = () => document.documentElement.style.getPropertyValue('--nb-moment-hue');
  const view = render(<MomentBackdrop moment="illustration:l" />);
  expect(hue()).toBe('231deg');
  view.rerender(<MomentBackdrop moment="illustration:o" />);
  expect(hue()).toBe('38deg');
  view.unmount();
  expect(hue()).toBe('');
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
  expect(document.documentElement.style.getPropertyValue('--nb-moment-width-ratio')).toBe('1.5');
  expect(document.documentElement.style.getPropertyValue('--nb-moment-height-ratio')).toBe('1');
  expect(document.documentElement.style.getPropertyValue('--nb-moment-right-reserve')).toContain(
    '0.25',
  );
  view.rerender(<MomentBackdrop moment="card:a-chatgpt" />);
  expect(view.container.querySelector('.nb-moment-character')).toHaveAttribute(
    'data-expanded',
    'false',
  );
  expect(document.documentElement.style.getPropertyValue('--nb-moment-extra-reserve')).toBe('');
  expect(document.documentElement.style.getPropertyValue('--nb-moment-width-ratio')).toBe('');
  expect(document.documentElement.style.getPropertyValue('--nb-moment-height-ratio')).toBe('');
  expect(document.documentElement.style.getPropertyValue('--nb-moment-right-reserve')).toBe('');
  expect(
    view.container.querySelector('.nb-moment-character img')!.getAttribute('style'),
  ).toBeNull();
});

it('uses one full-screen scene with a separately registered transparent character', async () => {
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
    '/assets/gacha/moment/s-claude/viewport-background.webp',
  );
  expect(view.container.querySelector('.nb-moment-backdrop')).toHaveAttribute(
    'data-registered',
    'true',
  );
  expect(view.container.querySelector('.nb-moment-backdrop')).toHaveAttribute(
    'data-viewport',
    'true',
  );
  expect(view.container.querySelector('.nb-moment-character__scene')).toBeNull();
  expect(view.container.querySelector('.nb-moment-character__plate')).toBeNull();
  expect(view.container.querySelector('.nb-moment-character__hero')).toHaveAttribute(
    'src',
    '/assets/gacha/motion/s-claude/hero.webp',
  );
  expect(vi.mocked(renderSSR).mock.calls[0][5]?.momentFrame).toBeUndefined();
  const character = view.container.querySelector<HTMLElement>('.nb-moment-character')!;
  const hero = view.container.querySelector<HTMLImageElement>('.nb-moment-character__hero')!;
  const shadow = view.container.querySelector<HTMLImageElement>('.nb-moment-character__shadow')!;
  const initialWidth = character.style.width;
  expect(hero.sizes).toBe(initialWidth);
  expect(shadow.sizes).toBe(initialWidth);
  vi.stubGlobal('innerWidth', 390);
  vi.stubGlobal('innerHeight', 844);
  fireEvent.resize(window);
  expect(character.style.width).not.toBe(initialWidth);
  expect(hero.sizes).toBe(character.style.width);
  expect(shadow.sizes).toBe(character.style.width);
  expect(document.documentElement.style.getPropertyValue('--nb-moment-extra-reserve')).toBe('0px');
  expect(document.documentElement.style.getPropertyValue('--nb-moment-height-ratio')).toBe('');
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

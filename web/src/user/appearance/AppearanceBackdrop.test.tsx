import { act, render, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { AppearanceBackdrop } from './AppearanceBackdrop';
import { renderWallpaper } from './wallpaper-motion';
import { renderSpecial } from '../activities/gacha/special-renderer';

vi.mock('./wallpaper-motion', () => ({ renderWallpaper: vi.fn() }));
vi.mock('../activities/gacha/special-renderer', () => ({ renderSpecial: vi.fn() }));
afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

function motionPreference(initial = false) {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  const query = new EventTarget() as MediaQueryList;
  Object.defineProperty(query, 'matches', { configurable: true, value: initial });
  vi.stubGlobal('matchMedia', () => query);
  return (reduced: boolean) => {
    Object.defineProperty(query, 'matches', { configurable: true, value: reduced });
    query.dispatchEvent(new Event('change'));
  };
}

it('releases the previous theme and switches to the static painting for reduced motion', async () => {
  const preference = motionPreference();
  const stop = vi.fn();
  vi.mocked(renderWallpaper).mockResolvedValue(stop);
  const view = render(<AppearanceBackdrop selected="celestial" />);
  await waitFor(() => expect(renderWallpaper).toHaveBeenCalledTimes(1));
  const first = vi.mocked(renderWallpaper).mock.calls[0];
  expect(first.slice(1, 3)).toEqual(['celestial', 'light']);
  await act(async () => {
    document.documentElement.dataset.theme = 'dark';
  });
  await waitFor(() => expect(renderWallpaper).toHaveBeenCalledTimes(2));
  expect(first[3].aborted).toBe(true);
  expect(stop).toHaveBeenCalled();
  expect(vi.mocked(renderWallpaper).mock.calls[1].slice(1, 3)).toEqual(['celestial', 'dark']);
  await act(async () => preference(true));
  expect(vi.mocked(renderWallpaper).mock.calls[1][3].aborted).toBe(true);
  expect(view.container.querySelector('canvas')).toHaveAttribute('data-ready', 'false');
  await act(async () => preference(false));
  await waitFor(() => expect(renderWallpaper).toHaveBeenCalledTimes(3));
  view.unmount();
  expect(vi.mocked(renderWallpaper).mock.calls[2][3].aborted).toBe(true);
});

it('releases a late renderer after leaving the account appearance', async () => {
  motionPreference();
  let resolve!: (stop: () => void) => void;
  vi.mocked(renderWallpaper).mockReturnValue(
    new Promise((done) => {
      resolve = done;
    }),
  );
  const view = render(<AppearanceBackdrop selected="wandering" />);
  await waitFor(() => expect(renderWallpaper).toHaveBeenCalledTimes(1));
  view.rerender(<AppearanceBackdrop selected="default" />);
  const stop = vi.fn();
  await act(async () => resolve(stop));
  expect(stop).toHaveBeenCalledOnce();
  expect(view.container.querySelector('canvas')).toBeNull();
});

it('replaces texture quality when crossing the display threshold without restarting on every resize', async () => {
  motionPreference();
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('innerHeight', 900);
  vi.stubGlobal('devicePixelRatio', 1);
  const stop = vi.fn();
  vi.mocked(renderWallpaper).mockResolvedValue(stop);
  const view = render(<AppearanceBackdrop selected="celestial" />);
  await waitFor(() => expect(renderWallpaper).toHaveBeenCalledTimes(1));
  expect(vi.mocked(renderWallpaper).mock.calls[0][4]).toBe('standard');
  await act(async () => {
    vi.stubGlobal('innerWidth', 3840);
    vi.stubGlobal('innerHeight', 2160);
    window.dispatchEvent(new Event('resize'));
  });
  await waitFor(() => expect(renderWallpaper).toHaveBeenCalledTimes(2));
  expect(vi.mocked(renderWallpaper).mock.calls[0][3].aborted).toBe(true);
  expect(stop).toHaveBeenCalledOnce();
  expect(vi.mocked(renderWallpaper).mock.calls[1][4]).toBe('high');
  await act(async () => {
    vi.stubGlobal('innerWidth', 2560);
    window.dispatchEvent(new Event('resize'));
  });
  expect(renderWallpaper).toHaveBeenCalledTimes(2);
  view.unmount();
});

it.each(['mirrorsea', 'amber', 'elopement', 'heart-thief', 'tomorrow'])(
  'uses matching %s scenery and cancels late renders',
  async (selected) => {
    const preference = motionPreference();
    document.documentElement.dataset.theme = 'light';
    let finish!: (stop: () => void) => void;
    vi.mocked(renderSpecial).mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    const view = render(<AppearanceBackdrop selected={selected} />);
    await waitFor(() => expect(renderSpecial).toHaveBeenCalledOnce());
    const call = vi.mocked(renderSpecial).mock.calls[0];
    expect(call[1]).toBe(selected);
    expect(call[2]).toMatchObject({ landscape: true, dark: false });
    expect(view.container.querySelector('img')).toHaveAttribute(
      'src',
      selected === 'tomorrow'
        ? '/assets/gacha/motion/r-chatgpt/background-light.webp'
        : '/assets/gacha/appearances/' + selected + '-light.webp',
    );
    await act(async () => preference(true));
    expect(call[2]?.signal?.aborted).toBe(true);
    const stop = vi.fn();
    await act(async () => finish(stop));
    expect(stop).toHaveBeenCalledOnce();
    expect(view.container.querySelector('canvas')).toHaveAttribute('data-ready', 'false');
  },
);

it.each([
  ['elopement', 'p-claude'],
  ['heart-thief', 'q-glm'],
] as const)(
  'keeps the independently placed %s character when animation loading fails',
  async (selected, id) => {
    motionPreference();
    vi.stubGlobal('innerWidth', 390);
    vi.stubGlobal('innerHeight', 844);
    document.documentElement.dataset.theme = 'dark';
    vi.mocked(renderSpecial).mockRejectedValue(new Error('image-unavailable'));
    const view = render(<AppearanceBackdrop selected={selected} />);
    await waitFor(() => expect(renderSpecial).toHaveBeenCalledOnce());
    expect(vi.mocked(renderSpecial).mock.calls[0][2]).toMatchObject({
      variant: 'appearance',
      dark: true,
      ambient: true,
    });
    const character = view.container.querySelector(
      '.nb-romance-still-character',
    ) as HTMLImageElement;
    expect(character).toHaveAttribute(
      'src',
      `/assets/gacha/motion/${id}/appearance-hero-dark.webp`,
    );
    expect(parseFloat(character.style.top)).toBeGreaterThan(136);
    expect(view.container.querySelector('canvas')).toHaveAttribute('data-ready', 'false');
    view.unmount();
  },
);

it('keeps the mirror reflection, touch light and contact layer when animation loading fails', async () => {
  motionPreference();
  vi.stubGlobal('innerWidth', 390);
  vi.stubGlobal('innerHeight', 844);
  document.documentElement.dataset.theme = 'dark';
  vi.mocked(renderSpecial).mockRejectedValue(new Error('image-unavailable'));
  const view = render(<AppearanceBackdrop selected="mirrorsea" />);
  await waitFor(() => expect(renderSpecial).toHaveBeenCalledOnce());
  expect(vi.mocked(renderSpecial).mock.calls[0][2]).toMatchObject({
    variant: 'appearance',
    ambient: true,
  });
  expect(view.container.querySelector('.nb-mirror-still-character')).toHaveAttribute(
    'src',
    '/assets/gacha/motion/n-gemini/appearance-hero-dark.webp',
  );
  expect(view.container.querySelector('.nb-mirror-still-reflection img')).toHaveAttribute(
    'src',
    '/assets/gacha/motion/n-gemini/appearance-reflection.webp',
  );
  expect(view.container.querySelector('.nb-mirror-still-light')).toHaveAttribute(
    'src',
    '/assets/gacha/motion/n-gemini/appearance-light.webp',
  );
  expect(view.container.querySelector('canvas')).toHaveAttribute('data-ready', 'false');
  view.unmount();
});

it('retains the responsive tomorrow layers when animation loading fails', async () => {
  motionPreference();
  vi.stubGlobal('innerWidth', 390);
  vi.stubGlobal('innerHeight', 844);
  document.documentElement.dataset.theme = 'dark';
  vi.mocked(renderSpecial).mockRejectedValue(new Error('image-unavailable'));
  const view = render(<AppearanceBackdrop selected="tomorrow" />);
  await waitFor(() => expect(renderSpecial).toHaveBeenCalledOnce());
  const images = [...view.container.querySelectorAll('.nb-tomorrow-still img')];
  expect(images.map((image) => image.getAttribute('src'))).toEqual([
    '/assets/gacha/motion/r-chatgpt/background-dark.webp',
    '/assets/gacha/motion/r-chatgpt/cloud.webp',
    '/assets/gacha/motion/r-chatgpt/hero-dark.webp',
  ]);
  expect(view.container.querySelector('canvas')).toHaveAttribute('data-ready', 'false');
  view.unmount();
});

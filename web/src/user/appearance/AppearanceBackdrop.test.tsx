import { act, render, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { AppearanceBackdrop } from './AppearanceBackdrop';
import { renderWallpaper } from './wallpaper-motion';

vi.mock('./wallpaper-motion', () => ({ renderWallpaper: vi.fn() }));
afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

function motionPreference(initial = false) {
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

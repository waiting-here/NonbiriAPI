import { act, render, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { MomentBackdrop } from './MomentBackdrop';
import { renderSSR } from '../activities/gacha/ssr-renderer';

vi.mock('../activities/gacha/ssr-renderer', () => ({ renderSSR: vi.fn() }));
afterEach(() => {
  vi.unstubAllGlobals();
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

import { act, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { taskFixture } from '@shared/picturebook/fixtures';
import { taskStatuses } from '@shared/picturebook/publicTypes';
import { PictureBookWait } from './PictureBookWait';
import { TaskDetail } from './TaskHistory';
import { pictureBookKeys } from './queries';

afterEach(() => vi.useRealTimers());

function browserState(initialReduced = false) {
  let reduced = initialReduced;
  const mediaEvents = new EventTarget();
  const media = {
    get matches() {
      return reduced;
    },
    addEventListener: (name: string, listener: EventListener) =>
      mediaEvents.addEventListener(name, listener),
    removeEventListener: (name: string, listener: EventListener) =>
      mediaEvents.removeEventListener(name, listener),
  };
  vi.stubGlobal('matchMedia', () => media);
  const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
  return {
    reduce(value: boolean) {
      act(() => {
        reduced = value;
        mediaEvents.dispatchEvent(new Event('change'));
      });
    },
    visible(value: boolean) {
      act(() => {
        visibility.mockReturnValue(value ? 'visible' : 'hidden');
        document.dispatchEvent(new Event('visibilitychange'));
      });
    },
  };
}

function images(container: HTMLElement) {
  return [...container.querySelectorAll<HTMLImageElement>('.picturebook-wait__frames img')];
}
function shown(container: HTMLElement) {
  return container.querySelector<HTMLImageElement>('.picturebook-wait__frame--visible')?.src;
}
function loaded(container: HTMLElement) {
  for (const image of images(container)) fireEvent.load(image);
}
function advance(ms: number) {
  act(() => vi.advanceTimersByTime(ms));
}

describe('picture book waiting illustration', () => {
  it('waits for all frames, holds each for two seconds, loops in eight seconds, and releases its timer', async () => {
    browserState();
    const network = vi.fn();
    vi.stubGlobal('fetch', network);
    const view = await renderWithProviders(<PictureBookWait status="running" />, {
      station: 'user',
    });
    vi.useFakeTimers();
    const frames = images(view.container);
    expect(frames).toHaveLength(4);
    expect(screen.queryByRole('img')).toBeNull();
    expect(frames.every((image) => image.alt === '')).toBe(true);
    expect(view.container.querySelector('.picturebook-wait__frames')).toHaveAttribute(
      'aria-hidden',
      'true',
    );
    for (const frame of frames.slice(0, 3)) fireEvent.load(frame);
    advance(6000);
    expect(shown(view.container)).toBe(frames[0].src);
    fireEvent.load(frames[3]);
    advance(1999);
    expect(shown(view.container)).toBe(frames[0].src);
    advance(1);
    expect(shown(view.container)).toBe(frames[1].src);
    advance(2000);
    expect(shown(view.container)).toBe(frames[2].src);
    advance(2000);
    expect(shown(view.container)).toBe(frames[3].src);
    advance(2000);
    expect(shown(view.container)).toBe(frames[0].src);
    expect(network).not.toHaveBeenCalled();
    view.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('keeps queueing static, pauses manually and in hidden pages, and follows motion preference changes', async () => {
    const browser = browserState();
    const view = await renderWithProviders(<PictureBookWait status="queued" />, {
      station: 'user',
    });
    vi.useFakeTimers();
    loaded(view.container);
    const frames = images(view.container);
    advance(8000);
    expect(shown(view.container)).toBe(frames[0].src);
    expect(screen.queryByRole('button')).toBeNull();
    view.rerender(<PictureBookWait status="dispatching" />);
    advance(2000);
    expect(shown(view.container)).toBe(frames[1].src);
    fireEvent.click(screen.getByRole('button', { name: 'Pause waiting animation' }));
    expect(screen.getByRole('button', { name: 'Resume waiting animation' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    advance(8000);
    expect(shown(view.container)).toBe(frames[1].src);
    browser.visible(false);
    fireEvent.click(screen.getByRole('button', { name: 'Resume waiting animation' }));
    advance(8000);
    expect(shown(view.container)).toBe(frames[1].src);
    browser.visible(true);
    advance(2000);
    expect(shown(view.container)).toBe(frames[2].src);
    browser.reduce(true);
    expect(shown(view.container)).toBe(frames[0].src);
    expect(screen.queryByRole('button')).toBeNull();
    advance(8000);
    expect(shown(view.container)).toBe(frames[0].src);
    browser.reduce(false);
    advance(2000);
    expect(shown(view.container)).toBe(frames[3].src);
    view.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('starts static for reduced motion and stops on every terminal task state', async () => {
    const browser = browserState(true);
    const view = await renderWithProviders(<PictureBookWait status="running" />, {
      station: 'user',
    });
    vi.useFakeTimers();
    loaded(view.container);
    const first = images(view.container)[0].src;
    advance(8000);
    expect(shown(view.container)).toBe(first);
    expect(screen.queryByRole('button')).toBeNull();
    browser.reduce(false);
    for (const status of taskStatuses.filter(
      (status) => !['queued', 'dispatching', 'running'].includes(status),
    )) {
      view.rerender(<PictureBookWait status="running" />);
      advance(2000);
      expect(vi.getTimerCount()).toBe(1);
      view.rerender(<PictureBookWait status={status} />);
      expect(images(view.container)).toHaveLength(0);
      expect(vi.getTimerCount()).toBe(0);
    }
    view.unmount();
  });

  it('offers a focusable keyboard pause control with localized labels', async () => {
    browserState();
    const view = await renderWithProviders(<PictureBookWait status="running" />, {
      station: 'user',
      locale: 'zh',
    });
    loaded(view.container);
    await view.user.tab();
    expect(screen.getByRole('button', { name: '暂停等待动画' })).toHaveFocus();
    await view.user.keyboard(' ');
    expect(screen.getByRole('button', { name: '恢复等待动画' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    await view.user.keyboard(' ');
    expect(screen.getByRole('button', { name: '暂停等待动画' })).toHaveAttribute(
      'aria-pressed',
      'false',
    );
  });

  it('falls back to the first frame when a later frame fails and removes a failed first frame', async () => {
    browserState();
    const view = await renderWithProviders(<PictureBookWait status="running" />, {
      station: 'user',
    });
    vi.useFakeTimers();
    loaded(view.container);
    const frames = images(view.container);
    advance(2000);
    expect(shown(view.container)).toBe(frames[1].src);
    fireEvent.error(frames[2]);
    expect(shown(view.container)).toBe(frames[0].src);
    advance(8000);
    expect(shown(view.container)).toBe(frames[0].src);
    expect(screen.queryByRole('button')).toBeNull();
    fireEvent.error(frames[0]);
    expect(images(view.container)).toHaveLength(0);
    view.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('retains the server task status and retry controls while stopping cached running animation on a query error', async () => {
    browserState();
    const view = await renderWithProviders(<div />, { station: 'user' });
    const task = taskFixture({ status: 'running', queue_position: null });
    const key = pictureBookKeys.task('1', task.id);
    view.queryClient.setQueryDefaults(key, { staleTime: Infinity });
    view.queryClient.setQueryData(key, task);
    view.rerender(<TaskDetail id={task.id} account="1" />);
    expect(await screen.findByRole('status')).toHaveTextContent('Generating');
    loaded(view.container);
    expect(screen.getByRole('button', { name: 'Pause waiting animation' })).toBeVisible();
    act(() => {
      view.queryClient
        .getQueryCache()
        .find({ queryKey: key, exact: true })
        ?.setState({
          status: 'error',
          error: new Error('Synthetic task query failed'),
          fetchStatus: 'idle',
        });
    });
    await screen.findByRole('button', { name: /retry/i });
    expect(screen.getByRole('status')).toHaveTextContent('Generating');
    expect(images(view.container)).toHaveLength(0);
    expect(screen.queryByRole('button', { name: 'Pause waiting animation' })).toBeNull();
  });
});

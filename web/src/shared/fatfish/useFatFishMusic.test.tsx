import { act, fireEvent, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { FATFISH_MUSIC_KEY, FATFISH_MUSIC_URL, useFatFishMusic } from './useFatFishMusic';

let instances: FakeAudio[];
let playResult: () => Promise<void>;

class FakeAudio {
  readonly src: string;
  loop = false;
  preload = '';
  paused = true;
  play = vi.fn(() => {
    this.paused = false;
    return playResult();
  });
  pause = vi.fn(() => {
    this.paused = true;
  });
  setAttribute = vi.fn();
  removeAttribute = vi.fn();
  load = vi.fn();
  remove = vi.fn();

  constructor(src: string) {
    this.src = src;
    instances.push(this);
  }
}

beforeEach(() => {
  instances = [];
  playResult = () => Promise.resolve();
  vi.stubGlobal('Audio', FakeAudio);
});

describe('Fat Fish music', () => {
  it('starts muted, then loops one local track after the player explicitly enables it', async () => {
    const session = {};
    const view = renderHook(() => useFatFishMusic(session, true));
    expect(view.result.current.enabled).toBe(false);
    fireEvent.pointerDown(window);
    expect(instances).toHaveLength(0);

    act(() => view.result.current.toggle());
    expect(view.result.current.enabled).toBe(true);
    expect(localStorage.getItem(FATFISH_MUSIC_KEY)).toBe('true');
    expect(instances).toHaveLength(1);
    expect(instances[0].src).toBe(FATFISH_MUSIC_URL);
    expect(instances[0].loop).toBe(true);
    expect(instances[0].play).toHaveBeenCalledTimes(1);
    fireEvent.pointerDown(window);
    expect(instances[0].play).toHaveBeenCalledTimes(1);

    act(() => view.result.current.toggle());
    expect(view.result.current.enabled).toBe(false);
    expect(localStorage.getItem(FATFISH_MUSIC_KEY)).toBe('false');
    expect(instances[0].pause).toHaveBeenCalled();
    fireEvent.keyDown(window, { key: 'Enter' });
    expect(instances[0].play).toHaveBeenCalledTimes(1);
  });

  it('remembers an enabled choice but waits for a fresh gesture after mounting', () => {
    localStorage.setItem(FATFISH_MUSIC_KEY, 'true');
    const session = {};
    const view = renderHook(() => useFatFishMusic(session, true));
    expect(view.result.current.enabled).toBe(true);
    expect(instances).toHaveLength(0);
    fireEvent.keyDown(window, { key: 'ArrowRight' });
    expect(instances).toHaveLength(1);
    expect(instances[0].play).toHaveBeenCalledTimes(1);
  });

  it('pauses on hidden or page exit, resumes once, and releases audio on session change and unmount', () => {
    const first = {};
    const view = renderHook(({ session }) => useFatFishMusic(session, true), {
      initialProps: { session: first },
    });
    act(() => view.result.current.toggle());
    const original = instances[0];
    let visibility: DocumentVisibilityState = 'hidden';
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility);
    fireEvent(document, new Event('visibilitychange'));
    expect(original.pause).toHaveBeenCalledTimes(1);
    visibility = 'visible';
    fireEvent(document, new Event('visibilitychange'));
    expect(original.play).toHaveBeenCalledTimes(2);
    fireEvent(window, new Event('pagehide'));
    expect(original.pause).toHaveBeenCalledTimes(2);
    fireEvent(window, new Event('pageshow'));
    expect(original.play).toHaveBeenCalledTimes(3);
    expect(instances).toHaveLength(1);

    view.rerender({ session: {} });
    expect(original.pause).toHaveBeenCalledTimes(3);
    expect(original.removeAttribute).toHaveBeenCalledWith('src');
    expect(original.load).toHaveBeenCalledTimes(1);
    expect(original.remove).toHaveBeenCalledTimes(1);
    fireEvent.pointerDown(window);
    expect(instances).toHaveLength(2);
    expect(instances[1].play).toHaveBeenCalledTimes(1);
    view.unmount();
    expect(instances[1].pause).toHaveBeenCalled();
    expect(instances[1].load).toHaveBeenCalledTimes(1);
  });

  it('keeps working when browser preference storage is disabled', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    const session = {};
    const view = renderHook(() => useFatFishMusic(session, true));
    expect(view.result.current.enabled).toBe(false);
    act(() => view.result.current.toggle());
    expect(view.result.current.enabled).toBe(true);
    expect(instances[0].play).toHaveBeenCalledTimes(1);
  });

  it('shows a retryable failure state when the browser refuses playback', async () => {
    playResult = () => Promise.reject(new Error('NotAllowedError'));
    const session = {};
    const view = renderHook(() => useFatFishMusic(session, true));
    act(() => view.result.current.toggle());
    await waitFor(() => expect(view.result.current.unavailable).toBe(true));
    expect(view.result.current.enabled).toBe(false);
    expect(localStorage.getItem(FATFISH_MUSIC_KEY)).toBe('false');
    expect(instances[0].pause).toHaveBeenCalled();

    playResult = () => Promise.resolve();
    act(() => view.result.current.toggle());
    expect(view.result.current.unavailable).toBe(false);
    expect(instances[0].play).toHaveBeenCalledTimes(2);
  });

  it('permits only one player instance to play at a time', () => {
    const firstSession = {};
    const first = renderHook(() => useFatFishMusic(firstSession, true));
    act(() => first.result.current.toggle());
    const secondSession = {};
    const second = renderHook(() => useFatFishMusic(secondSession, true));
    expect(second.result.current.enabled).toBe(true);
    fireEvent.keyDown(window, { key: 'Enter' });
    expect(instances).toHaveLength(2);
    expect(instances[0].pause).toHaveBeenCalled();
    expect(instances[1].paused).toBe(false);
  });
});

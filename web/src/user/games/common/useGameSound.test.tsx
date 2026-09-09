import { StrictMode } from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useGameSound } from './useGameSound';

const engines = vi.hoisted(
  () =>
    [] as {
      unlock: ReturnType<typeof vi.fn>;
      play: ReturnType<typeof vi.fn>;
      silence: ReturnType<typeof vi.fn>;
      close: ReturnType<typeof vi.fn>;
    }[],
);
vi.mock('./sound', () => ({
  createGameSound: () => {
    const sound = {
      unlock: vi.fn(async () => undefined),
      play: vi.fn(),
      silence: vi.fn(),
      close: vi.fn(),
    };
    engines.push(sound);
    return sound;
  },
}));

function Harness({ game = 'rps' }: { readonly game?: 'rps' | 'fishing' | 'linklink' }) {
  const sound = useGameSound(game);
  return (
    <>
      <button aria-pressed={sound.enabled} onClick={sound.toggle}>
        Sound
      </button>
      <button onClick={() => sound.play('win')}>Result</button>
    </>
  );
}

function visibility(value: DocumentVisibilityState) {
  Object.defineProperty(document, 'visibilityState', { configurable: true, value });
  fireEvent(document, new Event('visibilitychange'));
}

describe('per-game sound lifecycle', () => {
  beforeEach(() => {
    engines.length = 0;
    localStorage.clear();
    visibility('visible');
  });

  it('defaults off, persists only a boolean per game, and keeps a single context', () => {
    const view = render(
      <StrictMode>
        <Harness />
      </StrictMode>,
    );
    fireEvent.pointerDown(window);
    fireEvent.click(screen.getByText('Result'));
    expect(engines).toHaveLength(0);
    expect(screen.getByText('Sound')).toHaveAttribute('aria-pressed', 'false');
    fireEvent.click(screen.getByText('Sound'));
    expect(localStorage.getItem('nonbiri.games.sound.v1.rps')).toBe('true');
    fireEvent.pointerDown(window);
    fireEvent.keyDown(window, { key: 'Enter' });
    fireEvent.click(screen.getByText('Result'));
    expect(engines).toHaveLength(1);
    expect(engines[0].play).toHaveBeenCalledWith('win');
    fireEvent.click(screen.getByText('Sound'));
    expect(engines[0].silence).toHaveBeenCalledTimes(1);
    expect(localStorage.getItem('nonbiri.games.sound.v1.rps')).toBe('false');
    fireEvent.click(screen.getByText('Result'));
    expect(engines[0].play).toHaveBeenCalledTimes(1);
    view.unmount();
    expect(engines[0].close).toHaveBeenCalledTimes(1);
    render(<Harness game="fishing" />);
    expect(screen.getByText('Sound')).toHaveAttribute('aria-pressed', 'false');
    expect(localStorage.getItem('nonbiri.games.sound.v1.fishing')).toBeNull();
  });

  it('requires a fresh gesture for a saved preference and never replays hidden events', () => {
    localStorage.setItem('nonbiri.games.sound.v1.rps', 'true');
    render(<Harness />);
    expect(screen.getByText('Sound')).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(screen.getByText('Result'));
    expect(engines).toHaveLength(0);
    fireEvent.keyDown(window, { key: 'ArrowLeft' });
    expect(engines).toHaveLength(1);
    act(() => visibility('hidden'));
    fireEvent.click(screen.getByText('Result'));
    fireEvent.pointerDown(window);
    expect(engines[0].play).not.toHaveBeenCalled();
    expect(engines[0].unlock).toHaveBeenCalledTimes(1);
    act(() => visibility('visible'));
    expect(engines[0].play).not.toHaveBeenCalled();
    expect(engines[0].unlock).toHaveBeenCalledTimes(1);
    fireEvent.pointerDown(window);
    fireEvent.click(screen.getByText('Result'));
    expect(engines[0].unlock).toHaveBeenCalledTimes(2);
    expect(engines[0].play).toHaveBeenCalledTimes(1);
  });

  it('treats corrupt preferences as off and tolerates blocked storage', () => {
    localStorage.setItem('nonbiri.games.sound.v1.rps', '{"enabled":true}');
    const first = render(<Harness />);
    expect(screen.getByText('Sound')).toHaveAttribute('aria-pressed', 'false');
    first.unmount();
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    render(<Harness />);
    fireEvent.click(screen.getByText('Sound'));
    expect(screen.getByText('Sound')).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(screen.getByText('Result'));
    expect(engines[0].play).toHaveBeenCalledWith('win');
  });
});

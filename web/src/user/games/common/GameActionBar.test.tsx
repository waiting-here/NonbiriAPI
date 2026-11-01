import { act, fireEvent } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { GameActionBar } from './GameActionBar';

it('updates the reserved height when the bar resizes and releases it on unmount', async () => {
  let resize: () => void = () => {};
  let height = 76;
  const observe = vi.fn(),
    disconnect = vi.fn();
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(callback: () => void) {
        resize = callback;
      }
      observe = observe;
      disconnect = disconnect;
    },
  );
  const bounds = vi
    .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
    .mockImplementation(() => ({ height }) as DOMRect);
  try {
    const view = await renderWithProviders(
      <GameActionBar cost="1,000">
        <button>Start</button>
      </GameActionBar>,
      { station: 'user' },
    );
    const root = document.documentElement;
    expect(root.style.getPropertyValue('--game-actionbar-h')).toBe('76px');
    expect(observe).toHaveBeenCalledWith(view.container.querySelector('.game-actionbar'));
    height = 112;
    act(() => resize());
    expect(root.style.getPropertyValue('--game-actionbar-h')).toBe('112px');
    view.unmount();
    expect(disconnect).toHaveBeenCalledOnce();
    expect(root.style.getPropertyValue('--game-actionbar-h')).toBe('');
  } finally {
    bounds.mockRestore();
    vi.unstubAllGlobals();
  }
});

it('escapes a mobile card, keeps its game scope, and returns inline on desktop', async () => {
  let mobile = true;
  const listeners = new Set<() => void>();
  vi.stubGlobal('matchMedia', () => ({
    get matches() {
      return mobile;
    },
    addEventListener: (_type: string, listener: () => void) => listeners.add(listener),
    removeEventListener: (_type: string, listener: () => void) => listeners.delete(listener),
  }));
  const start = vi.fn();
  try {
    const view = await renderWithProviders(
      <main className="game-page">
        <div className="bidding-game">
          <section className="card">
            <GameActionBar cost="1,000">
              <button onClick={start}>Start</button>
            </GameActionBar>
          </section>
        </div>
      </main>,
      { station: 'user' },
    );
    expect(view.container.querySelector('.bidding-game > .game-actionbar')).not.toBeNull();
    expect(view.container.querySelector('.card .game-actionbar')).toBeNull();
    fireEvent.click(view.getByRole('button', { name: 'Start' }));
    expect(start).toHaveBeenCalledOnce();
    act(() => {
      mobile = false;
      listeners.forEach((listener) => listener());
    });
    expect(view.container.querySelector('.card > .game-actionbar')).not.toBeNull();
    act(() => {
      mobile = true;
      listeners.forEach((listener) => listener());
    });
    expect(view.container.querySelectorAll('.game-actionbar')).toHaveLength(1);
    view.unmount();
    expect(document.querySelector('.game-actionbar')).toBeNull();
    expect(listeners.size).toBe(0);
  } finally {
    vi.unstubAllGlobals();
  }
});

import { act } from '@testing-library/react';
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

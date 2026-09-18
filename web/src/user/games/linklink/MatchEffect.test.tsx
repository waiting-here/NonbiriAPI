import { act, render } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { MatchEffect, type MatchAnimation } from './MatchEffect';

const animation: MatchAnimation = {
  key: 'match',
  pair: [
    { row: 0, col: 0 },
    { row: 0, col: 1 },
  ],
  path: [
    { row: 0, col: 0 },
    { row: 0, col: 1 },
  ],
  before: {
    kind: 'active',
    sessionID: 'll_AAAAAAAAAAAAAAAAAAAAAA',
    spec: '6x8',
    opportunitiesInitial: 2,
    opportunitiesRemaining: 2,
    rulesVersion: 2,
    payment: { general: '0', game: '3' },
    price: '3',
    revision: '1',
    board: { rows: 6, cols: 8, tiles: [] },
    pairsRemoved: 0,
    totalPairs: 24,
    startedAt: 1_800_000_000,
    deadline: 1_800_000_150,
    serverNow: 1_800_000_001,
  },
};

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function setup(initialWidth = '40px') {
  let width = initialWidth;
  let notifyResize = () => {};
  const frames = new Map<number, FrameRequestCallback>();
  let frameID = 0;
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
    frames.set(++frameID, callback);
    return frameID;
  });
  vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id));
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(callback: () => void) {
        notifyResize = callback;
      }
      observe() {}
      disconnect() {}
    },
  );
  vi.spyOn(window, 'getComputedStyle').mockImplementation(
    (element) =>
      ({
        width: element.isConnected ? width : '',
        height: element.isConnected ? '40px' : '',
        paddingLeft: '8px',
        paddingTop: '8px',
        columnGap: '4px',
        rowGap: '4px',
      }) as CSSStyleDeclaration,
  );
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({
    x: 0,
    y: 0,
    top: 0,
    left: 0,
    bottom: 280,
    right: 360,
    width: 360,
    height: 280,
    toJSON: () => ({}),
  });
  const rendered = render(
    <div>
      <button aria-rowindex={1} aria-colindex={1} />
      <MatchEffect animation={animation} />
    </div>,
  );
  return {
    ...rendered,
    svg: rendered.container.querySelector('svg')!,
    setWidth: (value: string) => {
      width = value;
    },
    resize: () => act(() => notifyResize()),
    nextFrame: () =>
      act(() => {
        const pending = [...frames.values()];
        frames.clear();
        pending.forEach((callback) => callback(0));
      }),
  };
}

it('waits for measurable layout and draws the path after resize', () => {
  const view = setup('auto');
  expect(view.svg.querySelector('polyline')).not.toHaveAttribute('points');
  expect(view.svg.outerHTML).not.toMatch(/NaN|Infinity/);
  view.setWidth('40px');
  view.resize();
  view.nextFrame();
  expect(view.svg.querySelector('polyline')).toHaveAttribute('points', '28,28 72,28');
});

it('does not write to a detached board from a pending resize frame', () => {
  const view = setup();
  view.resize();
  const geometry = view.svg.outerHTML;
  view.container.remove();
  view.nextFrame();
  expect(view.svg.outerHTML).toBe(geometry);
});

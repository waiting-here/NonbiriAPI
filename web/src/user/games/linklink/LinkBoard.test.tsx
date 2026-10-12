import { render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { LinkBoard } from './LinkBoard';
import type { LinkLinkState } from './types';

const glyph = vi.hoisted(() => vi.fn());
vi.mock('../copy', () => ({ useGameCopy: () => ({ text: (key: string) => key }) }));
vi.mock('./TileGlyph', () => ({
  TileGlyph: ({ tileKey }: { tileKey: string }) => {
    glyph(tileKey);
    return <span>{tileKey}</span>;
  },
}));

it('renders only changed tiles when selection or pending feedback changes', () => {
  const tiles = Array.from({ length: 100 }, (_, index) => ({
    row: Math.floor(index / 10),
    col: index % 10,
    tileKey: 'tile_01',
    removed: false,
  }));
  const state: LinkLinkState = {
    kind: 'active',
    sessionID: 'll_AAAAAAAAAAAAAAAAAAAAAA',
    spec: '10x10',
    revision: '1',
    price: '1000',
    payment: { general: '1000', game: '0' },
    rulesVersion: 2,
    opportunitiesInitial: 50,
    opportunitiesRemaining: 50,
    pairsRemoved: 0,
    totalPairs: 50,
    startedAt: 1_800_000_000,
    deadline: 1_800_000_240,
    serverNow: 1_800_000_000,
    board: { rows: 10, cols: 10, tiles },
  };
  const props = {
    state,
    selected: null,
    busy: false,
    pending: null,
    animation: null,
    hint: null,
    onSelect: vi.fn(),
  };
  const view = render(<LinkBoard {...props} />);
  expect(glyph).toHaveBeenCalledTimes(100);
  glyph.mockClear();
  view.rerender(<LinkBoard {...props} selected={{ row: 0, col: 0 }} />);
  expect(glyph).toHaveBeenCalledTimes(1);
  glyph.mockClear();
  view.rerender(
    <LinkBoard
      {...props}
      selected={{ row: 0, col: 0 }}
      busy
      pending={[
        { row: 0, col: 0 },
        { row: 0, col: 1 },
      ]}
    />,
  );
  expect(glyph).toHaveBeenCalledTimes(2);
  expect(
    screen.getAllByRole('gridcell').filter((cell) => cell.getAttribute('aria-busy') === 'true'),
  ).toHaveLength(2);
  expect(
    screen.getAllByRole('gridcell').every((cell) => !(cell as HTMLButtonElement).disabled),
  ).toBe(true);
});

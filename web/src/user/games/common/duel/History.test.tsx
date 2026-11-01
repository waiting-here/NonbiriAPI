import { screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { biddingCodec } from '../../bidding/normalize';
import { DuelHistory, DuelRoundLog } from './History';

const reads = vi.hoisted(() => ({ history: vi.fn(), rounds: vi.fn() }));
vi.mock('./api', () => ({
  duelKeys: { root: (game: string) => ['duel', game] },
  readHistory: reads.history,
  readRounds: reads.rounds,
  readDetail: vi.fn(),
}));

beforeEach(() => {
  reads.history.mockReset();
  reads.rounds.mockReset();
});

it('returns from the third history page through each visited page', async () => {
  reads.history.mockImplementation(async (_codec, cursor) => {
    const page = cursor === 'third' ? 3 : cursor === 'second' ? 2 : 1;
    return {
      items: [
        {
          id: 'history-' + page,
          outcome: 'win',
          terminalAt: 1800000000 + page,
          scores: [page, 0],
          you: 0,
        },
      ],
      nextCursor: page === 1 ? 'second' : page === 2 ? 'third' : null,
    };
  });
  const view = await renderWithProviders(
    <DuelHistory codec={biddingCodec} onClose={vi.fn()} renderRound={() => null} />,
    { station: 'user', role: 'user' },
  );
  const next = () => screen.getByRole('button', { name: 'Next page', exact: true });
  const previous = () => screen.getByRole('button', { name: 'Previous page', exact: true });
  await screen.findByRole('button', { name: /1 : 0/ });
  expect(previous()).toBeDisabled();
  await view.user.click(next());
  await screen.findByRole('button', { name: /2 : 0/ });
  expect(screen.getByText('Page 2')).toBeVisible();
  await view.user.click(next());
  await screen.findByRole('button', { name: /3 : 0/ });
  expect(next()).toBeDisabled();
  await view.user.click(previous());
  await screen.findByRole('button', { name: /2 : 0/ });
  expect(screen.getByText('Page 2')).toBeVisible();
  await view.user.click(previous());
  await screen.findByRole('button', { name: /1 : 0/ });
  expect(previous()).toBeDisabled();
});

it('returns through round log pages and refreshes the first page', async () => {
  reads.rounds.mockImplementation(async (_codec, _id, _active, cursor) => {
    const page = cursor === 'third' ? 3 : cursor === 'second' ? 2 : 1;
    return {
      items: [{ round: page, timeouts: [false, false] }],
      nextCursor: page === 1 ? 'second' : page === 2 ? 'third' : null,
    };
  });
  const view = await renderWithProviders(
    <DuelRoundLog
      codec={biddingCodec}
      id="history"
      active={false}
      you={0}
      renderRound={(round) => <p>Log item {round.round}</p>}
    />,
    { station: 'user', role: 'user' },
  );
  const next = () => screen.getByRole('button', { name: 'Next page', exact: true });
  await screen.findByText('Log item 1');
  await view.user.click(next());
  await screen.findByText('Log item 2');
  await view.user.click(next());
  await screen.findByText('Log item 3');
  await view.user.click(screen.getByRole('button', { name: 'Previous page' }));
  await screen.findByText('Log item 2');
  expect(screen.getByText('Page 2')).toBeVisible();
  await view.user.click(screen.getByRole('button', { name: 'Refresh / first page' }));
  await screen.findByText('Log item 1');
  expect(screen.getByText('Page 1')).toBeVisible();
});

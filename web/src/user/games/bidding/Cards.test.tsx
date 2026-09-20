import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { homeValue } from '../common/duel/normalize';
import { BiddingControls, PublicCards, RewardDeck, remainingRewardRanks } from './Cards';
import { biddingCodec } from './normalize';
import { biddingHomeWire } from './testFixtures';

describe('bidding decisions', () => {
  it('keeps selection private until explicit lock and never removes a card optimistically', async () => {
    const state = homeValue(biddingHomeWire(), biddingCodec).current!;
    const onAction = vi.fn();
    const rendered = await renderWithProviders(
      <BiddingControls state={state} blocked={false} onAction={onAction} />,
      { station: 'user' },
    );
    expect(screen.getByRole('button', { name: 'Lock in bid' })).toBeDisabled();
    await rendered.user.click(screen.getByRole('button', { name: 'Bid Hearts K (13)' }));
    expect(onAction).not.toHaveBeenCalled();
    await rendered.user.click(screen.getByRole('button', { name: 'Lock in bid' }));
    expect(onAction).toHaveBeenCalledExactlyOnceWith({ kind: 'bid', card: 13 });
    expect(screen.getAllByRole('button', { name: /^Bid / })).toHaveLength(13);
    rendered.rerender(
      <BiddingControls
        state={{ ...state, locked: [true, false], view: { ...state.view, selected: 8 } }}
        blocked={false}
        onAction={onAction}
      />,
    );
    expect(screen.getByRole('button', { name: 'Bid Hearts 8 (8)' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    expect(screen.getByRole('button', { name: 'Bid Hearts K (13)' })).toBeDisabled();
  });
  it('lets only the current dealer decide on the joker and blocks expired actions', async () => {
    const state = homeValue(biddingHomeWire('joker'), biddingCodec).current!;
    const onAction = vi.fn();
    const rendered = await renderWithProviders(
      <BiddingControls state={state} blocked={false} onAction={onAction} />,
      { station: 'user' },
    );
    await rendered.user.click(screen.getByRole('button', { name: 'Save joker' }));
    expect(onAction).toHaveBeenCalledExactlyOnceWith({ kind: 'joker', use: false });
    rendered.rerender(<BiddingControls state={state} blocked onAction={onAction} />);
    fireEvent.click(screen.getByRole('button', { name: 'Use joker · double reward' }));
    expect(onAction).toHaveBeenCalledTimes(1);
    rendered.rerender(
      <BiddingControls state={{ ...state, you: 1 }} blocked={false} onAction={onAction} />,
    );
    expect(screen.getByRole('group', { name: 'Your thirteen cards' })).toBeInTheDocument();
    expect(screen.getAllByRole('button')).toHaveLength(13);
  });
  it('keeps both hands in fixed A-K positions and greys played cards', async () => {
    const state = homeValue(biddingHomeWire(), biddingCodec).current!;
    const view = {
      ...state.view,
      hands: [
        [2, 4, 6],
        [1, 3, 5],
      ] as const,
      played: [
        [1, 3, 5, 7, 8, 9, 10, 11, 12, 13],
        [2, 4, 6, 8, 9, 10, 11, 12, 13],
      ] as const,
    };
    const rendered = await renderWithProviders(<PublicCards view={view} you={0} />, {
      station: 'user',
    });
    expect(
      screen.getByRole('group', { name: 'Opponent’s thirteen cards' }).querySelectorAll('button'),
    ).toHaveLength(13);
    expect(
      screen.getByRole('button', { name: /Opponent’s thirteen cards Spades 2 \(2\).*Played/ }),
    ).toHaveClass('is-played');
    expect(rendered.container.querySelectorAll('.bid-card:disabled')).toHaveLength(13);
  });
  it('shows a sorted remaining rank set without exposing future reward order', async () => {
    const state = homeValue(biddingHomeWire(), biddingCodec).current!;
    const view = {
      ...state.view,
      rewards: [
        ...state.view.rewards,
        {
          round: 2,
          side: 0 as const,
          rank: 9,
          multiplier: 1,
          status: 'pool' as const,
          owner: null,
        },
      ],
    };
    expect(remainingRewardRanks(view, 0, 1)).toContain(9);
    const rendered = await renderWithProviders(
      <RewardDeck view={view} side={0} round={1} you={0} />,
      {
        station: 'user',
      },
    );
    await rendered.user.click(rendered.container.querySelector('summary')!);
    expect(
      screen.getByText('Ranks only; this does not reveal the future order.'),
    ).toBeInTheDocument();
    expect(screen.getByText(/9/)).toBeInTheDocument();
  });
});

import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { homeValue } from '../common/duel/normalize';
import { BiddingControls } from './Cards';
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
    await rendered.user.click(screen.getByRole('button', { name: 'Bid K (13)' }));
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
    expect(screen.getByRole('button', { name: 'Bid 8 (8)' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    expect(screen.getByRole('button', { name: 'Bid K (13)' })).toBeDisabled();
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
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});

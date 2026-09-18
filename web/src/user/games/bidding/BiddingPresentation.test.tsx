import { act, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { homeValue } from '../common/duel/normalize';
import { biddingCodec } from './normalize';
import { BiddingPresentation } from './BiddingPresentation';
import { RewardDeck } from './Cards';
import { biddingHomeWire } from './testFixtures';

type MutableWire = Omit<ReturnType<typeof biddingHomeWire>, 'current' | 'latest_result'> & {
  current: ReturnType<typeof biddingHomeWire>['current'] | null;
  latest_result: unknown;
};

function progressed() {
  const wire = biddingHomeWire();
  wire.server_now += 1;
  wire.current!.server_now += 1;
  wire.current!.revision = '2';
  wire.current!.phase_seq = '2';
  wire.current!.locked = [true, true];
  wire.current!.view.played = [[13], [13]];
  wire.current!.view.hand_remaining = [
    Array.from({ length: 12 }, (_, index) => index + 1),
    Array.from({ length: 12 }, (_, index) => index + 1),
  ];
  wire.current!.view.rewards = [
    ...wire.current!.view.rewards,
    { round: 2, side: 0, rank: 13, multiplier: 2, status: 'pool', owner: null },
    { round: 2, side: 1, rank: 4, multiplier: 1, status: 'pool', owner: null },
  ];
  return homeValue(wire, biddingCodec);
}

function withQueue() {
  const live = homeValue(biddingHomeWire(), biddingCodec);
  return { ...live, current: null, queue: {} as never } as typeof live;
}

describe('bidding presentation timing', () => {
  it('does not replay the initial snapshot and survives a one-second poll', async () => {
    vi.useFakeTimers();
    try {
      const before = homeValue(biddingHomeWire(), biddingCodec);
      const after = progressed();
      const cues = vi.fn();
      const view = await renderWithProviders(<BiddingPresentation home={before} onCue={cues} />, {
        station: 'user',
      });
      expect(screen.queryByText(/Both bids revealed together/)).not.toBeInTheDocument();
      view.rerender(<BiddingPresentation home={after} onCue={cues} />);
      await act(async () => {
        vi.advanceTimersByTime(1);
      });
      view.rerender(<BiddingPresentation home={after} onCue={cues} />);
      expect(screen.getByText('Both bids revealed together')).toBeInTheDocument();
      expect(cues).toHaveBeenCalledWith('bidding_reveal');
      await act(async () => {
        vi.advanceTimersByTime(1199);
      });
      expect(screen.getByText(/Tie — the pool carries to the next round/)).toBeInTheDocument();
      expect(cues).not.toHaveBeenCalledWith('bidding_pot_collect');
      await act(async () => {
        vi.advanceTimersByTime(800);
      });
      expect(screen.getAllByText(/pool points/).length).toBeGreaterThan(0);
    } finally {
      vi.useRealTimers();
    }
  });

  it('shows real reward values when a queued match opens its first round', async () => {
    const queued = withQueue();
    const live = homeValue(biddingHomeWire(), biddingCodec);
    const view = await renderWithProviders(<BiddingPresentation home={queued} />, {
      station: 'user',
    });
    view.rerender(<BiddingPresentation home={live} />);
    expect(screen.getByLabelText(/Diamonds 7/)).toBeInTheDocument();
    expect(screen.getByLabelText(/Clubs J/)).toBeInTheDocument();
  });

  it('measures each real reward deck to its matching drawn reward', async () => {
    const queued = withQueue();
    const live = homeValue(biddingHomeWire(), biddingCodec);
    const rect = vi
      .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
      .mockImplementation(function (this: HTMLElement) {
        return this.classList.contains('bid-reward-deck--0') ||
          this.parentElement?.classList.contains('bid-reward-deck--0')
          ? ({
              left: 10,
              top: 20,
              width: 52,
              height: 72,
              right: 62,
              bottom: 92,
              x: 10,
              y: 20,
              toJSON() {},
            } as DOMRect)
          : ({
              left: 300,
              top: 100,
              width: 80,
              height: 120,
              right: 380,
              bottom: 220,
              x: 300,
              y: 100,
              toJSON() {},
            } as DOMRect);
      });
    try {
      const view = await renderWithProviders(
        <>
          <BiddingPresentation home={queued} />
          <RewardDeck view={live.current!.view} side={0} round={1} you={0} />
          <RewardDeck view={live.current!.view} side={1} round={1} you={0} />
        </>,
        { station: 'user' },
      );
      view.rerender(
        <>
          <BiddingPresentation home={live} />
          <RewardDeck view={live.current!.view} side={0} round={1} you={0} />
          <RewardDeck view={live.current!.view} side={1} round={1} you={0} />
        </>,
      );
      const target = document.querySelector<HTMLElement>('.bid-presentation .bid-reward--0');
      expect(target).toHaveClass('is-drawing-from-deck');
      expect(target?.style.getPropertyValue('--draw-x')).toBe('-304px');
      expect(target?.style.getPropertyValue('--draw-scale')).toBe('0.65');
    } finally {
      rect.mockRestore();
    }
  });

  it('shows the complete carried pool and the local owner after settlement', async () => {
    vi.useFakeTimers();
    try {
      const before = progressed();
      const afterWire = biddingHomeWire();
      afterWire.current!.view.played = [
        [13, 12],
        [13, 12],
      ];
      afterWire.current!.view.hand_remaining = [
        Array.from({ length: 11 }, (_, i) => i + 1),
        Array.from({ length: 11 }, (_, i) => i + 1),
      ];
      afterWire.current!.view.rewards = [...before.current!.view.rewards].map((reward) =>
        reward.round === 2 ? { ...reward, status: 'awarded' as const, owner: 1 as const } : reward,
      );
      const after = homeValue(afterWire, biddingCodec);
      const cues = vi.fn();
      const view = await renderWithProviders(<BiddingPresentation home={before} onCue={cues} />, {
        station: 'user',
      });
      view.rerender(<BiddingPresentation home={after} onCue={cues} />);
      await act(async () => {
        vi.advanceTimersByTime(1201);
      });
      expect(screen.getByText(/Opponent collects the whole pool/)).toBeInTheDocument();
      expect(screen.getAllByText(/18 pool points/).length).toBeGreaterThan(0);
      expect(cues).toHaveBeenCalledWith('bidding_pot_collect');
    } finally {
      vi.useRealTimers();
    }
  });

  it('shows the terminal discard outcome after the last tied round', async () => {
    vi.useFakeTimers();
    try {
      const before = progressed();
      const wire = biddingHomeWire() as MutableWire;
      wire.current!.view.played = [
        [13, 12],
        [13, 12],
      ];
      wire.current!.view.hand_remaining = [
        Array.from({ length: 11 }, (_, i) => i + 1),
        Array.from({ length: 11 }, (_, i) => i + 1),
      ];
      wire.current!.view.rewards = [
        ...before.current!.view.rewards.slice(0, 2),
        { round: 2, side: 0, rank: 13, multiplier: 1, status: 'discarded' as const, owner: null },
        { round: 2, side: 1, rank: 4, multiplier: 1, status: 'discarded' as const, owner: null },
      ];
      const view = wire.current!.view;
      wire.current = null;
      wire.latest_result = {
        id: 'bid_AAAAAAAAAAAAAAAAAAAAAA',
        game: 'bidding',
        mode: 'tier1',
        terminal_at: wire.server_now + 1,
        outcome: 'draw',
        reason: 'rounds',
        scores: [18, 18],
        own_payment: { general: '2', game: '3' },
        own_refund: { general: '2', game: '3' },
        prize_general: '0',
        rake: { platform: '0', welfare: '0', thursday: '0' },
        you: 0,
        profiles: [{ kind: 'anonymous' }, { kind: 'public', display_name: 'Card player' }],
        view,
        resolution: null,
      };
      const after = homeValue(wire as never, biddingCodec);
      const rendered = await renderWithProviders(<BiddingPresentation home={before} />, {
        station: 'user',
      });
      rendered.rerender(<BiddingPresentation home={after} />);
      await act(async () => {
        vi.advanceTimersByTime(1500);
      });
      expect(screen.getByText('Final tie — the pool is discarded')).toBeInTheDocument();
      expect(screen.getAllByText(/Discarded/).length).toBeGreaterThanOrEqual(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('queues a later reward draw behind the active settlement timeline', async () => {
    vi.useFakeTimers();
    try {
      const before = progressed();
      const settledWire = biddingHomeWire() as MutableWire;
      settledWire.current!.view.played = [
        [13, 12],
        [13, 12],
      ];
      settledWire.current!.view.hand_remaining = [
        Array.from({ length: 11 }, (_, i) => i + 1),
        Array.from({ length: 11 }, (_, i) => i + 1),
      ];
      settledWire.current!.view.rewards = [...before.current!.view.rewards].map((r) =>
        r.round === 2 ? { ...r, status: 'awarded', owner: 1 } : r,
      );
      const settled = homeValue(settledWire, biddingCodec);
      const nextWire = settledWire;
      nextWire.server_now += 1;
      nextWire.current!.server_now += 1;
      nextWire.current!.round = 3;
      nextWire.current!.phase_seq = '3';
      nextWire.current!.view.rewards = [
        ...settled.current!.view.rewards,
        { round: 3, side: 0, rank: 6, multiplier: 1, status: 'pool', owner: null },
        { round: 3, side: 1, rank: 8, multiplier: 1, status: 'pool', owner: null },
      ];
      const next = homeValue(nextWire, biddingCodec);
      const view = await renderWithProviders(<BiddingPresentation home={before} />, {
        station: 'user',
      });
      view.rerender(<BiddingPresentation home={settled} />);
      await act(async () => {
        vi.advanceTimersByTime(1201);
      });
      view.rerender(<BiddingPresentation home={next} />);
      expect(screen.getByText(/Opponent collects the whole pool/)).toBeInTheDocument();
      await act(async () => {
        vi.advanceTimersByTime(1399);
      });
      expect(screen.getByText(/Round 3: draw rewards from both decks/)).toBeInTheDocument();
      expect(screen.getByLabelText(/Diamonds 6/)).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it('does not replay a hidden update and cancels when the session changes', async () => {
    vi.useFakeTimers();
    try {
      const before = homeValue(biddingHomeWire(), biddingCodec);
      const after = progressed();
      const view = await renderWithProviders(<BiddingPresentation home={before} />, {
        station: 'user',
      });
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
      document.dispatchEvent(new Event('visibilitychange'));
      view.rerender(<BiddingPresentation home={after} />);
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
      document.dispatchEvent(new Event('visibilitychange'));
      await act(async () => {
        vi.advanceTimersByTime(4000);
      });
      expect(screen.queryByText('Both bids revealed together')).not.toBeInTheDocument();
      view.rerender(<BiddingPresentation home={before} />);
      await act(async () => {
        vi.advanceTimersByTime(4000);
      });
      expect(screen.queryByText(/pool points/)).not.toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it('keeps factual timing and cleanup under reduced motion', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('matchMedia', () => ({
      matches: true,
      media: '',
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    try {
      const cues = vi.fn();
      const before = progressed();
      const afterWire = biddingHomeWire() as MutableWire;
      afterWire.current!.view.played = [
        [13, 12],
        [13, 12],
      ];
      afterWire.current!.view.hand_remaining = [
        Array.from({ length: 11 }, (_, i) => i + 1),
        Array.from({ length: 11 }, (_, i) => i + 1),
      ];
      afterWire.current!.view.rewards = [...before.current!.view.rewards].map((r) =>
        r.round === 2 ? { ...r, status: 'awarded', owner: 1 } : r,
      );
      const after = homeValue(afterWire, biddingCodec);
      const view = await renderWithProviders(<BiddingPresentation home={before} onCue={cues} />, {
        station: 'user',
      });
      view.rerender(<BiddingPresentation home={after} onCue={cues} />);
      await act(async () => {
        vi.advanceTimersByTime(1);
      });
      expect(cues).toHaveBeenCalledWith('bidding_reveal');
      await act(async () => {
        vi.advanceTimersByTime(1199);
      });
      expect(cues).toHaveBeenCalledWith('bidding_pot_collect');
      await act(async () => {
        vi.advanceTimersByTime(1400);
      });
      expect(screen.queryByText(/pool points/)).not.toBeInTheDocument();
    } finally {
      vi.unstubAllGlobals();
      vi.useRealTimers();
    }
  });
});

import { act, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { installJsonFetchFixtures, renderWithProviders } from '../../../../test/unit/support';
import { gamesSnapshotWire } from '../common/testFixtures';
import { RPSGame } from './RPSGame';
import { rpsStateWire, rpsTestSessionID } from './testFixtures';

const audio = vi.hoisted(() => ({
  play: vi.fn(),
  unlock: vi.fn(async () => undefined),
  silence: vi.fn(),
  close: vi.fn(),
}));
vi.mock('../common/sound', () => ({ createGameSound: () => audio }));

function install(home: unknown) {
  const snapshot = gamesSnapshotWire();
  snapshot.tutorial_rps_seen = true;
  return installJsonFetchFixtures([
    { method: 'GET', path: '/api/games', body: snapshot },
    { method: 'GET', path: '/api/games/rps/state', body: home },
    {
      method: 'POST',
      path: `/api/games/rps/sessions/${rpsTestSessionID}/lease`,
      body: { expires_at: 1_800_000_025 },
    },
    ...['quick', 'standard'].flatMap((mode) =>
      ['net_profit', 'profit_rate'].map((board) => ({
        method: 'GET',
        path: `/api/games/rps/leaderboard?mode=${mode}&board=${board}`,
        body: {
          mode,
          board,
          window_days: 30,
          window_start: 1_700_000_000,
          min_sessions: 10,
          rows: [],
          me: null,
        },
      })),
    ),
  ]);
}

function pending(known: boolean) {
  return {
    kind: 'pending_result',
    result: {
      rules_version: known ? 2 : 1,
      own_buy_in_general: known ? '3' : null,
      own_buy_in_game: known ? '2' : null,
      own_returned_general: known ? '6' : null,
      session_id: rpsTestSessionID,
      mode: 'quick',
      terminal_reason: 'quick_resolved',
      own_seat_no: 0,
      own_input: '20',
      own_returned: '21',
      own_wallet_net: '1',
      own_buy_in: known ? '5' : null,
      own_cash_out: known ? '6' : null,
      seats: [
        { seat_no: 0, result: 'win', gesture: known ? 'paper' : null },
        { seat_no: 1, result: 'loss', gesture: known ? 'rock' : null },
        { seat_no: 2, result: 'deidentified', gesture: known ? 'rock' : null },
      ],
      created_at: 1_800_000_000,
    },
  };
}

afterEach(() => vi.unstubAllGlobals());

describe('RPS presentation in the real game page', () => {
  it.each([true, false])(
    'keeps own transfers distinct from cumulative amounts and renders only saved hands (%s)',
    async (known) => {
      vi.stubGlobal(
        'IntersectionObserver',
        class {
          observe() {}
          disconnect() {}
        },
      );
      const fetch = install(pending(known));
      const view = await renderWithProviders(<RPSGame />, { station: 'user', route: '/games/rps' });
      await screen.findByRole('heading', { name: 'Quick' });
      const seats = within(screen.getByRole('list', { name: 'Player results' })).getAllByRole('listitem');
      expect(seats).toHaveLength(3);
      expect(seats[0]).toHaveTextContent('You');
      expect(seats[2]).toHaveTextContent('Identity-hidden seat');
      if (known) {
        expect(seats[0]).toHaveTextContent('Paper');
        expect(seats[1]).toHaveTextContent('Rock');
        expect(seats[2]).toHaveTextContent('Rock');
        expect(view.container.querySelectorAll('.rps-result__seats svg')).toHaveLength(3);
        expect(screen.getByText('Starting buy-in (actual input)').parentElement).toHaveTextContent(
          'General credits 3 creditsGame credits 2 credits',
        );
        expect(screen.getByText('Ending cash-out in general credits').parentElement).toHaveTextContent(
          '6 credits',
        );
      } else {
        expect(view.container.querySelectorAll('.rps-result__seats svg')).toHaveLength(0);
        expect(screen.getAllByText('Not recorded for this historical result')).toHaveLength(5);
      }
      expect(screen.getByText('Your total input').parentElement).toHaveTextContent('20 credits');
      expect(screen.getByText('Your total returned').parentElement).toHaveTextContent('21 credits');
      expect(view.container.querySelector('.rps-result-orbit')).toBeNull();
      expect(fetch.mock.calls.some(([url]) => String(url).endsWith('/pending-result/ack'))).toBe(
        false,
      );
    },
  );

  it('emphasizes new live phases immediately, keeps controls usable and never replays snapshot, gap or hidden transitions', async () => {
    const sources: EventTarget[] = [];
    vi.stubGlobal(
      'EventSource',
      class extends EventTarget {
        onopen: (() => void) | null = null;
        constructor() {
          super();
          sources.push(this);
          queueMicrotask(() => this.onopen?.());
        }
        close() {}
      },
    );
    const initial = rpsStateWire('gesture');
    install({ kind: 'session', session: initial });
    const view = await renderWithProviders(<RPSGame />, { station: 'user', route: '/games/rps' });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Rock' })).toBeEnabled());
    expect(view.container.querySelector('.rps-phase-flash')).toBeNull();
    expect(screen.getByText('This ongoing pool has no recorded tie count.')).toBeInTheDocument();
    const frame = (type: 'snapshot' | 'delta', state: ReturnType<typeof rpsStateWire>) => {
      act(() =>
        sources.at(-1)!.dispatchEvent(
          new MessageEvent(type, {
            lastEventId: 'sse_AAAAAAAAAAAAAAAAAAAAAA',
            data: JSON.stringify({
              version: 1,
              channel: 'rps',
              type,
              revision: state.revision,
              identity_epoch: state.identity_epoch,
              occurred_at: state.server_now,
              data: { kind: 'session', session: state },
            }),
          }),
        ),
      );
    };
    frame('snapshot', initial);
    await view.user.click(screen.getByRole('button', { name: 'Sound off' }));
    expect(audio.play).not.toHaveBeenCalled();
    const next = rpsStateWire('dealer_raise', '2');
    next.phase_seq = '2';
    frame('delta', next);
    await waitFor(() => expect(view.container.querySelector('.rps-phase-flash')).not.toBeNull());
    expect(screen.getByRole('button', { name: 'Do not raise' })).toBeEnabled();
    expect(
      screen.getByText('You are the dealer: raise or keep the current stake.'),
    ).toBeInTheDocument();
    const flash = view.container.querySelector('.rps-phase-flash');
    frame('delta', next);
    expect(view.container.querySelector('.rps-phase-flash')).toBe(flash);
    expect(audio.play.mock.calls).toEqual([['phase']]);
    const restored = rpsStateWire('free_pool_gesture', '3');
    restored.phase_seq = '3';
    Object.assign(restored.round_summary, { pool_tie_count: '5' });
    frame('snapshot', restored);
    expect(audio.play.mock.calls).toEqual([['phase']]);
    expect(view.container.querySelector('.rps-phase-flash')).toBeNull();
    expect(view.container.querySelector('.rps-atmosphere')).toHaveAttribute('data-level', '3');
    expect(view.container.querySelectorAll('.rps-atmosphere i').length).toBeLessThanOrEqual(32);
    act(() =>
      sources.at(-1)!.dispatchEvent(
        new MessageEvent('gap', {
          lastEventId: 'sse_AAAAAAAAAAAAAAAAAAAAAA',
          data: JSON.stringify({
            version: 1,
            channel: 'rps',
            type: 'gap',
            revision: null,
            identity_epoch: null,
            occurred_at: restored.server_now,
            data: { reason: 'process_restart', last_event_id: null },
          }),
        }),
      ),
    );
    const afterGap = rpsStateWire('followers', '4');
    afterGap.phase_seq = '4';
    frame('delta', afterGap);
    expect(audio.play.mock.calls).toEqual([['phase']]);
    expect(view.container.querySelector('.rps-phase-flash')).toBeNull();
    frame('snapshot', afterGap);
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    act(() => document.dispatchEvent(new Event('visibilitychange')));
    const hidden = rpsStateWire('gesture', '5');
    hidden.phase_seq = '5';
    frame('delta', hidden);
    expect(view.container.querySelector('.rps-phase-flash')).toBeNull();
    expect(view.container.querySelector('.rps-match')).toHaveClass('is-background');
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
    act(() => document.dispatchEvent(new Event('visibilitychange')));
    expect(view.container.querySelector('.rps-phase-flash')).toBeNull();
    await waitFor(() =>
      expect(
        within(view.container.querySelector('.rps-action-panel') as HTMLElement).getByRole(
          'button',
          {
            name: 'Rock',
          },
        ),
      ).toBeEnabled(),
    );
    expect(audio.play.mock.calls).toEqual([['phase']]);
    const follower = rpsStateWire('followers', '6');
    follower.phase_seq = '6';
    frame('delta', follower);
    expect(audio.play.mock.calls).toEqual([['phase'], ['follow']]);
    vi.stubGlobal(
      'IntersectionObserver',
      class {
        observe() {}
        disconnect() {}
      },
    );
    const processing = rpsStateWire('terminal_processing', '7');
    processing.phase_seq = '7';
    frame('delta', processing);
    expect(audio.play.mock.calls).toEqual([['phase'], ['follow']]);
    const terminal = pending(false);
    terminal.result.mode = 'standard';
    terminal.result.terminal_reason = 'standard_round_limit';
    const finish = () =>
      act(() =>
        sources.at(-1)!.dispatchEvent(
          new MessageEvent('delta', {
            lastEventId: 'sse_AAAAAAAAAAAAAAAAAAAAAA',
            data: JSON.stringify({
              version: 1,
              channel: 'rps',
              type: 'delta',
              revision: null,
              identity_epoch: null,
              occurred_at: 1_800_000_000,
              data: terminal,
            }),
          }),
        ),
      );
    finish();
    finish();
    expect(audio.play.mock.calls).toEqual([['phase'], ['follow'], ['win']]);
    await view.user.click(screen.getByRole('button', { name: 'Sound on' }));
    await view.user.click(screen.getByRole('button', { name: 'Sound off' }));
    expect(audio.play.mock.calls).toEqual([['phase'], ['follow'], ['win']]);
  });
});

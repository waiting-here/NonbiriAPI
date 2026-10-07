import { act, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import frameHTML from '../../../../public/assets/gwent/interface/index.html?raw';
import cards from '../../../../public/assets/gwent/interface/src/cards/cards.json';
import abilities from './abilities.json';
import { installNativeDialog } from '../../../../test/unit/nativeDialog';
const viewPath = '../../../../public/assets/gwent/interface/src/ui/battlefield.js';
const { ArenaView } = await import(viewPath);
const timerPath = '../../../../public/assets/gwent/interface/src/ui/decision-timer.js';
const { installDecisionTimer } = await import(timerPath);
vi.mock('../../../../public/assets/gwent/interface/src/main.js', () => ({
  boot: async () => {
    installDecisionTimer();
    const arena = {
      speed: 0.12,
      view: null as unknown,
      label: vi.fn(),
      delay: vi.fn(),
      fail: vi.fn(),
      command: vi.fn(),
      start: vi.fn(),
      lobby: vi.fn(),
      clock: { stop: vi.fn() },
      matchAbort: { abort: vi.fn() },
    };
    arena.view = new ArenaView(arena);
    return {
      arena,
      catalog: cards,
      setup: { update: vi.fn(), panel: document.createElement('div') },
    };
  },
}));
installNativeDialog();
it('keeps an active-match replay through snapshots, resumes play on return and shows a restored result', async () => {
  document.body.innerHTML = new DOMParser().parseFromString(frameHTML, 'text/html').body.innerHTML;
  document.documentElement.dataset.motion = 'reduced';
  vi.stubGlobal('ability_dict', abilities);
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.stubGlobal('game', { state: 'idle' });
  vi.stubGlobal('GameState', { END_SCREEN: 'ended' });
  const posted = vi.spyOn(window, 'postMessage');
  const platformPath = '../../../../public/assets/gwent/interface/platform.js';
  await import(platformPath);
  await waitFor(() =>
    expect(posted.mock.calls.some(([message]) => message.type === 'mounted')).toBe(true),
  );
  const player = (faction: string) => ({
    faction,
    lives: 2,
    passed: false,
    hand_count: 10,
    deck_count: 16,
    grave: [],
    leader: { ...cards.find((c) => c.id === `${faction}_leader`), instance_id: 1000 },
    leader_available: true,
    boost: 0,
    shield: false,
  });
  const view = {
    version: 1,
    round: 1,
    phase: 'turn',
    turn: 0,
    self: player('openai'),
    enemy: player('deepseek'),
    hand: [],
    board: ['enemy', 'self'].flatMap((side) =>
      ['close', 'ranged', 'siege'].map((row) => ({
        side,
        row,
        total: 0,
        weather: false,
        cards: [],
      })),
    ),
    weather: [],
    legal_actions: [{ kind: 'pass' }],
    rounds: [],
  };
  const result = {
    id: 'previous',
    you: 0,
    outcome: 'win',
    prize: '5',
    refund: { balance: '0', gameBalance: '0' },
    view,
    profiles: [{ kind: 'anonymous' }, { kind: 'anonymous' }],
  };
  const dispatch = (message: Record<string, unknown>) =>
    act(() =>
      window.dispatchEvent(
        new MessageEvent('message', {
          origin: location.origin,
          source: window,
          data: { channel: 'nonbiri.gwent', ...message },
        }),
      ),
    );
  const snapshot = { blocked: false, remaining: 20, home: { current: null, latestResult: result } };
  dispatch({ type: 'snapshot', snapshot });
  expect(document.getElementById('result-overlay')!.hidden).toBe(false);
  expect(document.getElementById('result-subtitle')!.textContent).toContain('奖金 5');
  expect(document.getElementById('rematch')!.textContent).toBe('返回大厅选择新局');
  document.getElementById('rematch')!.click();
  expect(document.getElementById('lobby')!.hidden).toBe(false);
  expect(posted.mock.calls.some(([message]) => message.type === 'rematch')).toBe(false);
  const current = {
    id: 'active',
    phaseSeq: '4',
    decisionID: '9',
    phase: 'turn',
    you: 0,
    view,
    profiles: result.profiles,
  };
  const active = { ...snapshot, home: { current, latestResult: result } };
  dispatch({ type: 'snapshot', snapshot: active });
  dispatch({ type: 'replay', replay: { result, initial: view, rounds: [] } });
  dispatch({ type: 'snapshot', snapshot: { ...active, remaining: 19 } });
  expect(document.querySelector('.platform-replay-controls')).not.toBeNull();
  expect(document.getElementById('battle-status')!.textContent).toContain('回放');
  document.querySelector<HTMLButtonElement>('.platform-replay-controls button:last-child')!.click();
  expect(document.querySelector('.platform-replay-controls')).toBeNull();
  expect(document.getElementById('decision-timer')!.hidden).toBe(false);
  expect(document.getElementById('battle-status')!.textContent).not.toContain('回放');
});

import { beforeEach, describe, expect, it, vi } from 'vitest';
import frameHTML from '../../../../public/assets/gwent/interface/index.html?raw';
import cards from '../../../../public/assets/gwent/interface/src/cards/cards.json';
import abilities from './abilities.json';
import { installNativeDialog } from '../../../../test/unit/nativeDialog';
const battlePath = '../../../../public/assets/gwent/interface/battle.js';
const viewPath = '../../../../public/assets/gwent/interface/src/ui/battlefield.js';
const timerPath = '../../../../public/assets/gwent/interface/src/ui/decision-timer.js';
const { PlatformBattle } = await import(battlePath);
const { ArenaView } = await import(viewPath);
const { installDecisionTimer } = await import(timerPath);
installNativeDialog();

beforeEach(() => {
  document.body.innerHTML = new DOMParser().parseFromString(frameHTML, 'text/html').body.innerHTML;
  document.documentElement.dataset.motion = 'reduced';
  (globalThis as unknown as { ability_dict: typeof abilities }).ability_dict = abilities;
  installDecisionTimer();
});
function fixture(spy = false) {
  const definition = cards.find(
    (card) =>
      card.faction === 'openai' &&
      card.type === 'unit' &&
      card.row === 'close' &&
      (card.abilities as string[]).includes('spy') === spy,
  )!;
  const card = { ...definition, instance_id: 2, base_power: definition.power };
  const player = (faction: string) => ({
    faction,
    lives: 2,
    passed: false,
    hand_count: 10,
    deck_count: 16,
    grave: [],
    leader: {
      ...cards.find((c) => c.id === faction + '_leader'),
      instance_id: faction === 'openai' ? 1000 : 1001,
    },
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
    hand: [card],
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
    legal_actions: [{ kind: 'play', card: 2, row: 'close' }],
    rounds: [],
  };
  const arena = {
    speed: 0.12,
    view: null as unknown,
    command: vi.fn(),
    start: vi.fn(),
    lobby: vi.fn(),
  };
  arena.view = new ArenaView(arena);
  const send = vi.fn(),
    host = { send, returnLobby: vi.fn() };
  const renderer = new PlatformBattle(arena, cards, host);
  const snapshot = {
    blocked: false,
    remaining: 25,
    home: {
      current: {
        id: 'match',
        phaseSeq: '1',
        decisionID: 'choice',
        you: 0,
        phase: 'turn',
        view,
        profiles: [{ kind: 'anonymous' }, { kind: 'anonymous' }],
      },
    },
  };
  renderer.render(snapshot);
  return { renderer, view, snapshot, send };
}

describe('original battlefield with authoritative actions', () => {
  it('selects a hand card and sends the supplied legal row action', () => {
    const f = fixture();
    document.querySelector<HTMLButtonElement>('#arena-hand button')!.click();
    expect(document.querySelector('#arena-me-close')!.classList.contains('target-row')).toBe(true);
    document.getElementById('arena-me-close')!.click();
    expect(f.send).toHaveBeenCalledWith({
      type: 'action',
      id: 'match',
      phaseSeq: '1',
      decisionID: 'choice',
      action: f.view.legal_actions[0],
    });
    expect(document.getElementById('arena-op-close')!.classList.contains('target-row')).toBe(false);
  });
  it('uses the opposing row for spies and never exposes an opposing hand', () => {
    const f = fixture(true);
    document.querySelector<HTMLButtonElement>('#arena-hand button')!.click();
    expect(document.getElementById('arena-op-close')!.classList.contains('target-row')).toBe(true);
    document.getElementById('arena-me-close')!.click();
    expect(
      f.send.mock.calls.filter(([message]) => ['action', 'surrender'].includes(message.type)),
    ).toHaveLength(0);
    document.getElementById('arena-op-close')!.click();
    expect(f.send.mock.calls.filter(([message]) => message.type === 'action')).toHaveLength(1);
    expect(document.querySelectorAll('#arena-hand button')).toHaveLength(1);
    expect(document.querySelectorAll('#arena-stats-op .arena-card')).toHaveLength(0);
  });
  it('renders a real target decision and sends the exact target action', () => {
    const f = fixture();
    const target = { ...f.view.hand[0], instance_id: 3 };
    const action = { kind: 'choose', card: 3 };
    const view = {
      ...f.view,
      choice: { kind: 'medic', cards: [target], rows: [], remaining: 1, can_quit: false },
      legal_actions: [action],
    };
    f.renderer.render({ ...f.snapshot, home: { current: { ...f.snapshot.home.current, view } } });
    const dialog = document.getElementById('arena-dialog') as HTMLDialogElement;
    expect(dialog.open).toBe(true);
    document.querySelector<HTMLButtonElement>('#dialog-cards button')!.click();
    expect(f.send).toHaveBeenCalledWith({
      type: 'action',
      id: 'match',
      phaseSeq: '1',
      decisionID: 'choice',
      action,
    });
    f.renderer.render({
      ...f.snapshot,
      blocked: true,
      home: { current: { ...f.snapshot.home.current, view } },
    });
    expect(document.querySelector<HTMLButtonElement>('#dialog-cards button')!.disabled).toBe(true);
  });
  it('keeps the human redraw button valid when an independent opponent action advances the global phase', () => {
    const f = fixture();
    const action = { kind: 'redraw', card: f.view.hand[0].instance_id };
    const view = {
      ...f.view,
      choice: { kind: 'mulligan', cards: f.view.hand, rows: [], remaining: 2, can_quit: true },
      legal_actions: [action, { kind: 'continue' }],
    };
    const current = { ...f.snapshot.home.current, view };
    f.renderer.render({ ...f.snapshot, home: { current } });
    const redraw = document.querySelector<HTMLButtonElement>('#dialog-cards button')!;
    f.renderer.render({ ...f.snapshot, home: { current: { ...current, phaseSeq: '2' } } });
    expect(document.querySelector('#dialog-cards button')).toBe(redraw);
    expect(redraw.disabled).toBe(false);
    redraw.click();
    expect(f.send).toHaveBeenLastCalledWith({
      type: 'action',
      id: 'match',
      phaseSeq: '1',
      decisionID: 'choice',
      action,
    });
    f.renderer.render({
      ...f.snapshot,
      home: { current: { ...current, phaseSeq: '3', decisionID: 'next' } },
    });
    redraw.click();
    expect(f.send.mock.calls.filter(([message]) => message.type === 'action')).toHaveLength(1);
    document.querySelector<HTMLButtonElement>('#dialog-cards button')!.click();
    expect(f.send).toHaveBeenLastCalledWith({
      type: 'action',
      id: 'match',
      phaseSeq: '3',
      decisionID: 'next',
      action,
    });
  });
  it('keeps surrender confirmation strict when only the global phase advances', () => {
    const f = fixture();
    f.renderer.confirmSurrender();
    const oldConfirm = document.querySelector<HTMLButtonElement>(
      '#dialog-actions button:last-child',
    )!;
    f.renderer.render({
      ...f.snapshot,
      home: { current: { ...f.snapshot.home.current, phaseSeq: '2' } },
    });
    oldConfirm.click();
    expect(f.send.mock.calls.filter(([message]) => message.type === 'surrender')).toHaveLength(0);
    f.renderer.confirmSurrender();
    document.querySelector<HTMLButtonElement>('#dialog-actions button:last-child')!.click();
    expect(f.send).toHaveBeenLastCalledWith({ type: 'surrender', id: 'match', phaseSeq: '2' });
  });
  it('rejects detached decision and surrender buttons after the window changes', () => {
    const f = fixture();
    const target = { ...f.view.hand[0], instance_id: 3 };
    const view = {
      ...f.view,
      choice: { kind: 'medic', cards: [target], rows: [], remaining: 1, can_quit: false },
      legal_actions: [{ kind: 'choose', card: 3 }],
    };
    const current = { ...f.snapshot.home.current, view };
    f.renderer.render({ ...f.snapshot, home: { current } });
    const oldChoice = document.querySelector<HTMLButtonElement>('#dialog-cards button')!;
    f.renderer.render({ ...f.snapshot, home: { current: { ...current, decisionID: 'next' } } });
    oldChoice.click();
    expect(
      f.send.mock.calls.filter(([message]) => ['action', 'surrender'].includes(message.type)),
    ).toHaveLength(0);
    f.renderer.confirmSurrender();
    const surrender = document.querySelector<HTMLButtonElement>(
      '#dialog-actions button:last-child',
    )!;
    f.renderer.render({ ...f.snapshot, home: { current: { ...current, decisionID: 'third' } } });
    surrender.click();
    expect(
      f.send.mock.calls.filter(([message]) => ['action', 'surrender'].includes(message.type)),
    ).toHaveLength(0);
  });
  it('restores the current target choice immediately after history, rules and card information close', () => {
    const f = fixture();
    const target = { ...f.view.hand[0], instance_id: 3 };
    const view = {
      ...f.view,
      choice: { kind: 'medic', cards: [target], rows: [], remaining: 1, can_quit: false },
      legal_actions: [{ kind: 'choose', card: 3 }],
    };
    f.renderer.render({ ...f.snapshot, home: { current: { ...f.snapshot.home.current, view } } });
    f.renderer.history({ items: [], nextCursor: null });
    document.querySelector<HTMLButtonElement>('#dialog-actions button:last-child')!.click();
    expect(document.getElementById('dialog-title')!.textContent).toContain('检查点恢复');
    expect(document.querySelectorAll('#dialog-cards button')).toHaveLength(1);
    f.renderer.arena.view.showDialog('规则', '说明', []);
    f.renderer.arena.view.closeDialog();
    expect(document.getElementById('dialog-title')!.textContent).toContain('检查点恢复');
    f.renderer.inspect([target], '卡牌详情');
    document.querySelector<HTMLButtonElement>('#dialog-actions button')!.click();
    expect(document.getElementById('dialog-title')!.textContent).toContain('检查点恢复');
    document.querySelector<HTMLButtonElement>('#dialog-cards button')!.click();
    expect(f.send.mock.calls.filter(([message]) => message.type === 'action')).toHaveLength(1);
  });
  it('shows recorded row scores and life changes, with honest fallback for older records', () => {
    const f = fixture();
    const record = {
      round: 1,
      scores: [15, 10],
      winner: 0,
      rows: [
        { row: 'close', scores: [8, 4] },
        { row: 'ranged', scores: [7, 6] },
        { row: 'siege', scores: [0, 0] },
      ],
      lives_before: [2, 2],
      lives_after: [2, 1],
    };
    f.renderer.roundSummary(record, f.view, 0);
    expect(document.getElementById('round-summary-rows')!.children).toHaveLength(3);
    expect(document.getElementById('round-summary-lives')!.textContent).toContain('2 → 1');
    f.renderer.roundSummary({ round: 1, scores: [15, 10], winner: 0 }, f.view, 0);
    expect(document.getElementById('round-summary-rows')!.children).toHaveLength(0);
    expect(document.getElementById('round-summary-lives')!.textContent).toContain('未保存生命变动');
  });
  it('keeps results, refund amounts and the return link reachable', () => {
    const f = fixture();
    f.renderer.showResult({
      outcome: 'win',
      prize: '12',
      refund: { balance: '0', gameBalance: '0' },
      view: f.view,
      you: 0,
      ai: { first_clear: true, reward: '3' },
    });
    expect(document.getElementById('result-overlay')!.hidden).toBe(false);
    expect(document.getElementById('result-subtitle')!.textContent).toContain('首次胜利奖励 3');
    expect(document.querySelector('#result-overlay a[href="/games"]')).not.toBeNull();
    document.getElementById('return-lobby')!.click();
    expect(document.getElementById('lobby')!.hidden).toBe(false);
  });
});

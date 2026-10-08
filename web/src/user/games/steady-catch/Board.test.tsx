import { act, fireEvent, render } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { CatchBoard } from './Board';
import { CatchAudio } from './audio';
import { newGame, advance, type Phrase } from './engine';
import { CatchSession, type Session } from './session';

const mocks = vi.hoisted(() => ({ language: 'zh', paint: vi.fn() }));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ i18n: { resolvedLanguage: mocks.language } }),
}));
vi.mock('./copy', async (original) => ({
  ...(await original<typeof import('./copy')>()),
  useCatchText: () => (zh: string) => zh,
}));
vi.mock('./render.mjs', () => ({
  createRenderer: () => ({ resize() {}, destroy() {}, paint: mocks.paint }),
}));
const phrases: Phrase[] = [
  { id: 'white', text: 'White', category: 'test', gold: false },
  { id: 'gold', text: 'Gold', category: 'test', gold: true },
];
let time = 0,
  frame: FrameRequestCallback;
beforeEach(() => {
  time = 0;
  mocks.language = 'zh';
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.stubGlobal(
    'requestAnimationFrame',
    vi.fn((callback: FrameRequestCallback) => {
      frame = callback;
      return 1;
    }),
  );
  vi.stubGlobal('cancelAnimationFrame', vi.fn());
});
function fixture(combo = 0, double = false, drops = false) {
  let authority: Session = {
    id: 'fixture',
    status: 'playing',
    revision: 1,
    state: newGame(1),
    payment: { game: '0', general: '0' },
    first_clear_reward: '0',
    first_clear: false,
    reward: '0',
    created_at: 1,
    expires_at: 1801,
    terminal_at: null,
    server_ms: 1000,
  };
  authority.state.spawn_in = 10000;
  authority.state.combo = combo;
  authority.state.effects.double = double ? 100 : 0;
  if (drops)
    authority.state.items = phrases.map((_, payload) => ({
      id: payload + 1,
      kind: 'phrase',
      payload,
      x: 300000,
      y: 414000,
      width: 152000,
      height: 62000,
      speed: 2000,
    }));
  const session = new CatchSession(
    authority,
    phrases,
    async (_, controls) => {
      authority = {
        ...authority,
        revision: authority.revision + 1,
        status: controls.action === 'pause' ? 'paused' : 'playing',
        state: advance(authority.state, controls.inputs, controls.until_tick, phrases),
      };
      return structuredClone(authority);
    },
    () => time,
  );
  session.active = true;
  return session;
}
async function tick(now: number) {
  time = now;
  await act(async () => {
    frame(now);
  });
}
function mount(session: CatchSession, onCatch = vi.fn()) {
  const audio = new CatchAudio();
  vi.spyOn(audio, 'beep');
  const view = () => (
    <CatchBoard session={session} phrases={phrases} audio={audio} onCatch={onCatch} />
  );
  return { ...render(view()), view, onCatch };
}
it('clears held input after a language rebuild and released key', async () => {
  const session = fixture();
  const screen = mount(session);
  fireEvent.keyDown(window, { key: 'd' });
  await tick(17);
  expect(session.state.direction).toBe(1);
  const position = session.state.x;
  mocks.language = 'en';
  screen.rerender(screen.view());
  fireEvent.keyUp(window, { key: 'd' });
  await tick(34);
  expect(session.state.direction).toBe(0);
  expect(session.state.x).toBe(position);
  fireEvent.keyDown(window, { key: 'd' });
  await tick(51);
  expect(session.state.x - position).toBe(9000);
});
it('keeps held keyboard movement ahead of mouse hover', async () => {
  const session = fixture();
  const screen = mount(session);
  const canvas = screen.container.querySelector('canvas')!;
  vi.spyOn(canvas, 'getBoundingClientRect').mockReturnValue({ left: 0, width: 600 } as DOMRect);
  const aim = vi.spyOn(session, 'aim');
  const hover = () => {
    const event = new MouseEvent('pointermove', { bubbles: true, clientX: 30 });
    Object.defineProperty(event, 'pointerType', { value: 'mouse' });
    fireEvent(canvas, event);
  };
  fireEvent.keyDown(window, { key: 'd' });
  hover();
  expect(aim).not.toHaveBeenCalled();
  await tick(17);
  expect(session.state.direction).toBe(1);
  fireEvent.keyUp(window, { key: 'd' });
  hover();
  expect(aim).toHaveBeenCalledWith(30000);
  await tick(34);
  expect(session.state.direction).toBe(0);
  expect(session.state.x).toBeLessThan(309000);
});
it.each([
  { combo: 0, double: false, points: [10, 20] },
  { combo: 3, double: true, points: [20, 80] },
])(
  'shows each true collection at combo $combo, double $double',
  async ({ combo, double, points }) => {
    const session = fixture(combo, double, true);
    const screen = mount(session);
    await tick(17);
    expect(screen.onCatch.mock.calls.map(([value]) => value)).toEqual([
      { id: 'white', text: 'White', points: points[0], combo: combo + 1, gold: false },
      { id: 'gold', text: 'Gold', points: points[1], combo: combo + 2, gold: true },
    ]);
    expect(mocks.paint.mock.calls.map((args) => args[3]?.text).filter(Boolean)).toEqual(
      points.map((p) => '+' + p),
    );
    expect(session.takeCollections()).toEqual([]);
    await tick(34);
    expect(screen.onCatch).toHaveBeenCalledTimes(2);
  },
);
it('preserves toast time while paused, then expires after active play', async () => {
  const session = fixture();
  const screen = mount(session);
  await tick(0);
  const toast = screen.container.querySelector('.toast')!;
  expect(toast).toHaveClass('visible');
  await act(async () => {
    await session.pause();
  });
  await tick(10000);
  expect(toast).toHaveClass('visible');
  await act(async () => {
    await session.resume();
  });
  for (let i = 1; i <= 60; i++) await tick(10000 + i * 45);
  expect(toast).not.toHaveClass('visible');
});

it('keeps the original sound and feedback when releasing a charged shield', async () => {
  const session = fixture();
  session.state.charge = 10;
  const audio = new CatchAudio();
  const beep = vi.spyOn(audio, 'beep');
  render(<CatchBoard session={session} phrases={phrases} audio={audio} onCatch={vi.fn()} />);
  fireEvent.keyDown(window, { key: ' ' });
  await tick(17);
  expect(beep).toHaveBeenCalledWith('prop', 0);
  expect(session.state.charge).toBe(0);
});

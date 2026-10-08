import { afterEach, describe, expect, it, vi } from 'vitest';
import phrases from '../../../../../internal/game/steadycatch/engine/phrases.json';
import { advance, newGame, LAST_TICK } from './engine';
import { CatchSession, type Controls, type Session } from './session';
function initial(): Session {
  return {
    id: 'sc_fixture',
    status: 'paused',
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
}
const settle = async () => {
  for (let i = 0; i < 8; i++) await Promise.resolve();
};
describe('catch preparation countdown', () => {
  afterEach(() => vi.useRealTimers());
  it('keeps preview controls local until all opening beats finish', async () => {
    vi.useFakeTimers();
    const value = initial();
    const send = vi.fn(async () => ({ ...value, status: 'playing' as const, revision: 2 }));
    const session = new CatchSession(value, phrases, send);
    const ready = session.beginCountdown();
    expect(session.countdown).toBe(3);
    session.move(1);
    session.frame();
    expect(session.previewX).toBeGreaterThan(value.state.x);
    expect(session.state).toEqual(value.state);
    await vi.advanceTimersByTimeAsync(1800);
    expect(session.countdown).toBe(0);
    expect(send).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(350);
    await ready;
    expect(send).toHaveBeenCalledExactlyOnceWith(value.id, {
      revision: 1,
      action: 'resume',
      until_tick: 0,
      inputs: [],
    });
    expect(session.active).toBe(true);
    expect(session.countdown).toBeNull();
  });
  it('cancels without dispatch, including a replacement countdown', async () => {
    vi.useFakeTimers();
    const value = initial();
    const send = vi.fn(async () => ({ ...value, status: 'playing' as const }));
    const session = new CatchSession(value, phrases, send);
    const first = session.beginCountdown();
    await vi.advanceTimersByTimeAsync(600);
    await session.pause();
    const second = session.beginCountdown(2);
    await vi.advanceTimersByTimeAsync(600);
    expect(session.countdown).toBe(1);
    await session.pause();
    await vi.advanceTimersByTimeAsync(3000);
    await Promise.all([first, second]);
    expect(send).not.toHaveBeenCalled();
    expect(session.active).toBe(false);
    expect(session.authority.status).toBe('paused');
    expect(session.countdown).toBeNull();
  });
});
describe('catch checkpoint coordination', () => {
  it('sends a local terminal tail after a delayed earlier checkpoint', async () => {
    let time = 0,
      authority = initial();
    authority.state.tick = LAST_TICK - 120;
    const writes: Controls[] = [];
    let release!: () => void;
    const first = new Promise<void>((resolve) => {
      release = resolve;
    });
    const session = new CatchSession(
      authority,
      phrases,
      async (_, request) => {
        writes.push(request);
        if (request.action === 'advance' && writes.length === 2) await first;
        const state = advance(authority.state, request.inputs, request.until_tick, phrases);
        authority = {
          ...authority,
          state,
          revision: authority.revision + 1,
          status: state.cause ? 'failed' : 'playing',
          terminal_at: state.cause ? 20 : null,
        };
        return structuredClone(authority);
      },
      () => time,
    );
    await session.resume();
    time = 1000;
    session.frame();
    await settle();
    time = 2000;
    session.frame();
    expect(session.active).toBe(false);
    expect(session.state.cause).toBeTruthy();
    expect(session.authority.state.tick).toBe(LAST_TICK - 120);
    release();
    await settle();
    expect(writes.map((w) => w.action)).toEqual(['resume', 'advance', 'advance']);
    expect(session.terminal).toBe(true);
    expect(session.state).toEqual(session.authority.state);
  });
  it('awaits both the current checkpoint and persisted pause before a quick menu resume', async () => {
    let time = 0,
      authority = initial();
    const writes: Controls[] = [];
    let releaseAdvance!: () => void, releasePause!: () => void;
    const advanceResponse = new Promise<void>((resolve) => {
      releaseAdvance = resolve;
    });
    const pauseResponse = new Promise<void>((resolve) => {
      releasePause = resolve;
    });
    const session = new CatchSession(
      authority,
      phrases,
      async (_, request) => {
        writes.push(request);
        if (request.action === 'advance') await advanceResponse;
        if (request.action === 'pause') await pauseResponse;
        authority = {
          ...authority,
          revision: authority.revision + 1,
          state: advance(authority.state, request.inputs, request.until_tick, phrases),
          status: request.action === 'pause' ? 'paused' : 'playing',
        };
        return structuredClone(authority);
      },
      () => time,
    );
    await session.resume();
    time = 1000;
    session.frame();
    await settle();
    let paused = false;
    const closeMenu = session.pause().then(async () => {
      paused = true;
      await session.resume();
    });
    await settle();
    expect(paused).toBe(false);
    expect(writes.map((w) => w.action)).toEqual(['resume', 'advance']);
    releaseAdvance();
    await settle();
    expect(paused).toBe(false);
    expect(writes.map((w) => w.action)).toEqual(['resume', 'advance', 'pause']);
    releasePause();
    await closeMenu;
    expect(writes.map((w) => w.action)).toEqual(['resume', 'advance', 'pause', 'resume']);
    expect(session.active).toBe(true);
    expect(session.authority.status).toBe('playing');
  });

  it('keeps rendering while a checkpoint is pending and pauses in order', async () => {
    let time = 0,
      authority = initial();
    const writes: Controls[] = [];
    let unblock: (() => void) | undefined;
    const session = new CatchSession(
      authority,
      phrases,
      async (_, request) => {
        writes.push(structuredClone(request));
        if (request.action === 'advance')
          await new Promise<void>((resolve) => {
            unblock = resolve;
          });
        authority = {
          ...authority,
          revision: authority.revision + 1,
          state: advance(authority.state, request.inputs, request.until_tick, phrases),
          status: request.action === 'pause' ? 'paused' : 'playing',
        };
        return structuredClone(authority);
      },
      () => time,
    );
    await session.resume();
    session.aim(500000);
    time = 1000;
    session.frame();
    await settle();
    expect(writes).toHaveLength(2);
    time = 2000;
    session.frame();
    expect(session.state.tick).toBe(120);
    expect(session.authority.state.tick).toBe(0);
    const paused = session.pause();
    expect(writes).toHaveLength(2);
    unblock!();
    await paused;
    await settle();
    expect(writes.map((r) => r.action)).toEqual(['resume', 'advance', 'pause']);
    expect(writes[2].inputs[0].tick).toBe(61);
    expect(authority.state.tick).toBe(120);
    expect(authority.status).toBe('paused');
    expect(session.active).toBe(false);
    expect(session.state).toEqual(authority.state);
  });
  it('replays an uncertain write byte for byte before pausing, without repeating controls', async () => {
    let time = 0,
      authority = initial(),
      lost = true;
    let savedRequest = '',
      savedResponse: Session | undefined;
    const writes: Controls[] = [];
    const session = new CatchSession(
      authority,
      phrases,
      async (_, request) => {
        writes.push(structuredClone(request));
        const body = JSON.stringify(request);
        if (body === savedRequest) return structuredClone(savedResponse!);
        authority = {
          ...authority,
          revision: authority.revision + 1,
          state: advance(authority.state, request.inputs, request.until_tick, phrases),
          status: request.action === 'pause' ? 'paused' : 'playing',
        };
        savedRequest = body;
        savedResponse = structuredClone(authority);
        if (request.action === 'advance' && lost) {
          lost = false;
          throw new Error('response lost');
        }
        return structuredClone(authority);
      },
      () => time,
    );
    await session.resume();
    time = 1000;
    session.frame();
    await settle();
    expect(session.error).toBeTruthy();
    expect(session.active).toBe(false);
    const tick = session.state.tick;
    time = 10000;
    session.frame();
    expect(session.state.tick).toBe(tick);
    await session.retry();
    expect(writes[1]).toEqual(writes[2]);
    expect(writes.map((r) => r.action)).toEqual(['resume', 'advance', 'advance', 'pause']);
    expect(session.error).toBeNull();
    expect(session.state).toEqual(authority.state);
    expect(authority.state.tick).toBe(60);
  });
  it('uses the confirmed revision to abandon without submitting a local score', async () => {
    let time = 0,
      authority = initial();
    const writes: Controls[] = [];
    const session = new CatchSession(
      authority,
      phrases,
      async (_, request) => {
        writes.push(request);
        authority = {
          ...authority,
          revision: authority.revision + 1,
          status: request.action === 'abandon' ? 'abandoned' : 'playing',
          terminal_at: request.action === 'abandon' ? 20 : null,
        };
        return authority;
      },
      () => time,
    );
    await session.resume();
    time = 500;
    session.frame();
    expect(session.state.tick).toBe(30);
    await session.abandon();
    expect(writes[1]).toEqual({ revision: 2, action: 'abandon', until_tick: 0, inputs: [] });
    expect(session.terminal).toBe(true);
    expect(session.state.tick).toBe(0);
  });
});

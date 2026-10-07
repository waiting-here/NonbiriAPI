import { describe, expect, it } from 'vitest';
import phrases from '../../../../../internal/game/steadycatch/engine/phrases.json';
import { advance, newGame } from './engine';
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
describe('catch checkpoint coordination', () => {
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
    expect(writes).toHaveLength(2);
    time = 2000;
    session.frame();
    expect(session.state.tick).toBe(120);
    expect(session.authority.state.tick).toBe(0);
    await session.pause();
    expect(writes).toHaveLength(2);
    unblock!();
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

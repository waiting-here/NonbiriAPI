import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { LakeController, type CastTransport } from './controller';
import { initialProfile, RULES_ID, start, step } from './rules';
import type { CastResult, CastView, CheckpointInput } from './api';

function fixture(ticks = 0): CastResult {
  const state = start(initialProfile(), () => 0, 1);
  for (let i = 0; i < ticks; i++) step(state.profile, state.cast, false);
  const cast: CastView = {
    id: 'lnc_AAAAAAAAAAAAAAAAAAAAAA',
    source_period_id: 'lnp_AAAAAAAAAAAAAAAAAAAAAA',
    rules_id: RULES_ID,
    generation: '1',
    revision: '1',
    ack_tick: state.cast.tick,
    phase: state.cast.phase as CastView['phase'],
    paused: false,
    readonly: false,
    state: state.cast,
    profile_revision: '1',
  };
  return {
    cast,
    profile: {
      readonly: false,
      revision: '1',
      rules_id: RULES_ID,
      profile: state.profile,
      wallet: { general_milli: '0', game_milli: '0' },
      settings: {
        revision: '1',
        ...{
          enabled: false,
          exchanges: {
            coins_to_general: { enabled: false, source_amount: '', target_amount: '' },
            general_to_coins: { enabled: false, source_amount: '', target_amount: '' },
            coins_to_game: { enabled: false, source_amount: '', target_amount: '' },
            game_to_coins: { enabled: false, source_amount: '', target_amount: '' },
          },
        },
      },
      cast,
    },
  };
}
function replay(result: CastResult, input: CheckpointInput): CastResult {
  const value = structuredClone(result);
  let held = input.initial_held;
  for (let tick = input.from_tick; tick <= input.to_tick; tick++) {
    const edge = input.edges.find((e) => e.tick === tick);
    if (edge) held = edge.held;
    step(value.profile.profile, value.cast.state, held);
    if (value.cast.state.phase === 'success' || value.cast.state.phase === 'failed') break;
  }
  value.cast.ack_tick = value.cast.state.tick;
  value.cast.phase = value.cast.state.phase as CastView['phase'];
  value.cast.revision = String(BigInt(value.cast.revision) + 1n);
  value.profile.revision = String(BigInt(value.profile.revision) + 1n);
  value.cast.profile_revision = value.profile.revision;
  value.profile.cast = value.cast;
  return value;
}
function transport(result: CastResult): CastTransport {
  return {
    checkpoint: vi.fn(async (_id, input) => (result = replay(result, input))),
    pause: vi.fn(async () => {
      result = structuredClone(result);
      result.cast.paused = true;
      result.cast.state.paused = true;
      result.cast.revision = String(BigInt(result.cast.revision) + 1n);
      return result;
    }),
    read: vi.fn(async () => result),
  };
}
describe('Lake cast control', () => {
  it('persists the unmount pause while suppressing late saves and adoption', async () => {
    const result = fixture(),
      saved = vi.fn(),
      notice = vi.fn(),
      server = transport(result);
    let confirmed = result;
    let finish!: (value: CastResult) => void;
    server.checkpoint = vi.fn(() =>
      new Promise<CastResult>((resolve) => {
        finish = resolve;
      }).then((value) => {
        confirmed = value;
        return value;
      }),
    );
    server.pause = vi.fn(async () => {
      confirmed = structuredClone(confirmed);
      confirmed.cast.paused = confirmed.cast.readonly = confirmed.cast.state.paused = true;
      confirmed.cast.state.held = false;
      return confirmed;
    });
    const controller = new LakeController(server, saved);
    controller.subscribe(notice);
    controller.adopt(result, true);
    for (let i = 0; i < 19; i++) controller.tick();
    const pending = controller.flush();
    notice.mockClear();
    controller.dispose();
    finish(replay(result, vi.mocked(server.checkpoint).mock.calls[0][1]));
    await pending;
    for (let i = 0; i < 4; i++) await Promise.resolve();
    expect(server.pause).toHaveBeenCalledOnce();
    expect(controller.snapshot().result?.cast.ack_tick).toBe(19);
    expect(controller.snapshot().status).toBe('paused');
    expect(saved).not.toHaveBeenCalled();
    expect(notice).not.toHaveBeenCalled();
    controller.adopt(result, true);
    controller.tick();
    expect(controller.snapshot().status).toBe('paused');
    expect(server.checkpoint).toHaveBeenCalledOnce();
  });
  it('does not adopt or publish a read that completes after the page leaves', async () => {
    const result = fixture(),
      saved = vi.fn(),
      notice = vi.fn(),
      server = transport(result);
    let finish!: (value: CastResult) => void;
    server.read = vi.fn(
      () =>
        new Promise<CastResult>((resolve) => {
          finish = resolve;
        }),
    );
    const controller = new LakeController(server, saved);
    controller.adopt(result);
    controller.subscribe(notice);
    const pending = controller.readCurrent();
    controller.dispose();
    finish(fixture(19));
    await pending;
    expect(controller.snapshot().result).toEqual(result);
    expect(saved).not.toHaveBeenCalled();
    expect(notice).not.toHaveBeenCalled();
    expect(server.pause).not.toHaveBeenCalled();
  });

  it('confirms a terminal tail accumulated while an earlier checkpoint was in flight', async () => {
    const result = fixture(240);
    result.cast.state.snapshot.hadCaught = true;
    result.cast.state.progress = 0.31;
    result.cast.state.barY = result.cast.state.snapshot.barHeight / 2;
    result.cast.state.barVelocity = 0;
    let release!: () => void;
    const heldResponse = new Promise<void>((resolve) => {
      release = resolve;
    });
    let confirmed = result;
    const server = transport(result);
    server.checkpoint = vi.fn(async (_id, input) => {
      if (vi.mocked(server.checkpoint).mock.calls.length === 1) await heldResponse;
      confirmed = replay(confirmed, input);
      return confirmed;
    });
    const controller = new LakeController(server);
    controller.adopt(result, true);
    controller.setHeld(true);
    for (let tick = 0; tick < 480; tick++) controller.tick();
    expect(server.checkpoint).toHaveBeenCalledTimes(1);
    expect(controller.projection()?.cast.phase).toBe('failed');
    expect(controller.queuedTicks()).toBeGreaterThan(120);
    release();
    await controller.flush();
    await Promise.resolve();
    expect(server.checkpoint).toHaveBeenCalledTimes(2);
    expect(controller.snapshot().status).toBe('terminal');
    expect(controller.queuedTicks()).toBe(0);
  });

  it('sends exact contiguous held edges and rebases to accepted replay', async () => {
    const result = fixture(),
      server = transport(result),
      controller = new LakeController(server);
    controller.adopt(result, true);
    for (let i = 0; i < 120; i++) {
      controller.setHeld(i >= 30 && i < 75);
      controller.tick();
    }
    await controller.flush();
    const call = vi.mocked(server.checkpoint).mock.calls[0];
    expect(call[1]).toMatchObject({
      from_tick: 1,
      to_tick: 120,
      initial_held: false,
      edges: [
        { tick: 31, held: true },
        { tick: 76, held: false },
      ],
    });
    expect(controller.projection()?.cast).toEqual(controller.snapshot().result?.cast.state);
    expect(controller.queuedTicks()).toBe(0);
  });
  it('retains the exact request after a lost confirmed response and pauses after recovery', async () => {
    const result = fixture();
    let confirmed = result,
      first = true;
    const server = transport(result);
    server.checkpoint = vi.fn(async (_id, input) => {
      if (first) {
        first = false;
        confirmed = replay(confirmed, input);
        throw new ApiError('network_error', 'Network response lost', 0);
      }
      return confirmed;
    });
    server.pause = vi.fn(async () => {
      confirmed = structuredClone(confirmed);
      confirmed.cast.paused = true;
      confirmed.cast.state.paused = true;
      return confirmed;
    });
    const saved = vi.fn(),
      controller = new LakeController(server, saved);
    controller.adopt(result, true);
    for (let i = 0; i < 120; i++) controller.tick();
    await expect(controller.flush()).rejects.toThrow();
    expect(controller.snapshot().status).toBe('unknown');
    await controller.retry();
    const calls = vi.mocked(server.checkpoint).mock.calls;
    expect(calls).toHaveLength(2);
    expect(calls[1]).toEqual(calls[0]);
    expect(server.pause).toHaveBeenCalledOnce();
    expect(controller.snapshot().status).toBe('paused');
    expect(saved).toHaveBeenCalledTimes(2);
    const tick = controller.projection()?.cast.tick;
    controller.setHeld(true);
    controller.tick();
    expect(controller.projection()?.cast.tick).toBe(tick);
  });
  it.each(['resume', 'closed'] as const)(
    'adopts a stopped checkpoint with no accepted ticks and recovery action %s',
    async (action) => {
      const result = fixture(19),
        recovery = structuredClone(result),
        server = transport(result),
        saved = vi.fn(),
        controller = new LakeController(server, saved);
      recovery.cast.paused = true;
      recovery.cast.readonly = true;
      recovery.cast.state.paused = true;
      recovery.cast.state.held = false;
      recovery.cast.revision = String(BigInt(result.cast.revision) + 1n);
      recovery.cast.recovery_action = action;
      recovery.profile.readonly = action === 'closed';
      let finish!: (value: CastResult) => void;
      server.checkpoint = vi.fn(
        () =>
          new Promise<CastResult>((resolve) => {
            finish = resolve;
          }),
      );
      controller.adopt(result, true);
      controller.setHeld(true);
      for (let i = 0; i < 19; i++) controller.tick();
      const pending = controller.flush();
      for (let i = 0; i < 7; i++) controller.tick();
      expect(vi.mocked(server.checkpoint).mock.calls[0][1]).toMatchObject({
        from_tick: 20,
        to_tick: 38,
      });
      expect(controller.queuedTicks()).toBe(26);
      finish(recovery);
      await pending;
      expect(controller.snapshot()).toEqual({ result: recovery, status: 'paused', error: null });
      expect(saved).toHaveBeenCalledExactlyOnceWith(recovery);
      expect(controller.queuedTicks()).toBe(0);
      expect(controller.projection()?.cast).toEqual(recovery.cast.state);
      controller.setHeld(true);
      controller.tick();
      await controller.flush();
      await controller.pause();
      await controller.retry();
      expect(controller.projection()?.cast).toEqual(recovery.cast.state);
      expect(server.checkpoint).toHaveBeenCalledOnce();
      expect(server.pause).not.toHaveBeenCalled();
      if (action === 'resume') {
        const resumed = structuredClone(recovery);
        resumed.cast.generation = '2';
        resumed.cast.revision = String(BigInt(recovery.cast.revision) + 1n);
        resumed.cast.paused = resumed.cast.readonly = resumed.cast.state.paused = false;
        delete resumed.cast.recovery_action;
        server.checkpoint = vi.fn(async (_id, input) => replay(resumed, input));
        controller.adopt(resumed, true);
        controller.tick();
        await controller.flush();
        expect(vi.mocked(server.checkpoint).mock.calls[0][1]).toMatchObject({
          generation: '2',
          expected_revision: resumed.cast.revision,
          from_tick: 20,
          to_tick: 20,
          initial_held: false,
          edges: [],
        });
        expect(controller.snapshot().result?.cast.ack_tick).toBe(20);
        expect(controller.snapshot().status).toBe('running');
      }
    },
  );
  it.each([
    { paused: false, tick: 19 },
    { paused: true, tick: 18 },
    { paused: true, tick: 21 },
  ])('rejects an invalid checkpoint acknowledgement %j', async ({ paused, tick }) => {
    const result = fixture(19),
      invalid = structuredClone(result),
      server = transport(result),
      saved = vi.fn(),
      controller = new LakeController(server, saved);
    invalid.cast.ack_tick = tick;
    invalid.cast.paused = invalid.cast.readonly = invalid.cast.state.paused = paused;
    server.checkpoint = vi.fn(async () => invalid);
    controller.adopt(result, true);
    controller.tick();
    await expect(controller.flush()).rejects.toMatchObject({ code: 'invalid_response' });
    expect(controller.snapshot().result).toEqual(result);
    expect(saved).not.toHaveBeenCalled();
  });
  it('keeps one request in flight and caps unconfirmed input at 480 ticks', async () => {
    const result = fixture(),
      server = transport(result);
    let finish!: (value: CastResult) => void;
    server.checkpoint = vi.fn(
      () =>
        new Promise<CastResult>((resolve) => {
          finish = resolve;
        }),
    );
    const controller = new LakeController(server);
    controller.adopt(result, true);
    for (let i = 0; i < 600; i++) controller.tick();
    expect(server.checkpoint).toHaveBeenCalledOnce();
    expect(controller.queuedTicks()).toBe(480);
    const input = vi.mocked(server.checkpoint).mock.calls[0][1];
    finish(replay(result, input));
    await controller.flush();
    expect(controller.queuedTicks()).toBe(360);
  });
  it('stops stale generations and does not pause casts observed from another device', async () => {
    const result = fixture(),
      server = transport(result),
      observer = new LakeController(server);
    observer.adopt(result);
    observer.dispose();
    expect(server.pause).not.toHaveBeenCalled();
    server.checkpoint = vi.fn(async () => {
      throw new ApiError('conflict', 'Control changed.', 409);
    });
    const controller = new LakeController(server);
    controller.adopt(result, true);
    for (let i = 0; i < 120; i++) controller.tick();
    await expect(controller.flush()).rejects.toThrow();
    expect(controller.snapshot().status).toBe('conflict');
    expect(controller.queuedTicks()).toBe(0);
    await controller.pause();
    expect(server.pause).not.toHaveBeenCalled();
  });
  it('saves queued ticks before clearing held input and pausing', async () => {
    const result = fixture(),
      server = transport(result),
      controller = new LakeController(server);
    controller.adopt(result, true);
    controller.setHeld(true);
    for (let i = 0; i < 19; i++) controller.tick();
    await controller.pause();
    expect(server.checkpoint).toHaveBeenCalledOnce();
    expect(vi.mocked(server.checkpoint).mock.calls[0][1].to_tick).toBe(19);
    expect(server.pause).toHaveBeenCalledOnce();
    expect(controller.snapshot().status).toBe('paused');
  });
});

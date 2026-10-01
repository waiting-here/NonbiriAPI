import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { LakeController, type CastTransport } from './controller';
import { initialProfile, RULES_ID, start, step } from './rules';
import type { CastResult, CastView, CheckpointInput } from './api';

function fixture(): CastResult {
  const state = start(initialProfile(), () => 0, 1);
  const cast: CastView = {
    id: 'lnc_AAAAAAAAAAAAAAAAAAAAAA',
    source_period_id: 'lnp_AAAAAAAAAAAAAAAAAAAAAA',
    rules_id: RULES_ID,
    generation: '1',
    revision: '1',
    ack_tick: 0,
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
      period: null,
      entitlement: null,
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

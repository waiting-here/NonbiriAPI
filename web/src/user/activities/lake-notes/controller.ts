import { operationKey, responseOutcomeUnknown } from '@shared/operations/api';
import { ApiError } from '@shared/query/http';
import { step, terminal, type Cast, type Profile } from './rules';
import {
  controlInput,
  terminalPhase,
  type CastResult,
  type CheckpointInput,
  type ControlInput,
} from './api';

export interface CastTransport {
  checkpoint(id: string, input: CheckpointInput, key: string): Promise<CastResult>;
  pause(id: string, input: ControlInput, key: string): Promise<CastResult>;
  read(id: string): Promise<CastResult>;
}
export type ControllerStatus =
  'readonly' | 'running' | 'saving' | 'paused' | 'unknown' | 'conflict' | 'terminal';
export interface ControllerSnapshot {
  result: CastResult | null;
  status: ControllerStatus;
  error: unknown;
}
type Attempt =
  | { kind: 'checkpoint'; id: string; input: CheckpointInput; key: string }
  | { kind: 'pause'; id: string; input: ControlInput; key: string };

/** One controller retains at most 480 unconfirmed ticks and one exact request intent. */
export class LakeController {
  private confirmed: CastResult | null = null;
  private predicted: { profile: Profile; cast: Cast } | null = null;
  private inputs: boolean[] = [];
  private held = false;
  private running = false;
  private controlling = false;
  private pausing = false;
  private stopped = false;
  private attempt: Attempt | null = null;
  private inFlight: Promise<void> | null = null;
  private listeners = new Set<() => void>();
  private value: ControllerSnapshot = { result: null, status: 'readonly', error: null };
  constructor(
    private transport: CastTransport,
    private saved: (result: CastResult) => void = () => undefined,
  ) {}
  snapshot = () => this.value;
  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  };
  projection = () => this.predicted;
  queuedTicks = () => this.inputs.length;
  private publish(status: ControllerStatus, error: unknown = null) {
    this.value = { result: this.confirmed, status, error };
    if (!this.stopped) this.listeners.forEach((fn) => fn());
  }
  adopt(result: CastResult, control = false) {
    if (this.inFlight) return;
    this.confirmed = structuredClone(result);
    this.inputs = [];
    this.attempt = null;
    this.held = false;
    this.pausing = false;
    this.controlling = control && !result.cast.paused && !terminalPhase(result.cast.phase);
    this.running = this.controlling;
    this.rebase();
    this.publish(
      terminalPhase(result.cast.phase)
        ? 'terminal'
        : this.running
          ? 'running'
          : result.cast.paused
            ? 'paused'
            : 'readonly',
    );
  }
  private rebase() {
    if (!this.confirmed) return;
    this.predicted = {
      profile: structuredClone(this.confirmed.profile.profile),
      cast: structuredClone(this.confirmed.cast.state),
    };
    if (!this.predicted.cast.paused)
      for (const held of this.inputs) {
        if (terminal(this.predicted.cast)) break;
        step(this.predicted.profile, this.predicted.cast, held);
      }
  }
  setHeld(held: boolean) {
    this.held = held && this.running && !this.pausing;
  }
  tick() {
    if (
      !this.running ||
      this.pausing ||
      !this.predicted ||
      terminal(this.predicted.cast) ||
      this.inputs.length >= 480
    )
      return;
    this.inputs.push(this.held);
    step(this.predicted.profile, this.predicted.cast, this.held);
    if (this.inputs.length >= 120 || terminal(this.predicted.cast))
      void this.flush().catch(() => undefined);
  }
  private nextAttempt(): Attempt | null {
    if (!this.confirmed || !this.inputs.length) return null;
    const cast = this.confirmed.cast,
      held = this.inputs.slice(0, 120);
    let previous = cast.state.held;
    const edges: CheckpointInput['edges'] = [];
    held.forEach((value, index) => {
      if (value !== previous) edges.push({ tick: cast.ack_tick + index + 1, held: value });
      previous = value;
    });
    return {
      kind: 'checkpoint',
      id: cast.id,
      key: operationKey(),
      input: {
        ...controlInput(cast),
        from_tick: cast.ack_tick + 1,
        to_tick: cast.ack_tick + held.length,
        initial_held: cast.state.held,
        edges,
      },
    };
  }
  private send(attempt: Attempt): Promise<void> {
    if (this.inFlight) return this.inFlight;
    this.attempt = attempt;
    this.publish('saving');
    const request = (async () => {
      try {
        const result =
          attempt.kind === 'checkpoint'
            ? await this.transport.checkpoint(attempt.id, attempt.input, attempt.key)
            : await this.transport.pause(attempt.id, attempt.input, attempt.key);
        if (result.cast.id !== attempt.id || result.cast.generation !== attempt.input.generation)
          throw new ApiError('conflict', 'Cast control changed.', 409);
        const oldTick = this.confirmed?.cast.ack_tick ?? 0;
        if (
          attempt.kind === 'checkpoint' &&
          (result.cast.ack_tick < attempt.input.from_tick ||
            result.cast.ack_tick > attempt.input.to_tick)
        )
          throw new ApiError('invalid_response', 'Invalid confirmed segment.', 200);
        this.inputs.splice(0, result.cast.ack_tick - oldTick);
        this.confirmed = structuredClone(result);
        this.attempt = null;
        if (terminalPhase(result.cast.phase) || result.cast.paused || result.cast.readonly) {
          this.inputs = [];
          this.running = false;
          this.controlling = false;
          this.held = false;
        }
        this.rebase();
        if (!this.stopped) this.saved(result);
        this.publish(
          terminalPhase(result.cast.phase)
            ? 'terminal'
            : result.cast.paused
              ? 'paused'
              : this.running
                ? 'running'
                : 'readonly',
        );
      } catch (error) {
        this.running = false;
        this.held = false;
        if (responseOutcomeUnknown(error)) this.publish('unknown', error);
        else {
          this.attempt = null;
          this.inputs = [];
          this.rebase();
          this.controlling = false;
          this.publish(
            error instanceof ApiError && error.status === 409 ? 'conflict' : 'paused',
            error,
          );
        }
        throw error;
      } finally {
        this.inFlight = null;
      }
    })();
    this.inFlight = request;
    return request;
  }
  async flush() {
    if (this.inFlight) return this.inFlight;
    if (this.attempt || (!this.running && !this.pausing)) return;
    const next = this.nextAttempt();
    if (next) await this.send(next);
  }
  async pause() {
    if (!this.controlling) return;
    this.held = false;
    this.pausing = true;
    if (this.inFlight) await this.inFlight;
    if (this.attempt) return;
    while (
      this.inputs.length &&
      this.confirmed &&
      !this.confirmed.cast.paused &&
      !terminalPhase(this.confirmed.cast.phase)
    )
      await this.flush();
    this.running = false;
    if (!this.confirmed || this.confirmed.cast.paused || terminalPhase(this.confirmed.cast.phase)) {
      this.pausing = false;
      return;
    }
    await this.send({
      kind: 'pause',
      id: this.confirmed.cast.id,
      input: controlInput(this.confirmed.cast),
      key: operationKey(),
    });
    this.pausing = false;
  }
  async retry() {
    if (!this.attempt) return;
    const previous = this.attempt;
    await this.send(previous);
    // A recovered connection never silently restarts input from an old device.
    this.running = false;
    this.held = false;
    await this.pause();
  }
  async readCurrent() {
    this.running = false;
    this.held = false;
    this.controlling = false;
    if (this.inFlight || !this.confirmed) return;
    const result = await this.transport.read(this.confirmed.cast.id);
    this.adopt(result, false);
    if (!this.stopped) this.saved(result);
  }
  dispose() {
    void this.pause().catch(() => undefined);
    this.stopped = true;
    this.listeners.clear();
  }
}

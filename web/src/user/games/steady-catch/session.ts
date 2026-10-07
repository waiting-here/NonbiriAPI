import {
  advance,
  HZ,
  LAST_TICK,
  type Input,
  type Phrase,
  type State,
  type CollectionEvent,
} from './engine';

export interface Session {
  id: string;
  status: 'playing' | 'paused' | 'completed' | 'failed' | 'abandoned' | 'cancelled';
  revision: number;
  state: State;
  payment: { general: string; game: string };
  first_clear_reward: string;
  first_clear: boolean;
  reward: string;
  created_at: number;
  expires_at: number;
  terminal_at: number | null;
  server_ms: number;
}
export interface Controls {
  revision: number;
  action: 'advance' | 'pause' | 'resume' | 'abandon';
  until_tick: number;
  inputs: Input[];
}
type Send = (id: string, value: Controls) => Promise<Session>;

// One ordered writer owns the local prediction and retains an exact request
// until its outcome is known. Rendering never waits for a checkpoint response.
export class CatchSession {
  authority: Session;
  state: State;
  active = false;
  busy = false;
  error: unknown = null;
  private listeners = new Set<() => void>();
  private inputs: Input[] = [];
  private collections: CollectionEvent[] = [];
  private pending: Controls | null = null;
  private inFlight: Promise<void> | null = null;
  private pauseWanted = false;
  private anchor = 0;
  private anchorTick = 0;
  private lastNotice = 0;
  private target: number;
  private direction = 0;
  private shieldWanted = false;

  constructor(
    initial: Session,
    private phrases: readonly Phrase[],
    private send: Send,
    private clock = () => performance.now(),
  ) {
    this.authority = initial;
    this.state = initial.state;
    this.target = initial.state.x;
  }
  subscribe(listener: () => void) {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }
  private notify() {
    for (const listener of this.listeners) listener();
  }
  get terminal() {
    return this.authority.terminal_at !== null;
  }
  aim(target: number) {
    this.target = Math.round(Math.max(0, Math.min(600000, target)));
    this.direction = 0;
  }
  move(direction: number) {
    if (direction === 0) this.target = this.state.x;
    this.direction = direction;
  }
  takeCollections() {
    const events = this.collections;
    this.collections = [];
    return events;
  }
  shield() {
    this.shieldWanted = true;
  }

  frame() {
    if (!this.active || this.error || this.terminal) return;
    const now = this.clock();
    const desired = Math.min(
      LAST_TICK,
      this.anchorTick + Math.floor(((now - this.anchor) * HZ) / 1000),
      this.authority.state.tick + 300,
    );
    const batch: Input[] = [];
    for (let tick = this.state.tick + 1; tick <= desired; tick++) {
      const input = {
        tick,
        target: this.target,
        direction: this.direction,
        ...(this.shieldWanted ? { shield: true } : {}),
      };
      this.shieldWanted = false;
      batch.push(input);
    }
    if (batch.length) {
      this.state = advance(this.state, batch, desired, this.phrases, (event) =>
        this.collections.push(event),
      );
      this.inputs.push(...batch.filter((input) => input.tick <= this.state.tick));
    }
    if (this.state.cause) this.active = false;
    if (!this.busy && (this.state.cause || this.state.tick - this.authority.state.tick >= HZ))
      void this.flush();
    if (now - this.lastNotice >= 100) {
      this.lastNotice = now;
      this.notify();
    }
  }

  async resume() {
    if (this.busy || this.error || this.terminal) return;
    if (this.authority.status === 'playing') {
      await this.pause();
      return;
    }
    this.pauseWanted = false;
    await this.transmit({
      revision: this.authority.revision,
      action: 'resume',
      until_tick: this.authority.state.tick,
      inputs: [],
    });
    if (
      !this.error &&
      !this.terminal &&
      !this.pauseWanted &&
      (this.authority.status as Session['status']) === 'playing'
    ) {
      this.anchor = this.clock();
      this.anchorTick = this.state.tick;
      this.active = true;
      this.notify();
    }
  }

  async pause() {
    this.active = false;
    this.pauseWanted = true;
    this.direction = 0;
    this.notify();
    if (this.inFlight) await this.inFlight;
    if (!this.error && !this.terminal && this.authority.status === 'playing') await this.flush();
  }

  async abandon() {
    if (this.busy || this.error || this.terminal) return;
    this.active = false;
    this.pauseWanted = false;
    this.inputs = [];
    this.state = this.authority.state;
    await this.transmit({
      revision: this.authority.revision,
      action: 'abandon',
      until_tick: this.authority.state.tick,
      inputs: [],
    });
  }

  async retry() {
    if (this.busy || !this.pending) return;
    this.error = null;
    this.pauseWanted = true;
    await this.transmit(this.pending);
  }

  private async flush() {
    if (this.busy || this.error || this.terminal) return;
    if (!this.pauseWanted && this.state.tick === this.authority.state.tick) return;
    await this.transmit({
      revision: this.authority.revision,
      action: this.pauseWanted ? 'pause' : 'advance',
      until_tick: this.state.tick,
      inputs: this.inputs.filter((input) => input.tick > this.authority.state.tick),
    });
  }

  private transmit(request: Controls): Promise<void> {
    if (this.inFlight) return this.inFlight;
    this.busy = true;
    this.pending = request;
    this.notify();
    this.inFlight = Promise.resolve().then(async () => {
      try {
        const next = await this.send(this.authority.id, request);
        this.authority = next;
        this.pending = null;
        this.error = null;
        this.inputs = this.inputs.filter((input) => input.tick > next.state.tick);
        if (this.terminal || next.status === 'paused' || request.action === 'resume') {
          this.state = next.state;
          this.inputs = [];
          this.active = false;
        }
      } catch (error) {
        this.active = false;
        this.pauseWanted = true;
        this.error = error;
      } finally {
        this.busy = false;
        this.inFlight = null;
        this.notify();
      }
      if (
        !this.error &&
        !this.terminal &&
        this.authority.status === 'playing' &&
        (this.pauseWanted ||
          this.state.tick - this.authority.state.tick >= HZ ||
          (this.state.cause && this.state.tick > this.authority.state.tick))
      )
        await this.flush();
    });
    return this.inFlight;
  }
}

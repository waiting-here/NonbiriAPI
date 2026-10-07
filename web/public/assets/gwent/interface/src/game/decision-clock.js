/* eslint-disable */
export const timingPolicy = Object.freeze({
  turnMs: 30000,
  mulliganMs: 20000,
  choiceMs: 15000,
  heartbeatMs: 5000,
  heartbeatTimeoutMs: 15000,
  reconnectGraceMs: 10000,
});

// Absolute deadlines: animation speed and background-tab tick frequency do not extend time.
export class DecisionClock {
  constructor({
    now = Date.now,
    schedule = (callback, delay) => setTimeout(callback, delay),
    cancel = (timer) => clearTimeout(timer),
    onChange = () => {},
  } = {}) {
    this.now = now;
    this.schedule = schedule;
    this.cancelTimer = cancel;
    this.onChange = onChange;
    this.generation = 0;
    this.active = null;
  }
  arm(phase, durationMs, expire) {
    this.stop();
    const token = ++this.generation;
    this.active = {
      phase,
      deadline: this.now() + Math.max(0, durationMs),
      expire,
      token,
    };
    this.tick(token);
    return token;
  }
  get phase() {
    return this.active?.phase;
  }
  get remainingMs() {
    return this.active ? Math.max(0, this.active.deadline - this.now()) : 0;
  }
  tick(token = this.active?.token) {
    if (!this.active || this.active.token !== token) return;
    this.cancelTimer(this.timer);
    if (this.remainingMs <= 0) {
      const expire = this.active.expire;
      this.active = null;
      this.onChange(null);
      expire();
      return;
    }
    this.onChange({
      phase: this.phase,
      remainingMs: this.remainingMs,
      deadline: this.active.deadline,
    });
    this.timer = this.schedule(() => this.tick(token), Math.min(250, this.remainingMs));
  }
  claim(token = this.active?.token) {
    if (!this.active || token !== this.active.token) return false;
    if (this.remainingMs <= 0) {
      this.tick(token);
      return false;
    }
    this.stop(token);
    return true;
  }
  stop(token) {
    if (token !== undefined && this.active?.token !== token) return;
    this.cancelTimer(this.timer);
    this.active = null;
    this.onChange(null);
  }
}

import { seedCommitForVersion } from './engine/canonical';
import { Engine } from './engine/engine';
import { decodeHex } from './engine/sha256';
import { DRAG_BUFFER, FIELD_HEIGHT, FIELD_WIDTH, MAX_INPUT_BYTES, MAX_INPUTS, TICKS_PER_SECOND, supportedVersions,
  type EngineState, type InputTuple, type ReplayResult } from './engine/types';
import { stars, validateInputs } from './engine/protocol';
import type { FatFishChallenge, FatFishChallengeTransport, FatFishSubmission } from './api';
import { bindCapability, capabilityHash, clearLocalCapability, deleteFishSession, freshKey,
  clearPendingCapability, clearReadOnlyChallenge, isReadOnlyChallenge, markReadOnlyChallenge,
  openFishDatabase, pendingCapability, pruneExpiredFishSessions, readCapability, readFishSession, requireLocalPlaySupport,
  writeFishSession, type StoredFishSession } from './storage';

export type PlayerPhase = 'idle' | 'prepared' | 'waiting' | 'running' | 'verifying' | 'terminal' | 'read_only' | 'error';
export interface FatFishPlayerSnapshot {
  phase: PlayerPhase;
  challenge: FatFishChallenge | null;
  state: EngineState | null;
  provisional: ReplayResult | null;
  error: string | null;
  canPlay: boolean;
}

export function targetTick(startAtMS: number, serverNowMS: number, monotonicAtReadMS: number,
  monotonicNowMS: number, durationSeconds: number): number {
  const elapsed = serverNowMS - startAtMS + monotonicNowMS - monotonicAtReadMS;
  return Math.max(0, Math.min(durationSeconds * TICKS_PER_SECOND,
    Math.floor(elapsed * TICKS_PER_SECOND / 1000)));
}
export function targetTickWithWall(startAtMS: number, serverNowMS: number,
  monotonicAtReadMS: number, monotonicNowMS: number, wallAtReadMS: number, wallNowMS: number,
  savedElapsedMS: number, savedWallMS: number, durationSeconds: number): number {
  const afterRead = Math.max(0, monotonicNowMS - monotonicAtReadMS, wallNowMS - wallAtReadMS);
  const fromServer = serverNowMS - startAtMS + afterRead;
  const fromSaved = savedElapsedMS > 0 ? savedElapsedMS + Math.max(0, wallNowMS - savedWallMS) : 0;
  return Math.max(0, Math.min(durationSeconds * TICKS_PER_SECOND,
    Math.floor(Math.max(fromServer, fromSaved) * TICKS_PER_SECOND / 1000)));
}
const terminalState = (state: string) =>
  state === 'settled_pass' || state === 'settled_fail' || state === 'abandoned' ||
  state === 'expired' || state === 'cancelled_refunded';

function validateChallengeVersion(view: FatFishChallenge): void {
  if (!supportedVersions(view.engine_version, view.scoring_version))
    throw new Error('The challenge uses an unsupported rules version.');
  if (view.level && (view.level.engine_version !== view.engine_version ||
      view.level.scoring_version !== view.scoring_version))
    throw new Error('The challenge rules version does not match its level.');
}

export class FatFishSessionController {
  private readonly transport: FatFishChallengeTransport;
  private readonly listeners = new Set<() => void>();
  private view: FatFishChallenge | null = null;
  private engine: Engine | null = null;
  private record: StoredFishSession | null = null;
  private database: IDBDatabase | null = null;
  private databaseOpening: Promise<IDBDatabase> | null = null;
  private initialWrite: Promise<void> | null = null;
  private clearPromise: Promise<void> | null = null;
  private capability = '';
  private phase: PlayerPhase = 'idle';
  private error: string | null = null;
  private owner = '';
  private releaseLock: (() => void) | null = null;
  private channel: BroadcastChannel | null = null;
  private permanentlyReadOnly = false;
  private disposed = false;
  private clearing = false;
  private anchorMonotonicMS = 0;
  private anchorWallMS = 0;
  private anchorServerNowMS = 0;
  private highestTick = 0;
  private inputBytes = 2;
  private inputCursor = 0;
  private inputRevision = 0;
  private durableRevision = 0;
  private pendingWrite: Promise<void> | null = null;
  private lastRecoveryAttemptMS = -Infinity;
  private writeTail: Promise<void> = Promise.resolve();
  private readonly inFlightFlushes = new Set<Promise<void>>();
  private autoSubmitAttempted = false;
  private actionTail: Promise<unknown> = Promise.resolve();

  constructor(transport: FatFishChallengeTransport) { this.transport = transport; }
  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
  private publish(): void { this.listeners.forEach((listener) => listener()); }
  snapshot(): FatFishPlayerSnapshot {
    return {
      phase: this.phase, challenge: this.view, state: this.engine?.state() ?? null,
      provisional: this.engine?.terminal ? this.engine.result() : null,
      error: this.error, canPlay: this.phase === 'running' && !this.engine?.terminal && !this.permanentlyReadOnly,
    };
  }
  private fail(error: unknown): never {
    this.error = error instanceof Error ? error.message : 'The challenge could not be continued.';
    this.phase = 'error';
    this.publish();
    throw error;
  }
  private async databaseReady(): Promise<IDBDatabase> {
    if (this.database) return this.database;
    if (!this.databaseOpening) {
      const opening = (async () => {
        const db = await openFishDatabase();
        try {
          await pruneExpiredFishSessions(db);
          if (this.disposed && !this.clearing && this.inFlightFlushes.size === 0)
            throw new Error('The challenge view has closed.');
          this.database = db;
          return db;
        } catch (error) { db.close(); throw error; }
      })();
      this.databaseOpening = opening;
      void opening.finally(() => { if (this.databaseOpening === opening) this.databaseOpening = null; })
        .catch(() => undefined);
    }
    return this.databaseOpening;
  }
  private assertOwner(): void {
    if (this.disposed || this.clearing || this.permanentlyReadOnly ||
        !this.releaseLock || !this.record || !this.view)
      throw new Error('This tab cannot control the challenge.');
  }
  private async acquire(id: string): Promise<boolean> {
    requireLocalPlaySupport();
    if (this.disposed || this.permanentlyReadOnly) return false;
    if (isReadOnlyChallenge(id)) {
      this.permanentlyReadOnly = true;
      this.phase = 'read_only';
      this.publish();
      return false;
    }
    this.owner = freshKey();
    const name = `nonbiri-fatfish:${id}`;
    const owned = await new Promise<boolean>((resolve, reject) => {
      navigator.locks.request(name, { ifAvailable: true, mode: 'exclusive' }, async (lock) => {
        if (!lock) { resolve(false); return; }
        await new Promise<void>((release) => { this.releaseLock = release; resolve(true); });
      }).catch(reject);
    });
    if (this.disposed) {
      this.releaseLock?.();
      this.releaseLock = null;
      return false;
    }
    if (!owned) {
      markReadOnlyChallenge(id);
      this.permanentlyReadOnly = true;
      this.phase = 'read_only';
      this.publish();
      return false;
    }
    this.channel = new BroadcastChannel(name);
    this.channel.onmessage = (event: MessageEvent<{ owner?: string; type?: string }>) => {
      if (event.data?.owner === this.owner) return;
      if (event.data?.type === 'hello') this.channel?.postMessage({ type: 'held', owner: this.owner });
      if (event.data?.type === 'held' && !this.releaseLock) {
        markReadOnlyChallenge(id);
        this.permanentlyReadOnly = true;
        this.phase = 'read_only';
        this.publish();
      }
    };
    this.channel.postMessage({ type: 'hello', owner: this.owner });
    return true;
  }
  private setView(view: FatFishChallenge): void {
    validateChallengeVersion(view);
    if (this.record && (view.id !== this.record.id || view.content_hash !== this.record.content_hash))
      throw new Error('The challenge content does not match its local record.');
    if (this.view && (view.engine_version !== this.view.engine_version ||
        view.scoring_version !== this.view.scoring_version || view.seed_commit !== this.view.seed_commit))
      throw new Error('The challenge version or seed commitment changed.');
    this.view = view;
    this.error = null;
    this.anchorServerNowMS = view.server_now_ms;
    this.anchorMonotonicMS = performance.now();
    this.anchorWallMS = Date.now();
    if (terminalState(view.state)) this.phase = 'terminal';
    else if (view.state === 'verifying') this.phase = 'verifying';
    else if (view.state === 'prepared') this.phase = 'prepared';
    else if (view.state === 'active') this.phase = 'waiting';
    this.publish();
  }
  prepare(): Promise<FatFishChallenge> {
    return this.queueAction(() => this.prepareSerial());
  }
  private async prepareSerial(): Promise<FatFishChallenge> {
    try {
      requireLocalPlaySupport();
      if (this.view) return this.view;
      const db = await this.databaseReady();
      const pending = pendingCapability(this.transport.prepareScope);
      let view: FatFishChallenge;
      try {
        view = await this.transport.prepare(capabilityHash(pending.capability), pending.key);
      } catch (error) {
        const current = await this.transport.current?.(pending.capability).catch(() => null);
        if (!current || current.state !== 'prepared') throw error;
        view = current;
      }
      if (view.state !== 'prepared' || !view.id || !/^[0-9a-f]{64}$/.test(view.content_hash))
        throw new Error('The server returned an invalid prepared challenge.');
      validateChallengeVersion(view);
      if (!await this.acquire(view.id)) throw new Error('This challenge is active in another tab.');
      this.capability = pending.capability;
      const record: StoredFishSession = {
        id: view.id, capability_hash: capabilityHash(this.capability), content_hash: view.content_hash,
        inputs: [], anchor_wall_ms: Date.now(), anchor_elapsed_ms: 0,
        start_key: '', submit_key: '', abandon_key: '', terminal_tick: null, accepted: false,
        accepted_at_ms: null,
        expires_at_ms: Date.now() + Math.max(0, view.prepare_until_ms - view.server_now_ms),
      };
      const initialWrite = writeFishSession(db, record);
      this.initialWrite = initialWrite;
      try { await initialWrite; }
      finally { if (this.initialWrite === initialWrite) this.initialWrite = null; }
      if (this.disposed) throw new Error('The challenge view has closed.');
      bindCapability(view.id, pending.capability, this.transport.prepareScope);
      this.record = record;
      this.setView(view);
      return view;
    } catch (error) {
      this.channel?.close(); this.channel = null;
      this.releaseLock?.(); this.releaseLock = null;
      return this.fail(error);
    }
  }
  recover(challengeID: string): Promise<FatFishChallenge> {
    return this.queueAction(() => this.recoverSerial(challengeID));
  }
  private async recoverSerial(challengeID: string): Promise<FatFishChallenge> {
    try {
      requireLocalPlaySupport();
      if (this.view) throw new Error('A challenge is already open in this tab.');
      this.capability = readCapability(challengeID);
      if (!await this.acquire(challengeID)) throw new Error('This challenge is active in another tab.');
      this.record = await readFishSession(await this.databaseReady(), challengeID);
      if (this.record.capability_hash !== capabilityHash(this.capability))
        throw new Error('The challenge capability does not match its local record.');
      const view = await this.transport.read(challengeID, this.capability);
      if (this.disposed) throw new Error('The challenge view has closed.');
      if (view.id !== challengeID || view.content_hash !== this.record.content_hash)
        throw new Error('The challenge content does not match its local record.');
      this.setView(view);
      if (terminalState(view.state)) { await this.clear(); return view; }
      if (view.state !== 'prepared') await this.installEngine(view);
      if (view.state === 'verifying' && this.record && this.record.submit_key && this.record.terminal_tick !== null &&
          !this.record.accepted) {
        this.record.accepted = true;
        this.record.accepted_at_ms = Date.now();
        this.record.expires_at_ms = Date.now() + 600000;
        await this.flush();
      }
      return view;
    } catch (error) {
      this.channel?.close();
      this.channel = null;
      this.releaseLock?.();
      this.releaseLock = null;
      this.permanentlyReadOnly = true;
      return this.fail(error);
    }
  }
  start(): Promise<FatFishChallenge> {
    return this.queueAction(() => this.startSerial());
  }
  private async startSerial(): Promise<FatFishChallenge> {
    try {
      this.assertOwner();
      if (this.view?.state === 'active') return this.view;
      if (this.view?.state !== 'prepared') throw new Error('The challenge is not prepared.');
      const record = this.record!;
      if (!record.start_key) record.start_key = freshKey();
      await this.flush();
      let view: FatFishChallenge;
      try { view = await this.transport.start(record.id, this.capability, record.start_key); }
      catch (error) {
        const current = await this.transport.read(record.id, this.capability).catch(() => null);
        if (!current || current.state !== 'active') throw error;
        view = current;
      }
      if (view.state !== 'active') throw new Error('The challenge did not start.');
      if (this.disposed) throw new Error('The challenge view has closed.');
      this.setView(view);
      record.expires_at_ms = Date.now() + Math.max(0, (view.submit_until_ms ?? view.server_now_ms) - view.server_now_ms);
      await this.flush();
      await this.installEngine(view);
      return view;
    } catch (error) {
      this.error = error instanceof Error ? error.message : String(error);
      if (this.view?.state === 'prepared') this.phase = 'prepared';
      else this.phase = 'error';
      this.publish();
      throw error;
    }
  }
  private async installEngine(view: FatFishChallenge): Promise<void> {
    const record = this.record;
    if (!record || !view.level || !view.seed || view.start_at_ms === null || view.end_at_ms === null ||
        view.submit_until_ms === null) throw new Error('The server did not provide valid recovery material.');
    const seed = decodeHex(view.seed);
    const engine = new Engine(view.level, seed);
    if (seed.length !== 32 || engine.contentHash !== view.content_hash ||
        seedCommitForVersion(view.id, view.period_id ?? view.version_id, view.node_id ?? view.version_id,
          view.content_hash, view.engine_version, view.scoring_version, seed) !== view.seed_commit)
      throw new Error('The challenge version or seed commitment changed.');
    validateInputs(record.inputs, engine.level);
    this.engine = engine;
    this.inputCursor = 0;
    this.inputBytes = JSON.stringify(record.inputs).length;
    const last = record.inputs.at(-1)?.[0] ?? -1;
    const due = this.dueTick();
    if (last > due) throw new Error('The saved input timeline is ahead of the server clock.');
    await this.catchUp();
    if (view.state === 'verifying') this.phase = 'verifying';
    else if (!engine.terminal) this.phase = due === 0 && view.server_now_ms < view.start_at_ms ? 'waiting' : 'running';
    this.publish();
  }
  private dueTick(): number {
    if (!this.view || !this.engine || this.view.start_at_ms === null) return 0;
    const tick = targetTickWithWall(this.view.start_at_ms, this.anchorServerNowMS,
      this.anchorMonotonicMS, performance.now(), this.anchorWallMS, Date.now(),
      this.record?.anchor_elapsed_ms ?? 0, this.record?.anchor_wall_ms ?? Date.now(),
      this.engine.level.duration_seconds);
    this.highestTick = Math.max(this.highestTick, tick);
    return this.highestTick;
  }
  advance(maxTicks = 64): void {
    if (!this.engine || !this.record || this.phase === 'read_only' || this.phase === 'error' || this.disposed) return;
    const target = this.dueTick();
    if (this.view?.start_at_ms !== null && this.anchorServerNowMS +
        Math.max(0, performance.now() - this.anchorMonotonicMS, Date.now() - this.anchorWallMS) < this.view!.start_at_ms! && target === 0) {
      this.phase = 'waiting';
      return;
    }
    if (this.phase === 'waiting') this.phase = 'running';
    if (this.durableRevision < this.inputRevision) {
      if (!this.pendingWrite) void this.flush().catch(() => undefined);
      return;
    }
    let count = 0;
    while (!this.engine.terminal && this.engine.tick < target && count++ < maxTicks) {
      const start = this.inputCursor;
      let end = start;
      while (end < this.record.inputs.length && this.record.inputs[end][0] === this.engine.tick) end++;
      try { this.engine.step(this.record.inputs.slice(start, end)); this.inputCursor = end; }
      catch (error) {
        this.error = error instanceof Error ? error.message : 'The saved input timeline is invalid.';
        this.phase = 'error';
        this.publish();
        return;
      }
    }
    if (this.engine.terminal) {
      this.record.terminal_tick = this.engine.tick;
      if (this.transport.autoSubmit && !this.autoSubmitAttempted && !this.record.accepted && this.view?.state === 'active') {
        this.autoSubmitAttempted = true;
        void this.submit().catch(() => undefined);
      }
      if (this.phase !== 'verifying') this.phase = 'running';
    }
    if (count) this.publish();
    if (this.view?.submit_until_ms !== null && this.view?.submit_until_ms !== undefined &&
        !this.record.accepted && this.anchorServerNowMS +
        Math.max(0, performance.now() - this.anchorMonotonicMS, Date.now() - this.anchorWallMS) > this.view.submit_until_ms) {
      void this.clear().catch(() => undefined);
      this.phase = 'error';
      this.error = 'The submission window has ended.';
      this.publish();
    }
  }
  private async catchUp(): Promise<void> {
    while (this.engine && !this.engine.terminal && this.engine.tick < this.dueTick()) {
      if (this.disposed) throw new Error('The challenge view has closed.');
      if (this.phase === 'error') throw new Error(this.error ?? 'The challenge cannot continue.');
      if (this.durableRevision < this.inputRevision) await this.flush();
      if (this.disposed) throw new Error('The challenge view has closed.');
      this.advance(128);
      if (!this.engine.terminal && this.engine.tick < this.dueTick())
        await new Promise<void>((resolve) => setTimeout(resolve, 0));
    }
    if (this.disposed) throw new Error('The challenge view has closed.');
  }
  private queueAction<T>(action: () => Promise<T>): Promise<T> {
    const guarded = () => {
      if (this.disposed) throw new Error('The challenge view has closed.');
      return action();
    };
    const run = this.actionTail.then(guarded, guarded);
    this.actionTail = run.catch(() => undefined);
    return run;
  }
  private async recordInput(input: InputTuple): Promise<void> {
    this.assertOwner();
    await this.catchUp();
    if (this.phase === 'error') throw new Error(this.error ?? 'The challenge cannot continue.');
    if (!this.engine || !this.record || this.engine.terminal || this.view?.state !== 'active' || this.record.accepted)
      throw new Error('The challenge is no longer accepting actions.');
    if (this.view.start_at_ms === null ||
        this.anchorServerNowMS + performance.now() - this.anchorMonotonicMS < this.view.start_at_ms)
      throw new Error('The challenge has not started yet.');
    const tick = this.engine.tick;
    if (tick >= this.engine.level.duration_seconds * TICKS_PER_SECOND) throw new Error('Time is up.');
    input[0] = tick;
    const inputs = this.record.inputs;
    const previous = inputs.at(-1);
    const oldBytes = this.inputBytes;
    let replaced = false;
    if (input[2] === 'place' && previous?.[0] === tick && previous[2] === 'place' && previous[3] === input[3]) {
      input[1] = previous[1];
      this.inputBytes -= JSON.stringify(previous).length;
      inputs[inputs.length - 1] = input;
      this.inputBytes += JSON.stringify(input).length;
      replaced = true;
    } else {
      input[1] = previous?.[0] === tick ? previous[1] + 1 : 0;
      if (inputs.length >= MAX_INPUTS) throw new Error('The input log is full.');
      const separator = inputs.length > 0 ? 1 : 0;
      inputs.push(input);
      this.inputBytes += JSON.stringify(input).length + separator;
    }
    try {
      if (this.inputBytes > MAX_INPUT_BYTES) throw new Error('The input log is full.');
      validateInputs(inputs.slice(-3), this.engine.level);
    } catch (error) {
      if (replaced && previous) inputs[inputs.length - 1] = previous;
      else inputs.pop();
      this.inputBytes = oldBytes;
      throw error;
    }
    this.inputRevision++;
    this.publish();
  }
  place(toolID: number, x: number, y: number): Promise<void> {
    return this.queueAction(async () => {
      if (!Number.isSafeInteger(x) || !Number.isSafeInteger(y) ||
          x < -DRAG_BUFFER || x > FIELD_WIDTH + DRAG_BUFFER ||
          y < -DRAG_BUFFER || y > FIELD_HEIGHT + DRAG_BUFFER)
        throw new Error('The tool position is outside the play area.');
      await this.recordInput([0, 0, 'place', toolID, x, y]);
    });
  }
  returnTool(toolID: number): Promise<void> {
    return this.queueAction(() => this.recordInput([0, 0, 'return', toolID]));
  }
  finish(): Promise<void> {
    return this.queueAction(async () => {
      if (!this.engine) throw new Error('The challenge is not running.');
      await this.catchUp();
      const current = this.engine.state();
      const fed = current.fish.filter((fish) => fish.status === 'fed').length;
      if (!stars(this.engine.level.thresholds, fed, this.engine.level.bowls, current.bowls)[1])
        throw new Error('The minimum fish and bowl goals have not been met.');
      await this.recordInput([0, 0, 'finish', 0]);
      this.advance(1);
      await this.flush();
    });
  }
  async flush(): Promise<void> {
    this.assertOwner();
    const record = this.record!;
    record.anchor_wall_ms = Date.now();
    record.anchor_elapsed_ms = Math.max(0, Math.floor(this.highestTick * 1000 / TICKS_PER_SECOND));
    const snapshot = structuredClone(record);
    const revision = this.inputRevision;
    const write = this.writeTail.catch(() => undefined).then(async () => {
      const db = await this.databaseReady();
      await writeFishSession(db, snapshot);
    });
    this.writeTail = write;
    this.pendingWrite = write;
    this.inFlightFlushes.add(write);
    try {
      await write;
      this.durableRevision = Math.max(this.durableRevision, revision);
    } catch (failure) {
      this.error = failure instanceof Error ? failure.message : 'The local challenge record could not be saved.';
      this.phase = 'error';
      this.publish();
      throw failure;
    } finally {
      this.inFlightFlushes.delete(write);
      if (this.pendingWrite === write) this.pendingWrite = null;
    }
    if (this.durableRevision < this.inputRevision) await this.flush();
  }
  async submit(): Promise<FatFishChallenge> {
    return this.queueAction(async () => {
      try {
        this.assertOwner();
        await this.catchUp();
        const record = this.record!;
        if (!this.engine?.terminal || record.terminal_tick === null)
          throw new Error('Finish the challenge before submitting.');
        if (!record.submit_key) record.submit_key = freshKey();
        await this.flush();
        const payload: FatFishSubmission = {
          tab_capability: this.capability, inputs: structuredClone(record.inputs),
          terminal_tick: record.terminal_tick,
        };
        let view: FatFishChallenge;
        try { view = await this.transport.submit(record.id, payload, record.submit_key); }
        catch (error) {
          const current = await this.transport.read(record.id, this.capability).catch(() => null);
          if (!current || current.state !== 'verifying' && !terminalState(current.state)) throw error;
          view = current;
        }
        if (this.disposed) throw new Error('The challenge view has closed.');
        this.setView(view);
        if (terminalState(view.state)) await this.clear();
        else if (view.state === 'verifying') {
          record.accepted = true;
          record.accepted_at_ms ??= Date.now();
          record.expires_at_ms = record.accepted_at_ms + 600000;
          await this.flush();
        }
        return view;
      } catch (error) { return this.fail(error); }
    });
  }
  async poll(): Promise<FatFishChallenge | null> {
    return this.queueAction(async () => {
      const record = this.record;
      if (!record || !this.view || this.disposed || this.permanentlyReadOnly || this.clearing) return this.view;
      let view = await this.transport.read(record.id, this.capability);
      if (this.disposed || this.clearing || this.record !== record) return this.view;
      if (view.state === 'verifying' && record.accepted && record.submit_key &&
          record.terminal_tick !== null && record.accepted_at_ms !== null &&
          Date.now() <= record.accepted_at_ms + 600000 &&
          performance.now() - this.lastRecoveryAttemptMS >= 10000) {
        this.lastRecoveryAttemptMS = performance.now();
        try {
          view = await this.transport.submit(record.id, {
            tab_capability: this.capability, inputs: structuredClone(record.inputs),
            terminal_tick: record.terminal_tick,
          }, record.submit_key);
        } catch { /* The accepted digest stays available for the next bounded retry. */ }
        if (this.disposed || this.clearing || this.record !== record) return this.view;
      }
      this.setView(view);
      if (terminalState(view.state)) await this.clear();
      else if (record.accepted && record.accepted_at_ms !== null &&
               Date.now() > record.accepted_at_ms + 600000) {
        await this.clear();
        this.phase = 'error';
        this.error = 'The accepted verification recovery window has ended.';
        this.publish();
      }
      return view;
    });
  }
  async abandon(): Promise<FatFishChallenge> {
    return this.queueAction(async () => {
      this.assertOwner();
      if (!this.transport.abandon) throw new Error('Abandon is unavailable in this mode.');
      const record = this.record!;
      if (!record.abandon_key) {
        record.abandon_key = freshKey();
        record.abandon_revision = this.view?.revision;
      }
      await this.flush();
      let view: FatFishChallenge;
      try { view = await this.transport.abandon(record.id, this.capability, record.abandon_key, record.abandon_revision); }
      catch (error) {
        const current = await this.transport.read(record.id, this.capability).catch(() => null);
        if (!current || !terminalState(current.state)) throw error;
        view = current;
      }
      if (this.disposed) throw new Error('The challenge view has closed.');
      this.setView(view);
      if (terminalState(view.state)) await this.clear();
      return view;
    });
  }
  private clear(): Promise<void> {
    if (this.clearPromise) return this.clearPromise;
    if (!this.record) return Promise.resolve();
    this.clearing = true;
    const id = this.record.id;
    const cleanup = (async () => {
      await this.writeTail.catch(() => undefined);
      let cleanupError: unknown;
      try { await deleteFishSession(await this.databaseReady(), id); }
      catch (error) { cleanupError = error; }
      clearLocalCapability(id);
      clearPendingCapability(this.transport.prepareScope);
      clearReadOnlyChallenge(id);
      this.record = null;
      this.capability = '';
      if (cleanupError) throw cleanupError;
    })();
    const finished = cleanup.finally(() => { this.clearing = false; this.clearPromise = null; });
    this.clearPromise = finished;
    return finished;
  }
  dispose(): void {
    if (this.disposed) return;
    const finalFlush = this.record && this.releaseLock && !this.clearing &&
      this.inputRevision > this.durableRevision ? this.flush().catch(() => undefined) : null;
    const drains: Promise<unknown>[] = [this.writeTail, ...this.inFlightFlushes];
    if (this.initialWrite) drains.push(this.initialWrite);
    if (this.clearPromise) drains.push(this.clearPromise);
    if (finalFlush) drains.push(finalFlush);
    this.disposed = true;
    const channel = this.channel, release = this.releaseLock;
    this.channel = null; this.releaseLock = null;
    void Promise.allSettled(drains).then(() => {
      channel?.close(); release?.(); this.database?.close(); this.database = null;
    });
    this.listeners.clear();
  }
}

export function createFatFishSessionController(transport: FatFishChallengeTransport): FatFishSessionController {
  return new FatFishSessionController(transport);
}

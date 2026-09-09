import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createGameSound, type GameSoundCue } from './sound';

class FakeParam {
  static failNextSetValueAtTime = false;
  static failNextLinearRamp = false;
  private currentValue = 0;
  readonly immediateWrites: number[] = [];
  readonly events: { readonly kind: 'set' | 'ramp'; readonly value: number; readonly at: number }[] =
    [];

  get value(): number {
    return this.currentValue;
  }

  set value(value: number) {
    this.currentValue = value;
    this.immediateWrites.push(value);
  }

  setValueAtTime(value: number, at: number): void {
    if (FakeParam.failNextSetValueAtTime) {
      FakeParam.failNextSetValueAtTime = false;
      throw new Error('setValueAtTime unavailable');
    }
    this.currentValue = value;
    this.events.push({ kind: 'set', value, at });
  }

  linearRampToValueAtTime(value: number, at: number): void {
    if (FakeParam.failNextLinearRamp) {
      FakeParam.failNextLinearRamp = false;
      throw new Error('linearRampToValueAtTime unavailable');
    }
    this.currentValue = value;
    this.events.push({ kind: 'ramp', value, at });
  }
}

class FakeNode {
  readonly connections: unknown[] = [];
  disconnectCalls = 0;

  connect(target: unknown): void {
    this.connections.push(target);
  }

  disconnect(): void {
    this.disconnectCalls += 1;
    this.connections.length = 0;
  }
}

class FakeGain extends FakeNode {
  readonly gain = new FakeParam();
}

class FakeOscillator extends FakeNode {
  type: OscillatorType = 'sine';
  readonly frequency = new FakeParam();
  readonly startTimes: number[] = [];
  readonly stopTimes: number[] = [];
  onended: (() => void) | null = null;

  start(at = 0): void {
    this.startTimes.push(at);
  }

  stop(at = 0): void {
    this.stopTimes.push(at);
  }
}

class FakeWaveShaper extends FakeNode {
  curve: Float32Array | null = null;
  oversample: OverSampleType = 'none';
}

class FakeAudioContext {
  static instances: FakeAudioContext[] = [];
  static resumeMode: 'resolve' | 'reject' | 'pending' = 'resolve';
  static readonly pendingResumes: (() => void)[] = [];

  readonly destination = new FakeNode();
  readonly gains: FakeGain[] = [];
  readonly oscillators: FakeOscillator[] = [];
  readonly waveshapers: FakeWaveShaper[] = [];
  readonly resume = vi.fn(() => {
    if (FakeAudioContext.resumeMode === 'reject') return Promise.reject(new Error('audio denied'));
    if (FakeAudioContext.resumeMode === 'pending')
      return new Promise<void>((resolve) => FakeAudioContext.pendingResumes.push(resolve));
    this.state = 'running';
    return Promise.resolve();
  });
  readonly close = vi.fn(() => {
    this.state = 'closed';
    return Promise.resolve();
  });
  currentTime = 10;
  state: AudioContextState = 'suspended';

  constructor() {
    FakeAudioContext.instances.push(this);
  }

  createGain(): FakeGain {
    const gain = new FakeGain();
    this.gains.push(gain);
    return gain;
  }

  createOscillator(): FakeOscillator {
    const oscillator = new FakeOscillator();
    this.oscillators.push(oscillator);
    return oscillator;
  }

  createWaveShaper(): FakeWaveShaper {
    if (FakeAudioContext.failWaveShaper) throw new Error('limiter unavailable');
    const waveshaper = new FakeWaveShaper();
    this.waveshapers.push(waveshaper);
    return waveshaper;
  }

  static failWaveShaper = false;
  static resolvePendingResumes(): void {
    for (const resolve of FakeAudioContext.pendingResumes.splice(0)) resolve();
  }
}

const allCues: readonly GameSoundCue[] = [
  'select',
  'link_match',
  'link_shuffle',
  'fishing_common',
  'fishing_rare',
  'fishing_epic',
  'phase',
  'follow',
  'win',
  'loss',
  'tie',
  'end',
];

function installAudioContext(): void {
  vi.stubGlobal('AudioContext', FakeAudioContext as unknown as typeof AudioContext);
}

function activeOscillators(context: FakeAudioContext): FakeOscillator[] {
  return context.oscillators.filter(
    (oscillator) => oscillator.startTimes.length === 1 && oscillator.stopTimes.length === 1,
  );
}

describe('shared game sound engine', () => {
  beforeEach(() => {
    FakeAudioContext.instances = [];
    FakeAudioContext.resumeMode = 'resolve';
    FakeAudioContext.failWaveShaper = false;
    FakeParam.failNextSetValueAtTime = false;
    FakeParam.failNextLinearRamp = false;
    FakeAudioContext.pendingResumes.length = 0;
    installAudioContext();
  });

  afterEach(() => {
    FakeAudioContext.pendingResumes.length = 0;
  });

  it('creates no context until a user gesture unlocks it and shares concurrent unlock work', async () => {
    const sound = createGameSound();
    sound.play('select');
    expect(FakeAudioContext.instances).toHaveLength(0);

    const first = sound.unlock();
    const second = sound.unlock();
    expect(second).toBe(first);
    await Promise.all([first, second]);

    expect(FakeAudioContext.instances).toHaveLength(1);
    expect(FakeAudioContext.instances[0].resume).toHaveBeenCalledTimes(1);
    expect(FakeAudioContext.instances[0].gains[0].gain.value).toBe(0.18);
    expect(FakeAudioContext.instances[0].gains[1].gain.value).toBe(1);
    expect(FakeAudioContext.instances[0].gains[0].connections[0]).toBe(
      FakeAudioContext.instances[0].destination,
    );
    expect(FakeAudioContext.instances[0].gains[1].connections[0]).toBe(
      FakeAudioContext.instances[0].waveshapers[0],
    );
    expect(FakeAudioContext.instances[0].waveshapers[0].curve?.[0]).toBe(-1);
    expect(FakeAudioContext.instances[0].waveshapers[0].curve?.at(-1)).toBe(1);
    expect(
      FakeAudioContext.instances[0].gains.every((gain) => gain.gain.immediateWrites.length === 0),
    ).toBe(true);
    expect(
      FakeAudioContext.instances[0].oscillators.every(
        (oscillator) => oscillator.frequency.immediateWrites.length === 0,
      ),
    ).toBe(true);
    sound.play('select');
    expect(FakeAudioContext.instances[0].oscillators).toHaveLength(1);
  });

  it('keeps unavailable, rejected, and late-closed audio paths silent and resolved', async () => {
    vi.stubGlobal('AudioContext', undefined);
    const unavailable = createGameSound();
    await expect(unavailable.unlock()).resolves.toBeUndefined();
    expect(() => unavailable.play('win')).not.toThrow();

    installAudioContext();
    FakeAudioContext.resumeMode = 'reject';
    const rejected = createGameSound();
    await expect(rejected.unlock()).resolves.toBeUndefined();
    expect(FakeAudioContext.instances[0].close).toHaveBeenCalledTimes(1);
    expect(() => rejected.play('end')).not.toThrow();

    FakeAudioContext.resumeMode = 'pending';
    const late = createGameSound();
    const unlocking = late.unlock();
    late.close();
    FakeAudioContext.resolvePendingResumes();
    await expect(unlocking).resolves.toBeUndefined();
    late.play('select');
    expect(FakeAudioContext.instances[1].oscillators).toHaveLength(0);
    expect(FakeAudioContext.instances[1].close).toHaveBeenCalledTimes(1);
  });

  it('uses smooth envelopes, bounded cue durations, and distinct terminal patterns', async () => {
    const durations = new Map<GameSoundCue, number>();
    const signatures = new Map<GameSoundCue, string>();
    for (const cue of allCues) {
      const sound = createGameSound();
      await sound.unlock();
      const context = FakeAudioContext.instances.at(-1)!;
      sound.play(cue);
      const starts = context.oscillators.flatMap((oscillator) => oscillator.startTimes);
      const stops = context.oscillators.flatMap((oscillator) => oscillator.stopTimes);
      durations.set(cue, Math.max(...stops) - Math.min(...starts));
      signatures.set(
        cue,
        context.oscillators
          .map((oscillator) => `${oscillator.type}:${oscillator.frequency.value}`)
          .join('|'),
      );
      expect(context.gains.slice(2).every((gain) =>
        gain.gain.events.some((event) => event.kind === 'ramp' && event.value > 0),
      )).toBe(true);
      expect(context.gains.slice(2).every((gain) =>
        gain.gain.events.some((event) => event.kind === 'ramp' && event.value === 0),
      )).toBe(true);
      expect(context.gains.every((gain) => gain.gain.immediateWrites.length === 0)).toBe(true);
      expect(
        context.oscillators.every((oscillator) => oscillator.frequency.immediateWrites.length === 0),
      ).toBe(true);
      sound.close();
    }

    const delayed = createGameSound();
    await delayed.unlock();
    const delayedContext = FakeAudioContext.instances.at(-1)!;
    delayed.play('link_match');
    const secondVoiceGain = delayedContext.gains[3].gain;
    const offset = secondVoiceGain.events.find(
      (event) => event.kind === 'set' && Math.abs(event.at - 10.07) < 0.000001,
    );
    expect(offset?.value).toBe(0);
    expect(
      secondVoiceGain.events.filter((event) => event.at <= (offset?.at ?? Number.POSITIVE_INFINITY)).every(
        (event) => event.value === 0,
      ),
    ).toBe(true);
    const firstAttack = secondVoiceGain.events.find(
      (event) => event.kind === 'ramp' && event.value > 0,
    );
    expect(firstAttack?.at).toBeGreaterThan(offset?.at ?? 0);
    expect(delayedContext.gains.every((gain) => gain.gain.immediateWrites.length === 0)).toBe(true);
    expect(
      delayedContext.oscillators.every(
        (oscillator) => oscillator.frequency.immediateWrites.length === 0,
      ),
    ).toBe(true);
    delayed.close();

    expect(durations.get('select')).toBeGreaterThanOrEqual(0.05);
    expect(durations.get('select')).toBeLessThanOrEqual(0.1);
    expect(durations.get('link_match')).toBeGreaterThanOrEqual(0.12);
    expect(durations.get('link_match')).toBeLessThanOrEqual(0.22);
    expect(durations.get('fishing_common')).toBeGreaterThanOrEqual(0.15);
    expect(durations.get('fishing_epic')).toBeLessThanOrEqual(0.5);
    expect(durations.get('phase')).toBeGreaterThanOrEqual(0.15);
    expect(durations.get('follow')).toBeLessThanOrEqual(0.25);
    for (const cue of ['win', 'loss', 'tie', 'end'] as const) {
      expect(durations.get(cue)).toBeGreaterThanOrEqual(0.25);
      expect(durations.get(cue)).toBeLessThanOrEqual(0.7);
    }
    expect([...durations.values()].every((duration) => duration <= 0.8)).toBe(true);
    expect(new Set((['win', 'loss', 'tie'] as const).map((cue) => signatures.get(cue))).size).toBe(3);
  });

  it('replaces rapid selections, protects the six-voice cap, and gives terminal cues priority', async () => {
    const sound = createGameSound();
    await sound.unlock();
    const context = FakeAudioContext.instances[0];

    for (let index = 0; index < 20; index += 1) sound.play('select');
    expect(activeOscillators(context)).toHaveLength(1);

    sound.play('phase');
    sound.play('fishing_epic');
    expect(activeOscillators(context).length).toBeLessThanOrEqual(6);
    sound.play('win');
    expect(activeOscillators(context)).toHaveLength(3);
    expect(context.oscillators.some((oscillator) => oscillator.stopTimes.length > 1)).toBe(true);

    const terminalSound = createGameSound();
    await terminalSound.unlock();
    const terminalContext = FakeAudioContext.instances.at(-1)!;
    terminalSound.play('win');
    terminalSound.play('tie');
    expect(activeOscillators(terminalContext)).toHaveLength(6);
    terminalSound.play('select');
    expect(activeOscillators(terminalContext)).toHaveLength(6);
    terminalSound.close();
  });

  it('drops cues while suspended and resumes the same context without replaying them', async () => {
    const sound = createGameSound();
    await sound.unlock();
    const context = FakeAudioContext.instances[0];

    sound.play('select');
    expect(activeOscillators(context)).toHaveLength(1);
    context.state = 'suspended';
    sound.play('win');
    expect(context.oscillators).toHaveLength(1);

    await sound.unlock();
    expect(FakeAudioContext.instances).toHaveLength(1);
    expect(context.resume).toHaveBeenCalledTimes(2);
    expect(activeOscillators(context)).toHaveLength(0);

    sound.play('win');
    expect(context.oscillators).toHaveLength(4);
    expect(activeOscillators(context)).toHaveLength(3);
    sound.close();
  });

  it('closes the whole graph when the hard limiter cannot be created', async () => {
    FakeAudioContext.failWaveShaper = true;
    const sound = createGameSound();
    await expect(sound.unlock()).resolves.toBeUndefined();

    const context = FakeAudioContext.instances[0];
    expect(context.close).toHaveBeenCalledTimes(1);
    expect(context.gains[0].connections).toHaveLength(0);
    expect(context.waveshapers).toHaveLength(0);
    sound.play('end');
    expect(context.oscillators).toHaveLength(0);
    sound.close();
  });

  it('fails closed when master gain automation is unavailable', async () => {
    FakeParam.failNextSetValueAtTime = true;
    const sound = createGameSound();
    await expect(sound.unlock()).resolves.toBeUndefined();

    const context = FakeAudioContext.instances[0];
    expect(context.close).toHaveBeenCalledTimes(1);
    expect(context.gains[0].disconnectCalls).toBe(1);
    expect(context.gains[0].connections).toHaveLength(0);
    sound.play('end');
    expect(context.oscillators).toHaveLength(0);
    sound.close();
  });

  it('cleans up a voice when envelope automation is unavailable', async () => {
    const sound = createGameSound();
    await sound.unlock();
    const context = FakeAudioContext.instances[0];
    FakeParam.failNextLinearRamp = true;

    expect(() => sound.play('select')).not.toThrow();
    expect(context.oscillators[0].startTimes).toHaveLength(0);
    expect(activeOscillators(context)).toHaveLength(0);
    expect(context.oscillators[0].disconnectCalls).toBe(1);
    expect(context.gains[2].disconnectCalls).toBe(1);

    sound.play('select');
    expect(activeOscillators(context)).toHaveLength(1);
    sound.close();
  });

  it('silences voice nodes without closing the context and close cleans the graph idempotently', async () => {
    const sound = createGameSound();
    await sound.unlock();
    const context = FakeAudioContext.instances[0];
    sound.play('link_match');
    sound.silence();
    expect(activeOscillators(context)).toHaveLength(0);
    expect(context.oscillators.every((oscillator) => oscillator.disconnectCalls === 1)).toBe(true);
    expect(context.gains.slice(2).every((gain) => gain.disconnectCalls === 1)).toBe(true);
    expect(context.close).not.toHaveBeenCalled();

    sound.play('select');
    expect(activeOscillators(context)).toHaveLength(1);
    sound.close();
    sound.close();
    expect(context.close).toHaveBeenCalledTimes(1);
    expect(context.gains[0].disconnectCalls).toBe(1);
    expect(context.gains[1].disconnectCalls).toBe(1);
    expect(context.waveshapers[0].disconnectCalls).toBe(1);
    expect(activeOscillators(context)).toHaveLength(0);
    expect(() => sound.play('unknown' as GameSoundCue)).not.toThrow();
  });
});

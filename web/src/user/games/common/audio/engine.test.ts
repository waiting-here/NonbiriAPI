import { afterEach, describe, expect, it, vi } from 'vitest';
import { createArcadeAudio, gainAt, nextBeat, type ArcadeAudio } from './engine';
import { duckLevel, effectMix } from './mix';

class Param {
  value = 0;
  events: [string, number, number][] = [];
  setValueAtTime(value: number, time: number) {
    this.events.push(['set', value, time]);
  }
  linearRampToValueAtTime(value: number, time: number) {
    this.events.push(['ramp', value, time]);
  }
  cancelScheduledValues(time: number) {
    this.events.push(['cancel', 0, time]);
  }
}
class Node {
  gain = new Param();
  connect = vi.fn();
  disconnect = vi.fn();
}
class Source extends Node {
  playbackRate = new Param();
  buffer: AudioBuffer | null = null;
  loop = false;
  loopStart = 0;
  loopEnd = 0;
  onended: (() => void) | null = null;
  start = vi.fn();
  stop = vi.fn();
}
class Context {
  currentTime = 10;
  state = 'suspended';
  destination = new Node();
  sources: Source[] = [];
  createGain() {
    return new Node();
  }
  createDynamicsCompressor() {
    return Object.assign(new Node(), {
      threshold: new Param(),
      knee: new Param(),
      ratio: new Param(),
      attack: new Param(),
      release: new Param(),
    });
  }
  createBufferSource() {
    const source = new Source();
    this.sources.push(source);
    return source;
  }
  decodeAudioData = vi.fn(async () => ({
    duration: 2919724 / 44100,
    sampleRate: 44100,
    length: 2919724,
  }));
  resume = vi.fn(async () => {
    this.state = 'running';
  });
  suspend = vi.fn(async () => {
    this.state = 'suspended';
  });
  close = vi.fn(async () => {
    this.state = 'closed';
  });
}
const settle = async () => {
  for (let i = 0; i < 80; i++) await Promise.resolve();
};
const engines: ArcadeAudio[] = [];
function setup() {
  const context = new Context();
  const fetcher = vi.fn(async () => ({ ok: true, arrayBuffer: async () => new ArrayBuffer(8) }));
  vi.stubGlobal('fetch', fetcher);
  const engine = createArcadeAudio({
    game: 'likes',
    quality: 'light',
    contextFactory: () => context as unknown as AudioContext,
  });
  engines.push(engine);
  return { context, fetcher, engine };
}
afterEach(() => {
  engines.splice(0).forEach((engine) => engine.close());
  vi.unstubAllGlobals();
});

describe('shared music clock', () => {
  it('ducks overlapping turning points without restarting or seeking the music', async () => {
    const { engine, context } = setup();
    engine.setMusic('battle');
    engine.setEffectsEnabled(true);
    await engine.unlock();
    await settle();
    const music = context.sources[0];
    const voiceGain = music.connect.mock.calls[0][0] as Node;
    const bus = voiceGain.connect.mock.calls[0][0] as Node;
    context.currentTime = 11;
    engine.play('common_select');
    await settle();
    expect(bus.gain.events).toHaveLength(0);
    engine.play('likes_combo');
    await settle();
    expect(bus.gain.events).toContainEqual(['ramp', 0.32, 11.035]);
    const firstRestore = bus.gain.events.at(-1)![2];
    context.currentTime = 11.2;
    engine.play('likes_combo');
    await settle();
    expect(bus.gain.events.at(-4)).toEqual(['set', 0.32, 11.2]);
    expect(bus.gain.events.at(-1)![2]).toBeGreaterThan(firstRestore);
    expect(music.start).toHaveBeenCalledOnce();
    expect(music.stop).not.toHaveBeenCalled();
    expect(context.sources.filter((source) => source.loop)).toHaveLength(1);
    engine.pause();
    expect(bus.gain.events.at(-1)).toEqual(['set', 1, 11.2]);
  });

  it('computes a continuous attack, hold and return level for interruption', () => {
    const envelope = { start: 1, attackEnd: 1.1, holdEnd: 1.4, end: 1.8, from: 1, level: 0.3 };
    expect(duckLevel(envelope, 1)).toBe(1);
    expect(duckLevel(envelope, 1.05)).toBeCloseTo(0.65);
    expect(duckLevel(envelope, 1.2)).toBe(0.3);
    expect(duckLevel(envelope, 1.6)).toBeCloseTo(0.65);
    expect(duckLevel(envelope, 1.8)).toBe(1);
    expect(duckLevel(null, 1.2)).toBe(1);
  });

  it('uses the decoded loop period for the next beat and complementary one-beat ramps', () => {
    const duration = 2919724 / 44100,
      epoch = 0.028;
    const start = nextBeat(66.2, epoch, duration);
    expect(start).toBeGreaterThanOrEqual(66.228);
    expect((start - epoch) / (duration / 192)).toBeCloseTo(
      Math.round((start - epoch) / (duration / 192)),
      10,
    );
    for (let t = 0; t <= 1; t += 0.017) {
      expect(
        gainAt({ start: 0, end: 1, from: 1, to: 0 }, t) +
          gainAt({ start: 0, end: 1, from: 0, to: 1 }, t),
      ).toBeCloseTo(1, 12);
    }
  });
  it('loads no media before unlock; loads the requested track before the other five', async () => {
    const { engine, fetcher, context } = setup();
    engine.setMusic('danger');
    expect(fetcher).not.toHaveBeenCalled();
    await engine.unlock();
    await settle();
    expect(fetcher.mock.calls.length).toBe(6);
    expect(String((fetcher.mock.calls as unknown[][])[0][0])).toContain('danger.mp3');
    expect(context.sources).toHaveLength(1);
    expect(context.sources[0].loop).toBe(true);
    expect(context.sources[0].loopEnd).toBeCloseTo(2919724 / 44100, 12);
  });
  it('keeps phase on transitions, replaces a queued target, and stops immediately for loss', async () => {
    const { engine, context } = setup();
    engine.setMusic('battle');
    await engine.unlock();
    await settle();
    context.currentTime = 11;
    engine.setMusic('accelerated');
    await settle();
    const queued = context.sources.at(-1)!;
    engine.setMusic('danger');
    await settle();
    expect(queued.stop).toHaveBeenCalled();
    const danger = context.sources.at(-1)!;
    const [time, offset] = danger.start.mock.calls[0] as number[];
    expect(offset).toBeCloseTo((time - 10.028) % (2919724 / 44100), 10);
    context.currentTime = time + 0.2;
    engine.setMusic('loss');
    expect(danger.stop).toHaveBeenLastCalledWith(context.currentTime + 0.012);
  });
  it('pauses, resumes the selected scene, and releases nodes and buffers on close', async () => {
    const { engine, context, fetcher } = setup();
    engine.setMusic('battle');
    await engine.unlock();
    await settle();
    engine.pause();
    await settle();
    expect(context.state).toBe('suspended');
    expect(context.sources[0].disconnect).toHaveBeenCalled();
    engine.setMusic('danger');
    await engine.resume();
    await settle();
    expect(context.sources.at(-1)?.start).toHaveBeenCalled();
    const requests = fetcher.mock.calls.length;
    engine.close();
    await settle();
    engine.play('likes_cast');
    engine.setMusic('lobby');
    await engine.unlock();
    expect(context.close).toHaveBeenCalledOnce();
    expect(fetcher).toHaveBeenCalledTimes(requests);
    expect(context.sources.every((source) => source.disconnect.mock.calls.length > 0)).toBe(true);
  });
});

describe('sample effects', () => {
  it('schedules resistance taps separately and cancels the future tap on mute', async () => {
    const { engine, context } = setup();
    engine.setEffectsEnabled(true);
    await engine.unlock();
    await settle();
    engine.play('common_lock');
    engine.play('common_lock', { delay: 0.09 });
    await settle();
    expect(context.sources.map((source) => source.start.mock.calls[0][0])).toEqual([10, 10.09]);
    engine.setEffectsEnabled(false);
    expect(context.sources[1].stop).toHaveBeenCalledOnce();
    expect(context.sources[1].disconnect).toHaveBeenCalledOnce();
  });

  it('bounds layered gain and pitch, and mutes every layer together', async () => {
    const { engine, context } = setup();
    engine.setEffectsEnabled(true);
    await engine.unlock();
    await settle();
    engine.play('likes_combo', { gain: 100, semitones: 40 });
    await settle();
    engine.play('likes_score_burst', { gain: 0.4, semitones: 2 });
    await settle();
    const [main, accent] = context.sources;
    const gain = main.connect.mock.calls[0][0] as Node;
    expect(gain.gain.events).toContainEqual(['set', effectMix('likes_combo').gain * 1.5, 10]);
    expect(main.playbackRate.events).toContainEqual(['set', 2 ** (4 / 12), 10]);
    expect(accent.playbackRate.events).toContainEqual(['set', 2 ** (2 / 12), 10]);
    engine.setEffectsEnabled(false);
    expect(main.stop).toHaveBeenCalledOnce();
    expect(accent.stop).toHaveBeenCalledOnce();
    context.currentTime += 1;
    engine.setEffectsEnabled(true);
    engine.play('likes_cast', { gain: NaN, semitones: Infinity });
    await settle();
    const plain = context.sources.at(-1)!;
    expect(plain.playbackRate.events).toContainEqual(['set', 1, 11]);
    expect((plain.connect.mock.calls[0][0] as Node).gain.events).toContainEqual([
      'set',
      effectMix('likes_cast').gain,
      11,
    ]);
  });

  it('cancels a pending deadline cue and stops its voice without cutting other effects', async () => {
    const { engine, context } = setup();
    engine.setEffectsEnabled(true);
    await engine.unlock();
    await settle();
    engine.play('likes_countdown');
    engine.stopEffect('likes_countdown');
    await settle();
    expect(context.sources).toHaveLength(0);
    context.currentTime += 1;
    engine.play('likes_countdown');
    engine.play('common_select');
    await settle();
    const [warning, selection] = context.sources;
    engine.stopEffect('likes_countdown');
    expect(warning.stop).toHaveBeenCalledOnce();
    expect(selection.stop).not.toHaveBeenCalled();
  });
  it('coalesces repeated bursts, bounds simultaneous voices, and respects mute', async () => {
    const { engine, context } = setup();
    engine.setEffectsEnabled(true);
    await engine.unlock();
    await settle();
    engine.play('likes_score_burst');
    engine.play('likes_score_burst');
    await settle();
    expect(context.sources).toHaveLength(1);
    for (let i = 0; i < 10; i++) {
      context.currentTime += 0.2;
      engine.play('likes_cast');
      await settle();
    }
    expect(
      context.sources.filter((source) => source.disconnect.mock.calls.length === 0).length,
    ).toBeLessThanOrEqual(6);
    const count = context.sources.length;
    engine.setEffectsEnabled(false);
    engine.play('common_win');
    await settle();
    expect(context.sources).toHaveLength(count);
  });
  it('does not complete an old asynchronous sound after backgrounding or unmount', async () => {
    const { engine, context } = setup();
    await engine.unlock();
    let resolve!: (response: unknown) => void;
    vi.stubGlobal(
      'fetch',
      vi.fn(
        () =>
          new Promise((done) => {
            resolve = done;
          }),
      ),
    );
    engine.setEffectsEnabled(true);
    engine.play('common_select');
    engine.pause();
    resolve({ ok: true, arrayBuffer: async () => new ArrayBuffer(8) });
    await settle();
    expect(context.sources).toHaveLength(0);
  });
});

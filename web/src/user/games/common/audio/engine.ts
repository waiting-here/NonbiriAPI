import {
  effectUrls,
  musicTracks,
  musicUrls,
  type AudioGame,
  type EffectCue,
  type MusicLoop,
  type MusicQuality,
  type MusicScene,
} from './assets';

const LEAD = 0.028;
const RELEASE = 0.012;
const BEATS = 192;
const MAX_EFFECTS = 6;
const STEPS = 32;
type Ramp = { start: number; end: number; from: number; to: number };
type Voice = {
  source: AudioBufferSourceNode;
  gain: GainNode;
  ramp: Ramp;
  starts: number;
  priority: number;
};

export function gainAt(ramp: Ramp, time: number): number {
  if (time <= ramp.start) return ramp.from;
  if (time >= ramp.end) return ramp.to;
  const position = ((time - ramp.start) / (ramp.end - ramp.start)) * STEPS;
  const lower = Math.floor(position);
  const curve = (step: number) => (1 - Math.cos((Math.PI * step) / STEPS)) / 2;
  const eased = curve(lower) + (curve(lower + 1) - curve(lower)) * (position - lower);
  return ramp.from + (ramp.to - ramp.from) * eased;
}

export function nextBeat(now: number, epoch: number, duration: number): number {
  const beat = duration / BEATS;
  return epoch + Math.ceil((now + LEAD - epoch) / beat) * beat;
}

export interface ArcadeAudio {
  unlock(): Promise<void>;
  setMusic(scene: MusicScene | null): void;
  setEffectsEnabled(enabled: boolean): void;
  play(cue: EffectCue): void;
  pause(): void;
  resume(): Promise<void>;
  close(): void;
}

export function createArcadeAudio(options: {
  game: AudioGame;
  quality: MusicQuality;
  onError?: () => void;
  contextFactory?: () => AudioContext;
}): ArcadeAudio {
  let context: AudioContext | null = null;
  let master: GainNode | null = null;
  let limiter: DynamicsCompressorNode | null = null;
  let closed = false,
    paused = false,
    effectsEnabled = false;
  let target: MusicScene | null = null,
    scheduled: MusicLoop | null = null;
  let duration = 0,
    epoch: number | null = null,
    effectGeneration = 0;
  let musicAbort = new AbortController();
  const effectsAbort = new AbortController();
  const musicBuffers = new Map<MusicLoop, AudioBuffer>();
  const effectBuffers = new Map<EffectCue, AudioBuffer>();
  const musicLoads = new Map<MusicLoop, Promise<AudioBuffer | null>>();
  const effectLoads = new Map<EffectCue, Promise<AudioBuffer | null>>();
  const musicVoices = new Set<Voice>(),
    effectVoices = new Set<Voice>();
  const recentEffects = new Map<EffectCue, number>();
  let preloading = false;

  const fail = () => {
    if (!closed) options.onError?.();
  };
  const detach = (voice: Voice, collection: Set<Voice>) => {
    collection.delete(voice);
    voice.source.onended = null;
    voice.source.disconnect();
    voice.gain.disconnect();
  };
  const stop = (voice: Voice, collection: Set<Voice>) => {
    try {
      voice.source.stop();
    } catch {
      /* Already ended. */
    }
    detach(voice, collection);
  };
  const silence = (collection: Set<Voice>, fade = false) => {
    for (const voice of collection) {
      if (!fade || !context || voice.starts > context.currentTime) stop(voice, collection);
      else {
        const now = context.currentTime;
        voice.gain.gain.cancelScheduledValues(now);
        voice.gain.gain.setValueAtTime(gainAt(voice.ramp, now), now);
        voice.gain.gain.linearRampToValueAtTime(0, now + RELEASE);
        voice.source.stop(now + RELEASE);
      }
    }
  };
  const voiceFor = (buffer: AudioBuffer, collection: Set<Voice>, priority: number): Voice => {
    const ctx = context!;
    const source = ctx.createBufferSource(),
      gain = ctx.createGain();
    source.buffer = buffer;
    source.connect(gain);
    gain.connect(master!);
    const voice: Voice = {
      source,
      gain,
      priority,
      starts: ctx.currentTime,
      ramp: { start: 0, end: 0, from: 1, to: 1 },
    };
    collection.add(voice);
    source.onended = () => detach(voice, collection);
    return voice;
  };
  const ramp = (voice: Voice, to: number, start: number, end: number) => {
    const now = context!.currentTime;
    const from = gainAt(voice.ramp, now);
    const param = voice.gain.gain;
    param.cancelScheduledValues(now);
    param.setValueAtTime(from, now);
    param.setValueAtTime(from, start);
    voice.ramp = { from, to, start, end };
    for (let step = 1; step <= STEPS; step++) {
      const time = start + ((end - start) * step) / STEPS;
      param.linearRampToValueAtTime(gainAt(voice.ramp, time), time);
    }
  };
  const decode = async (url: string, signal: AbortSignal): Promise<AudioBuffer | null> => {
    try {
      const response = await fetch(url, { signal, credentials: 'same-origin' });
      if (!response.ok) throw new Error('Audio unavailable');
      const bytes = await response.arrayBuffer();
      if (closed || signal.aborted || !context) return null;
      const decoded = await context.decodeAudioData(bytes);
      return closed || signal.aborted ? null : decoded;
    } catch {
      if (!signal.aborted) fail();
      return null;
    }
  };
  const loadMusic = (track: MusicLoop): Promise<AudioBuffer | null> => {
    const cached = musicBuffers.get(track);
    if (cached) return Promise.resolve(cached);
    const pending = musicLoads.get(track);
    if (pending) return pending;
    const signal = musicAbort.signal;
    const loading = decode(musicUrls[options.quality][track], signal)
      .then((buffer) => {
        if (!buffer || signal.aborted) return null;
        if (duration && Math.abs(buffer.duration - duration) > 0.5 / buffer.sampleRate) {
          fail();
          return null;
        }
        duration ||= buffer.duration;
        musicBuffers.set(track, buffer);
        return buffer;
      })
      .finally(() => {
        if (!signal.aborted) musicLoads.delete(track);
      });
    musicLoads.set(track, loading);
    return loading;
  };
  const loadEffect = (cue: EffectCue): Promise<AudioBuffer | null> => {
    const cached = effectBuffers.get(cue);
    if (cached) return Promise.resolve(cached);
    const pending = effectLoads.get(cue);
    if (pending) return pending;
    const loading = decode(effectUrls[cue], effectsAbort.signal)
      .then((buffer) => {
        if (buffer) effectBuffers.set(cue, buffer);
        return buffer;
      })
      .finally(() => effectLoads.delete(cue));
    effectLoads.set(cue, loading);
    return loading;
  };
  const preloadMusic = async () => {
    if (preloading) return;
    preloading = true;
    const signal = musicAbort.signal;
    try {
      for (const track of musicTracks) {
        if (closed || signal.aborted || !target || target === 'loss') break;
        if (track !== 'loss-stinger') await loadMusic(track);
      }
    } finally {
      if (!signal.aborted) preloading = false;
    }
  };
  const ensureMusic = async () => {
    if (
      closed ||
      paused ||
      !context ||
      context.state !== 'running' ||
      !target ||
      target === 'loss' ||
      scheduled === target
    )
      return;
    const requested = target,
      signal = musicAbort.signal;
    const buffer = await loadMusic(requested);
    if (
      !buffer ||
      closed ||
      paused ||
      signal.aborted ||
      requested !== target ||
      scheduled === requested ||
      context.state !== 'running'
    )
      return;
    const now = context.currentTime;
    epoch ??= now + LEAD;
    const start = nextBeat(now, epoch, duration),
      end = start + duration / BEATS;
    for (const voice of musicVoices) {
      if (voice.starts > now) stop(voice, musicVoices);
      else {
        ramp(voice, 0, start, end);
        voice.source.stop(end);
      }
    }
    const voice = voiceFor(buffer, musicVoices, 0);
    voice.starts = start;
    voice.ramp = { start: now, end: now, from: 0, to: 0 };
    voice.gain.gain.setValueAtTime(0, now);
    voice.source.loop = true;
    voice.source.loopStart = 0;
    voice.source.loopEnd = duration;
    ramp(voice, 0.72, start, end);
    voice.source.start(start, (((start - epoch) % duration) + duration) % duration);
    scheduled = requested;
    void preloadMusic();
  };
  const warmEffects = async () => {
    for (const cue of Object.keys(effectUrls) as EffectCue[]) {
      if (closed || !effectsEnabled) return;
      if (cue.startsWith('common_') || cue.startsWith(`${options.game}_`)) await loadEffect(cue);
    }
  };
  const unlock = async () => {
    if (closed || paused) return;
    try {
      if (!context) {
        context = options.contextFactory ? options.contextFactory() : new AudioContext();
        master = context.createGain();
        master.gain.value = 0.8;
        limiter = context.createDynamicsCompressor();
        limiter.threshold.value = -3;
        limiter.knee.value = 0;
        limiter.ratio.value = 20;
        limiter.attack.value = 0.003;
        limiter.release.value = 0.08;
        master.connect(limiter);
        limiter.connect(context.destination);
      }
      await context.resume();
      if (closed || paused) return;
      void ensureMusic();
      if (effectsEnabled) void warmEffects();
    } catch {
      fail();
    }
  };
  return {
    unlock,
    setMusic(scene) {
      if (closed || target === scene) return;
      target = scene;
      if (!scene || scene === 'loss') {
        scheduled = null;
        silence(musicVoices, true);
        if (!scene) {
          musicAbort.abort();
          musicAbort = new AbortController();
          musicLoads.clear();
          musicBuffers.clear();
          duration = 0;
          epoch = null;
          preloading = false;
        }
      } else void ensureMusic();
    },
    setEffectsEnabled(enabled) {
      effectsEnabled = enabled;
      if (!enabled) {
        effectGeneration++;
        silence(effectVoices, true);
      } else if (context?.state === 'running') void warmEffects();
    },
    play(cue) {
      if (closed || paused || !effectsEnabled || context?.state !== 'running') return;
      const now = context.currentTime;
      const spacing = cue === 'likes_score_burst' || cue === 'likes_combo' ? 0.18 : 0.06;
      if (now - (recentEffects.get(cue) ?? -Infinity) < spacing) return;
      recentEffects.set(cue, now);
      const generation = effectGeneration;
      const priority =
        /_(win|loss|draw|natural)$/.test(cue) || cue === 'likes_loss_stinger'
          ? 3
          : cue.startsWith('common_')
            ? 1
            : 2;
      if (cue === 'likes_loss_stinger') {
        this.setMusic('loss');
        silence(effectVoices, true);
      }
      void loadEffect(cue).then((buffer) => {
        if (
          !buffer ||
          closed ||
          paused ||
          !effectsEnabled ||
          generation !== effectGeneration ||
          context?.state !== 'running' ||
          context.currentTime - now > 0.35
        )
          return;
        if (priority === 3) silence(effectVoices, true);
        if (effectVoices.size >= MAX_EFFECTS) {
          const weakest = [...effectVoices].sort(
            (a, b) => a.priority - b.priority || a.starts - b.starts,
          )[0];
          if (weakest.priority > priority) return;
          stop(weakest, effectVoices);
        }
        const voice = voiceFor(buffer, effectVoices, priority);
        voice.source.start();
      });
    },
    pause() {
      paused = true;
      effectGeneration++;
      silence(musicVoices);
      silence(effectVoices);
      scheduled = null;
      if (context?.state === 'running') void context.suspend().catch(fail);
    },
    async resume() {
      paused = false;
      await unlock();
    },
    close() {
      if (closed) return;
      closed = true;
      effectGeneration++;
      musicAbort.abort();
      effectsAbort.abort();
      silence(musicVoices);
      silence(effectVoices);
      musicLoads.clear();
      effectLoads.clear();
      musicBuffers.clear();
      effectBuffers.clear();
      recentEffects.clear();
      master?.disconnect();
      limiter?.disconnect();
      if (context && context.state !== 'closed') void context.close().catch(() => {});
      context = null;
      master = null;
      limiter = null;
    },
  };
}

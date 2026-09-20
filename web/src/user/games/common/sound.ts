export type GameSoundCue =
  | 'select'
  | 'link_match'
  | 'link_shuffle'
  | 'fishing_common'
  | 'fishing_rare'
  | 'fishing_epic'
  | 'phase'
  | 'reveal'
  | 'follow'
  | 'win'
  | 'loss'
  | 'tie'
  | 'end';

export interface GameSound {
  unlock(): Promise<void>;
  play(cue: GameSoundCue, variation?: { readonly chain: number }): void;
  silence(): void;
  close(): void;
}

type SoundGroup = 'select' | 'stage' | 'terminal' | 'effect';

interface ToneSpec {
  readonly frequency: number;
  readonly offset: number;
  readonly length: number;
  readonly level: number;
  readonly type: OscillatorType;
}

interface CueSpec {
  readonly duration: number;
  readonly priority: number;
  readonly group: SoundGroup;
  readonly tones: readonly ToneSpec[];
}

interface ActiveVoice {
  readonly oscillator: OscillatorNode;
  readonly gain: GainNode;
  readonly endAt: number;
  readonly priority: number;
  readonly group: SoundGroup;
  stopped: boolean;
}

type AudioContextConstructor = new () => AudioContext;

const MAX_VOICES = 6;
const MASTER_GAIN = 0.24;
const MIN_RELEASE = 0.006;
const TERMINAL_PRIORITY = 70;

function tone(
  frequency: number,
  offset: number,
  length: number,
  type: OscillatorType = 'sine',
  level = 0.52,
): ToneSpec {
  return { frequency, offset, length, level, type };
}

const CUES: Readonly<Record<GameSoundCue, CueSpec>> = {
  select: {
    duration: 0.075,
    priority: 10,
    group: 'select',
    tones: [tone(440, 0, 0.06, 'sine', 0.44)],
  },
  link_match: {
    duration: 0.18,
    priority: 30,
    group: 'effect',
    tones: [
      tone(660, 0, 0.12, 'triangle', 0.48),
      tone(880, 0.07, 0.09, 'triangle', 0.4),
    ],
  },
  link_shuffle: {
    duration: 0.16,
    priority: 15,
    group: 'effect',
    tones: [
      tone(330, 0, 0.09, 'sawtooth', 0.3),
      tone(495, 0.06, 0.09, 'triangle', 0.34),
    ],
  },
  fishing_common: {
    duration: 0.2,
    priority: 20,
    group: 'effect',
    tones: [
      tone(392, 0, 0.12, 'sine', 0.42),
      tone(523.25, 0.08, 0.1, 'triangle', 0.38),
    ],
  },
  fishing_rare: {
    duration: 0.32,
    priority: 25,
    group: 'effect',
    tones: [
      tone(392, 0, 0.17, 'triangle', 0.42),
      tone(587.33, 0.1, 0.16, 'triangle', 0.42),
      tone(783.99, 0.2, 0.1, 'sine', 0.36),
    ],
  },
  fishing_epic: {
    duration: 0.46,
    priority: 35,
    group: 'effect',
    tones: [
      tone(329.63, 0, 0.18, 'triangle', 0.4),
      tone(493.88, 0.1, 0.17, 'triangle', 0.44),
      tone(659.25, 0.2, 0.17, 'sine', 0.44),
      tone(987.77, 0.3, 0.13, 'sine', 0.38),
    ],
  },
  phase: {
    duration: 0.2,
    priority: 40,
    group: 'stage',
    tones: [
      tone(440, 0, 0.1, 'square', 0.25),
      tone(660, 0.1, 0.08, 'triangle', 0.32),
    ],
  },
  reveal: {
    duration: 0.24,
    priority: 45,
    group: 'stage',
    tones: [tone(174.61, 0, 0.1, 'triangle', 0.45), tone(523.25, 0.025, 0.18, 'triangle', 0.52)],
  },
  follow: {
    duration: 0.2,
    priority: 45,
    group: 'stage',
    tones: [
      tone(330, 0, 0.11, 'square', 0.24),
      tone(440, 0.1, 0.08, 'triangle', 0.32),
    ],
  },
  win: {
    duration: 0.48,
    priority: 70,
    group: 'terminal',
    tones: [
      tone(523.25, 0, 0.2, 'triangle', 0.5),
      tone(659.25, 0.14, 0.2, 'triangle', 0.5),
      tone(783.99, 0.28, 0.17, 'sine', 0.48),
    ],
  },
  loss: {
    duration: 0.4,
    priority: 70,
    group: 'terminal',
    tones: [
      tone(392, 0, 0.18, 'triangle', 0.46),
      tone(311.13, 0.12, 0.18, 'triangle', 0.44),
      tone(233.08, 0.24, 0.13, 'sine', 0.42),
    ],
  },
  tie: {
    duration: 0.36,
    priority: 70,
    group: 'terminal',
    tones: [
      tone(440, 0, 0.14, 'triangle', 0.44),
      tone(466.16, 0.11, 0.14, 'triangle', 0.44),
      tone(440, 0.22, 0.1, 'sine', 0.4),
    ],
  },
  end: {
    duration: 0.62,
    priority: 80,
    group: 'terminal',
    tones: [
      tone(392, 0, 0.2, 'triangle', 0.44),
      tone(523.25, 0.14, 0.2, 'triangle', 0.46),
      tone(659.25, 0.28, 0.2, 'triangle', 0.48),
      tone(1046.5, 0.42, 0.15, 'sine', 0.44),
    ],
  },
};

function getAudioContextConstructor(): AudioContextConstructor | null {
  try {
    const scope = globalThis as typeof globalThis & {
      webkitAudioContext?: AudioContextConstructor;
    };
    const candidate = scope.AudioContext ?? scope.webkitAudioContext;
    return typeof candidate === 'function' ? candidate : null;
  } catch {
    return null;
  }
}

function disconnect(node: AudioNode | null): void {
  if (!node) return;
  try {
    node.disconnect();
  } catch {
    // A partially created device graph is already unusable.
  }
}

function setParam(param: AudioParam | undefined, value: number, at: number): void {
  if (!param) throw new Error('audio parameter unavailable');
  param.setValueAtTime(value, at);
}

function rampParam(param: AudioParam | undefined, value: number, at: number): void {
  if (!param) throw new Error('audio parameter unavailable');
  param.linearRampToValueAtTime(value, at);
}

function currentTimeOf(context: AudioContext): number {
  try {
    const value = context.currentTime;
    return Number.isFinite(value) && value >= 0 ? value : 0;
  } catch {
    return 0;
  }
}

function isRunning(context: AudioContext): boolean {
  try {
    return context.state === 'running';
  } catch {
    return false;
  }
}

function hardLimiterCurve(): Float32Array<ArrayBuffer> {
  // Web Audio clamps inputs outside [-1, 1] to the curve endpoints. An
  // identity curve therefore provides a hard clamp before the master gain.
  const points = 4097;
  const curve = new Float32Array(points) as Float32Array<ArrayBuffer>;
  for (let index = 0; index < points; index += 1) {
    curve[index] = (index / (points - 1)) * 2 - 1;
  }
  return curve;
}

function safePromise(value: PromiseLike<unknown> | void): Promise<void> {
  return Promise.resolve(value).then(
    () => undefined,
    () => undefined,
  );
}

export function createGameSound(): GameSound {
  let context: AudioContext | null = null;
  let mix: GainNode | null = null;
  let master: GainNode | null = null;
  let limiter: WaveShaperNode | null = null;
  let opening: Promise<void> | null = null;
  let unlocked = false;
  let failed = false;
  let closed = false;
  const voices: ActiveVoice[] = [];

  const removeVoice = (voice: ActiveVoice): void => {
    const index = voices.indexOf(voice);
    if (index >= 0) voices.splice(index, 1);
  };

  const releaseVoiceNodes = (voice: ActiveVoice): void => {
    try {
      voice.oscillator.onended = null;
    } catch {
      // Ignore devices with incomplete event implementations.
    }
    disconnect(voice.gain);
    disconnect(voice.oscillator);
  };

  const stopVoice = (voice: ActiveVoice): void => {
    if (voice.stopped) return;
    voice.stopped = true;
    try {
      voice.oscillator.stop();
    } catch {
      // It may have ended naturally or a fake/device may already be closed.
    }
    releaseVoiceNodes(voice);
    removeVoice(voice);
  };

  const silence = (): void => {
    for (const voice of [...voices]) stopVoice(voice);
  };

  const releaseGraph = (): void => {
    disconnect(limiter);
    disconnect(mix);
    disconnect(master);
    limiter = null;
    mix = null;
    master = null;
    const oldContext = context;
    context = null;
    unlocked = false;
    if (!oldContext) return;
    try {
      void safePromise(oldContext.close());
    } catch {
      // Closing an already unavailable device is best effort.
    }
  };

  const pruneVoices = (now: number): void => {
    for (const voice of [...voices]) {
      if (voice.endAt <= now) stopVoice(voice);
    }
  };

  const installGraph = (created: AudioContext): void => {
    let nextMaster: GainNode | null = null;
    let nextMix: GainNode | null = null;
    let nextLimiter: WaveShaperNode | null = null;
    try {
      nextMaster = created.createGain();
      nextMix = created.createGain();
      nextLimiter = created.createWaveShaper();
      if (!nextMaster || !nextMix || !nextLimiter) throw new Error('sound graph unavailable');
      const at = currentTimeOf(created);
      setParam(nextMaster.gain, MASTER_GAIN, at);
      setParam(nextMix.gain, 1, at);
      nextLimiter.curve = hardLimiterCurve();
      nextLimiter.oversample = '4x';
      nextMix.connect(nextLimiter);
      nextLimiter.connect(nextMaster);
      nextMaster.connect(created.destination);
      mix = nextMix;
      limiter = nextLimiter;
      master = nextMaster;
    } catch {
      disconnect(nextLimiter);
      disconnect(nextMix);
      disconnect(nextMaster);
      throw new Error('sound graph unavailable');
    }
  };

  const unlock = (): Promise<void> => {
    if (closed || failed) return Promise.resolve();
    if (context && unlocked && isRunning(context)) return Promise.resolve();
    if (opening) return opening;

    opening = (async () => {
      try {
        let created = context;
        if (!created) {
          const Constructor = getAudioContextConstructor();
          if (!Constructor) {
            failed = true;
            return;
          }
          created = new Constructor();
          context = created;
          installGraph(created);
        } else if (unlocked) {
          // Drop cues that were scheduled before a device suspension or
          // interruption; they must not replay after the context resumes.
          silence();
        }
        await Promise.resolve(typeof created.resume === 'function' ? created.resume() : undefined);
        if (closed || context !== created || !isRunning(created)) {
          if (context === created) releaseGraph();
          return;
        }
        unlocked = true;
      } catch {
        failed = true;
        if (context) releaseGraph();
      }
    })().finally(() => {
      opening = null;
    });
    return opening;
  };

  const scheduleVoice = (
    created: AudioContext,
    spec: CueSpec,
    toneSpec: ToneSpec,
    now: number,
  ): ActiveVoice => {
    if (!mix) throw new Error('sound graph unavailable');
    let oscillator: OscillatorNode | null = null;
    let gain: GainNode | null = null;
    try {
      oscillator = created.createOscillator();
      gain = created.createGain();
      if (!oscillator || !gain) throw new Error('sound nodes unavailable');
      const endAt = now + spec.duration;
      const noteStart = now + Math.min(Math.max(0, toneSpec.offset), spec.duration - MIN_RELEASE);
      const noteEnd = Math.min(
        endAt - MIN_RELEASE / 2,
        noteStart + Math.max(MIN_RELEASE * 2, toneSpec.length),
      );
      const noteSpan = Math.max(MIN_RELEASE, noteEnd - noteStart);
      const attack = Math.min(0.012, noteSpan / 3);
      const release = Math.min(0.018, noteSpan / 3);
      oscillator.type = toneSpec.type;
      setParam(oscillator.frequency, toneSpec.frequency, now);
      oscillator.connect(gain);
      gain.connect(mix);
      setParam(gain.gain, 0, now);
      if (noteStart > now) setParam(gain.gain, 0, noteStart);
      rampParam(gain.gain, toneSpec.level, noteStart + attack);
      setParam(gain.gain, toneSpec.level, Math.max(noteStart + attack, noteEnd - release));
      rampParam(gain.gain, 0, noteEnd);

      const voice: ActiveVoice = {
        oscillator,
        gain,
        endAt,
        priority: spec.priority,
        group: spec.group,
        stopped: false,
      };
      oscillator.onended = () => {
        if (voice.stopped) return;
        voice.stopped = true;
        releaseVoiceNodes(voice);
        removeVoice(voice);
      };
      oscillator.start(now);
      oscillator.stop(endAt);
      return voice;
    } catch (error) {
      try {
        oscillator?.stop();
      } catch {
        // The oscillator may not have started.
      }
      disconnect(gain);
      disconnect(oscillator);
      throw error;
    }
  };

  const play = (cue: GameSoundCue, variation?: { readonly chain: number }): void => {
    if (closed || failed || !unlocked || !context || !mix || !master || !isRunning(context)) return;
    if (typeof cue !== 'string' || !Object.prototype.hasOwnProperty.call(CUES, cue)) return;
    const base = CUES[cue];
    const chain =
      cue === 'link_match' && Number.isFinite(variation?.chain)
        ? Math.max(1, Math.min(3, variation!.chain))
        : 1;
    const spec =
      chain > 1
        ? {
            ...base,
            tones: base.tones.map((note) => ({
              ...note,
              frequency: note.frequency * 2 ** ((chain - 1) / 12),
              level: note.level * (1 + (chain - 1) * 0.08),
            })),
          }
        : base;
    const now = currentTimeOf(context);
    pruneVoices(now);

    if (spec.group === 'select') {
      for (const voice of [...voices]) {
        if (voice.group === 'select') stopVoice(voice);
      }
    }
    if (spec.priority >= TERMINAL_PRIORITY) {
      for (const voice of [...voices]) {
        if (voice.priority < spec.priority) stopVoice(voice);
      }
    }

    const required = spec.tones.length;
    if (required > MAX_VOICES) return;
    while (voices.length + required > MAX_VOICES) {
      const candidates = voices
        .filter((voice) => voice.priority <= spec.priority)
        .sort((left, right) => left.priority - right.priority || left.endAt - right.endAt);
      const victim = candidates[0];
      if (!victim) return;
      stopVoice(victim);
    }

    const createdVoices: ActiveVoice[] = [];
    try {
      for (const toneSpec of spec.tones) {
        const voice = scheduleVoice(context, spec, toneSpec, now);
        if (!voice.stopped) {
          voices.push(voice);
          createdVoices.push(voice);
        }
      }
    } catch {
      for (const voice of createdVoices) stopVoice(voice);
    }
  };

  const close = (): void => {
    if (closed) return;
    closed = true;
    silence();
    releaseGraph();
  };

  return { unlock, play, silence, close };
}

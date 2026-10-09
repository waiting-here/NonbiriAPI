import type { Rarity } from './types';
export type Cue = 'gather' | 'spread' | Rarity | 'spotlight' | 'theme';
const notes: Record<Cue, number[]> = {
  gather: [261.63, 392, 523.25, 783.99, 1046.5],
  spread: [392, 493.88, 587.33, 783.99],
  N: [659.25],
  R: [659.25, 987.77],
  SR: [523.25, 659.25, 783.99],
  SSR: [261.63, 523.25, 659.25, 783.99, 1046.5, 1318.51],
  spotlight: [261.63, 392, 523.25, 783.99, 1046.5],
  theme: [523.25, 659.25, 783.99, 1046.5, 1318.51, 1567.98],
};
export function scheduleCue(ctx: BaseAudioContext, cue: Cue, start = ctx.currentTime) {
  const sources: AudioScheduledSourceNode[] = [];
  const tail = cue === 'SSR' || cue === 'theme' || cue === 'spotlight' ? 1.5 : 0.7;
  function tone(frequency: number, at: number, volume: number, duration: number, pan: number) {
    const osc = ctx.createOscillator(),
      gain = ctx.createGain(),
      panner = ctx.createStereoPanner();
    osc.type = 'sine';
    osc.frequency.value = frequency;
    gain.gain.setValueAtTime(0, at);
    gain.gain.linearRampToValueAtTime(volume, at + 0.009);
    gain.gain.exponentialRampToValueAtTime(0.00001, at + duration);
    panner.pan.value = pan;
    osc.connect(gain);
    gain.connect(panner);
    panner.connect(ctx.destination);
    osc.start(at);
    osc.stop(at + duration + 0.03);
    sources.push(osc);
    osc.onended = () => {
      osc.disconnect();
      gain.disconnect();
      panner.disconnect();
    };
  }
  notes[cue].forEach((frequency, i) => {
    const at = start + i * (cue === 'gather' ? 0.19 : cue === 'theme' ? 0.105 : 0.06);
    const pan = (i / Math.max(1, notes[cue].length - 1) - 0.5) * 0.65;
    for (const [ratio, volume, decay] of [
      [1, 0.12, tail],
      [2.001, 0.023, 0.4],
      [3.98, 0.008, 0.18],
    ]) {
      tone(frequency * ratio, at, volume / Math.sqrt(notes[cue].length), decay, pan);
      if (tail > 1)
        tone(
          frequency * ratio,
          at + 0.14,
          (volume * 0.18) / Math.sqrt(notes[cue].length),
          decay * 0.7,
          -pan,
        );
    }
  });
  if (cue === 'gather' || cue === 'spread' || cue === 'SSR' || cue === 'spotlight') {
    const length = cue === 'gather' ? 1.22 : cue === 'spread' ? 0.4 : 0.8;
    const buffer = ctx.createBuffer(1, Math.ceil(ctx.sampleRate * length), ctx.sampleRate);
    const data = buffer.getChannelData(0);
    let seed = 71377;
    for (let i = 0; i < data.length; i++) {
      seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
      data[i] = (seed / 2147483648 - 1) * 0.3;
    }
    const noise = ctx.createBufferSource(),
      filter = ctx.createBiquadFilter(),
      gain = ctx.createGain();
    noise.buffer = buffer;
    filter.type = 'bandpass';
    filter.Q.value = 0.7;
    filter.frequency.setValueAtTime(cue === 'gather' ? 400 : 2800, start);
    filter.frequency.exponentialRampToValueAtTime(cue === 'gather' ? 6200 : 500, start + length);
    gain.gain.setValueAtTime(0, start);
    gain.gain.linearRampToValueAtTime(cue === 'gather' ? 0.09 : 0.13, start + length * 0.32);
    gain.gain.exponentialRampToValueAtTime(0.00001, start + length);
    noise.connect(filter);
    filter.connect(gain);
    gain.connect(ctx.destination);
    noise.start(start);
    noise.stop(start + length);
    sources.push(noise);
    noise.onended = () => {
      noise.disconnect();
      filter.disconnect();
      gain.disconnect();
    };
  }
  if (cue === 'SSR' || cue === 'spotlight') {
    tone(130.815, start, 0.15, 0.8, 0);
    tone(196, start, 0.065, 1.15, -0.12);
  }
  return sources;
}
export class GachaAudio {
  private ctx?: AudioContext;
  private bgm?: HTMLAudioElement;
  private effects = new Set<AudioScheduledSourceNode>();
  async playMusic(enabled: boolean) {
    if (!enabled) {
      this.bgm?.pause();
      return;
    }
    this.bgm ??= new Audio('/assets/gacha/audio/lobby.mp3');
    this.bgm.loop = true;
    this.bgm.volume = 0.16;
    try {
      await this.bgm.play();
    } catch {
      /* Retry after the next user gesture. */
    }
  }
  cue(cue: Cue, enabled: boolean) {
    if (!enabled) return;
    this.ctx ??= new AudioContext();
    const ctx = this.ctx;
    void ctx.resume();
    for (const source of scheduleCue(ctx, cue)) {
      this.effects.add(source);
      source.addEventListener('ended', () => this.effects.delete(source), { once: true });
    }
  }
  pause() {
    this.bgm?.pause();
    this.effects.forEach((source) => {
      try {
        source.stop();
      } catch {
        /* Already ended. */
      }
    });
    this.effects.clear();
  }
  dispose() {
    this.pause();
    this.bgm = undefined;
    void this.ctx?.close();
    this.ctx = undefined;
  }
}

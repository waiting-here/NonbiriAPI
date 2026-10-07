export class CatchAudio {
  private context: AudioContext | null = null;
  private enabled = false;

  enable(enabled: boolean) {
    this.enabled = enabled;
    if (!enabled) return;
    try {
      this.context ??= new AudioContext();
      void this.context
        .resume()
        .then(() => this.beep('prop'))
        .catch(() => undefined);
    } catch {
      // Sound is optional when the browser cannot create an audio context.
    }
  }

  beep(kind: 'catch' | 'hit' | 'prop', combo = 0) {
    const audio = this.context;
    if (!this.enabled || !audio || audio.state !== 'running') return;
    try {
      const osc = audio.createOscillator(),
        gain = audio.createGain(),
        now = audio.currentTime;
      const freq = kind === 'hit' ? 160 : kind === 'prop' ? 660 : 440 + Math.min(combo, 20) * 13;
      osc.type = kind === 'hit' ? 'triangle' : 'sine';
      osc.frequency.setValueAtTime(freq, now);
      osc.frequency.exponentialRampToValueAtTime(kind === 'hit' ? 60 : freq * 1.5, now + 0.11);
      gain.gain.setValueAtTime(0.0001, now);
      gain.gain.exponentialRampToValueAtTime(0.075, now + 0.01);
      gain.gain.exponentialRampToValueAtTime(0.0001, now + 0.15);
      osc.connect(gain);
      gain.connect(audio.destination);
      osc.start(now);
      osc.stop(now + 0.17);
    } catch {
      // Losing audio output must not stop the game loop.
    }
  }

  dispose() {
    if (this.context) void this.context.close().catch(() => undefined);
    this.context = null;
  }
}

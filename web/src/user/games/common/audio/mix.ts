import type { EffectCue } from './assets';

export interface EffectMix {
  readonly gain: number;
  readonly priority: 1 | 2 | 3;
  readonly duck?: { readonly level: number; readonly hold: number; readonly release: number };
}

const touch: EffectMix = { gain: 0.65, priority: 1 };
const action: EffectMix = { gain: 1.1, priority: 2 };
const turn: EffectMix = {
  gain: 1.5,
  priority: 2,
  duck: { level: 0.32, hold: 0.24, release: 0.3 },
};
const ending: EffectMix = {
  gain: 1.55,
  priority: 3,
  duck: { level: 0.22, hold: 0.45, release: 0.45 },
};

/** Mix by the meaning of the event; values are independent of reward currencies. */
export function effectMix(cue: EffectCue): EffectMix {
  if (/^common_(win|loss|draw)$/.test(cue) || cue === 'likes_loss_stinger') return ending;
  if (
    cue === 'likes_combo' ||
    cue === 'likes_overload' ||
    cue === 'bidding_pot_collect' ||
    cue === 'blackjack_natural' ||
    cue === 'blackjack_bust'
  )
    return turn;
  if (cue.startsWith('common_') || cue === 'likes_countdown' || cue === 'likes_pay') return touch;
  if (cue === 'likes_cast') return { gain: 1, priority: 2 };
  if (cue === 'likes_score_burst') return { gain: 1.3, priority: 2 };
  return action;
}

export interface EffectPlayback {
  readonly gain?: number;
  readonly semitones?: number;
  readonly delay?: number;
  readonly accent?: boolean;
}

/** Keep layered accents bounded, including malformed values from a caller. */
export function effectPlayback(mix: EffectMix, variation?: EffectPlayback) {
  const gain = Number.isFinite(variation?.gain) ? Math.max(0, Math.min(1.5, variation!.gain!)) : 1;
  const semitones = Number.isFinite(variation?.semitones)
    ? Math.max(-4, Math.min(4, variation!.semitones!))
    : 0;
  const delay = Number.isFinite(variation?.delay)
    ? Math.max(0, Math.min(0.2, variation!.delay!))
    : 0;
  return { gain: mix.gain * gain, rate: 2 ** (semitones / 12), delay };
}

export interface DuckEnvelope {
  readonly start: number;
  readonly attackEnd: number;
  readonly holdEnd: number;
  readonly end: number;
  readonly from: number;
  readonly level: number;
}

export function duckLevel(envelope: DuckEnvelope | null, now: number): number {
  if (!envelope || now >= envelope.end) return 1;
  if (now <= envelope.start) return envelope.from;
  if (now < envelope.attackEnd)
    return (
      envelope.from +
      (envelope.level - envelope.from) *
        ((now - envelope.start) / (envelope.attackEnd - envelope.start))
    );
  if (now <= envelope.holdEnd) return envelope.level;
  return (
    envelope.level +
    (1 - envelope.level) * ((now - envelope.holdEnd) / (envelope.end - envelope.holdEnd))
  );
}

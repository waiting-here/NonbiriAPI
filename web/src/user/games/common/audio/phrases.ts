import type { EffectCue } from './assets';
import type { EffectPlayback } from './mix';

export interface EffectVoice extends EffectPlayback {
  readonly cue: EffectCue;
}

/** A bounded accent for consecutive follow-ups in the same round and seat. */
export function followUpVoices(count: number): readonly EffectVoice[] {
  const stage = Math.max(1, Math.min(3, count));
  const voices: EffectVoice[] = [
    { cue: 'likes_combo', gain: 0.9 + (stage - 1) * 0.075, semitones: (stage - 1) * 2 },
  ];
  if (stage >= 2)
    voices.push({
      cue: 'likes_score_burst',
      gain: stage === 2 ? 0.38 : 0.58,
      semitones: stage === 2 ? 0 : 2,
      accent: true,
    });
  if (stage >= 3) voices.push({ cue: 'likes_cast', gain: 0.45, semitones: -3, accent: true });
  return voices;
}

/** Result phrases describe applied layers; they never replace the skill's awarded likes. */
export function applicationVoices(success: number, resisted: number): readonly EffectVoice[] {
  if (resisted === 0) return [{ cue: 'likes_buff', gain: 1.1 }];
  if (success > 0)
    return [
      { cue: 'likes_buff', gain: 0.85 },
      { cue: 'likes_cleanse', gain: 0.95, semitones: 2 },
    ];
  return [
    { cue: 'likes_cleanse', gain: 1.15, semitones: -3 },
    { cue: 'common_lock', gain: 1.25, semitones: -4 },
    { cue: 'common_lock', gain: 1.1, semitones: 3, delay: 0.09 },
  ];
}

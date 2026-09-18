import type { AudioGame, MusicQuality } from './assets';

export const MUSIC_QUALITY_KEY = 'nonbiri.games.music-quality.v1';
export function readMusicQuality(): MusicQuality {
  try {
    return localStorage.getItem(MUSIC_QUALITY_KEY) === 'lossless' ? 'lossless' : 'light';
  } catch {
    return 'light';
  }
}
export function writeMusicQuality(quality: MusicQuality): void {
  try {
    localStorage.setItem(MUSIC_QUALITY_KEY, quality);
  } catch {
    /* Storage may be disabled. */
  }
}
export function readAudioPreference(game: AudioGame, kind: 'sound' | 'music'): boolean {
  try {
    return localStorage.getItem(`nonbiri.games.${kind}.v1.${game}`) === 'true';
  } catch {
    return false;
  }
}
export function writeAudioPreference(
  game: AudioGame,
  kind: 'sound' | 'music',
  enabled: boolean,
): void {
  try {
    localStorage.setItem(`nonbiri.games.${kind}.v1.${game}`, String(enabled));
  } catch {
    /* Keep the in-memory choice when storage is unavailable. */
  }
}

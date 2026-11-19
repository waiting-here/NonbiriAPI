import { artSourceSet, highResolutionArt } from '../activities/gacha/art-resolution';
import { useBackgroundPlayback } from './playback-context';

export function useBackgroundSources() {
  const { playback } = useBackgroundPlayback();
  return playback.highest ? (src: string) => highResolutionArt(src) : artSourceSet;
}

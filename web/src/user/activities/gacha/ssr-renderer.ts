import type { ArtworkPlayback } from '@shared/animation/artwork-playback';
import type { MomentExpansion } from './moment-frame';
import { specialCards } from './special-scenes';

export async function renderSSR(
  canvas: HTMLCanvasElement,
  id: string,
  frozenTime?: number,
  paused?: () => boolean,
  highResolution = false,
  options: {
    playback?: ArtworkPlayback;
    characterOnly?: boolean;
    signal?: AbortSignal;
    momentFrame?: MomentExpansion;
  } = {},
) {
  if (options.signal?.aborted) return () => {};
  if (/^[gij]-/.test(id)) {
    const { renderClassicSSR } = await import('./classic/renderer');
    if (options.signal?.aborted) return () => {};
    return renderClassicSSR(canvas, id, { ...options, frozenTime, paused, highResolution });
  }
  if (id === 'l-chatgpt' || id === 'm-deepseek') {
    const { renderMythicSSR } = await import('./mythic/renderer');
    if (options.signal?.aborted) return () => {};
    return renderMythicSSR(canvas, id, { ...options, frozenTime, paused, highResolution });
  }
  if (id === 's-chatgpt' || id === 's-claude') {
    const { renderSweetHideAndSeek } = await import('./sweet-hide-and-seek/renderer');
    if (options.signal?.aborted) return () => {};
    return renderSweetHideAndSeek(canvas, id, { ...options, frozenTime, paused, highResolution });
  }
  if (
    id === 't-chatgpt' ||
    id === 't-claude' ||
    id === 't-glm' ||
    id === 't-deepseek' ||
    id === 't-gemini'
  ) {
    const { renderChristmas } = await import('./christmas/renderer');
    if (options.signal?.aborted) return () => {};
    return renderChristmas(canvas, id, { ...options, frozenTime, paused, highResolution });
  }
  if (id === 'u-gemini' || id === 'u-glm' || id === 'u-deepseek') {
    const { renderEncore } = await import('./encore/renderer');
    if (options.signal?.aborted) return () => {};
    return renderEncore(canvas, id, { ...options, frozenTime, paused, highResolution });
  }
  const theme = specialCards[id];
  if (theme) {
    const { renderSpecial } = await import('./special-renderer');
    if (options.signal?.aborted) return () => {};
    return renderSpecial(canvas, theme, {
      ...options,
      frozenTime,
      paused,
      highResolution,
      ambient: !!options.playback,
    });
  }
  throw new Error('card-animation-unavailable');
}

import type { MomentFrame } from './moment-frame';
import type { SpecialTheme } from './special-scenes';

export async function renderSpecial(
  canvas: HTMLCanvasElement,
  theme: SpecialTheme,
  options: {
    landscape?: boolean;
    mirrorFlow?: boolean;
    dark?: boolean;
    highResolution?: boolean;
    characterOnly?: boolean;
    momentFrame?: MomentFrame;
    characterPlate?: boolean;
    backgroundOnly?: boolean;
    frozenTime?: number;
    paused?: () => boolean;
    signal?: AbortSignal;
    ambient?: boolean;
    exportSize?: boolean;
    blink?: number;
    variant?: 'card' | 'cover' | 'illustration' | 'appearance';
  } = {},
) {
  if (options.signal?.aborted) return () => {};
  if (theme === 'tomorrow') {
    const { renderTomorrow } = await import('./tomorrow/renderer');
    if (options.signal?.aborted) return () => {};
    return renderTomorrow(canvas, {
      ...options,
      walking: true,
      characterPlate: options.characterOnly || options.characterPlate,
    });
  }
  const config = { ...options, polished: true };
  if (theme === 'mirrorsea') {
    const { renderMirrorSea } = await import('./mirror-sea-renderer');
    if (options.signal?.aborted) return () => {};
    return renderMirrorSea(canvas, config);
  }
  if (theme === 'amber') {
    const { renderAmber } = await import('./amber/renderer');
    if (options.signal?.aborted) return () => {};
    return renderAmber(canvas, config);
  }
  const { renderRomance } = await import('./romance/renderer');
  if (options.signal?.aborted) return () => {};
  return renderRomance(canvas, theme, config);
}

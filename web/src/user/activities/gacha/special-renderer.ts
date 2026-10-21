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
  const config = { ...options, polished: true };
  if (theme === 'mirrorsea') {
    const { renderMirrorSea } = await import('./mirror-sea-renderer');
    return renderMirrorSea(canvas, config);
  }
  if (theme === 'amber') {
    const { renderAmber } = await import('./amber/renderer');
    return renderAmber(canvas, config);
  }
  const { renderRomance } = await import('./romance/renderer');
  return renderRomance(canvas, theme, config);
}

import type { MomentFrame } from '../moment-frame';
export type SceneVariant = 'card' | 'cover' | 'illustration' | 'appearance';
export type SceneLayer = 'complete' | 'background' | 'hero';
export function createScene(
  id: 'elopement' | 'heart-thief',
  polished?: boolean,
  options?: {
    highResolution?: boolean;
    dark?: boolean;
    characterOnly?: boolean;
    momentFrame?: MomentFrame;
  },
): Promise<{
  paintCharacter(
    canvas: HTMLCanvasElement,
    dark?: boolean,
    time?: number,
    forcedBlink?: number,
  ): void;
  paint(
    canvas: HTMLCanvasElement,
    time: number,
    variant?: SceneVariant,
    dark?: boolean,
    layer?: SceneLayer,
    motion?: boolean,
    forcedBlink?: number,
    viewportWidth?: number,
  ): void;
  dispose(): void;
}>;

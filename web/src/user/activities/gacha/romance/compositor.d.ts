export type SceneVariant = 'card' | 'cover' | 'illustration' | 'appearance';
export type SceneLayer = 'complete' | 'background' | 'hero';
export function createScene(
  id: 'elopement' | 'heart-thief',
  polished?: boolean,
): Promise<{
  paint(
    canvas: HTMLCanvasElement,
    time: number,
    variant?: SceneVariant,
    dark?: boolean,
    layer?: SceneLayer,
    motion?: boolean,
    forcedBlink?: number,
  ): void;
  dispose(): void;
}>;

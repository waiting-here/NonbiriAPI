import type { Phrase, State } from './engine';
export interface RenderEvent {
  kind: 'catch' | 'hit' | 'prop';
  text?: string;
}
export function createRenderer(
  canvas: HTMLCanvasElement,
  phrases: readonly Phrase[],
  english: boolean,
): {
  resize(): void;
  paint(state: State | null, active: boolean, elapsed: number, event?: RenderEvent): void;
  destroy(): void;
};

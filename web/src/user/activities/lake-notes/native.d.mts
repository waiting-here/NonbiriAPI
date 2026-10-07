import type { LakeController, ControllerSnapshot } from './controller';
import type { Action, Profile } from './rules';
import type { LakeText } from './copy';
export interface LakeBridge {
  language: 'zh' | 'en';
  translate(text: string): string;
  profile(): Profile;
  revision(): string;
  snapshot(): ControllerSnapshot;
  projection: LakeController['projection'];
  held(held: boolean): void;
  tick(): void;
  act(action: Action): Promise<unknown>;
  start(): void;
  resume(): void;
  pause(): void;
  blocked(): boolean;
  busy(): boolean;
  readonly(): boolean;
  text: LakeText;
}
export function mountLake(
  root: HTMLElement,
  bridge: LakeBridge,
): { refresh(): void; dispose(): void };

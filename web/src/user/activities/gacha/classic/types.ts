export type Point = readonly [number, number];
export type Rect = readonly [number, number, number, number];

export const classicRoles = ['chatgpt', 'claude', 'glm', 'deepseek', 'gemini'] as const;
export type ClassicCard = `${'g' | 'i' | 'j'}-${(typeof classicRoles)[number]}`;

export type MeshMotion = {
  kind: 'mesh';
  root: Point;
  tip: Point;
  amplitude: Point;
  period: number;
  delay: number;
  protect?: readonly Rect[];
  seams?: readonly Rect[];
  ribbon?: { path: readonly Point[]; range: readonly [number, number]; frequency: number };
};

export type RigidMotion = {
  kind: 'rigid';
  parent?: RigidMotion;
  pivot: Point;
  amplitude: Point;
  rotation: number;
  period: number;
  delay: number;
};

export type BreathMotion = {
  kind: 'breath';
  chest: Rect;
  amount: number;
  protect: readonly Rect[];
};

export type Layer = {
  name: string;
  rect: Rect;
  motion?: MeshMotion | RigidMotion | BreathMotion;
  eyes?: boolean;
  swim?: RigidMotion;
};

export type CardParts = {
  portrait: readonly Layer[];
  expanded?: readonly Layer[];
};

export type ScenicPart = {
  path: string;
  rect: Rect;
  motion?: RigidMotion;
  effect?: 'flame' | 'cascade' | 'river' | 'sky' | 'fireworks';
  color?: string;
  front?: boolean;
  swim?: boolean;
};

export type Environment = {
  base: string;
  parts: readonly ScenicPart[];
};

export type CodexPetStateId =
  | 'idle'
  | 'running-right'
  | 'running-left'
  | 'waving'
  | 'jumping'
  | 'failed'
  | 'waiting'
  | 'running'
  | 'review';

export interface CodexPetManifest {
  readonly id: string;
  readonly displayName: string;
  readonly description: string;
  readonly spriteVersionNumber?: number;
  readonly spritesheetPath: string;
}

export interface CodexPetRowSpec {
  readonly state: CodexPetStateId;
  readonly row: number;
  readonly frames: number;
  readonly durationsMs: readonly number[];
  readonly labelZh: string;
  readonly emoji: string;
}

/**
 * Official Codex V2 Pet 8x11 Atlas Row & Timing Contract
 * (Matches ~/.codex/skills/hatch-pet/references/animation-rows.md)
 */
export const CODEX_PET_COLUMNS = 8;
export const CODEX_PET_V2_TOTAL_ROWS = 11;
export const CODEX_PET_V1_TOTAL_ROWS = 9;

export const CODEX_PET_ROWS: Record<CodexPetStateId, CodexPetRowSpec> = {
  idle: {
    state: 'idle',
    row: 0,
    frames: 6,
    durationsMs: [280, 110, 110, 140, 140, 320],
    labelZh: '待机呼吸 (idle)',
    emoji: '🫧',
  },
  'running-right': {
    state: 'running-right',
    row: 1,
    frames: 8,
    durationsMs: [120, 120, 120, 120, 120, 120, 120, 220],
    labelZh: '向右游走 (running-right)',
    emoji: '➡️',
  },
  'running-left': {
    state: 'running-left',
    row: 2,
    frames: 8,
    durationsMs: [120, 120, 120, 120, 120, 120, 120, 220],
    labelZh: '向左游走 (running-left)',
    emoji: '⬅️',
  },
  waving: {
    state: 'waving',
    row: 3,
    frames: 4,
    durationsMs: [140, 140, 140, 280],
    labelZh: '挥手问候 (waving)',
    emoji: '👋',
  },
  jumping: {
    state: 'jumping',
    row: 4,
    frames: 5,
    durationsMs: [140, 140, 140, 140, 280],
    labelZh: '欢呼跳跃 (jumping)',
    emoji: '🌟',
  },
  failed: {
    state: 'failed',
    row: 5,
    frames: 8,
    durationsMs: [140, 140, 140, 140, 140, 140, 140, 240],
    labelZh: '受惊鼓腮 (failed)',
    emoji: '😤',
  },
  waiting: {
    state: 'waiting',
    row: 6,
    frames: 6,
    durationsMs: [150, 150, 150, 150, 150, 260],
    labelZh: '眼巴巴等投喂 (waiting)',
    emoji: '🥺',
  },
  running: {
    state: 'running',
    row: 7,
    frames: 6,
    durationsMs: [120, 120, 120, 120, 120, 220],
    labelZh: '捧碗干饭 (running)',
    emoji: '🍚',
  },
  review: {
    state: 'review',
    row: 8,
    frames: 6,
    durationsMs: [150, 150, 150, 150, 150, 280],
    labelZh: '歪头观察 (review)',
    emoji: '🧐',
  },
};

export const CODEX_PET_LOOK_DEGREES: readonly string[] = [
  '000',
  '022.5',
  '045',
  '067.5',
  '090',
  '112.5',
  '135',
  '157.5',
  '180',
  '202.5',
  '225',
  '247.5',
  '270',
  '292.5',
  '315',
  '337.5',
];

export interface PetHighFpsStateSlice {
  readonly sheet?: string;
  readonly columns?: number;
  readonly rows?: number;
  readonly cellWidth?: number;
  readonly cellHeight?: number;
  readonly startSlot: number;
  readonly frames: number;
  readonly frameMs: number;
}

export interface PetCanvasBox {
  /** Bleed offset relative to logical 192x208 body box width/height (typically <= 0) */
  readonly offsetX: number;
  readonly offsetY: number;
  /** Expanded canvas width/height multiplier relative to logical 192x208 body box (>= 1) */
  readonly scaleX: number;
  readonly scaleY: number;
}

export interface PetHighFpsLayout {
  readonly version: 1;
  readonly spritesheetPath?: string;
  readonly lookSpritesheetPath?: string;
  readonly columns: number;
  readonly rows: number;
  readonly cellWidth: number;
  readonly cellHeight: number;
  readonly canvasBox?: PetCanvasBox;
  readonly states: Partial<Record<CodexPetStateId, PetHighFpsStateSlice>>;
  readonly feast?: PetHighFpsStateSlice;
  readonly workerFeast?: PetHighFpsStateSlice;
  readonly workerIdle?: PetHighFpsStateSlice;
  readonly sleeping?: PetHighFpsStateSlice;
  readonly look360?: PetHighFpsStateSlice;
  readonly lookStartSlot?: number;
}

export interface LoadedPetSheet {
  readonly image: HTMLImageElement;
  readonly columns: number;
  readonly cellWidth: number;
  readonly cellHeight: number;
}

export interface LoadedCodexPetAssets {
  manifest: CodexPetManifest;
  atlasImage: HTMLImageElement;
  lookAtlasImage?: HTMLImageElement;
  lookColumns?: number;
  lookCellWidth?: number;
  lookCellHeight?: number;
  stateSheets?: Partial<Record<CodexPetStateId, LoadedPetSheet>>;
  feastSheet?: LoadedPetSheet;
  workerFeastSheet?: LoadedPetSheet;
  workerIdleSheet?: LoadedPetSheet;
  sleepingSheet?: LoadedPetSheet;
  look360Sheet?: LoadedPetSheet;
  cellWidth: number;
  cellHeight: number;
  hasLookDirections: boolean;
  isHdAtlas: boolean;
  highFpsLayout?: PetHighFpsLayout | null;
}

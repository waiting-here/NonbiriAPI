export type Amount = string;
export interface Effects {
  barHeight: number;
  acceleration: number;
  progressGain: number;
  progressLoss: number;
  rareWeight: number;
  legendWeight: number;
  bottomBounce: number;
  barbedCount: number;
  qualityBonus: number;
  spinnerSeconds: number;
  sonar: boolean;
  biteDelay: number;
  epicWeight: number;
  fishSpeed: number;
}
export interface FishType {
  name: string;
  kind: string;
  location: string;
  rarity: string;
  weight: number;
  behavior: string;
  style: string;
  length: number[];
  difficulty: number;
  basePrice: number;
  periods?: string[];
  weathers?: string[];
  description?: string;
}
export interface Loadout {
  rod: string;
  tackle1?: string;
  tackle2?: string;
  tackle3?: string;
  bait?: string;
}
export interface Catch {
  id: number;
  kind: string;
  length: number;
  quality: number;
  perfect: boolean;
  locked: boolean;
}
export interface RecordEntry {
  caught: Amount;
  maxLength: number;
  bestQuality: number;
  perfectCount: Amount;
}
export interface Contract {
  id: string;
  slot: string;
  day: number;
  type: string;
  target: number;
  kind?: string;
  location?: string;
  progress: number;
  status: string;
}
export interface Profile {
  coins: Amount;
  xp: Amount;
  first?: string;
  second?: string;
  third?: string;
  fourth?: string;
  location: string;
  day: number;
  clockMinutes: number;
  ownedGear: string[];
  equipped: Loadout;
  baitStock: Record<string, number>;
  selectedBait?: string;
  basket: Catch[];
  records: Record<string, RecordEntry>;
  nextCatchId: number;
  savedLoadouts: (Loadout | null)[];
  debrisStock: Record<string, number>;
  trashRecovered: Amount;
  treasureOpened: Amount;
  contracts: Contract[];
  contractsDay: number;
  completedContracts: Amount;
  streak: Amount;
}
export interface Challenge {
  difficulty: number;
  fishSpeed: number;
  tempo: number;
  gain: number;
  loss: number;
}
export interface FishState {
  dartDirection: number;
  reverseRemaining: number;
  position: number;
  y: number;
  speed: number;
  target: number;
  drift: number;
}
export interface MotionRandom {
  state: number;
  draw_count: number;
}
export interface Snapshot {
  first?: string;
  second?: string;
  third?: string;
  fourth?: string;
  level: number;
  hadCaught: boolean;
  rod: string;
  bait?: string;
  location: string;
  effects: Effects;
  barHeight: number;
}
export interface EncounterPlan {
  waitSeconds: number;
  biteTick: number;
  biteDay: number;
  biteClockMinutes: number;
  debris?: string;
  fishKind?: string;
  length: number;
  sizeFactor: number;
  challenge: Challenge;
  treasureY?: number;
}
export interface TerminalResult {
  success: boolean;
  perfect: boolean;
  quality: number;
  xp: number;
  overflow: boolean;
  catchValue: number;
  debris?: string;
}
export interface TreasureReward {
  coins: number;
  bait: string;
  count: number;
}
export interface Cast {
  bitePreparationRemaining: number;
  rules_id: string;
  snapshot: Snapshot;
  plan: EncounterPlan;
  phase: string;
  paused: boolean;
  tick: number;
  held: boolean;
  waitRemaining: number;
  barY: number;
  barVelocity: number;
  progress: number;
  wasHit: boolean;
  elapsed: number;
  hitTime: number;
  effectiveTime: number;
  currentMissTime: number;
  longestMissTime: number;
  fish?: FishState;
  treasure?: { y: number; progress: number; secured: boolean };
  motion: MotionRandom;
  result?: TerminalResult;
  reward?: TreasureReward;
}
export interface Action {
  quantity?: number;
  action: string;
  id?: string;
  slot?: string;
  index?: number;
  fish_ids?: number[];
  locked?: boolean;
}

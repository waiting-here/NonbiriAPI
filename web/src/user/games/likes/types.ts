import type { Pair, Seat } from '../common/duel/types';
import type { RoleID } from './catalog';
import type { JSONValue } from './value';

export interface Selection {
  role: RoleID;
  harness: string | null;
  skills: string[];
}
export interface Choice {
  skillId: string;
  pay?: 'auto' | 'api';
  cleanseMode?: 'self' | 'opponent';
  targets?: string[];
}
export interface Purchase {
  item: 'sub' | 'api' | 'charge' | 'cleanse' | 'regulator';
  target?: string;
}
export interface Plan {
  purchases: Purchase[];
  main: Choice | null;
  extra: Choice[];
}
export interface Status {
  key: string;
  kind: string;
  name: string;
  positive: boolean;
  category?: string;
  p: number;
  q: number;
  remaining: number;
  layers: number;
  targetSkill?: string;
  expires?: number;
  refreshedTurn?: number;
  buffId: string;
  appliedBy?: string;
  activeFrom: number;
  persistentLayers?: number;
}
export interface Subscription {
  burstInitial: number;
  totalInitial: number;
  totalCap: number;
  burstResetAt: number | null;
  totalResetAt: number | null;
}
export interface Sample {
  id: number;
  skillId: string;
  success: boolean;
  kind: string;
  derived: boolean;
}
export interface TurnRecord {
  skipped: boolean;
  stunned: boolean;
  last: Sample | null;
  lastMain: Sample | null;
  lastSuccess: Sample | null;
  lastCopyable: Sample | null;
  nonbasicSuccess?: boolean;
}
export interface Distill {
  template: string | null;
  level: 'I' | 'II' | null;
  learning: number;
  usedSamples: number[];
}
export interface Player {
  role: RoleID;
  harness: string | null;
  activeSlots: number;
  gold: number;
  likes: number;
  burstCap: number;
  burst: number;
  sub: number;
  api: number;
  images: number;
  apiPack: number;
  trial?: number;
  resources: Record<string, number>;
  resourceCaps: Record<string, number>;
  subscription: Subscription;
  normalTurns: number;
  stunned: boolean;
  overloaded: boolean;
  effects: Status[];
  revealed: string[];
  slots: (string | null)[];
  fog: boolean;
  loadout?: string[];
  used: Record<string, number>;
  skillDecay: Record<string, number>;
  distill: Distill | null;
}
export interface LikesView {
  energy: number;
  players: Pair<Player>;
  records: Pair<TurnRecord | null>;
  lockedPlan: Plan | null;
}
export interface EffectCue {
  key: string;
  kind: string;
  buff_id: string;
  layers: number;
  remaining: number;
  active_from: number;
}
export interface Resources {
  gold: number;
  likes: number;
  burst: number;
  burst_cap: number;
  sub: number;
  sub_cap: number;
  api: number;
  trial: number;
  resources: Record<string, number>;
  resource_caps: Record<string, number>;
  effects: EffectCue[];
}
export interface Frame {
  stage: string;
  players: Pair<Resources>;
  energy: number;
}
export interface Score {
  original: number;
  parts: { key: string; amount: number; buff_id?: string }[];
  intrinsic: number;
  conditional: number;
  before_multiplier: number;
  multiplier: number;
  passive: number;
  final: number;
}
export interface Cast {
  skillId: string;
  derived: boolean;
  main: boolean;
  energy: number;
  token: number;
  likes: number;
  passiveLikes: number;
  trialPayment: number;
  subPayment: number;
  apiPayment: number;
  gold: number;
  resourceCosts: Record<string, number>;
  templateId: string;
  success: true;
  level: 'I' | 'II' | null;
  step?: { kind: 'main' | 'extra' | 'flash'; index: number; likes: Pair<number> };
  characterPassive?: string;
  applications?: {
    buffID: string;
    target: Seat;
    success: number;
    resisted: number;
    derived: boolean;
  }[];
}
export interface LikesEvent {
  id: number;
  round: number;
  stage: string;
  kind: string;
  seat: Seat | null;
  data: Record<string, JSONValue>;
  score: Score | null;
  cast: Cast | null;
  transition: { before: Frame; after: Frame } | null;
}
export interface Presentation {
  plans: Pair<Plan>;
  before: Frame;
  after: Frame;
  frames: Frame[];
  events: LikesEvent[];
  timeline?: PresentationStep[];
}
export interface PresentationStep {
  stage: string;
  durationMS: number;
  eventIDs: number[];
}
export interface RoundFacts extends Presentation {
  round: number;
  draws: { ordinal: number; candidate_count: number; index: number }[];
  result: { winner: Seat | null; reason: string; scores: Pair<number> } | null;
}
export interface LikesAction {
  kind: 'plan';
  plan: Plan;
}

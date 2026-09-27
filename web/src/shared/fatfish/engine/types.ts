export const ENGINE_VERSION = 1;
export const SCORING_VERSION = 1;
export const FIELD_WIDTH = 480 * 64;
export const FIELD_HEIGHT = 560 * 64;
export const DRAG_BUFFER = 128 * 64;
export const FISH_RADIUS = 8 * 64;
export const TICKS_PER_SECOND = 60;
export const SUBSTEPS = 2;
export const MAX_INPUTS = 72000;
export const MAX_INPUT_BYTES = 4 << 20;
export const MAX_LEVEL_BYTES = 256 << 10;

export interface Point { x: number; y: number }
export interface Polygon { outer: Point[]; holes: Point[][] }
export interface FishSpec { id: number; x: number; y: number; heading: number }
export interface Shape { id: number; polygon: Polygon }
export interface Tool extends Shape { resource_key: string; placed: boolean; x: number; y: number }
export interface Bowl extends Shape { required: number; capacity: number }
export interface Switch extends Shape { mode: "latch" | "hold" }
export interface Gate extends Shape { initially_open: boolean; mode: "any" | "all"; switch_ids: number[] }
export interface Direction extends Shape { mode: "entry" | "oneway"; heading: number }

export interface Level {
  format: "nonbiri-fatfish-level";
  format_version: 1;
  engine_version: 1;
  scoring_version: 1;
  duration_seconds: number;
  speed_pixels_per_second: number;
  thresholds: [number, number, number];
  fish: FishSpec[];
  tools: Tool[];
  solids: Shape[];
  hazards: Shape[];
  bowls: Bowl[];
  switches: Switch[];
  gates: Gate[];
  directions: Direction[];
}

export type InputTuple =
  | [number, number, "place", number, number, number]
  | [number, number, "return" | "finish" | "abandon", number];

export interface FishState {
  id: number;
  x: number;
  y: number;
  heading: number;
  status: "walking" | "fed" | "lost";
  bowl_id: number;
  turn_dir: number;
  turn_distance: number;
  flow_id: number;
  speed_remainder: number;
  x_remainder: number;
  y_remainder: number;
  rng: [number, number, number, number];
}
export interface ToolState { id: number; placed: boolean; x: number; y: number }
export interface SwitchState { id: number; active: boolean; triggered: boolean; occupied: boolean }
export interface GateState { id: number; open: boolean; pending: boolean }
export interface BowlState { id: number; count: number }
export interface EngineState {
  tick: number;
  solid_revision: number;
  fish: FishState[];
  tools: ToolState[];
  switches: SwitchState[];
  gates: GateState[];
  bowls: BowlState[];
  terminal: boolean;
  reason: string;
}
export interface ReplayResult {
  engine_version: number;
  scoring_version: number;
  content_hash: string;
  final_state_hash: string;
  terminal_tick: number;
  reason: string;
  fed: number;
  total: number;
  bowl_counts: BowlState[];
  passed: boolean;
  stars: number;
  score_units: number;
}
